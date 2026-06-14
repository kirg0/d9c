package cmdline

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ghostStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#414868"))

type cmdDef struct {
	name string
	hint string
}

// ── per-resource command sets ─────────────────────────────────────────────────

var containerCmds = []cmdDef{
	{"cp", "<local-path> <container-dir> (upload)"},
	{"exec", "[command] (default: shell)"},
	{"files", "[path] (filesystem browser)"},
	{"kill", "[signal]"},
	{"logs", "[--tail 100] [--since 1h] [--until …]"},
	{"restart", ""},
	{"rm", "[-f]"},
	{"run", "(modal form: image/ports/env/volumes)"},
	{"start", ""},
	{"stop", ""},
}

var imageCmds = []cmdDef{
	{"build", "<dir> [tag]"},
	{"exec", "(one-off --rm -it container form)"},
	{"tag", "<new-ref>"},
	{"push", ""},
	{"history", "(layers)"},
	{"pull", ""},
	{"prune", ""},
	{"rm", "[-f]"},
	{"run", "(form, selected image pre-filled)"},
}

var networkCmds = []cmdDef{
	{"create", "(modal form)"},
	{"rm", ""},
}

var volumeCmds = []cmdDef{
	{"create", "(modal form)"},
	{"prune", ""},
	{"rm", ""},
}

var hostCmds = []cmdDef{
	{"connect", ""},
	{"add", "<name> <url>"},
	{"edit", "<name> <url>"},
	{"rm", ""},
}

var composeCmds = []cmdDef{
	{"create", "<dir>"},
	{"up", ""},
	{"down", ""},
	{"pull", ""},
	{"config", ""},
	{"edit", ""},
	{"backup", ""},
	{"backups", "(catalog)"},
	{"restore", "[file.tar.gz]"},
	{"start", ""},
	{"stop", ""},
	{"restart", ""},
	{"pause", ""},
	{"unpause", ""},
	{"remove", ""},
}

// View-switching commands are always available, plus the global events feed
// and system-wide operations.
var viewCmds = []cmdDef{
	{"containers", ""},
	{"images", ""},
	{"networks", ""},
	{"volumes", ""},
	{"hosts", "(= dashboard: STATUS + агрегат docker info)"},
	{"compose", ""},
	{"events", "(live daemon events)"},
	{"system", "df | prune (полная очистка с подтверждением)"},
	{"theme", "<name> (сменить цветовую тему на лету)"},
	{"interval", "<dur> | pause | resume (интервал автообновления)"},
	{"alert", "cpu <%> | mem <%> | off (пороги CPU/MEM)"},
}

// CommandMsg is emitted when the user presses Enter in the command line.
type CommandMsg struct {
	Name string
	Args []string
}

type Model struct {
	input      textinput.Model
	lastErr    string
	resource   string // "containers" | "images" | "networks" | "volumes"
	pluginCmds []cmdDef
}

func New() Model {
	ti := textinput.New()
	ti.CharLimit = 256
	m := Model{input: ti, resource: "containers"}
	m.updatePlaceholder()
	return m
}

// SetResource updates the active resource context so autocomplete shows the
// right command set.
func (m *Model) SetResource(r string) {
	m.resource = r
	m.updatePlaceholder()
}

// SetPluginCommands registers the user-defined plugin names available in the
// current view so they appear in autocomplete.
func (m *Model) SetPluginCommands(names []string) {
	m.pluginCmds = make([]cmdDef, 0, len(names))
	for _, n := range names {
		m.pluginCmds = append(m.pluginCmds, cmdDef{n, "(plugin)"})
	}
}

// IsBuiltin reports whether name is a built-in command for the current view (so
// callers can let built-ins take precedence over same-named plugins).
func (m Model) IsBuiltin(name string) bool {
	for _, c := range m.builtinCommands() {
		if c.name == name {
			return true
		}
	}
	return false
}

func (m *Model) updatePlaceholder() {
	switch m.resource {
	case "images":
		m.input.Placeholder = "run  exec  build <dir> [tag]  tag <new-ref>  push  history  pull  rm [-f]  prune…"
	case "networks":
		m.input.Placeholder = "create  rm  networks  containers…"
	case "volumes":
		m.input.Placeholder = "create  rm  prune  volumes  containers…"
	case "hosts":
		m.input.Placeholder = "connect  add <name> <url>  edit <name> <url>  rm…"
	case "compose":
		m.input.Placeholder = "create <dir>  up  down  pull  config  edit  backup  start  stop  restart  pause  unpause  remove…"
	default:
		m.input.Placeholder = "run  start  stop  restart  logs  rm  kill  exec  events…"
	}
}

