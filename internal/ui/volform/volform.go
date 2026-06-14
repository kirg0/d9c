// Package volform renders the modal "create volume" form shown in the volumes
// view.
package volform

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	fieldCount    = 2 // name, driver
	defaultDriver = "local"
)

// Model is the two-field (Name, Driver) create-volume form.
type Model struct {
	name   textinput.Model
	driver textinput.Model
	focus  int // 0 = name, 1 = driver
	errMsg string
}

// New builds an empty form.
func New() Model {
	n := textinput.New()
	n.Placeholder = "my-vol"
	n.CharLimit = 64
	n.Width = 44

	d := textinput.New()
	d.Placeholder = "local"
	d.CharLimit = 32
	d.Width = 44

	return Model{name: n, driver: d}
}

// Open prepares the form to create a new volume, pre-filled with the default
// driver.
func (m *Model) Open() {
	m.errMsg = ""
	m.name.SetValue("")
	m.driver.SetValue(defaultDriver)
	m.focusField(0)
}

// SetError shows a validation/operation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

func (m *Model) focusField(i int) {
	m.focus = (i%fieldCount + fieldCount) % fieldCount
	m.name.Blur()
	m.driver.Blur()
	if m.focus == 0 {
		m.name.Focus()
		m.name.CursorEnd()
	} else {
		m.driver.Focus()
		m.driver.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// Name returns the trimmed name field.
func (m Model) Name() string { return strings.TrimSpace(m.name.Value()) }

// Driver returns the trimmed driver field.
func (m Model) Driver() string { return strings.TrimSpace(m.driver.Value()) }

// Update forwards key events to the focused field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.focus == 0 {
		m.name, cmd = m.name.Update(msg)
	} else {
		m.driver, cmd = m.driver.Update(msg)
	}
	return m, cmd
}

// View renders the form centered within the given area.
func (m Model) View(width, height int) string {
	field := func(label string, ti textinput.Model, active bool) string {
		labelStyle := styles.FormLabel
		marker := "  "
		if active {
			labelStyle = styles.FormLabelActive
			marker = "▸ "
		}
		return marker + labelStyle.Render(label) + "\n  " + ti.View()
	}

	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Create volume "))
	b.WriteString("\n\n")
	b.WriteString(field("Name", m.name, m.focus == 0))
	b.WriteString("\n\n")
	b.WriteString(field("Driver", m.driver, m.focus == 1))
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("tab switch · enter create · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
