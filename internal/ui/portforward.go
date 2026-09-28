package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/i18n"
	"github.com/kirg0/d9c/internal/portfwd"
	"github.com/kirg0/d9c/internal/ui/pfform"
	"github.com/kirg0/d9c/internal/ui/styles"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// forwardDialer returns the backend as a tunnel dialer, or nil when the backend
// cannot forward ports (CRI, nerdctl, not connected). An explicit nil keeps the
// interface value truly nil instead of a typed-nil dialer.
func forwardDialer(b docker.Backend) portfwd.Dialer {
	if fw, ok := b.(docker.PortForwarder); ok {
		return fw
	}
	return nil
}

// errForwardUnsupported is shown when the active backend has no port-forward
// capability.
func errForwardUnsupported() error {
	return errors.New(i18n.T(
		"port-forward недоступен для этого подключения (нужен Docker/Podman по ssh:// или tcp://)",
		"port-forward is not available for this connection (needs Docker/Podman over ssh:// or tcp://)"))
}

// openPortForward starts the port-forward flow for the cursor row: the
// selected container in the containers view, or the running containers of the
// selected project in the compose view (fetched off the event loop).
func (m Model) openPortForward() (tea.Model, tea.Cmd) {
	if forwardDialer(m.backend) == nil {
		m.err = errForwardUnsupported().Error()
		return m, nil
	}
	switch m.resource {
	case ViewContainers:
		id := m.selectedID()
		if id == "" {
			return m, nil
		}
		for _, c := range m.containers {
			if c.ID != id {
				continue
			}
			if c.State != "running" {
				m.err = i18n.T("port-forward: контейнер не запущен (состояние: ", "port-forward: container is not running (state: ") + c.State + ")"
				return m, nil
			}
			targets := []pfform.Target{{ID: c.ID, Name: c.Name, Ports: docker.ContainerPorts(c.Ports)}}
			return m, func() tea.Msg { return openPortForwardFormMsg{targets: targets} }
		}
	case ViewCompose:
		project := m.selectedID()
		if project != "" {
			return m, composeForwardTargetsCmd(m.backend, project, m.composeNameFor(project))
		}
	}
	return m, nil
}

// composeForwardTargetsCmd lists a compose project's running containers as
// port-forward targets.
func composeForwardTargetsCmd(b docker.Backend, project, name string) tea.Cmd {
	return func() tea.Msg {
		cs, err := b.ListComposeContainers(project)
		if err != nil {
			return errMsg{err}
		}
		targets := composeForwardTargets(cs)
		if len(targets) == 0 {
			return errMsg{fmt.Errorf(i18n.T("port-forward: в проекте %s нет запущенных контейнеров", "port-forward: project %s has no running containers"), name)}
		}
		return openPortForwardFormMsg{targets: targets}
	}
}

// composeForwardTargets keeps the running containers, those with known TCP
// ports first (the likely forward targets), preserving the order otherwise.
func composeForwardTargets(cs []docker.Container) []pfform.Target {
	var withPorts, without []pfform.Target
	for _, c := range cs {
		if c.State != "running" {
			continue
		}
		t := pfform.Target{ID: c.ID, Name: c.Name, Ports: docker.ContainerPorts(c.Ports)}
		if len(t.Ports) > 0 {
			withPorts = append(withPorts, t)
		} else {
			without = append(without, t)
		}
	}
	return append(withPorts, without...)
}

// startForwardCmd resolves the remote target (validating the port is
// reachable) and opens the local listener, off the event loop.
func startForwardCmd(pf *portfwd.Manager, fw docker.PortForwarder, spec portfwd.Spec) tea.Cmd {
	return func() tea.Msg {
		target, err := fw.PortTarget(spec.ContainerID, spec.ContainerPort)
		if err != nil {
			return portForwardStartedMsg{err: err}
		}
		spec.Target = target
		info, err := pf.Start(spec)
		return portForwardStartedMsg{info: info, err: err}
	}
}

// toggleForwardCmd stops or resumes a tunnel (resume re-binds the local port).
func toggleForwardCmd(pf *portfwd.Manager, id int) tea.Cmd {
	return func() tea.Msg {
		_, err := pf.Toggle(id)
		return portForwardChangedMsg{err: err}
	}
}

// handlePortForwardForm drives the port-forward modal: Tab/arrows switch
// fields, Enter validates and opens the tunnel, Esc (global) cancels.
func (m Model) handlePortForwardForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.pfForm.Busy() {
			return m, nil
		}
		t, ok := m.pfForm.Target()
		if !ok {
			return m, nil
		}
		cport, lport, err := m.pfForm.Values()
		if err != nil {
			m.pfForm.SetError(err.Error())
			return m, nil
		}
		fw, ok := m.backend.(docker.PortForwarder)
		if !ok {
			m.pfForm.SetError(errForwardUnsupported().Error())
			return m, nil
		}
		m.pfForm.SetError("")
		m.pfForm.SetBusy(true)
		spec := portfwd.Spec{ContainerID: t.ID, Name: t.Name, ContainerPort: cport, LocalPort: lport}
		return m, startForwardCmd(m.pf, fw, spec)
	case "tab", "down":
		m.pfForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.pfForm.Prev()
		return m, nil
	}
	updated, cmd := m.pfForm.Update(msg)
	m.pfForm = updated
	return m, cmd
}

