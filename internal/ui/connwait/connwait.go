// Package connwait renders the modal status window shown while d9c dials a
// saved host that needs no credential prompt (SSH by key, TCP, unix). It shows
// a spinner while the connection is in flight; on failure the window stays open
// with the error so the user can retry (Enter) or close it (Esc).
package connwait

import (
	"strings"

	"github.com/kirg0/d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the connection-progress window.
type Model struct {
	spinner  spinner.Model
	hostName string
	hostURL  string
	errMsg   string
	busy     bool
}

// New builds an idle connection-progress window.
func New() Model {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = styles.FormBusy
	return Model{spinner: sp}
}

// Open prepares the window for a host and marks the dial in flight, returning
// the command that starts the spinner.
func (m *Model) Open(hostName, hostURL string) tea.Cmd {
	m.hostName = hostName
	m.hostURL = hostURL
	m.errMsg = ""
	m.busy = true
	return m.spinner.Tick
}

// Retry clears the error and marks a new dial in flight, returning the command
// that restarts the spinner.
func (m *Model) Retry() tea.Cmd {
	m.errMsg = ""
	m.busy = true
	return m.spinner.Tick
}

// Busy reports whether a connect is currently in flight.
func (m Model) Busy() bool { return m.busy }

// SetError shows a failure inside the window (keeps it open) and clears the
// busy state so the user can retry or close.
func (m *Model) SetError(s string) {
	m.errMsg = s
	m.busy = false
}

// Err returns the error currently shown, if any.
func (m Model) Err() string { return m.errMsg }

// HostName returns the saved host's display name.
func (m Model) HostName() string { return m.hostName }

// HostURL returns the host URL being dialed.
func (m Model) HostURL() string { return m.hostURL }

// Tick advances the spinner; used while a connect is in flight.
func (m Model) Tick(msg spinner.TickMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// View renders the window centered within the given area.
func (m Model) View(width, height int) string {
	name := m.hostName
	if name == "" {
		name = m.hostURL
	}

	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Connect to " + name + " "))
	b.WriteString("\n\n")
	b.WriteString("  " + styles.FormLabel.Render(m.hostURL))
	b.WriteString("\n\n")
	if m.busy {
		b.WriteString(m.spinner.View() + " " + styles.FormBusy.Render("connecting to "+name+"…") + "\n")
		b.WriteString(styles.FormHint.Render("this may take a while · esc cancel"))
	} else {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
		b.WriteString(styles.FormHint.Render("enter retry · esc close"))
	}

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
