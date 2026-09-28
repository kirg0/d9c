package ui

import (
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/keymap"
	"github.com/kirg0/d9c/internal/plugins"
	"github.com/kirg0/d9c/internal/ui/cmdline"

	tea "github.com/charmbracelet/bubbletea"
)

// readOnlyContainersModel returns a containers-view model (rows loaded) with
// session-wide read-only mode on, plus its fake backend.
func readOnlyContainersModel(t *testing.T, ps *plugins.Set) (Model, *docker.FakeBackend) {
	t.Helper()
	fb := docker.NewFakeBackend()
	m := NewModel(&config.Config{Host: "tcp://h:2375", ReadOnly: true}, fb, nil, nil, false)
	m.SetPlugins(ps)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	cs, _ := fb.ListContainers(false)
	tm, _ = tm.Update(containersUpdatedMsg{cs})
	return tm.(Model), fb
}

func TestGuardCommand(t *testing.T) {
	tests := []struct {
		view    ResourceView
		name    string
		args    []string
		blocked bool
	}{
		{ViewContainers, "stop", nil, true},
		{ViewContainers, "rm", []string{"-f"}, true},
		{ViewContainers, "exec", nil, true},
		{ViewContainers, "cp", nil, true},
		{ViewContainers, "logs", nil, false},
		{ViewContainers, "files", nil, false},
		{ViewContainers, "images", nil, false},
		{ViewContainers, "theme", nil, false},
		{ViewImages, "push", nil, true},
		{ViewImages, "build", nil, true},
		{ViewImages, "history", nil, false},
		{ViewNetworks, "create", nil, true},
		{ViewVolumes, "prune", nil, true},
		{ViewCompose, "up", nil, true},
		{ViewCompose, "restore", nil, true},
		{ViewCompose, "backups", nil, false},
		{ViewHosts, "rm", nil, false},
		{ViewHosts, "system", []string{"prune"}, true},
		{ViewHosts, "system", []string{"df"}, false},
	}
	for _, tt := range tests {
		m := Model{resource: tt.view, roGlobal: true}
		err := m.guardCommand(&cmdline.CommandMsg{Name: tt.name, Args: tt.args})
		if (err != nil) != tt.blocked {
			t.Errorf("%s :%s %v blocked = %v, want %v", tt.view, tt.name, tt.args, err != nil, tt.blocked)
		}
		// With read-only off nothing is ever refused.
		m.roGlobal = false
		if err := m.guardCommand(&cmdline.CommandMsg{Name: tt.name, Args: tt.args}); err != nil {
			t.Errorf("%s :%s refused without read-only: %v", tt.view, tt.name, err)
		}
	}
}

func TestGuardCommandMessage(t *testing.T) {
	m := Model{resource: ViewContainers, roGlobal: true}
	err := m.guardCommand(&cmdline.CommandMsg{Name: "system", Args: []string{"prune"}})
	if err == nil || !strings.Contains(err.Error(), "read-only mode: :system prune is disabled") {
		t.Errorf("err = %v, want the read-only message naming :system prune", err)
	}
}

// The dispatcher refuses the command before touching the backend.
func TestDispatchStopRefusedInReadOnly(t *testing.T) {
	fb := docker.NewFakeBackend()
	m := Model{
		backend:  fb,
		resource: ViewContainers,
		roGlobal: true,
		selected: map[string]bool{"9ae942fd8fbc": true},
	}
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "stop"})
	if err == nil || cmd != nil {
		t.Fatalf("dispatch stop = (%v, %v), want refused", cmd, err)
	}
	cs, _ := fb.ListContainers(true)
	for _, c := range cs {
		if c.ID == "9ae942fd8fbc" && c.State != "running" {
			t.Errorf("container state = %q, must stay running", c.State)
		}
	}
}

func TestMutatingKeyOp(t *testing.T) {
	km := keymap.Default()
	tests := []struct {
		view     ResourceView
		key      string
		selected bool
		wantOp   string
		want     bool
	}{
		{ViewContainers, "x", false, "exec", true},
		{ViewContainers, "l", false, "", false},
		{ViewContainers, "r", true, "", false}, // refresh, not image removal
		{ViewCompose, "e", false, "edit", true},
		{ViewImages, "r", true, "rm", true},
		{ViewImages, "r", false, "", false},
		{ViewImages, "x", false, "", false},
		{ViewHosts, "e", false, "", false},
		{ViewContainers, "unbound", false, "", false},
	}
	for _, tt := range tests {
		op, ok := mutatingKeyOp(tt.view, tt.key, km, tt.selected)
		if ok != tt.want || op != tt.wantOp {
			t.Errorf("mutatingKeyOp(%s, %q, sel=%v) = (%q, %v), want (%q, %v)", tt.view, tt.key, tt.selected, op, ok, tt.wantOp, tt.want)
		}
	}
}

func TestExecKeyRefusedInReadOnly(t *testing.T) {
	m, _ := readOnlyContainersModel(t, nil)
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cmd != nil {
		t.Error("x must not start exec in read-only mode")
	}
	if got := tm.(Model).err; !strings.Contains(got, "read-only") {
		t.Errorf("footer err = %q, want read-only message", got)
	}
}

func TestImageSelectionRemoveRefusedInReadOnly(t *testing.T) {
	tm, _ := imagesModel(t)
	m := tm.(Model)
	m.roGlobal = true
	m.selected = map[string]bool{"sha": true}
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Error("r must not open the remove confirmation in read-only mode")
	}
	if tm.(Model).mode == ModeConfirm || !strings.Contains(tm.(Model).err, "read-only") {
		t.Errorf("mode = %v err = %q, want refusal", tm.(Model).mode, tm.(Model).err)
	}
}