func (m *Model) Focus() { m.input.Focus(); m.lastErr = "" }
func (m *Model) Blur()  { m.input.Blur() }
func (m *Model) Reset() { m.input.Reset() }

func (m *Model) SetError(err string) { m.lastErr = err }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "tab" {
		if g := m.ghost(); g.completion != "" {
			m.input.SetValue(m.input.Value() + g.completion)
			m.input.CursorEnd()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) Parse() *CommandMsg {
	parts := strings.Fields(m.input.Value())
	if len(parts) == 0 {
		return nil
	}
	return &CommandMsg{Name: strings.ToLower(parts[0]), Args: parts[1:]}
}

func (m Model) View(width int) string {
	prefix := styles.BottomBarPrefix.Render(":")
	inputWidth := width - lipgloss.Width(prefix)

	if m.lastErr != "" {
		inputView := styles.BottomBar.Width(inputWidth).Render(styles.ErrorStyle.Render(m.lastErr))
		return lipgloss.JoinHorizontal(lipgloss.Left, prefix, inputView)
	}

	rendered := m.input.View()
	if gs := m.ghostString(); gs != "" {
		rendered += ghostStyle.Render(gs)
	}
	inputView := styles.BottomBar.Width(inputWidth).Render(rendered)
	return lipgloss.JoinHorizontal(lipgloss.Left, prefix, inputView)
}

// ── autocomplete ──────────────────────────────────────────────────────────────

type ghostResult struct {
	completion string
	hint       string
}

// resourceCmds returns the built-in command set specific to a resource view.
func resourceCmds(resource string) []cmdDef {
	switch resource {
	case "images":
		return imageCmds
	case "networks":
		return networkCmds
	case "volumes":
		return volumeCmds
	case "hosts":
		return hostCmds
	case "compose":
		return composeCmds
	default:
		return containerCmds
	}
}

// builtinCommands returns the built-in command set for the current resource plus
// the always-available view-switch commands.
func (m Model) builtinCommands() []cmdDef {
	specific := resourceCmds(m.resource)
	out := make([]cmdDef, 0, len(specific)+len(viewCmds))
	out = append(out, specific...)
	out = append(out, viewCmds...)
	return out
}

// CmdHelp is a built-in command name with its argument hint, for the help screen.
type CmdHelp struct {
	Name string
	Hint string
}

// CommandsFor returns the built-in commands specific to the given resource view
// (excluding the global view-switch commands), for documentation/help.
func CommandsFor(resource string) []CmdHelp {
	specific := resourceCmds(resource)
	out := make([]CmdHelp, 0, len(specific))
	for _, c := range specific {
		out = append(out, CmdHelp{Name: c.name, Hint: c.hint})
	}
	return out
}

// commands returns the full autocomplete set: built-ins plus user plugins.
func (m Model) commands() []cmdDef {
	builtins := m.builtinCommands()
	if len(m.pluginCmds) == 0 {
		return builtins
	}
	out := make([]cmdDef, 0, len(builtins)+len(m.pluginCmds))
	out = append(out, builtins...)
	out = append(out, m.pluginCmds...)
	return out
}

func (m Model) ghost() ghostResult {
	val := m.input.Value()
	if val == "" {
		return ghostResult{}
	}
	parts := strings.Fields(val)
	if len(parts) == 0 {
		return ghostResult{}
	}
	name := strings.ToLower(parts[0])
	trailingSpace := strings.HasSuffix(val, " ")
	cmds := m.commands()

	switch {
	case len(parts) == 1 && !trailingSpace:
		// Exact match wins (e.g. "rm" must not suggest "rmi").
		for _, c := range cmds {
			if c.name == name {
				return ghostResult{hint: c.hint}
			}
		}
		// First prefix completion.
		for _, c := range cmds {
			if strings.HasPrefix(c.name, name) && c.name != name {
				return ghostResult{completion: c.name[len(name):], hint: c.hint}
			}
		}

	case len(parts) == 1 && trailingSpace:
		for _, c := range cmds {
			if c.name == name {
				return ghostResult{hint: c.hint}
			}
		}
	}
	return ghostResult{}
}

func (m Model) ghostString() string {
	g := m.ghost()
	switch {
	case g.completion != "" && g.hint != "":
		return g.completion + " " + g.hint
	case g.completion != "":
		return g.completion
	case g.hint != "":
		if strings.HasSuffix(m.input.Value(), " ") {
			return g.hint
		}
		return " " + g.hint
	}
	return ""
}
