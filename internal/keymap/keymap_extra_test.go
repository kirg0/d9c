package keymap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestActionsStableAndDetached(t *testing.T) {
	a := Actions()
	if len(a) == 0 {
		t.Fatal("Actions should not be empty")
	}
	if len(a) != len(Names()) {
		t.Errorf("Actions (%d) and Names (%d) should agree", len(a), len(Names()))
	}
	// The returned slice is a copy: mutating it must not affect the next call.
	a[0] = Action("mutated")
	if Actions()[0] == "mutated" {
		t.Error("Actions should return a detached copy")
	}
}

func TestLoadFileBranches(t *testing.T) {
	dir := t.TempDir()

	// Missing file: defaults.
	m, err := Load(filepath.Join(dir, "ghost.yaml"))
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if m.KeyFor(Copy) != Default().KeyFor(Copy) {
		t.Error("missing file should yield defaults")
	}

	// Valid override.
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("keys:\n  copy: Y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err = Load(path)
	if err != nil || m.KeyFor(Copy) != "Y" {
		t.Errorf("override = %q / %v", m.KeyFor(Copy), err)
	}

	// Malformed YAML.
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("malformed YAML should fail")
	}
}
