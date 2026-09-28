package ui

import (
	"fmt"
	"strings"

	"github.com/kirg0/d9c/internal/i18n"
	"github.com/kirg0/d9c/internal/keymap"
	"github.com/kirg0/d9c/internal/ui/cmdline"
)

// Read-only mode forbids every action that changes state on the Docker host.
// It is on for the whole session when set globally (-read-only flag or
// "readOnly: true" in d9c-config.yaml) and, per host, while connected to a saved
// host marked "read_only: true". The checks live here and run before dispatch:
//   - guardCommand at the top of dispatchCommand (built-in `:` commands);
//   - mutatingKeyOp in handleNormal (keys that start a mutation);
//   - pluginCmd for plugins flagged "mutating: true" (command or key);
//   - the backup picker's restore (Enter).
// Forms (run/push/build/create/cp/…) are reachable only through those commands,
// so blocking the command also blocks the form.

// readOnly reports whether mutating actions are currently forbidden: globally,
// or for the host the session is connected to.
func (m Model) readOnly() bool {
	if m.roGlobal {
		return true
	}
	return m.cfg != nil && m.hostStore.ReadOnlyFor(m.cfg.Host)
}

// errReadOnly is the footer error shown when a mutating action is refused; op
// names the action (":stop", "exec", a plugin name…).
func errReadOnly(op string) error {
	return fmt.Errorf(i18n.T("режим только для чтения: %s запрещено", "read-only mode: %s is disabled"), op)
}

// guardCommand refuses a built-in `:` command that mutates the Docker host while
// read-only mode is on. Plugins are checked by pluginCmd (their own flag).
func (m Model) guardCommand(cmd *cmdline.CommandMsg) error {
	if !m.readOnly() || !cmdline.IsMutating(m.pluginScope(), cmd.Name, cmd.Args) {
		return nil
	}
	name := ":" + cmd.Name
	if cmd.Name == "system" {
		name = ":system prune"
	}
	return errReadOnly(name)
}

// mutatingKeyOp reports whether a normal-mode key starts an action that mutates
// the Docker host in the given view, returning the action's name for the error
// message. imagesSelected is true while images are bulk-selected (then `r`
// removes them). Plugin keys are not covered here (see pluginCmd).
func mutatingKeyOp(view ResourceView, key string, km keymap.Map, imagesSelected bool) (string, bool) {
	if view == ViewImages && imagesSelected && key == "r" {
		return "rm", true
	}
	action, ok := km.ActionFor(key)
	if !ok {
		return "", false
	}
	switch {
	case action == keymap.Exec && view == ViewContainers:
		return "exec", true
	case action == keymap.Edit && view == ViewCompose:
		return "edit", true
	}
	return "", false
}

// syncReadOnly pushes the current read-only state to the command line so its
// autocomplete/placeholder hide the mutating commands. Called whenever the view
// or the connected host changes (via refreshPluginCmds).
func (m *Model) syncReadOnly() {
	m.cmdline.SetReadOnly(m.readOnly())
}

// SetReadOnly turns read-only mode on for the whole session (the -read-only flag
// or "readOnly: true" in the config file). There is intentionally no way to turn
// it off at runtime.
func (m *Model) SetReadOnly(v bool) {
	m.roGlobal = m.roGlobal || v
	m.refreshPluginCmds()
}

// readOnlyCommandList renders the `:` commands still available in the current
// view under read-only mode, for the command-mode footer.
func (m Model) readOnlyCommandList() string {
	cmds := cmdline.CommandsFor(m.pluginScope(), m.composeHostOps, true)
	if len(cmds) == 0 {
		return i18n.T("изменения запрещены (read-only)", "changes are disabled (read-only)")
	}
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return strings.Join(names, " · ")
}
