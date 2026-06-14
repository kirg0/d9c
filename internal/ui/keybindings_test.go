package ui

import (
	"strings"
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/keymap"

	tea "github.com/charmbracelet/bubbletea"
)

// newKeysTestModel builds a containers-view model primed with sample data so
// key handling has rows to act on.
func newKeysTestModel(t *testing.T) Model {
	t.Helper()
	fb := docker.NewFakeBackend()
	m := NewModel(&config.Config{}, fb, nil, nil, false)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	cs, _ := fb.ListContainers(false)
	tm, _ = tm.Update(containersUpdatedMsg{cs})
	return tm.(Model)
}

func pressRune(t *testing.T, m Model, r rune) Model {
	t.Helper()
	var tm tea.Model = m
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return tm.(Model)
}

// TestDefaultFilterKey confirms the out-of-the-box "/" opens the filter.
func TestDefaultFilterKey(t *testing.T) {
	m := newKeysTestModel(t)
	if got := pressRune(t, m, '/'); got.mode != ModeFilter {
		t.Errorf("'/' mode = %v, want ModeFilter", got.mode)
	}
}

// TestRemappedFilterKey remaps filter to "f": the new key opens the filter and
// the old "/" no longer does.
func TestRemappedFilterKey(t *testing.T) {
	km, err := keymap.Resolve(map[string]string{"filter": "f"})
	if err != nil {
		t.Fatal(err)
	}
	m := newKeysTestModel(t)
	m.SetKeymap(km)

	if got := pressRune(t, m, 'f'); got.mode != ModeFilter {
		t.Errorf("remapped 'f' mode = %v, want ModeFilter", got.mode)
	}
	if got := pressRune(t, m, '/'); got.mode != ModeNormal {
		t.Errorf("old '/' mode = %v, want ModeNormal (unbound)", got.mode)
	}
}

// TestRemappedHelpReflectsKeys verifies the help screen shows the configured
// (remapped) keys, not the hard-coded defaults.
func TestRemappedHelpReflectsKeys(t *testing.T) {
	km, err := keymap.Resolve(map[string]string{"logs": "g", "stats": "z"})
	if err != nil {
		t.Fatal(err)
	}
	m := newKeysTestModel(t)
	m.SetKeymap(km)
	help := m.buildHelpContent()
	if !strings.Contains(help, "Логи") {
		t.Fatal("help is missing the logs row")
	}
	// The containers section row for logs should advertise "g" now.
	for _, r := range m.resourceKeyRows() {
		if r.desc == "Логи" && r.key != "g" {
			t.Errorf("logs help key = %q, want g", r.key)
		}
		if strings.HasPrefix(r.desc, "Метрики") && r.key != "z" {
			t.Errorf("stats help key = %q, want z", r.key)
		}
	}
}
