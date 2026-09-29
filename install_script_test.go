package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRelease serves the subset of the GitHub releases layout install.sh
// uses: /latest (redirect to /tag/<tag>) and /download/<tag>/<asset>.
type fakeRelease struct {
	tag     string
	archive []byte
	sums    string
}

func newFakeRelease(t *testing.T, tag, goos, goarch string) *fakeRelease {
	t.Helper()
	stage := fmt.Sprintf("d9c_%s_%s_%s", tag, goos, goarch)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho d9c " + tag + "\n")
	for _, f := range []struct {
		name string
		mode int64
		data []byte
	}{
		{stage + "/d9c", 0o755, body},
		{stage + "/README.md", 0o644, []byte("readme")},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	sums := fmt.Sprintf("%x  %s.tar.gz\n%s  other.zip\n", sum, stage, strings.Repeat("0", 64))
	return &fakeRelease{tag: tag, archive: buf.Bytes(), sums: sums}
}

func (f *fakeRelease) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tag/"+f.tag, http.StatusFound)
	})
	mux.HandleFunc("/tag/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("release page"))
	})
	mux.HandleFunc("/download/"+f.tag+"/", func(w http.ResponseWriter, r *http.Request) {
		switch name := filepath.Base(r.URL.Path); {
		case name == "checksums.txt":
			_, _ = w.Write([]byte(f.sums))
		case strings.HasSuffix(name, ".tar.gz") && strings.Contains(f.sums, name):
			_, _ = w.Write(f.archive)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

// runInstaller runs install.sh with the given environment; it skips when the
// POSIX tools the script needs are missing (e.g. a bare Windows box).
func runInstaller(t *testing.T, env ...string) (string, error) {
	t.Helper()
	for _, tool := range []string{"sh", "tar", "curl", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallScript(t *testing.T) {
	rel := newFakeRelease(t, "v9.9.9", "linux", "amd64")
	srv := httptest.NewServer(rel.handler())
	defer srv.Close()

	tests := []struct {
		name    string
		env     []string
		wantErr string // empty = success
	}{
		{name: "latest release", env: []string{"D9C_ARCH=x86_64"}},
		{name: "explicit version without v", env: []string{"D9C_VERSION=9.9.9", "D9C_ARCH=amd64"}},
		{name: "unsupported arch", env: []string{"D9C_ARCH=mips"}, wantErr: "unsupported architecture"},
		{name: "unsupported os", env: []string{"D9C_OS=Plan9", "D9C_ARCH=amd64"}, wantErr: "unsupported OS"},
		{name: "asset not released", env: []string{"D9C_ARCH=arm64"}, wantErr: "download failed"},
		{name: "unknown version", env: []string{"D9C_VERSION=v1.0.0", "D9C_ARCH=amd64"}, wantErr: "download failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.ToSlash(t.TempDir())
			env := append([]string{"D9C_BASE_URL=" + srv.URL, "D9C_INSTALL_DIR=" + dir, "D9C_OS=linux"}, tt.env...)
			out, err := runInstaller(t, env...)
			bin := filepath.Join(dir, "d9c")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(out, tt.wantErr) {
					t.Fatalf("want failure containing %q, got err=%v\n%s", tt.wantErr, err, out)
				}
				if _, statErr := os.Stat(bin); statErr == nil {
					t.Error("binary installed despite failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, out)
			}
			if !strings.Contains(out, "Checksum OK") || !strings.Contains(out, "Installed d9c v9.9.9") {
				t.Errorf("unexpected output:\n%s", out)
			}
			got, readErr := os.ReadFile(bin)
			if readErr != nil || !strings.Contains(string(got), "echo d9c v9.9.9") {
				t.Errorf("installed binary: err=%v content=%q", readErr, got)
			}
		})
	}
}

func TestInstallScriptChecksumMismatch(t *testing.T) {
	rel := newFakeRelease(t, "v9.9.9", "linux", "amd64")
	rel.archive = append(rel.archive, 0) // tampered after the checksum was taken
	srv := httptest.NewServer(rel.handler())
	defer srv.Close()

	dir := filepath.ToSlash(t.TempDir())
	out, err := runInstaller(t, "D9C_BASE_URL="+srv.URL, "D9C_INSTALL_DIR="+dir, "D9C_OS=linux", "D9C_ARCH=amd64")
	if err == nil || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got err=%v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "d9c")); statErr == nil {
		t.Error("tampered binary was installed")
	}
}
