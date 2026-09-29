package dist

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// hashFor fabricates a distinct, valid-looking sha256 per file name.
func hashFor(name string) string {
	h := fmt.Sprintf("%x", name)
	for len(h) < 64 {
		h += "0"
	}
	return h[:64]
}

func sumsFor(version string, platforms ...string) Checksums {
	s := Checksums{}
	for _, p := range platforms {
		goos, goarch, _ := strings.Cut(p, "/")
		name := ArchiveName(version, goos, goarch)
		s[name] = hashFor(name)
	}
	return s
}

var allPlatforms = []string{
	"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64",
}

func TestParseChecksums(t *testing.T) {
	good := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		in      string
		want    Checksums
		wantErr string
	}{
		{
			name: "sha256sum text and binary mode, blank lines",
			in:   good + "  d9c_v1.0.0_linux_amd64.tar.gz\n\n" + strings.ToUpper(good) + " *d9c_v1.0.0_windows_amd64.zip\n",
			want: Checksums{"d9c_v1.0.0_linux_amd64.tar.gz": good, "d9c_v1.0.0_windows_amd64.zip": good},
		},
		{name: "empty", in: "\n\n", wantErr: "empty"},
		{name: "one field", in: good + "\n", wantErr: "line 1"},
		{name: "bad hash", in: "xyz  file.tar.gz\n", wantErr: "invalid sha256"},
		{name: "short hash", in: good[:10] + "  f\n", wantErr: "invalid sha256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseChecksums(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestArchiveName(t *testing.T) {
	tests := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "d9c_v1.2.3_linux_amd64.tar.gz"},
		{"darwin", "arm64", "d9c_v1.2.3_darwin_arm64.tar.gz"},
		{"windows", "amd64", "d9c_v1.2.3_windows_amd64.zip"},
	}
	for _, tt := range tests {
		if got := ArchiveName("1.2.3", tt.goos, tt.goarch); got != tt.want {
			t.Errorf("ArchiveName(%s/%s) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestDownloadURLRepo(t *testing.T) {
	r := Release{Version: "1.2.3"}
	want := "https://github.com/kirg0/d9c/releases/download/v1.2.3/d9c_v1.2.3_linux_amd64.tar.gz"
	if got := r.DownloadURL("linux", "amd64"); got != want {
		t.Errorf("default repo: %q, want %q", got, want)
	}
	r.Repo = "me/fork"
	if got := r.DownloadURL("linux", "amd64"); !strings.HasPrefix(got, "https://github.com/me/fork/releases/") {
		t.Errorf("custom repo: %q", got)
	}
}

func TestHomebrewFormula(t *testing.T) {
	r := Release{Version: "1.29.0", Sums: sumsFor("1.29.0", allPlatforms...)}
	got, err := r.HomebrewFormula()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"class D9c < Formula",
		`version "1.29.0"`,
		`license "MIT"`,
		`homepage "https://github.com/kirg0/d9c"`,
		`bin.install "d9c"`,
		`shell_output("#{bin}/d9c -version")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formula lacks %q:\n%s", want, got)
		}
	}
	// Every unix archive appears with its own hash right after its url.
	for _, p := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
		goos, goarch, _ := strings.Cut(p, "/")
		name := ArchiveName("1.29.0", goos, goarch)
		pair := fmt.Sprintf("url %q\n      sha256 %q", r.DownloadURL(goos, goarch), hashFor(name))
		if !strings.Contains(got, pair) {
			t.Errorf("formula lacks url/sha256 pair for %s", p)
		}
	}
	// Order matters: on_intel then on_arm inside each OS block.
	if strings.Index(got, "darwin_amd64") > strings.Index(got, "darwin_arm64") {
		t.Error("darwin amd64 must precede arm64")
	}
	if strings.Contains(got, "windows") {
		t.Error("formula must not reference windows archives")
	}
}

func TestHomebrewFormulaErrors(t *testing.T) {
	tests := []struct {
		name    string
		r       Release
		wantErr string
	}{
		{"missing linux arm64", Release{Version: "1.0.0", Sums: sumsFor("1.0.0", "linux/amd64", "darwin/amd64", "darwin/arm64")}, "linux_arm64"},
		{"v prefix", Release{Version: "v1.0.0", Sums: sumsFor("v1.0.0", allPlatforms...)}, "MAJOR.MINOR.PATCH"},
		{"empty version", Release{Sums: sumsFor("1.0.0", allPlatforms...)}, "MAJOR.MINOR.PATCH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.r.HomebrewFormula(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestScoopManifest(t *testing.T) {
	r := Release{Version: "1.29.0", Sums: sumsFor("1.29.0", allPlatforms...)}
	raw, err := r.ScoopManifest()
	if err != nil {
		t.Fatal(err)
	}
	var m scoopManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, raw)
	}
	if m.Version != "1.29.0" || m.Bin != "d9c.exe" || m.License != "MIT" || m.Checkver != "github" {
		t.Errorf("header fields: %+v", m)
	}
	amd := m.Architecture.AMD64
	if amd == nil || amd.URL != r.DownloadURL("windows", "amd64") ||
		amd.Hash != hashFor("d9c_v1.29.0_windows_amd64.zip") || amd.ExtractDir != "d9c_v1.29.0_windows_amd64" {
		t.Errorf("64bit = %+v", amd)
	}
	if m.Architecture.ARM64 == nil || m.Architecture.ARM64.ExtractDir != "d9c_v1.29.0_windows_arm64" {
		t.Errorf("arm64 = %+v", m.Architecture.ARM64)
	}
	up := m.Autoupdate.Architecture.AMD64
	if up == nil || up.URL != "https://github.com/kirg0/d9c/releases/download/v$version/d9c_v$version_windows_amd64.zip" ||
		up.ExtractDir != "d9c_v$version_windows_amd64" || up.Hash != "" {
		t.Errorf("autoupdate 64bit = %+v", up)
	}
	if m.Autoupdate.Hash.URL != "$baseurl/checksums.txt" {
		t.Errorf("autoupdate hash = %+v", m.Autoupdate.Hash)
	}
	// Scoop placeholders must survive JSON encoding verbatim.
	if !strings.Contains(string(raw), "v$version/") {
		t.Errorf("placeholder escaped:\n%s", raw)
	}
	// Field order follows Scoop's convention (version first, autoupdate last).
	if s := string(raw); strings.Index(s, `"version"`) > strings.Index(s, `"autoupdate"`) {
		t.Error("version must come before autoupdate")
	}
}

func TestScoopManifestOptionalARM64(t *testing.T) {
	r := Release{Version: "1.0.0", Sums: sumsFor("1.0.0", "windows/amd64")}
	raw, err := r.ScoopManifest()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "arm64") {
		t.Errorf("arm64 must be omitted without its archive:\n%s", raw)
	}
}

func TestScoopManifestErrors(t *testing.T) {
	if _, err := (Release{Version: "1.0.0", Sums: sumsFor("1.0.0", "windows/arm64")}).ScoopManifest(); err == nil ||
		!strings.Contains(err.Error(), "windows_amd64") {
		t.Errorf("missing amd64: err = %v", err)
	}
	if _, err := (Release{Version: "latest", Sums: sumsFor("latest", "windows/amd64")}).ScoopManifest(); err == nil {
		t.Error("non-semver version must fail")
	}
}