func TestBackupRestoreRefusedInReadOnly(t *testing.T) {
	m := Model{
		mode:           ModeBackupPicker,
		composeHostOps: true,
		roGlobal:       true,
		backupItems:    []backupEntry{{name: "b.tar.gz", path: "b.tar.gz"}},
	}
	tm, cmd := m.handleBackupPicker(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !strings.Contains(tm.(Model).err, "read-only") {
		t.Errorf("restore = (cmd %v, err %q), want refusal", cmd != nil, tm.(Model).err)
	}
}

func TestMutatingPluginRefusedInReadOnly(t *testing.T) {
	ps := plugins.New([]plugins.Plugin{
		{Name: "bounce", Key: "ctrl+b", Scope: "containers", Command: "echo", Mutating: true},
		{Name: "top", Key: "ctrl+t", Scope: "containers", Command: "echo"},
	})
	m, _ := readOnlyContainersModel(t, ps)

	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "bounce"})
	if err != nil || cmd == nil {
		t.Fatalf("dispatch = (%v, %v), want a cmd yielding the refusal", cmd, err)
	}
	em, ok := cmd().(errMsg)
	if !ok || !strings.Contains(em.err.Error(), "read-only mode: bounce") {
		t.Errorf("plugin result = %#v, want read-only errMsg", em)
	}

	// Hidden from the footer and help; the safe plugin stays.
	visible := m.visiblePlugins()
	if len(visible) != 1 || visible[0].Name != "top" {
		t.Errorf("visible plugins = %+v, want only top", visible)
	}
	if help := m.buildHelpContent(); strings.Contains(help, ":bounce") || !strings.Contains(help, ":top") {
		t.Error("help must hide the mutating plugin and keep the safe one")
	}
}

func TestReadOnlyHeaderFooterHelp(t *testing.T) {
	m, _ := readOnlyContainersModel(t, nil)
	if h := m.viewHeader(); !strings.Contains(h, " RO ") {
		t.Errorf("header missing RO badge: %q", h)
	}
	footer := m.viewFooter()
	if strings.Contains(footer, "Shell") || !strings.Contains(footer, "Logs") {
		t.Errorf("footer should hide Shell but keep Logs: %q", footer)
	}
	help := m.buildHelpContent()
	for _, hidden := range []string{":stop", ":cp", ":system prune", "Shell in the container"} {
		if strings.Contains(help, hidden) {
			t.Errorf("help should hide %q in read-only mode", hidden)
		}
	}
	for _, kept := range []string{"Read-only mode (RO)", ":logs", ":system df"} {
		if !strings.Contains(help, kept) {
			t.Errorf("help should show %q", kept)
		}
	}

	m.mode = ModeCommand
	if f := m.viewFooter(); strings.Contains(f, "stop") || !strings.Contains(f, "logs") {
		t.Errorf("command footer should list only read-only commands: %q", f)
	}

	// Read-only off: badge gone, mutating hints back.
	fb := docker.NewFakeBackend()
	rw := NewModel(&config.Config{Host: "tcp://h:2375"}, fb, nil, nil, false)
	var tm tea.Model = rw
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	rw = tm.(Model)
	if strings.Contains(rw.viewHeader(), " RO ") || !strings.Contains(rw.viewFooter(), "Shell") {
		t.Error("without read-only the badge must be absent and Shell shown")
	}
}

// A saved host marked read_only switches the mode on while connected to it and
// off again after connecting elsewhere.
func TestPerHostReadOnlyFollowsConnection(t *testing.T) {
	store := hosts.NewStore([]hosts.Host{
		{Name: "prod", Host: "tcp://prod:2375", ReadOnly: true},
		{Name: "dev", Host: "tcp://dev:2375"},
	}, nil)
	m := NewModel(&config.Config{Host: "tcp://dev:2375"}, docker.NewFakeBackend(), store, nil, false)
	if m.readOnly() {
		t.Fatal("dev host must not be read-only")
	}
	var tm tea.Model = m
	tm, _ = tm.Update(connectResultMsg{backend: docker.NewFakeBackend(), host: "tcp://prod:2375"})
	m = tm.(Model)
	if !m.readOnly() {
		t.Fatal("connected to prod: read-only expected")
	}
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "stop"}); err == nil {
		t.Error("stop must be refused on the read-only host")
	}
	if !m.cmdline.IsBuiltin("stop") {
		t.Error("stop must stay a builtin (only hidden from autocomplete)")
	}
	tm, _ = tm.Update(connectResultMsg{backend: docker.NewFakeBackend(), host: "tcp://dev:2375"})
	if tm.(Model).readOnly() {
		t.Error("back on dev: read-only must be off")
	}
}

// SetReadOnly can only turn the session-wide mode on, never off.
func TestSetReadOnlyIsSticky(t *testing.T) {
	m := NewModel(&config.Config{ReadOnly: true}, docker.NewFakeBackend(), nil, nil, false)
	m.SetReadOnly(false)
	if !m.readOnly() {
		t.Error("SetReadOnly(false) must not disable the -read-only flag")
	}
	m2 := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m2.SetReadOnly(true)
	if !m2.readOnly() {
		t.Error("SetReadOnly(true) must enable read-only mode")
	}
}
