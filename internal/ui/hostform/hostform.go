// Package hostform renders the modal add/edit form for the hosts view.
package hostform

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const defaultHostValue = "ssh://user@host"

// Model is the two-field (Name, Host) modal form.
type Model struct {
	name     textinput.Model
	host     textinput.Model
	focus    int // 0 = name, 1 = host
	title    string
	editing  bool
	origName string
	errMsg   string
}

// New builds an empty form.
func New() Model {
	n := textinput.New()
	n.Placeholder = "my-host"
	n.CharLimit = 64
	n.Width = 44

	h := textinput.New()
	h.Placeholder = "ssh://user@host or tcp://host:2375"
	h.CharLimit = 256
	h.Width = 44

	return Model{name: n, host: h}
}

// OpenAdd prepares the form to create a new host, pre-filled with base defaults.
func (m *Model) OpenAdd() {
	m.editing = false
	m.origName = ""
	m.title = "Add host"
	m.name.SetValue("")
	m.host.SetValue(defaultHostValue)
	m.errMsg = ""
	m.focusField(0)
}

// OpenEdit prepares the form to edit an existing host, pre-filled with its values.
func (m *Model) OpenEdit(name, host string) {
	m.editing = true
	m.origName = name
	m.title = "Edit host"
	m.name.SetValue(name)
	m.host.SetValue(host)
	m.errMsg = ""
	m.focusField(0)
}

// SetError shows a validation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

func (m *Model) focusField(i int) {
	m.focus = (i%2 + 2) % 2
	if m.focus == 0 {
		m.name.Focus()
		m.host.Blur()
		m.name.CursorEnd()
	} else {
		m.host.Focus()
		m.name.Blur()
		m.host.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// Name returns the trimmed name field.
func (m Model) Name() string { return strings.TrimSpace(m.name.Value()) }

// Host returns the trimmed host field.
func (m Model) Host() string { return strings.TrimSpace(m.host.Value()) }

// OrigName returns the name of the host being edited (empty when adding).
func (m Model) OrigName() string { return m.origName }

// IsEditing reports whether the form is editing an existing host.
func (m Model) IsEditing() bool { return m.editing }

// Update forwards key events to the focused field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.focus == 0 {
		m.name, cmd = m.name.Update(msg)
	} else {
		m.host, cmd = m.host.Update(msg)
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
	b.WriteString(styles.FormTitle.Render(" " + m.title + " "))
	b.WriteString("\n\n")
	b.WriteString(field("Name", m.name, m.focus == 0))
	b.WriteString("\n\n")
	b.WriteString(field("Host", m.host, m.focus == 1))
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("tab switch · enter save · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
