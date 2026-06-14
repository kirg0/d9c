package netform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenDefaults(t *testing.T) {
	m := New()
	m.Open()
	if got := m.Driver(); got != defaultDriver {
		t.Errorf("driver = %q, want %q", got, defaultDriver)
	}
	if m.Name() != "" || m.Subnet() != "" || m.Gateway() != "" {
		t.Errorf("name/subnet/gateway should be empty after Open, got %q/%q/%q", m.Name(), m.Subnet(), m.Gateway())
	}
}

func TestNextPrevWraps(t *testing.T) {
	m := New()
	m.Open() // focus 0
	m.Prev() // wraps to last field
	if m.focus != fieldCount-1 {
		t.Errorf("focus after Prev from 0 = %d, want %d", m.focus, fieldCount-1)
	}
	m.Next() // back to 0
	if m.focus != 0 {
		t.Errorf("focus after Next = %d, want 0", m.focus)
	}
}

func TestTypingRoutesToFocusedField(t *testing.T) {
	m := New()
	m.Open() // name focused
	for _, r := range "mynet" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Name(); got != "mynet" {
		t.Errorf("name = %q, want mynet", got)
	}
	// Move to subnet (field 2) and type a CIDR.
	m.Next()
	m.Next()
	for _, r := range "10.0.0.0/24" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Subnet(); got != "10.0.0.0/24" {
		t.Errorf("subnet = %q, want 10.0.0.0/24", got)
	}
}

func TestViewShowsError(t *testing.T) {
	m := New()
	m.Open()
	m.SetError("boom")
	if got := m.View(80, 24); !strings.Contains(got, "boom") {
		t.Error("view should render the error message")
	}
}
