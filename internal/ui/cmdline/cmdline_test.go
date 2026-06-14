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
