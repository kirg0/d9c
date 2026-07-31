package ui

import (
	"strings"
	"testing"
	"time"

	"d9c/internal/alerts"
	"d9c/internal/hosts"
	"d9c/internal/plugins"
)

func TestSetAlerts(t *testing.T) {
	h := newSizedModel(t)
	m := h.cur()
	m.SetAlerts(alerts.Thresholds{CPU: 80, Mem: 90})
	if m.alerts.CPU != 80 || m.alerts.Mem != 90 {
		t.Errorf("alerts = %+v", m.alerts)
	}
}

func TestPluginVarsPerResource(t *testing.T) {
	h := newSizedModel(t)
	h.step(containersUpdatedMsg{h.fb.Containers})

	m := h.cur()
	vars := m.pluginVars()
	if vars["NAME"] != "web" || vars["IMAGE"] != "nginx:1.25" || vars["ID"] == "" {
		t.Errorf("container vars = %+v", vars)
	}

	// The table keeps its own cursor, so assert against whatever row is
	// selected rather than a fixed element.
	m.resource = ViewImages
	m.images = h.fb.Images
	m.refreshTableRows()
	vars = m.pluginVars()
	found := false
	for _, im := range h.fb.Images {
		if im.ID == vars["ID"] && vars["TAGS"] == im.Tags {
			found = true
		}
	}
	if !found {
		t.Errorf("image vars = %+v", vars)
	}

	m.resource = ViewNetworks
	m.networks = h.fb.Networks
	m.refreshTableRows()
	vars = m.pluginVars()
	found = false
	for _, n := range h.fb.Networks {
		if n.ID == vars["ID"] && vars["NAME"] == n.Name && vars["DRIVER"] == n.Driver {
			found = true
		}
	}
	if !found {
		t.Errorf("network vars = %+v", vars)
	}

	m.resource = ViewVolumes
	m.volumes = h.fb.Volumes
	m.refreshTableRows()
	vars = m.pluginVars()
	found = false
	for _, v := range h.fb.Volumes {
		if v.Name == vars["NAME"] && vars["DRIVER"] == v.Driver {
			found = true
		}
	}
	if !found {
		t.Errorf("volume vars = %+v", vars)
	}

	m.resource = ViewHosts
	m.hosts = []hosts.Host{{Name: "lab", Host: "ssh://me@lab"}}
	m.refreshTableRows()
	if vars := m.pluginVars(); vars["NAME"] != "lab" || vars["HOST"] != "ssh://me@lab" {
		t.Errorf("host vars = %+v", vars)
	}

	m.resource = ViewCompose
	m.composes = h.fb.Composes
	m.refreshTableRows()
	vars = m.pluginVars()
	found = false
	for _, p := range h.fb.Composes {
		if p.Identity() == vars["ID"] && vars["PROJECT"] == p.Project && vars["PATH"] == p.WorkingDir {
			found = true
		}
	}
	if !found {
		t.Errorf("compose vars = %+v", vars)
	}
}

func TestStreamLocalProcess(t *testing.T) {
	// Success: output line arrives, channel closes.
	ch, stop, err := streamLocalProcess(testShell(), testShellArgs("echo hello"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer stop()
	var lines []string
	for l := range ch {
		lines = append(lines, l)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "hello") {
		t.Errorf("lines = %v", lines)
	}

	// Non-zero exit appends a trailing error line.
	ch, stop, err = streamLocalProcess(testShell(), testShellArgs("exit 3"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer stop()
	lines = nil
	for l := range ch {
		lines = append(lines, l)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "error:") {
		t.Errorf("exit-3 lines = %v", lines)
	}

	// A missing binary fails at start.
	if _, _, err := streamLocalProcess("definitely-not-a-binary-42", nil); err == nil {
		t.Error("missing binary should fail to start")
	}

	// stop kills a long-running process and the channel still closes.
	ch, stop, err = streamLocalProcess(testShell(), testShellArgs(testSleepScript))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	stop()
	stop() // idempotent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close after stop")
		}
	}
}

func TestPluginCmdBackground(t *testing.T) {
	h := newSizedModel(t)
	h.step(containersUpdatedMsg{h.fb.Containers})
	m := h.cur()

	p := plugins.Plugin{Name: "echoer", Command: testShell(), Args: testShellArgs("echo ${NAME}"), Background: true}
	cmd := m.pluginCmd(p)
	msg := cmd()
	op, ok := msg.(opStartedMsg)
	if !ok {
		t.Fatalf("msg = %#v, want opStartedMsg", msg)
	}
	defer op.stop()
	var lines []string
	for l := range op.ch {
		lines = append(lines, l)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "web") {
		t.Errorf("plugin output = %v, want substituted container name", lines)
	}

	// Background plugin with a bad binary surfaces errMsg.
	bad := plugins.Plugin{Name: "bad", Command: "definitely-not-a-binary-42", Background: true}
	if msg := m.pluginCmd(bad)(); msg == nil {
		t.Error("bad plugin should yield a message")
	} else if _, ok := msg.(errMsg); !ok {
		t.Errorf("msg = %#v, want errMsg", msg)
	}

	// Interactive plugin returns a tea.ExecProcess command.
	inter := plugins.Plugin{Name: "shell", Command: testShell()}
	if c := m.pluginCmd(inter); c == nil {
		t.Error("interactive plugin should return a cmd")
	}
}
