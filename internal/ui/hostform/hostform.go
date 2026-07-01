// Package hostform renders the modal add/edit form for the hosts view.
package hostform

import (
	"strings"

	"d9c/internal/hosts"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const defaultHostValue = "ssh://user@host"

// Field identifiers for the dynamic focus order.
const (
	fName = iota
	fHost
	fAuth
	fKeyPath
)

// Model is the add/edit modal form. Beyond Name/Host it offers an SSH auth
// selector (key vs. password) and, for key auth, an optional private-key path —
// these extra fields appear only while the Host URL is ssh://.
type Model struct {
	name     textinput.Model
	host     textinput.Model
	keyPath  textinput.Model
	auth     string // hosts.SSHAuthKey or hosts.SSHAuthPassword
	focus    int    // index into fields()
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

	k := textinput.New()
	k.Placeholder = "key path (empty = ssh-agent/default keys)"
	k.CharLimit = 256
	k.Width = 44

	return Model{name: n, host: h, keyPath: k, auth: hosts.SSHAuthKey}
}

// OpenAdd prepares the form to create a new host, pre-filled with base defaults.
func (m *Model) OpenAdd() {
	m.editing = false
	m.origName = ""
	m.title = "Add host"
	m.name.SetValue("")
	m.host.SetValue(defaultHostValue)
	m.keyPath.SetValue("")
	m.auth = hosts.SSHAuthKey
	m.errMsg = ""
	m.focusField(0)
}

// OpenEdit prepares the form to edit an existing host, pre-filled from h.
func (m *Model) OpenEdit(h hosts.Host) {
	m.editing = true
	m.origName = h.Name
	m.title = "Edit host"
	m.name.SetValue(h.Name)
	m.host.SetValue(h.Host)
	m.keyPath.SetValue(h.SSHKeyPath)
	m.auth = hosts.SSHAuthPassword
	if h.SSHAuth != hosts.SSHAuthPassword {
		m.auth = hosts.SSHAuthKey
	}
	m.errMsg = ""
	m.focusField(0)
}

// SetError shows a validation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

// isSSH reports whether the Host URL is reached over SSH (ssh:// or
// nerdctl+ssh://), so the auth fields apply.
func (m Model) isSSH() bool {
	return hosts.IsSSH(strings.TrimSpace(m.host.Value()))
}

// fields returns the focusable field ids in order, given the current Host URL
// and auth selection (auth/key-path appear only for ssh:// + key auth).
func (m Model) fields() []int {
	f := []int{fName, fHost}
	if m.isSSH() {
		f = append(f, fAuth)
		if m.auth == hosts.SSHAuthKey {
			f = append(f, fKeyPath)
		}
	}
	return f
}

// current returns the field id under focus.
func (m Model) current() int {
	fields := m.fields()
	if m.focus < 0 || m.focus >= len(fields) {
		return fName
	}
	return fields[m.focus]
}

func (m *Model) focusField(i int) {
	n := len(m.fields())
	m.focus = (i%n + n) % n
	field := m.current()
	m.name.Blur()
	m.host.Blur()
	m.keyPath.Blur()
	switch field {
	case fName:
		m.name.Focus()
		m.name.CursorEnd()
	case fHost:
		m.host.Focus()
		m.host.CursorEnd()
	case fKeyPath:
		m.keyPath.Focus()
		m.keyPath.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// ToggleAuth flips the SSH auth selector between key and password. It is a no-op
// unless the auth field is focused.
func (m *Model) ToggleAuth() {
	if m.current() != fAuth {
		return
	}
	if m.auth == hosts.SSHAuthKey {
		m.auth = hosts.SSHAuthPassword
	} else {
		m.auth = hosts.SSHAuthKey
	}
}

// OnAuthField reports whether the SSH auth selector is currently focused.
func (m Model) OnAuthField() bool { return m.current() == fAuth }

// Name returns the trimmed name field.
func (m Model) Name() string { return strings.TrimSpace(m.name.Value()) }

// Host returns the trimmed host field.
func (m Model) Host() string { return strings.TrimSpace(m.host.Value()) }

// Auth returns the selected SSH auth method, or "" for non-ssh hosts.
func (m Model) Auth() string {
	if !m.isSSH() {
		return ""
	}
	return m.auth
}

// KeyPath returns the trimmed key path (only meaningful for ssh:// + key auth).
func (m Model) KeyPath() string {
	if !m.isSSH() || m.auth != hosts.SSHAuthKey {
		return ""
	}
	return strings.TrimSpace(m.keyPath.Value())
}

// Result assembles the edited host record (with normalised auth metadata).
func (m Model) Result() hosts.Host {
	return hosts.Host{
		Name:       m.Name(),
		Host:       m.Host(),
		SSHAuth:    m.Auth(),
		SSHKeyPath: m.KeyPath(),
	}
}

// OrigName returns the name of the host being edited (empty when adding).
func (m Model) OrigName() string { return m.origName }

// IsEditing reports whether the form is editing an existing host.
func (m Model) IsEditing() bool { return m.editing }

// Update forwards key events to the focused text field. The auth selector field
// carries no text input (it is driven by ToggleAuth from the parent handler).
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.current() {
	case fName:
		m.name, cmd = m.name.Update(msg)
	case fHost:
		m.host, cmd = m.host.Update(msg)
	case fKeyPath:
		m.keyPath, cmd = m.keyPath.Update(msg)
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
	b.WriteString(field("Name", m.name, m.current() == fName))
	b.WriteString("\n\n")
	b.WriteString(field("Host", m.host, m.current() == fHost))
	if m.isSSH() {
		b.WriteString("\n\n")
		b.WriteString(m.authField())
		if m.auth == hosts.SSHAuthKey {
			b.WriteString("\n\n")
			b.WriteString(field("Key path", m.keyPath, m.current() == fKeyPath))
		}
	}
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	hint := "tab switch · enter save · esc cancel"
	if m.current() == fAuth {
		hint = "←/→/space choose · tab next · enter save · esc cancel"
	}
	b.WriteString(styles.FormHint.Render(hint))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

// authField renders the key/password selector as two labeled options with the
// active one highlighted.
func (m Model) authField() string {
	active := m.current() == fAuth
	labelStyle := styles.FormLabel
	marker := "  "
	if active {
		labelStyle = styles.FormLabelActive
		marker = "▸ "
	}
	option := func(label string, selected bool) string {
		if selected {
			return styles.FormLabelActive.Render("● " + label)
		}
		return styles.FormLabel.Render("○ " + label)
	}
	key := option("Key", m.auth == hosts.SSHAuthKey)
	pwd := option("Password", m.auth == hosts.SSHAuthPassword)
	return marker + labelStyle.Render("Authentication") + "\n  " + key + "   " + pwd
}
