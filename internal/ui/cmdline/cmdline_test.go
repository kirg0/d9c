package cmdline

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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

func TestSetPluginCommands(t *testing.T) {
	m := New()
	m.SetPluginCommands([]string{"mycmd"})
	m.input.SetValue("myc")
	if g := m.ghost(); g.completion != "md" || g.hint != "(plugin)" {
		t.Errorf("plugin autocomplete = %+v, want completion md + (plugin) hint", g)
	}
	if m.IsBuiltin("mycmd") {
		t.Error("plugin command must not be reported as builtin")
	}
}

func TestFocusBlurReset(t *testing.T) {
	m := New()
	m.SetError("boom")
	m.Focus()
	if m.lastErr != "" {
		t.Error("Focus should clear the last error")
	}
	if !m.input.Focused() {
		t.Error("Focus should focus the input")
	}
	m.Blur()
	if m.input.Focused() {
		t.Error("Blur should unfocus the input")
	}
	m.input.SetValue("stop")
	m.Reset()
	if m.input.Value() != "" {
		t.Error("Reset should clear the input")
	}
}

func TestUpdateTabCompletes(t *testing.T) {
	m := New()
	m.Focus()
	m.input.SetValue("sto")
	m.input.CursorEnd()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != "stop" {
		t.Errorf("tab should complete sto -> stop, got %q", got)
	}
	// Tab with no available completion is a no-op.
	m.input.SetValue("zzz")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != "zzz" {
		t.Errorf("tab without completion should be a no-op, got %q", got)
	}
	// Regular keys go to the text input.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	if got := m.input.Value(); got != "zzz!" {
		t.Errorf("rune should be typed into the input, got %q", got)
	}
}

func TestViewStates(t *testing.T) {
	m := New()
	m.Focus()
	m.input.SetValue("sto")
	m.input.CursorEnd()
	if got := m.View(60); !strings.Contains(got, "p ") { // ghost completion "p" + hint space
		t.Error("view should render the ghost completion")
	}
	m.SetError("unknown command")
	if got := m.View(60); !strings.Contains(got, "unknown command") {
		t.Error("view should render the error instead of the input")
	}
}

func TestGhostBranches(t *testing.T) {
	m := New()
	m.Focus()

	// Exact match without trailing space: hint only.
	m.input.SetValue("logs")
	if g := m.ghost(); g.completion != "" || g.hint == "" {
		t.Errorf("exact match should give hint only, got %+v", g)
	}
	// Exact match with trailing space: hint only.
	m.input.SetValue("logs ")
	if g := m.ghost(); g.completion != "" || g.hint == "" {
		t.Errorf("exact match + space should give hint only, got %+v", g)
	}
	// Unknown name with trailing space: nothing.
	m.input.SetValue("zzz ")
	if g := m.ghost(); g.completion != "" || g.hint != "" {
		t.Errorf("unknown + space should give nothing, got %+v", g)
	}
	// Two words: autocomplete is off.
	m.input.SetValue("logs --tail")
	if g := m.ghost(); g.completion != "" || g.hint != "" {
		t.Errorf("two words should give nothing, got %+v", g)
	}
	// Empty input.
	m.input.SetValue("")
	if g := m.ghost(); g.completion != "" || g.hint != "" {
		t.Errorf("empty input should give nothing, got %+v", g)
	}
	// Whitespace-only input: Fields yields no parts.
	m.input.SetValue("   ")
	if g := m.ghost(); g.completion != "" || g.hint != "" {
		t.Errorf("whitespace input should give nothing, got %+v", g)
	}
}

func TestGhostString(t *testing.T) {
	m := New()
	m.Focus()

	// completion + hint ("ru" -> "run" has a hint).
	m.input.SetValue("ru")
	if gs := m.ghostString(); !strings.HasPrefix(gs, "n ") {
		t.Errorf("ghostString for ru = %q, want completion+hint", gs)
	}
	// completion only ("sto" -> "stop", empty hint).
	m.input.SetValue("sto")
	if gs := m.ghostString(); gs != "p" {
		t.Errorf("ghostString for sto = %q, want p", gs)
	}
	// hint only, no trailing space: leading space added.
	m.input.SetValue("logs")
	if gs := m.ghostString(); !strings.HasPrefix(gs, " ") {
		t.Errorf("ghostString for logs = %q, want leading space", gs)
	}
	// hint only, trailing space: hint as-is.
	m.input.SetValue("logs ")
	if gs := m.ghostString(); strings.HasPrefix(gs, " ") || gs == "" {
		t.Errorf("ghostString for 'logs ' = %q, want bare hint", gs)
	}
	// nothing.
	m.input.SetValue("zzz")
	if gs := m.ghostString(); gs != "" {
		t.Errorf("ghostString for zzz = %q, want empty", gs)
	}
}

func TestPlaceholderPerResource(t *testing.T) {
	m := New()
	tests := []struct {
		resource string
		want     string
	}{
		{"containers", "run  start  stop"},
		{"images", "build <dir>"},
		{"networks", "create  rm  networks"},
		{"volumes", "create  rm  prune"},
		{"hosts", "connect  add"},
		{"compose", "create <dir>  up  down"},
	}
	for _, tt := range tests {
		m.SetResource(tt.resource)
		if !strings.Contains(m.input.Placeholder, tt.want) {
			t.Errorf("placeholder for %s = %q, want it to contain %q", tt.resource, m.input.Placeholder, tt.want)
		}
	}
	// tcp:// compose placeholder hides host-only ops.
	m.SetResource("compose")
	m.SetHostCompose(false)
	if strings.Contains(m.input.Placeholder, "up  down") || !strings.Contains(m.input.Placeholder, "backups") {
		t.Errorf("tcp compose placeholder = %q, want API-only set", m.input.Placeholder)
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
