package ui

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"d9c/internal/alerts"
	"d9c/internal/keymap"
	"d9c/internal/plugins"

	tea "github.com/charmbracelet/bubbletea"
)

// SetPlugins installs the user-defined plugin set and refreshes command
// autocomplete for the current view. Safe to call with a nil set.
func (m *Model) SetPlugins(p *plugins.Set) {
	m.plugins = p
	m.refreshPluginCmds()
}

// SetKeymap installs the resolved normal-mode key bindings.
func (m *Model) SetKeymap(km keymap.Map) {
	m.keys = km
}

// SetAlerts installs the resource-usage alert thresholds (from the config file)
// and refreshes the container view so any breaching rows are flagged at once.
func (m *Model) SetAlerts(t alerts.Thresholds) {
	m.alerts = t
	m.applyColumns(m.width)
	m.refreshTableRows()
}

// refreshPluginCmds pushes the plugin names available in the current view into
// the command line for autocomplete.
func (m *Model) refreshPluginCmds() {
	var names []string
	for _, p := range m.plugins.ForScope(m.pluginScope()) {
		names = append(names, p.Name)
	}
	m.cmdline.SetPluginCommands(names)
}

// pluginScope maps the active resource view to a plugin scope string.
func (m Model) pluginScope() string {
	return strings.ToLower(m.resource.String())
}

// pluginForKey returns the plugin bound to key in the current scope, if any.
func (m Model) pluginForKey(key string) (plugins.Plugin, bool) {
	return m.plugins.ByKey(m.pluginScope(), key)
}

// scopedPluginsWithKeys returns plugins for the current view that bind a key, so
// the footer can advertise them.
func (m Model) scopedPluginsWithKeys() []plugins.Plugin {
	var out []plugins.Plugin
	for _, p := range m.plugins.ForScope(m.pluginScope()) {
		if p.Key != "" {
			out = append(out, p)
		}
	}
	return out
}

// pluginVars builds the ${PLACEHOLDER} substitution map for the selected row.
func (m Model) pluginVars() map[string]string {
	vars := map[string]string{"HOST": m.cfg.Host}
	id := m.selectedID()
	vars["ID"] = id
	switch m.resource {
	case ViewContainers:
		for _, c := range m.containers {
			if c.ID == id {
				vars["NAME"], vars["IMAGE"] = c.Name, c.Image
				vars["STATUS"], vars["STATE"], vars["PORTS"] = c.Status, c.State, c.Ports
				break
			}
		}
	case ViewImages:
		for _, im := range m.images {
			if im.ID == id {
				vars["NAME"], vars["IMAGE"], vars["TAGS"] = im.Tags, im.Tags, im.Tags
				break
			}
		}
	case ViewNetworks:
		for _, n := range m.networks {
			if n.ID == id {
				vars["NAME"], vars["DRIVER"] = n.Name, n.Driver
				break
			}
		}
	case ViewVolumes:
		for _, v := range m.volumes {
			if v.Name == id {
				vars["NAME"], vars["DRIVER"] = v.Name, v.Driver
				break
			}
		}
	case ViewHosts:
		for _, h := range m.hosts {
			if h.Name == id {
				vars["NAME"], vars["HOST"] = h.Name, h.Host
				break
			}
		}
	case ViewCompose:
		for _, p := range m.composes {
			if p.WorkingDir == id {
				vars["NAME"], vars["PATH"], vars["STATUS"] = p.Name, p.WorkingDir, p.Status
				vars["PROJECT"] = p.Project
				break
			}
		}
	}
	return vars
}

// pluginCmd runs a plugin: interactive plugins take over the terminal via
// tea.Exec, background plugins stream their output into the operation console.
func (m Model) pluginCmd(p plugins.Plugin) tea.Cmd {
	name, args := plugins.Substitute(p, m.pluginVars())
	if p.Background {
		title := "plugin: " + p.Name
		return func() tea.Msg {
			ch, stop, err := streamLocalProcess(name, args)
			if err != nil {
				return errMsg{err}
			}
			return opStartedMsg{ch: ch, stop: stop, title: title}
		}
	}
	c := exec.Command(name, args...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{err: err} })
}

// streamLocalProcess starts a local command and streams its combined
// stdout/stderr line-by-line into the returned channel, which closes when the
// process exits (a non-zero exit appends a trailing "error: …" line). The
// returned stop kills the process and unblocks producers stuck on a send nobody
// reads; the caller MUST call it when it abandons the channel, otherwise the
// process and goroutines leak.
func streamLocalProcess(name string, args []string) (<-chan string, func(), error) {
	c := exec.Command(name, args...)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := c.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", name, err)
	}

	out := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			if c.Process != nil {
				_ = c.Process.Kill()
			}
		})
	}
	send := func(line string) bool {
		select {
		case out <- line:
			return true
		case <-done:
			return false
		}
	}
	var wg sync.WaitGroup
	scan := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if !send(sc.Text()) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			send("error: read output: " + err.Error())
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	go func() {
		wg.Wait()
		if err := c.Wait(); err != nil {
			send("error: " + err.Error())
		}
		stop() // release process handle when it ends naturally
		close(out)
	}()
	return out, stop, nil
}