// onPortForwardStarted handles the outcome of opening a tunnel: success closes
// the form with a footer notice and marks the row; a failure stays in the form
// (or lands in the footer if the form was already dismissed).
func (m Model) onPortForwardStarted(msg portForwardStartedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if m.mode == ModePortForwardForm {
			m.pfForm.SetError(msg.err.Error())
			return m, nil
		}
		m.err = "port-forward: " + msg.err.Error()
		return m, nil
	}
	if m.mode == ModePortForwardForm {
		m.mode = ModeNormal
	}
	m.err = ""
	m.copyNotif = fmt.Sprintf("⇄ %s → %s:%d", msg.info.LocalAddr(), msg.info.Spec.Name, msg.info.Spec.ContainerPort)
	m.refreshTableRows()
	return m, clearCopyNotifCmd()
}

// openPortForwards opens the tunnel list overlay.
func (m *Model) openPortForwards() {
	m.pfCursor = 0
	m.pfErr = ""
	m.mode = ModePortForwards
}

// handlePortForwards drives the tunnel list: navigate, s/space stop-resume,
// d delete, y copy the local address, q (esc globally) close.
func (m Model) handlePortForwards(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.pf.List()
	if m.pfCursor >= len(list) {
		m.pfCursor = max(len(list)-1, 0)
	}
	switch msg.String() {
	case "up", "k":
		if m.pfCursor > 0 {
			m.pfCursor--
		}
	case "down", "j":
		if m.pfCursor < len(list)-1 {
			m.pfCursor++
		}
	case "s", " ":
		if m.pfCursor < len(list) {
			return m, toggleForwardCmd(m.pf, list[m.pfCursor].ID)
		}
	case "d", "delete":
		if m.pfCursor < len(list) {
			m.pf.Remove(list[m.pfCursor].ID)
			m.pfErr = ""
			if m.pfCursor >= len(list)-1 && m.pfCursor > 0 {
				m.pfCursor--
			}
			m.refreshTableRows()
		}
	case "y":
		if m.pfCursor < len(list) {
			addr := list[m.pfCursor].LocalAddr()
			_ = clipboard.WriteAll(addr)
			m.copyNotif = i18n.T("скопировано: ", "copied: ") + addr
			return m, clearCopyNotifCmd()
		}
	case "q":
		m.mode = ModeNormal
		m.refreshTableRows()
	}
	return m, nil
}

// forwardRow renders one tunnel as a single list line.
func forwardRow(t portfwd.Info) string {
	line := fmt.Sprintf("%-21s → %s:%d", t.LocalAddr(), t.Spec.Name, t.Spec.ContainerPort)
	if t.Spec.Target != "" {
		line += "  (" + t.Spec.Target + ")"
	}
	return line + fmt.Sprintf("  %s  conns %d/%d", t.State, t.Conns, t.Total)
}

// viewPortForwardsOverlay renders the tunnel list centered over the table.
func (m Model) viewPortForwardsOverlay() string {
	bodyH := m.height - 2 // header + footer
	list := m.pf.List()

	var rows []string
	if len(list) == 0 {
		rows = append(rows, "    "+styles.CopyMenuLabel.Render(i18n.T(
			"туннелей нет — F в Containers/Compose открывает форму проброса",
			"no tunnels — press F in Containers/Compose to forward a port")))
	}
	lines := make([]string, len(list))
	maxW := 0
	for i, t := range list {
		lines[i] = forwardRow(t)
		maxW = max(maxW, lipgloss.Width(lines[i]))
	}
	for i, t := range list {
		label := lines[i] + strings.Repeat(" ", maxW-lipgloss.Width(lines[i]))
		switch {
		case i == m.pfCursor:
			rows = append(rows, " ▶  "+styles.CopyMenuSelected.Render(" "+label+" "))
		case t.State == portfwd.Failing:
			rows = append(rows, "    "+styles.StatusExited.Render(label))
		case t.State == portfwd.Stopped:
			rows = append(rows, "    "+styles.HelpMuted.Render(label))
		default:
			rows = append(rows, "    "+styles.CopyMenuLabel.Render(label))
		}
		if t.State == portfwd.Failing && t.LastErr != "" {
			rows = append(rows, "       "+styles.StatusExited.Render("✖ "+truncateRunes(t.LastErr, 90)))
		}
	}
	if m.pfErr != "" {
		rows = append(rows, "", "    "+styles.FormError.Render("✖ "+m.pfErr))
	}

	hint := styles.CopyMenuHint.Render(i18n.T(
		"  ↑/↓ выбор   s стоп/старт   d удалить   y копировать адрес   q/esc закрыть",
		"  ↑/↓ select   s stop/start   d delete   y copy address   q/esc close"))
	title := styles.CopyMenuTitle.Render(" Port-forwards ")
	content := title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return overlayCenter(m.viewNormal(), panel, m.width, bodyH)
}

// forwardMarkers maps container ID → the "⇄:<port>" marker shown in the PORTS
// column for containers with an open tunnel.
func forwardMarkers(byContainer map[string][]int) map[string]string {
	if len(byContainer) == 0 {
		return nil
	}
	out := make(map[string]string, len(byContainer))
	for id, ports := range byContainer {
		parts := make([]string, len(ports))
		for i, p := range ports {
			parts[i] = fmt.Sprintf(":%d", p)
		}
		out[id] = "⇄" + strings.Join(parts, ",")
	}
	return out
}
