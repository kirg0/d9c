package ui

import (
	"strings"

	"d9c/internal/docker"

	tea "github.com/charmbracelet/bubbletea"
)

// This file holds the modal form key handlers (host/push/net/vol/run/exec),
// split out of update.go.

// handleHostForm drives the add/edit modal: Enter saves (validating against the
// store), Tab/arrows switch fields, everything else edits the focused field.
// Esc is handled by the global key handler and cancels without saving.
func (m Model) handleHostForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := m.hostForm.Name()
		host := m.hostForm.Host()
		var err error
		if m.hostForm.IsEditing() {
			err = m.hostStore.Edit(m.hostForm.OrigName(), name, host)
		} else {
			err = m.hostStore.Add(name, host)
		}
		if err == nil {
			err = m.hostStore.Save()
		}
		if err != nil {
			m.hostForm.SetError(err.Error())
			return m, nil
		}
		m.mode = ModeNormal
		m.relayout()
		return m, fetchHosts(m.hostStore)
	case "tab", "down":
		m.hostForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.hostForm.Prev()
		return m, nil
	}
	updated, cmd := m.hostForm.Update(msg)
	m.hostForm = updated
	return m, cmd
}

// handlePushForm drives the registry-credentials modal: Tab/arrows switch
// fields, Enter starts the push with the entered credentials (an empty username
// means anonymous), and Esc (handled globally) cancels. Credentials are
// remembered for the session keyed by registry so the next push pre-fills.
func (m Model) handlePushForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		auth := docker.RegistryAuth{
			Registry: m.pushForm.Registry(),
			Username: m.pushForm.Username(),
			Password: m.pushForm.Password(),
		}
		ref := m.pushForm.Ref()
		if m.pushAuth == nil {
			m.pushAuth = map[string]docker.RegistryAuth{}
		}
		m.pushAuth[auth.Registry] = auth
		m.mode = ModeNormal
		m.relayout()
		return m, streamOpCmd(func() (<-chan string, func(), error) { return m.backend.PushImage(ref, auth) }, "push: "+ref)
	case "tab", "down":
		m.pushForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.pushForm.Prev()
		return m, nil
	}
	updated, cmd := m.pushForm.Update(msg)
	m.pushForm = updated
	return m, cmd
}

// handleNetForm drives the create-network modal: Tab/arrows switch fields, Enter
// creates the network with the entered options, and Esc (handled globally)
// cancels. A blank driver defaults to "bridge" in the backend.
func (m Model) handleNetForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.netForm.Name() == "" {
			m.netForm.SetError("name is required")
			return m, nil
		}
		opts := docker.NetworkCreateOptions{
			Name:    m.netForm.Name(),
			Driver:  m.netForm.Driver(),
			Subnet:  m.netForm.Subnet(),
			Gateway: m.netForm.Gateway(),
		}
		return m, containerAction(func() error { return m.backend.CreateNetwork(opts) })
	case "tab", "down":
		m.netForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.netForm.Prev()
		return m, nil
	}
	updated, cmd := m.netForm.Update(msg)
	m.netForm = updated
	return m, cmd
}

// handleVolForm drives the create-volume modal: Tab/arrows switch fields, Enter
// creates the volume with the entered options, and Esc (handled globally)
// cancels. A blank driver defaults to "local" in the backend.
func (m Model) handleVolForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.volForm.Name() == "" {
			m.volForm.SetError("name is required")
			return m, nil
		}
		opts := docker.VolumeCreateOptions{
			Name:   m.volForm.Name(),
			Driver: m.volForm.Driver(),
		}
		return m, containerAction(func() error { return m.backend.CreateVolume(opts) })
	case "tab", "down":
		m.volForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.volForm.Prev()
		return m, nil
	}
	updated, cmd := m.volForm.Update(msg)
	m.volForm = updated
	return m, cmd
}

// handlePullForm drives the pull-image modal: Enter pulls the entered image,
// Esc (handled globally) cancels. A backend failure is shown inside the form via
// the actionResultMsg branch.
func (m Model) handlePullForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While a pull is in flight, swallow keys so a second Enter can't fire a
	// duplicate pull (esc is handled globally and still cancels the modal).
	if m.pullForm.Busy() {
		return m, nil
	}
	if msg.String() == "enter" {
		ref := m.pullForm.Image()
		if ref == "" {
			m.pullForm.SetError("image is required")
			return m, nil
		}
		spin := m.pullForm.Pulling()
		return m, tea.Batch(spin, containerAction(func() error { return m.backend.PullImage(ref) }))
	}
	updated, cmd := m.pullForm.Update(msg)
	m.pullForm = updated
	return m, cmd
}

// handleBuildForm drives the build-image modal: Tab/arrows switch fields, Enter
// starts the build in the streaming progress console, Esc (handled globally)
// cancels. A blank context directory keeps the form open with an error.
func (m Model) handleBuildForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		dir := m.buildForm.Dir()
		if dir == "" {
			m.buildForm.SetError("context dir is required")
			return m, nil
		}
		tag := m.buildForm.Tag()
		m.mode = ModeNormal
		m.relayout()
		return m, streamOpCmd(func() (<-chan string, func(), error) { return m.backend.BuildImage(dir, tag) }, "build: "+dir)
	case "tab", "down":
		m.buildForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.buildForm.Prev()
		return m, nil
	}
	updated, cmd := m.buildForm.Update(msg)
	m.buildForm = updated
	return m, cmd
}

// handleRunForm drives the run-container wizard: Tab/arrows switch fields,
// Enter creates and starts the container, Esc (handled globally) cancels. A
// backend failure is shown inside the form via the actionResultMsg branch.
func (m Model) handleRunForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While a create/run is in flight, swallow keys so a second Enter can't fire
	// a duplicate run (esc is handled globally and still cancels the modal).
	if m.runForm.Busy() {
		return m, nil
	}
	switch msg.String() {
	case "enter":
		if m.runForm.Image() == "" {
			m.runForm.SetError("image is required")
			return m, nil
		}
		opts := docker.RunOptions{
			Image:   m.runForm.Image(),
			Name:    m.runForm.Name(),
			Ports:   splitList(m.runForm.Ports()),
			Env:     splitList(m.runForm.Env()),
			Volumes: splitList(m.runForm.Volumes()),
		}
		spin := m.runForm.Running()
		return m, tea.Batch(spin, containerAction(func() error { return m.backend.RunContainer(opts) }))
	case "tab", "down":
		m.runForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.runForm.Prev()
		return m, nil
	}
	updated, cmd := m.runForm.Update(msg)
	m.runForm = updated
	return m, cmd
}

// handleExecForm drives the one-off run wizard: Tab/arrows switch fields,
// Enter starts a disposable interactive container (empty command = shell) and
// opens the embedded terminal; Esc (handled globally) cancels. A backend
// failure comes back as errMsg and is shown inside the still-open form.
func (m Model) handleExecForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.execForm.Image() == "" {
			m.execForm.SetError("image is required")
			return m, nil
		}
		opts := docker.ExecRunOptions{
			Image:   m.execForm.Image(),
			Volumes: splitList(m.execForm.Volumes()),
			Cmd:     strings.Fields(m.execForm.Command()),
		}
		return m, runInteractiveCmd(m.backend, opts)
	case "tab", "down":
		m.execForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.execForm.Prev()
		return m, nil
	}
	updated, cmd := m.execForm.Update(msg)
	m.execForm = updated
	return m, cmd
}

// splitList splits a comma-separated form field into trimmed, non-empty items.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
