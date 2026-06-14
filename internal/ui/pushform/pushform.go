// Package pushform renders the modal registry-credentials form shown before
// pushing an image to a private registry.
package pushform

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const fieldCount = 3 // registry, username, password

// Model is the three-field (Registry, Username, Password) modal form. An empty
// username submits an anonymous push.
type Model struct {
	registry textinput.Model
	username textinput.Model
	password textinput.Model
	focus    int // 0 = registry, 1 = username, 2 = password
	ref      string
	errMsg   string
}

// New builds an empty form with a masked password field.
func New() Model {
	r := textinput.New()
	r.Placeholder = "registry host (empty = docker.io)"
	r.CharLimit = 256
	r.Width = 44

	u := textinput.New()
	u.Placeholder = "username (empty = anonymous)"
	u.CharLimit = 128
	u.Width = 44

	p := textinput.New()
	p.Placeholder = "password / token"
	p.CharLimit = 256
	p.Width = 44
	p.EchoMode = textinput.EchoPassword
	p.EchoCharacter = '•'

	return Model{registry: r, username: u, password: p}
}

// Open prepares the form to push ref, pre-filled with the inferred registry and
// any remembered credentials for that registry.
func (m *Model) Open(ref, registry, username, password string) {
	m.ref = ref
	m.errMsg = ""
	m.registry.SetValue(registry)
	m.username.SetValue(username)
	m.password.SetValue(password)
	m.focusField(0)
}

// SetError shows a validation/operation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

func (m *Model) focusField(i int) {
	m.focus = (i%fieldCount + fieldCount) % fieldCount
	m.registry.Blur()
	m.username.Blur()
	m.password.Blur()
	switch m.focus {
	case 0:
		m.registry.Focus()
		m.registry.CursorEnd()
	case 1:
		m.username.Focus()
		m.username.CursorEnd()
	default:
		m.password.Focus()
		m.password.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// Ref returns the image reference being pushed.
func (m Model) Ref() string { return m.ref }

// Registry returns the trimmed registry field.
func (m Model) Registry() string { return strings.TrimSpace(m.registry.Value()) }

// Username returns the trimmed username field.
func (m Model) Username() string { return strings.TrimSpace(m.username.Value()) }

// Password returns the password field verbatim (not trimmed — tokens may have
// significant characters, though leading/trailing spaces are unusual).
func (m Model) Password() string { return m.password.Value() }

// Update forwards key events to the focused field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focus {
	case 0:
		m.registry, cmd = m.registry.Update(msg)
	case 1:
		m.username, cmd = m.username.Update(msg)
	default:
		m.password, cmd = m.password.Update(msg)
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
	b.WriteString(styles.FormTitle.Render(" Push: " + m.ref + " "))
	b.WriteString("\n\n")
	b.WriteString(field("Registry", m.registry, m.focus == 0))
	b.WriteString("\n\n")
	b.WriteString(field("Username", m.username, m.focus == 1))
	b.WriteString("\n\n")
	b.WriteString(field("Password", m.password, m.focus == 2))
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("tab switch · enter push · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
