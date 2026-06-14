package runform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenPrefillsImage(t *testing.T) {
	m := New()
	m.Open("nginx:1.25")
	if got := m.Image(); got != "nginx:1.25" {
		t.Errorf("image = %q, want nginx:1.25", got)
	}
	if m.Name() != "" || m.Ports() != "" || m.Env() != "" || m.Volumes() != "" {
		t.Error("optional fields must be empty after Open")
	}
	// Re-opening clears previous values.
	m.Open("")
	if m.Image() != "" {
		t.Errorf("image after reopen = %q, want empty", m.Image())
	}
}

func TestNextPrevWraps(t *testing.T) {
	m := New()
	m.Open("") // focus 0
	m.Prev()   // wraps to last field
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
	m.Open("")
	for _, r := range "redis:7" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Image(); got != "redis:7" {
		t.Errorf("image = %q, want redis:7", got)
	}
	// Move to Ports (field 2) and type a mapping.
	m.Next()
	m.Next()
	for _, r := range "6379:6379" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Ports(); got != "6379:6379" {
		t.Errorf("ports = %q, want 6379:6379", got)
	}
}

func TestViewShowsError(t *testing.T) {
	m := New()
	m.Open("")
	m.SetError("boom")
	if got := m.View(100, 40); !strings.Contains(got, "boom") {
		t.Error("view should render the error message")
	}
}
