package appdir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirFrom(t *testing.T) {
	exe := func() (string, error) { return filepath.Join("opt", "d9c", "d9c.exe"), nil }
	noExe := func() (string, error) { return "", errors.New("no exe") }
	tests := []struct {
		name string
		home func() (string, error)
		exe  func() (string, error)
		want string
	}{
		{"home", func() (string, error) { return "home", nil }, exe, filepath.Join("home", DirName)},
		{"home error falls back to binary dir", func() (string, error) { return "", errors.New("no home") }, exe, filepath.Join("opt", "d9c")},
		{"empty home falls back to binary dir", func() (string, error) { return "", nil }, exe, filepath.Join("opt", "d9c")},
		{"nothing known: current dir", func() (string, error) { return "", errors.New("no home") }, noExe, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dirFrom(tt.home, tt.exe); got != tt.want {
				t.Errorf("dirFrom = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, want := Path("x.yaml"), filepath.Join(home, DirName, "x.yaml"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := LegacyPath("x.yaml"), filepath.Join(filepath.Dir(exe), "x.yaml"); got != want {
		t.Errorf("LegacyPath = %q, want %q", got, want)
	}
}

func TestMigrate(t *testing.T) {
	tests := []struct {
		name       string
		legacy     string // legacy content; "" = no legacy file
		dst        string // existing dst content; "" = no dst file
		samePath   bool
		wantCopied bool
		wantDst    string // expected dst content afterwards; "" = must not exist
	}{
		{name: "copies legacy into new dir", legacy: "theme: nord\n", wantCopied: true, wantDst: "theme: nord\n"},
		{name: "no legacy file", wantDst: ""},
		{name: "dst already exists wins", legacy: "theme: nord\n", dst: "theme: dracula\n", wantDst: "theme: dracula\n"},
		{name: "same path is a no-op", legacy: "theme: nord\n", samePath: true, wantDst: "theme: nord\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			legacy := filepath.Join(root, "bin", "d9c-config.yaml")
			dst := filepath.Join(root, "home", DirName, "d9c-config.yaml")
			if tt.samePath {
				dst = legacy
			}
			if tt.legacy != "" {
				writeFile(t, legacy, tt.legacy)
			}
			if tt.dst != "" {
				writeFile(t, dst, tt.dst)
			}

			copied, err := Migrate(dst, legacy)
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if copied != tt.wantCopied {
				t.Errorf("copied = %v, want %v", copied, tt.wantCopied)
			}
			got, err := os.ReadFile(dst)
			switch {
			case tt.wantDst == "" && err == nil:
				t.Errorf("dst created unexpectedly: %q", got)
			case tt.wantDst != "" && string(got) != tt.wantDst:
				t.Errorf("dst = %q (%v), want %q", got, err, tt.wantDst)
			}
			if tt.legacy != "" {
				if _, err := os.Stat(legacy); err != nil {
					t.Errorf("legacy file must be left in place: %v", err)
				}
			}
		})
	}
}

func TestMigrateErrors(t *testing.T) {
	root := t.TempDir()

	// Legacy path is a directory: reading it fails.
	legacyDir := filepath.Join(root, "legacy-dir")
	if err := os.Mkdir(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(filepath.Join(root, "a", "dst.yaml"), legacyDir); err == nil {
		t.Error("unreadable legacy: want error")
	}

	// dst's parent is a regular file: creating the directory fails.
	legacy := filepath.Join(root, "legacy.yaml")
	writeFile(t, legacy, "x: 1\n")
	blocker := filepath.Join(root, "blocker")
	writeFile(t, blocker, "")
	if _, err := Migrate(filepath.Join(blocker, "dst.yaml"), legacy); err == nil {
		t.Error("uncreatable dst dir: want error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
