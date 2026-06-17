package cmdline

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
)

func modelWithValue(val string) Model {
	ti := textinput.New()
	ti.SetValue(val)
	return Model{input: ti}
}

func TestParse_Empty(t *testing.T) {
	m := modelWithValue("")
	if m.Parse() != nil {
		t.Error("expected nil for empty input")
	}
}

func TestParse_SimpleCommand(t *testing.T) {
	m := modelWithValue("stop")
	cmd := m.Parse()
	if cmd == nil {
		t.Fatal("expected non-nil CommandMsg")
	}
	if cmd.Name != "stop" {
		t.Errorf("Name = %q, want %q", cmd.Name, "stop")
	}
	if len(cmd.Args) != 0 {
		t.Errorf("Args = %v, want empty", cmd.Args)
	}
}

func TestParse_CommandWithArgs(t *testing.T) {
	m := modelWithValue("logs --tail 50")
	cmd := m.Parse()
	if cmd == nil {
		t.Fatal("expected non-nil CommandMsg")
	}
	if cmd.Name != "logs" {
		t.Errorf("Name = %q, want %q", cmd.Name, "logs")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "--tail" || cmd.Args[1] != "50" {
		t.Errorf("Args = %v, want [--tail 50]", cmd.Args)
	}
}

func TestParse_UppercaseNormalized(t *testing.T) {
	m := modelWithValue("STOP")
	cmd := m.Parse()
	if cmd == nil || cmd.Name != "stop" {
		t.Errorf("expected name=stop, got %v", cmd)
	}
}

func TestParse_ExtraSpaces(t *testing.T) {
	m := modelWithValue("  restart  ")
	cmd := m.Parse()
	if cmd == nil || cmd.Name != "restart" {
		t.Errorf("expected name=restart, got %v", cmd)
	}
}

// On a tcp:// connection (hostCompose=false) the SSH-only compose commands must
// disappear from the command set, while the API-driven lifecycle ops and the
// local backup catalog stay.
func TestComposeCommandsHiddenOverTCP(t *testing.T) {
	hidden := []string{"create", "up", "down", "pull", "config", "edit", "backup", "restore"}
	kept := []string{"start", "stop", "restart", "pause", "unpause", "remove", "backups"}

	ssh := CommandsFor("compose", true)
	for _, name := range append(append([]string{}, hidden...), kept...) {
		if !containsCmd(ssh, name) {
			t.Errorf("ssh compose help should list %q", name)
		}
	}

	tcp := CommandsFor("compose", false)
	for _, name := range hidden {
		if containsCmd(tcp, name) {
			t.Errorf("tcp compose help must NOT list SSH-only command %q", name)
		}
	}
	for _, name := range kept {
		if !containsCmd(tcp, name) {
			t.Errorf("tcp compose help should still list API command %q", name)
		}
	}
}

// SetHostCompose must drive autocomplete: a hidden command yields no completion
// over tcp:// but completes over ssh://.
func TestSetHostComposeFiltersAutocomplete(t *testing.T) {
	m := New()
	m.SetResource("compose")
	m.SetHostCompose(false)
	m.input.SetValue("up")
	if g := m.ghost(); g.completion != "" || g.hint != "" {
		t.Errorf("tcp:// should not autocomplete hidden command 'up', got %+v", g)
	}
	m.SetHostCompose(true)
	m.input.SetValue("dow")
	if g := m.ghost(); g.completion != "n" {
		t.Errorf("ssh:// should complete 'dow' -> 'down', got %+v", g)
	}
}

func TestIsComposeHostOp(t *testing.T) {
	for _, name := range []string{"up", "down", "pull", "config", "edit", "create", "backup", "restore"} {
		if !IsComposeHostOp(name) {
			t.Errorf("%q should be a host-only compose op", name)
		}
	}
	for _, name := range []string{"start", "stop", "restart", "backups", "remove"} {
		if IsComposeHostOp(name) {
			t.Errorf("%q should NOT be a host-only compose op", name)
		}
	}
}

func containsCmd(cmds []CmdHelp, name string) bool {
	for _, c := range cmds {
		if c.Name == name {
			return true
		}
	}
	return false
}

// events is a global command and must be recognised as a builtin in every
// resource view (so a same-named plugin can't shadow it and autocomplete
// suggests it).
func TestEventsIsGlobalBuiltin(t *testing.T) {
	for _, res := range []string{"containers", "images", "networks", "volumes", "hosts", "compose"} {
		m := New()
		m.SetResource(res)
		if !m.IsBuiltin("events") {
			t.Errorf("events should be builtin in %q view", res)
		}
	}
}
