package buildform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenPrefills(t *testing.T) {
	m := New()
	m.Open("./ctx", "app:latest")
	if got := m.Dir(); got != "./ctx" {
		t.Errorf("dir = %q, want ./ctx", got)
	}
	if got := m.Tag(); got != "app:latest" {
		t.Errorf("tag = %q, want app:latest", got)
	}
	if m.focus != 0 {
		t.Errorf("focus after Open = %d, want 0", m.focus)
	}
}

func TestOpenResetsError(t *testing.T) {
	m := New()
	m.SetError("boom")
	m.Open("", "")
	if m.errMsg != "" {
		t.Errorf("errMsg after Open = %q, want empty", m.errMsg)
	}
}

func TestNextPrevWraps(t *testing.T) {
	m := New()
	m.Open("", "")
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
	m.Open("", "")
	for _, r := range "./app" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Dir(); got != "./app" {
		t.Errorf("dir = %q, want ./app", got)
	}
	m.Next()
	for _, r := range "img:v1" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Tag(); got != "img:v1" {
		t.Errorf("tag = %q, want img:v1", got)
	}
}

func TestGettersTrim(t *testing.T) {
	m := New()
	m.Open("  ./ctx  ", "  app:v2  ")
	if got := m.Dir(); got != "./ctx" {
		t.Errorf("Dir should trim, got %q", got)
	}
	if got := m.Tag(); got != "app:v2" {
		t.Errorf("Tag should trim, got %q", got)
	}
}

func TestViewShowsFieldsAndError(t *testing.T) {
	m := New()
	m.Open("", "")
	m.SetError("boom")
	got := m.View(80, 24)
	if !strings.Contains(got, "Build image") ||
		!strings.Contains(got, "Context dir") ||
		!strings.Contains(got, "Tag") ||
		!strings.Contains(got, "boom") {
		t.Error("view should render title, both labels and the error message")
	}
	// Active-field marker on second field after Next.
	m.Next()
	if !strings.Contains(m.View(80, 24), "▸") {
		t.Error("view should render the active-field marker")
	}
}
