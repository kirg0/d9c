package help

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpScrolls(t *testing.T) {
	m := New()
	m.SetSize(40, 5)
	m.SetContent(strings.Repeat("line\n", 50))
	if v := m.View(); !strings.Contains(v, "line") {
		t.Error("view should render the content")
	}
	before := m.viewport.YOffset
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.viewport.YOffset <= before {
		t.Error("down key should scroll the viewport")
	}
}
