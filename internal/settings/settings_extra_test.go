package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	if got := DefaultPath(); !strings.HasSuffix(got, "d9c-config.yaml") {
		t.Errorf("DefaultPath = %q", got)
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d9c-config.yaml")

	// Missing file: empty store bound to the path.
	s, err := Load(path)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if s.Path() != path {
		t.Errorf("Path = %q", s.Path())
	}
	if s.HasHosts() {
		t.Error("fresh store should have no hosts")
	}

	// Save writes the file; a reload sees the data.
	if err := s.SetTheme("dracula"); err != nil {
		t.Fatalf("set theme: %v", err)
	}
	s2, err := Load(path)
	if err != nil || s2.File.Theme != "dracula" {
		t.Errorf("reload = %+v / %v", s2.File, err)
	}

	// Malformed YAML is an error.
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("malformed YAML should fail")
	}

	// Save into a non-existent directory fails.
	bad := &Store{path: filepath.Join(t.TempDir(), "ghost", "sub", "cfg.yaml")}
	if err := bad.Save(); err == nil {
		t.Error("save into missing directory should fail")
	}
}
