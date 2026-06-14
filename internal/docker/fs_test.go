package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseLsEntries(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []FileEntry
	}{
		{
			name: "empty",
			out:  "",
			want: nil,
		},
		{
			name: "mixed files and dirs",
			out:  "bin/\n.bashrc\netc/\nhello.txt\n",
			want: []FileEntry{
				{Name: "bin", IsDir: true},
				{Name: ".bashrc", IsDir: false},
				{Name: "etc", IsDir: true},
				{Name: "hello.txt", IsDir: false},
			},
		},
		{
			name: "carriage returns and blank lines are tolerated",
			out:  "app/\r\n\r\nmain.go\r\n",
			want: []FileEntry{
				{Name: "app", IsDir: true},
				{Name: "main.go", IsDir: false},
			},
		},
		{
			name: "name with spaces stays intact",
			out:  "my data/\nread me.txt\n",
			want: []FileEntry{
				{Name: "my data", IsDir: true},
				{Name: "read me.txt", IsDir: false},
			},
		},
		{
			name: "lone slash line is skipped",
			out:  "/\nreal\n",
			want: []FileEntry{
				{Name: "real", IsDir: false},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLsEntries(tt.out)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseLsEntries(%q) = %#v, want %#v", tt.out, got, tt.want)
			}
		})
	}
}

// TestTarRoundtrip packs a local tree with tarLocal and unpacks it with
// extractTar (the two halves of docker cp), checking the content survives.
func TestTarRoundtrip(t *testing.T) {
	src := t.TempDir()
	// A small tree: file at root, a subdir with a file.
	mustWrite(t, filepath.Join(src, "top.txt"), "hello top\n")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "sub", "inner.txt"), "hello inner\n")

	buf, err := tarLocal(src, mustStat(t, src))
	if err != nil {
		t.Fatalf("tarLocal: %v", err)
	}

	dest := t.TempDir()
	if err := extractTar(buf, dest); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	base := filepath.Base(src)
	if got := mustRead(t, filepath.Join(dest, base, "top.txt")); got != "hello top\n" {
		t.Errorf("top.txt = %q", got)
	}
	if got := mustRead(t, filepath.Join(dest, base, "sub", "inner.txt")); got != "hello inner\n" {
		t.Errorf("sub/inner.txt = %q", got)
	}
}

// TestTarSingleFile packs and extracts a lone file.
func TestTarSingleFile(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "one.txt")
	mustWrite(t, file, "single\n")

	buf, err := tarLocal(file, mustStat(t, file))
	if err != nil {
		t.Fatalf("tarLocal: %v", err)
	}
	dest := t.TempDir()
	if err := extractTar(buf, dest); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	if got := mustRead(t, filepath.Join(dest, "one.txt")); got != "single\n" {
		t.Errorf("one.txt = %q", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// TestFakeListPath exercises the demo backend's canned filesystem.
func TestFakeListPath(t *testing.T) {
	f := NewFakeBackend()
	entries, err := f.ListPath("9ae942fd8fbc", "/")
	if err != nil {
		t.Fatalf("ListPath(/): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("ListPath(/) returned no entries")
	}
	if _, err := f.ListPath("9ae942fd8fbc", "/nonexistent"); err == nil {
		t.Fatal("ListPath(/nonexistent) should error")
	}
}

// TestFakeCopyFromContainer writes a placeholder download into a temp dir.
func TestFakeCopyFromContainer(t *testing.T) {
	dir := t.TempDir()
	f := NewFakeBackend()
	// chdir so the fake's "." destination lands in the temp dir.
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := f.CopyFromContainer("9ae942fd8fbc", "/etc/hosts", "."); err != nil {
		t.Fatalf("CopyFromContainer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hosts")); err != nil {
		t.Fatalf("expected ./hosts to exist: %v", err)
	}
}
