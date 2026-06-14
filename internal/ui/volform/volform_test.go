package volform

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
	if m.Name() != "" {
		t.Errorf("name should be empty after Open, got %q", m.Name())
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
	for _, r := range "data" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Name(); got != "data" {
		t.Errorf("name = %q, want data", got)
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
