package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/dist"
)

func TestRunWritesFormulaAndManifest(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for _, p := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"} {
		goos, goarch, _ := strings.Cut(p, "/")
		fmt.Fprintf(&sb, "%s  %s\n", strings.Repeat("b", 64), dist.ArchiveName("2.0.0", goos, goarch))
	}
	sums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(sums, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "pkg")
	if err := run([]string{"-version", "v2.0.0", "-checksums", sums, "-out", out}); err != nil {
		t.Fatal(err)
	}
	rb, err := os.ReadFile(filepath.Join(out, "d9c.rb"))
	if err != nil || !strings.Contains(string(rb), `version "2.0.0"`) {
		t.Errorf("d9c.rb: err=%v\n%s", err, rb)
	}
	js, err := os.ReadFile(filepath.Join(out, "d9c.json"))
	if err != nil || !strings.Contains(string(js), `"version": "2.0.0"`) {
		t.Errorf("d9c.json: err=%v\n%s", err, js)
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"-version", "1.0.0", "-checksums", filepath.Join(dir, "nope.txt")}); err == nil {
		t.Error("missing checksums file must fail")
	}
	partial := filepath.Join(dir, "partial.txt")
	line := strings.Repeat("c", 64) + "  " + dist.ArchiveName("1.0.0", "linux", "amd64") + "\n"
	if err := os.WriteFile(partial, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-version", "1.0.0", "-checksums", partial, "-out", dir}); err == nil ||
		!strings.Contains(err.Error(), "homebrew") {
		t.Errorf("incomplete checksums: err = %v", err)
	}
}
