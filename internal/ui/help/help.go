// Package help renders the scrollable keyboard/command reference overlay.
package help

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Model is a simple scrollable text viewer for the help screen.
type Model struct {
	viewport viewport.Model
	content  string
}

func New() Model {
	return Model{viewport: viewport.New(0, 0)}
}

// SetSize resizes the viewport and re-applies the current content.
func (m *Model) SetSize(width, height int) {
	m.viewport.Width = width
	m.viewport.Height = height
	m.viewport.SetContent(m.content)
}

// SetContent replaces the help text and scrolls back to the top.
func (m *Model) SetContent(s string) {
	m.content = s
	m.viewport.SetContent(s)
	m.viewport.GotoTop()
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) View() string { return m.viewport.View() }
