package filter

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelLifecycle(t *testing.T) {
	m := New()
	m.Focus()
	for _, r := range "web" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Value(); got != "web" {
		t.Errorf("value = %q, want web", got)
	}
	if v := m.View(60); !strings.Contains(v, "/") {
		t.Error("view should render the / prefix")
	}
	m.Blur()
	m.Reset()
	if m.Value() != "" {
		t.Error("Reset should clear the input")
	}
}

func TestViewShowsRegexpError(t *testing.T) {
	m := New()
	m.Focus()
	for _, r := range "re:[" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if v := m.View(80); !strings.Contains(v, "⚠") {
		t.Error("view should warn about the malformed regexp")
	}
}
