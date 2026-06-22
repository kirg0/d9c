package pullform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenClearsState(t *testing.T) {
	m := New()
	m.image.SetValue("stale")
	m.SetError("boom")
	m.Open()
	if m.Image() != "" {
		t.Errorf("image after Open = %q, want empty", m.Image())
	}
	if m.errMsg != "" {
		t.Errorf("errMsg after Open = %q, want empty", m.errMsg)
	}
}

func TestTypingRoutesToImage(t *testing.T) {
	m := New()
	m.Open()
	for _, r := range "nginx:1.25" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Image(); got != "nginx:1.25" {
		t.Errorf("image = %q, want nginx:1.25", got)
	}
}

func TestImageTrimsWhitespace(t *testing.T) {
	m := New()
	m.Open()
	m.image.SetValue("  redis  ")
	if got := m.Image(); got != "redis" {
		t.Errorf("image = %q, want redis", got)
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
