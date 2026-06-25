package settings

import (
	"os"
	"path/filepath"
	"testing"

	"d9c/internal/hosts"
	"d9c/internal/keymap"
	"d9c/internal/theme"
	"d9c/internal/ui/styles"
)

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "d9c-config.yaml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if s.HasHosts() {
		t.Error("expected no hosts for a missing file")
	}
	pal, err := s.Palette()
	if err != nil {
		t.Fatalf("Palette: %v", err)
	}
	def, _ := theme.ByName(theme.DefaultName)
	if pal != def {
		t.Errorf("missing file should resolve to the default palette")
	}
	thr, err := s.Alerts()
	if err != nil {
		t.Fatalf("Alerts: %v", err)
	}
	if thr.Active() {
		t.Error("expected disabled alerts for a missing file")
	}
}

func TestLoadResolvesEverySection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d9c-config.yaml")
	const data = `theme: dracula
keys:
  logs: g
alerts:
  cpu: 80
  mem: 90
hosts:
  - name: prod
    host: ssh://user@host
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	pal, err := s.Palette()
	if err != nil {
		t.Fatalf("Palette: %v", err)
	}
	if dracula, _ := theme.ByName("dracula"); pal != dracula {
		t.Error("theme: dracula did not resolve to the dracula palette")
	}

	km, err := s.Keymap()
	if err != nil {
		t.Fatalf("Keymap: %v", err)
	}
	if got := km.KeyFor(keymap.Logs); got != "g" {
		t.Errorf("logs key = %q, want g", got)
	}

	thr, err := s.Alerts()
	if err != nil {
		t.Fatalf("Alerts: %v", err)
	}
	if thr.CPU != 80 || thr.Mem != 90 {
		t.Errorf("thresholds = %+v, want cpu 80 mem 90", thr)
	}

	hs := s.Hosts()
	if h, ok := hs.Find("prod"); !ok || h.Host != "ssh://user@host" {
		t.Errorf("host not loaded: %+v ok=%v", h, ok)
	}
}

// TestHostCRUDPreservesOtherSections is the core unified-file guarantee: editing
// hosts must rewrite the file without losing the theme/keys/alerts sections.
func TestHostCRUDPreservesOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d9c-config.yaml")
	const data = `theme: nord
alerts:
  cpu: 50
  mem: 0
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	hs := s.Hosts()
	if err := hs.Add("lab", "tcp://lab:2375"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := hs.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload from disk: the host is there AND the theme/alerts survived.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reloaded.Hosts().Find("lab"); !ok {
		t.Error("host not persisted into the unified file")
	}
	if pal, _ := reloaded.Palette(); pal != mustTheme(t, "nord") {
		t.Error("theme section was clobbered by a host edit")
	}
	if thr, _ := reloaded.Alerts(); thr.CPU != 50 {
		t.Error("alerts section was clobbered by a host edit")
	}
}

func TestSetThemePersistsAndClearsColors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d9c-config.yaml")
	const data = `theme: nord
colors:
  primary: "#ffffff"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.SetTheme("dracula"); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if pal, _ := reloaded.Palette(); pal != mustTheme(t, "dracula") {
		t.Error("SetTheme did not persist the chosen theme")
	}
	if len(reloaded.File.Colors) != 0 {
		t.Errorf("SetTheme should clear stale color overrides, got %v", reloaded.File.Colors)
	}
}

func TestSetHostsAndMigrationHelpers(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "d9c-config.yaml")}
	if s.HasHosts() {
		t.Fatal("fresh store should have no hosts")
	}
	s.SetHosts([]hosts.Host{{Name: "a", Host: "tcp://a:2375"}})
	if !s.HasHosts() {
		t.Fatal("SetHosts should populate the host list")
	}
}

func mustTheme(t *testing.T, name string) styles.Palette {
	t.Helper()
	p, ok := theme.ByName(name)
	if !ok {
		t.Fatalf("unknown built-in theme %q", name)
	}
	return p
}
