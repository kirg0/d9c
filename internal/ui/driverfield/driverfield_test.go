package driverfield

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestNewSelectsFirstPreset(t *testing.T) {
	m := New([]string{"bridge", "host"})
	if got := m.Value(); got != "bridge" {
		t.Errorf("default value = %q, want bridge", got)
	}
	if m.IsCustom() {
		t.Error("first preset should not be custom")
	}
}

func TestSetMatchesPreset(t *testing.T) {
	m := New([]string{"bridge", "host", "overlay"})
	m.Set("overlay")
	if got := m.Value(); got != "overlay" {
		t.Errorf("value = %q, want overlay", got)
	}
}

func TestSetUnknownGoesCustom(t *testing.T) {
	m := New([]string{"local"})
	m.Set("nfs")
	if !m.IsCustom() {
		t.Fatal("unknown driver should select custom")
	}
	if got := m.Value(); got != "nfs" {
		t.Errorf("custom value = %q, want nfs", got)
	}
}

func TestSetEmptySelectsFirst(t *testing.T) {
	m := New([]string{"bridge", "host"})
	m.Set("host")
	m.Set("")
	if got := m.Value(); got != "bridge" {
		t.Errorf("value after Set(\"\") = %q, want bridge", got)
	}
}

func TestCyclingWraps(t *testing.T) {
	m := New([]string{"bridge", "host"}) // options: bridge, host, custom…
	m.Prev()                             // wrap to last (custom…)
	if !m.IsCustom() {
		t.Error("Prev from first should wrap to custom")
	}
	m.Next() // wrap back to first
	if got := m.Value(); got != "bridge" {
		t.Errorf("value after wrap = %q, want bridge", got)
	}
}

func TestArrowsCycleViaUpdate(t *testing.T) {
	m := New([]string{"bridge", "host"})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := m.Value(); got != "host" {
		t.Errorf("value after right = %q, want host", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := m.Value(); got != "bridge" {
		t.Errorf("value after left = %q, want bridge", got)
	}
}

func TestTypingInCustom(t *testing.T) {
	m := New([]string{"local"})
	m.Focus()
	m.Next() // move to custom…
	if !m.IsCustom() {
		t.Fatal("expected custom selected")
	}
	for _, r := range "rexray" {
		m, _ = m.Update(key(string(r)))
	}
	if got := m.Value(); got != "rexray" {
		t.Errorf("custom value = %q, want rexray", got)
	}
}

func TestTypingIgnoredOnPreset(t *testing.T) {
	m := New([]string{"bridge", "host"})
	m.Focus() // bridge selected, not custom
	m, _ = m.Update(key("x"))
	if got := m.Value(); got != "bridge" {
		t.Errorf("typing on a preset should not change value, got %q", got)
	}
}

func TestViewShowsOptions(t *testing.T) {
	m := New([]string{"bridge", "host"})
	out := m.View(true)
	for _, want := range []string{"bridge", "host", CustomLabel} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}
