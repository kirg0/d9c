// Package connform renders the modal credential prompt shown when connecting to
// an SSH host configured for password authentication. The login is pre-filled
// from the saved host (and remains editable); the password is never stored.
package connform

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the two-field (Login, Password) credential prompt.
type Model struct {
	login    textinput.Model
	password textinput.Model
	focus    int // 0 = login, 1 = password
	hostName string
	hostURL  string
	errMsg   string
}

// New builds an empty credential prompt.
func New() Model {
	l := textinput.New()
	l.Placeholder = "login"
	l.CharLimit = 128
	l.Width = 40

	p := textinput.New()
	p.Placeholder = "password"
	p.CharLimit = 256
	p.Width = 40
	p.EchoMode = textinput.EchoPassword
	p.EchoCharacter = '•'

	return Model{login: l, password: p}
}

// Open prepares the prompt for a host, pre-filling the login and focusing the
// password field when a login is already known (otherwise the login field).
func (m *Model) Open(hostName, hostURL, login string) {
	m.hostName = hostName
	m.hostURL = hostURL
	m.login.SetValue(login)
	m.password.SetValue("")
	m.errMsg = ""
	if login != "" {
		m.focusField(1)
	} else {
		m.focusField(0)
	}
}

// SetError shows a validation message inside the prompt (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

func (m *Model) focusField(i int) {
	m.focus = (i%2 + 2) % 2
	if m.focus == 0 {
		m.login.Focus()
		m.password.Blur()
		m.login.CursorEnd()
	} else {
		m.password.Focus()
		m.login.Blur()
		m.password.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// Login returns the trimmed login field.
func (m Model) Login() string { return strings.TrimSpace(m.login.Value()) }

// Password returns the password field verbatim (not trimmed: spaces may matter).
func (m Model) Password() string { return m.password.Value() }

// HostName returns the saved host's name.
func (m Model) HostName() string { return m.hostName }

// HostURL returns the saved host's URL.
func (m Model) HostURL() string { return m.hostURL }

// Update forwards key events to the focused field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.focus == 0 {
		m.login, cmd = m.login.Update(msg)
	} else {
		m.password, cmd = m.password.Update(msg)
	}
	return m, cmd
}

// View renders the prompt centered within the given area.
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
	b.WriteString(styles.FormTitle.Render(" Connect to " + m.hostName + " "))
	b.WriteString("\n\n")
	b.WriteString(field("Login", m.login, m.focus == 0))
	b.WriteString("\n\n")
	b.WriteString(field("Password", m.password, m.focus == 1))
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("tab switch · enter connect · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
