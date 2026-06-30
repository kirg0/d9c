package ui

import (
	"path/filepath"
	"strings"

	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/i18n"
	"d9c/internal/ui/cpform"

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
		rec := m.hostForm.Result()
		var err error
		if m.hostForm.IsEditing() {
			err = m.hostStore.EditHost(m.hostForm.OrigName(), rec)
		} else {
			err = m.hostStore.AddHost(rec)
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
	case "left", "right", " ":
		// The SSH auth selector toggles with arrows/space; on the text fields
		// these keys edit normally (ToggleAuth is a no-op off the auth field).
		if m.hostForm.OnAuthField() {
			m.hostForm.ToggleAuth()
			return m, nil
		}
	}
	updated, cmd := m.hostForm.Update(msg)
	m.hostForm = updated
	return m, cmd
}

// handleConnectAuth drives the SSH credential prompt shown before connecting to
// a password-auth host: Tab/arrows switch fields, Enter rewrites the host URL
// with the (editable) login, stashes the password on the live config and
// connects, and Esc (handled globally) cancels. The password is never saved to
// the host store — it lives only on m.cfg for the session/auto-reconnect.
func (m Model) handleConnectAuth(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While a connect is in flight, swallow keys so a second Enter can't fire a
	// duplicate dial (esc is handled globally and still cancels the modal).
	if m.connForm.Busy() {
		return m, nil
	}
	switch msg.String() {
	case "enter":
		login := m.connForm.Login()
		if login == "" {
			m.connForm.SetError(i18n.T("логин обязателен", "login is required"))
			return m, nil
		}
		url := hosts.WithSSHUser(m.connForm.HostURL(), login)
		m.cfg.SSHPassword = m.connForm.Password()
		m.cfg.SSHKeyFile = ""
		m.cfg.Host = url
		// Keep the modal open showing a "connecting…" status; the result lands as
		// connectResultMsg, which closes it on success or shows the error inline.
		spin := m.connForm.Connecting()
		return m, tea.Batch(spin, connectCmd(m.cfg, url))
	case "tab", "down":
		m.connForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.connForm.Prev()
		return m, nil
	}
	updated, cmd := m.connForm.Update(msg)
	m.connForm = updated
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

// handleCpForm drives the upload-to-container wizard: Tab switches between the
// local file picker and the destination field. In the picker, Enter/l descends
// into a directory (Enter on a file jumps to the destination field), Backspace/h
// ascends, and arrows move the cursor. With the destination field focused, Enter
// uploads the highlighted local entry into the typed container directory. Esc is
// handled globally; a backend failure lands inside the form (actionResultMsg).
func (m Model) handleCpForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While an upload is in flight, swallow keys so a second Enter can't fire a
	// duplicate upload (esc is handled globally and still cancels the modal).
	if m.cpForm.Busy() {
		return m, nil
	}
	if s := msg.String(); s == "tab" || s == "shift+tab" {
		m.cpForm.ToggleFocus()
		return m, nil
	}

	if m.cpForm.OnBrowser() {
		switch msg.String() {
		case "enter", "l", "right":
			e := m.cpForm.Selected()
			if e.IsDir {
				return m, cpListCmd(filepath.Join(m.cpForm.CurrentDir(), e.Name))
			}
			// A file is chosen: move to the destination field to confirm.
			m.cpForm.ToggleFocus()
			return m, nil
		case "backspace", "h", "left", "-":
			return m, cpListCmd(cpform.Parent(m.cpForm.CurrentDir()))
		}
		updated, cmd := m.cpForm.Update(msg)
		m.cpForm = updated
		return m, cmd
	}

	// Destination field focused.
	if msg.String() == "enter" {
		src := m.cpForm.SourcePath()
		if src == "" {
			m.cpForm.SetError("select a local file or directory")
			return m, nil
		}
		dest := m.cpForm.Dest()
		if dest == "" {
			m.cpForm.SetError("container directory is required")
			return m, nil
		}
		id := m.cpForm.ContainerID()
		spin := m.cpForm.Running()
		return m, tea.Batch(spin, containerAction(func() error { return m.backend.CopyToContainer(id, src, dest) }))
	}
	updated, cmd := m.cpForm.Update(msg)
	m.cpForm = updated
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
