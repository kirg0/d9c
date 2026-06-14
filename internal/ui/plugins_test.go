package ui

import (
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/plugins"
	"d9c/internal/ui/cmdline"

	tea "github.com/charmbracelet/bubbletea"
)

// containersModelWithPlugins returns a model in the containers view (running
// containers loaded) with the given plugin set installed.
func containersModelWithPlugins(t *testing.T, ps *plugins.Set) tea.Model {
	t.Helper()
	fb := docker.NewFakeBackend()
	m := NewModel(&config.Config{Host: "tcp://h:2375"}, fb, nil, nil, false)
	m.SetPlugins(ps)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	tm, _ = tm.Update(containersUpdatedMsg{cs})
	return tm
}

func TestPluginDispatchInScope(t *testing.T) {
	ps := plugins.New([]plugins.Plugin{
		{Name: "top", Scope: "containers", Command: "echo", Args: []string{"${NAME}"}},
	})
	m := containersModelWithPlugins(t, ps).(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "top"})
	if err != nil {
		t.Fatalf("dispatch plugin: unexpected err %v", err)
	}
	if cmd == nil {
		t.Fatal("dispatch plugin returned nil cmd")
	}
}

func TestPluginDispatchScopeMismatch(t *testing.T) {
	ps := plugins.New([]plugins.Plugin{
		{Name: "layers", Scope: "images", Command: "dive"},
	})
	m := containersModelWithPlugins(t, ps).(Model)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "layers"}); err == nil {
		t.Error("images-scoped plugin must not run in containers view")
	}
}

func TestPluginDoesNotShadowBuiltin(t *testing.T) {
	cl := cmdline.New()
	cl.SetResource("containers")
	if !cl.IsBuiltin("stop") {
		t.Error("stop should be a built-in container command")
	}
	if cl.IsBuiltin("dive") {
		t.Error("dive should not be a built-in")
	}
}

func TestPluginKeyBinding(t *testing.T) {
	ps := plugins.New([]plugins.Plugin{
		{Name: "shell", Key: "ctrl+t", Scope: "containers", Command: "echo"},
	})
	tm := containersModelWithPlugins(t, ps)
	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if cmd == nil {
		t.Fatal("ctrl+t should trigger the bound plugin")
	}
	// A key with no plugin binding must not produce a plugin command.
	if _, ok := tm.(Model).pluginForKey("ctrl+z"); ok {
		t.Error("ctrl+z should not be bound")
	}
}

func TestPluginVars(t *testing.T) {
	m := containersModelWithPlugins(t, plugins.New(nil)).(Model)
	vars := m.pluginVars()
	if vars["NAME"] != "web" {
		t.Errorf("NAME = %q, want web", vars["NAME"])
	}
	if vars["IMAGE"] != "nginx:1.25" {
		t.Errorf("IMAGE = %q, want nginx:1.25", vars["IMAGE"])
	}
	if vars["HOST"] != "tcp://h:2375" {
		t.Errorf("HOST = %q, want the connected host", vars["HOST"])
	}
	if vars["ID"] == "" {
		t.Error("ID should be populated for the selected container")
	}
}

func TestPluginBackgroundDispatch(t *testing.T) {
	ps := plugins.New([]plugins.Plugin{
		{Name: "df", Scope: "*", Background: true, Command: "echo", Args: []string{"hi"}},
	})
	m := containersModelWithPlugins(t, ps).(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "df"})
	if err != nil || cmd == nil {
		t.Fatalf("background plugin dispatch: cmd=%v err=%v", cmd, err)
	}
}

func TestNoPluginsUnknownCommand(t *testing.T) {
	m := containersModelWithPlugins(t, plugins.New(nil)).(Model)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "nonsense"}); err == nil {
		t.Error("unknown command with no plugin should error")
	}
}
