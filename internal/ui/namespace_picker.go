package ui

import (
	"d9c/internal/docker"
	"d9c/internal/i18n"

	tea "github.com/charmbracelet/bubbletea"
)

// namespacesLoadedMsg carries the containerd namespaces fetched off the event
// loop (Namespaces() talks to the daemon). err is set when the query failed.
type namespacesLoadedMsg struct {
	names   []string
	current string
	err     error
}

// loadNamespacesCmd queries the backend's namespaces in a tea.Cmd (IO must not
// run inside Update) and reports them via namespacesLoadedMsg.
func loadNamespacesCmd(nb docker.NamespacedBackend) tea.Cmd {
	return func() tea.Msg {
		names, err := nb.Namespaces()
		return namespacesLoadedMsg{names: names, current: nb.CurrentNamespace(), err: err}
	}
}

// openNamespacePicker opens the namespace selector, positioning the cursor on
// the currently active namespace.
func (m *Model) openNamespacePicker(names []string, current string) {
	m.nsNames = names
	m.nsCursor = 0
	for i, n := range names {
		if n == current {
			m.nsCursor = i
			break
		}
	}
	m.mode = ModeNamespacePicker
}

// handleNamespacePicker drives the namespace selector: the cursor moves the
// highlight, Enter switches to the highlighted namespace (and refreshes the
// current view), q/esc cancels. Unlike theme/lang there is no live preview —
// switching re-queries the daemon, so it happens only on commit.
func (m Model) handleNamespacePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.nsCursor > 0 {
			m.nsCursor--
		}
	case "down", "j":
		if m.nsCursor < len(m.nsNames)-1 {
			m.nsCursor++
		}
	case "enter":
		if m.nsCursor < len(m.nsNames) {
			if nb, ok := m.backend.(docker.NamespacedBackend); ok {
				nb.SetNamespace(m.nsNames[m.nsCursor])
				m.copyNotif = i18n.T("namespace: ", "namespace: ") + nb.CurrentNamespace()
			}
			m.mode = ModeNormal
			return m, tea.Batch(m.fetchCurrentResource(), clearCopyNotifCmd())
		}
	case "q":
		m.mode = ModeNormal
	}
	return m, nil
}
