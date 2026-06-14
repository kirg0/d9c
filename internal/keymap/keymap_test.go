package keymap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultBindings(t *testing.T) {
	m := Default()
	cases := []struct {
		action Action
		key    string
	}{
		{Inspect, "i"},
		{Logs, "l"},
		{Filter, "/"},
		{Command, ":"},
		{Select, " "},
		{Refresh, "r"},
		{Help, "?"},
	}
	for _, c := range cases {
		if got := m.KeyFor(c.action); got != c.key {
			t.Errorf("KeyFor(%q) = %q, want %q", c.action, got, c.key)
		}
		if a, ok := m.ActionFor(c.key); !ok || a != c.action {
			t.Errorf("ActionFor(%q) = %q,%v, want %q,true", c.key, a, ok, c.action)
		}
	}
}

func TestResolveOverrides(t *testing.T) {
	m, err := Resolve(map[string]string{"logs": "g", "select": "space"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := m.KeyFor(Logs); got != "g" {
		t.Errorf("logs key = %q, want g", got)
	}
	// "g" now triggers logs; the old "l" no longer maps to anything.
	if a, ok := m.ActionFor("g"); !ok || a != Logs {
		t.Errorf("ActionFor(g) = %q,%v, want logs,true", a, ok)
	}
	if _, ok := m.ActionFor("l"); ok {
		t.Error("ActionFor(l) should be unbound after remapping logs")
	}
	// "space" alias normalizes to the bubbletea space key string.
	if got := m.KeyFor(Select); got != " " {
		t.Errorf("select key = %q, want space (\" \")", got)
	}
	// Untouched actions keep their defaults.
	if got := m.KeyFor(Inspect); got != "i" {
		t.Errorf("inspect key = %q, want i (unchanged)", got)
	}
}

func TestResolveErrors(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		wantSub   string
	}{
		{"unknown action", map[string]string{"frobnicate": "z"}, "unknown action"},
		{"empty key", map[string]string{"logs": "   "}, "empty key"},
		{"reserved enter", map[string]string{"logs": "enter"}, "reserved"},
		{"reserved q", map[string]string{"refresh": "q"}, "reserved"},
		{"conflict between overrides", map[string]string{"logs": "z", "stats": "z"}, "bound to both"},
		{"conflict with default", map[string]string{"refresh": "i"}, "bound to both"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Resolve(c.overrides)
			if err == nil {
				t.Fatalf("Resolve(%v) = nil error, want error containing %q", c.overrides, c.wantSub)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error = %q, want substring %q", err, c.wantSub)
			}
		})
	}
}

func TestResolveNilYieldsDefaults(t *testing.T) {
	m, err := Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve(nil): %v", err)
	}
	if m.KeyFor(Logs) != "l" {
		t.Error("Resolve(nil) should equal the defaults")
	}
}

func TestLoadMissingFileIsDefaults(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load(missing): %v", err)
	}
	if m.KeyFor(Filter) != "/" {
		t.Error("missing config should yield default bindings")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d9c-config.yaml")
	const data = "theme: dracula\nkeys:\n  filter: f\n  logs: g\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.KeyFor(Filter) != "f" {
		t.Errorf("filter key = %q, want f", m.KeyFor(Filter))
	}
	if m.KeyFor(Logs) != "g" {
		t.Errorf("logs key = %q, want g", m.KeyFor(Logs))
	}
}

func TestLoadInvalidConfigErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d9c-config.yaml")
	if err := os.WriteFile(path, []byte("keys:\n  logs: q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load should reject binding an action to a reserved key")
	}
}

func TestDisplay(t *testing.T) {
	m := Default()
	if got := m.Display(Select); got != "space" {
		t.Errorf("Display(Select) = %q, want space", got)
	}
	if got := m.Display(Logs); got != "l" {
		t.Errorf("Display(Logs) = %q, want l", got)
	}
}
