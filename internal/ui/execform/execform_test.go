package execform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenPrefillsImage(t *testing.T) {
	m := New()
	m.Open("alpine:3.20")
	if got := m.Image(); got != "alpine:3.20" {
		t.Errorf("image = %q, want alpine:3.20", got)
	}
	if m.Volumes() != "" || m.Command() != "" {
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
	for _, r := range "busybox" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Image(); got != "busybox" {
		t.Errorf("image = %q, want busybox", got)
	}
	// Move to Command (field 2) and type one.
	m.Next()
	m.Next()
	for _, r := range "ls -la" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Command(); got != "ls -la" {
		t.Errorf("command = %q, want ls -la", got)
	}
}

func TestViewShowsError(t *testing.T) {
	m := New()
	m.Open("")
	m.SetError("boom")
	if got := m.View(100, 30); !strings.Contains(got, "boom") {
		t.Error("view should render the error message")
	}
}
