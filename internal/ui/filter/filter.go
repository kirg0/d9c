package filter

import (
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	input textinput.Model
}

func New() Model {
	ti := textinput.New()
	ti.Placeholder = "filter  (text · re:<rx> · status: · label:k[=v] · network:)…"
	ti.CharLimit = 128
	return Model{input: ti}
}

func (m *Model) Focus() {
	m.input.Focus()
}

func (m *Model) Blur() {
	m.input.Blur()
}

func (m *Model) Reset() {
	m.input.Reset()
}

func (m Model) Value() string {
	return m.input.Value()
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) View(width int) string {
	prefix := styles.BottomBarPrefix.Render("/")
	// A malformed regexp (re:<rx>) empties the table; show why inline instead of
	// leaving the user staring at zero rows.
	suffix := ""
	if err := Compile(m.input.Value()).Err(); err != nil {
		suffix = styles.FormError.Render(" ⚠ " + err.Error())
	}
	inputWidth := max(width-lipgloss.Width(prefix)-lipgloss.Width(suffix), 1)
	inputView := styles.BottomBar.Width(inputWidth).Render(m.input.View())
	return lipgloss.JoinHorizontal(lipgloss.Left, prefix, inputView, suffix)
}
