package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"d9c/internal/ui/styles"
)

func TestDefaultPathTheme(t *testing.T) {
	if got := DefaultPath(); !strings.HasSuffix(got, "d9c-config.yaml") {
		t.Errorf("DefaultPath = %q", got)
	}
}

func TestLoadBranches(t *testing.T) {
	dir := t.TempDir()

	// Missing file: default palette.
	pal, err := Load(filepath.Join(dir, "ghost.yaml"))
	if err != nil || pal != styles.DefaultPalette() {
		t.Errorf("missing file = %v / %v, want default palette", pal, err)
	}

	// Valid file with a theme and an override.
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("theme: dracula\ncolors:\n  primary: \"#ff0000\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pal, err = Load(path)
	if err != nil || pal.Primary != "#ff0000" {
		t.Errorf("valid file = %+v / %v", pal, err)
	}

	// Malformed YAML.
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("malformed YAML should fail")
	}
}

func TestFieldForAllKeys(t *testing.T) {
	pal := styles.DefaultPalette()
	for _, key := range colorKeys() {
		if fieldFor(&pal, key) == nil {
			t.Errorf("fieldFor(%q) = nil, want a palette field", key)
		}
	}
	if fieldFor(&pal, "nope") != nil {
		t.Error("unknown key should map to nil")
	}
}
