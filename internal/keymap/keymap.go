// Package keymap maps the normal-mode action keys (inspect, logs, filter, …) to
// the keys that trigger them, loaded from the same d9c-config.yaml file the
// theme uses. It ships sensible defaults and lets the user remap any action via
// a "keys:" section without touching navigation (j/k/arrows), Enter or the exit
// keys (q/esc/Ctrl+C), which stay fixed so the app can never lock itself out.
//
// Example d9c-config.yaml:
//
//	keys:                # optional; only the actions you want to change
//	  logs: g            # show logs with "g" instead of "l"
//	  filter: f          # filter with "f" instead of "/"
package keymap

import (
	"fmt"
	"maps"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Action identifies a remappable normal-mode action. The string value is also
// the key used under "keys:" in the config file.
type Action string

// The remappable normal-mode actions. Navigation, Enter and the exit keys are
// intentionally not actions: they stay fixed.
const (
	Inspect   Action = "inspect"
	Logs      Action = "logs"
	Edit      Action = "edit"
	Exec      Action = "exec"
	Filter    Action = "filter"
	Command   Action = "command"
	ToggleAll Action = "toggle-all"
	Stats     Action = "stats"
	Select    Action = "select"
	Copy      Action = "copy"
	Refresh   Action = "refresh"
	Pause     Action = "pause"
	Help      Action = "help"
)

// actionOrder lists every action in a stable order (config validation, docs).
var actionOrder = []Action{
	Inspect, Logs, Edit, Exec, Filter, Command,
	ToggleAll, Stats, Select, Copy, Refresh, Pause, Help,
}

// defaults holds the built-in key for every action. Space is stored as the
// bubbletea key string " " (the config accepts the alias "space").
var defaults = map[Action]string{
	Inspect:   "i",
	Logs:      "l",
	Edit:      "e",
	Exec:      "x",
	Filter:    "/",
	Command:   ":",
	ToggleAll: "a",
	Stats:     "s",
	Select:    " ",
	Copy:      "y",
	Refresh:   "r",
	Pause:     "p",
	Help:      "?",
}

// reserved keys carry a fixed meaning the user can't take over for an action:
// rebinding them could trap the user with no way to quit or escape a mode.
var reserved = map[string]bool{
	"enter":  true,
	"esc":    true,
	"escape": true,
	"q":      true,
	"ctrl+c": true,
}

// Map binds actions to keys (and the reverse, for key dispatch). The zero value
// is unusable; build one with Default or Resolve.
type Map struct {
	byAction map[Action]string
	byKey    map[string]Action
}

// Default returns the built-in key bindings.
func Default() Map {
	return build(defaults)
}

// build constructs a Map from an action→key table, deriving the reverse index.
func build(byAction map[Action]string) Map {
	m := Map{
		byAction: make(map[Action]string, len(byAction)),
		byKey:    make(map[string]Action, len(byAction)),
	}
	for a, k := range byAction {
		m.byAction[a] = k
		m.byKey[k] = a
	}
	return m
}

// KeyFor returns the key bound to the action (empty if the action is unknown).
func (m Map) KeyFor(a Action) string { return m.byAction[a] }

// ActionFor returns the action a key triggers, reporting whether one is bound.
func (m Map) ActionFor(key string) (Action, bool) {
	a, ok := m.byKey[key]
	return a, ok
}

// Actions returns every action in stable order (for the help screen).
func Actions() []Action { return append([]Action(nil), actionOrder...) }

// Names returns the configurable action names in stable order (for docs/errors).
func Names() []string {
	out := make([]string, len(actionOrder))
	for i, a := range actionOrder {
		out[i] = string(a)
	}
	return out
}

// config is the on-disk shape of the "keys:" section.
type config struct {
	Keys map[string]string `yaml:"keys"`
}

// Load reads the config file at path and resolves its "keys:" overrides on top
// of the defaults. A missing file yields the defaults (not an error); malformed
// YAML, an unknown action, an empty/reserved key, or a key bound to two actions
// is an error.
func Load(path string) (Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Map{}, fmt.Errorf("read config file: %w", err)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Map{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return Resolve(cfg.Keys)
}

// Resolve applies the given action→key overrides on top of the defaults,
// validating action names, keys and conflicts. A nil/empty map yields the
// defaults unchanged.
func Resolve(overrides map[string]string) (Map, error) {
	byAction := make(map[Action]string, len(defaults))
	maps.Copy(byAction, defaults)
	for rawName, rawKey := range overrides {
		name := Action(strings.ToLower(strings.TrimSpace(rawName)))
		if _, ok := defaults[name]; !ok {
			return Map{}, fmt.Errorf("unknown action %q (valid: %s)", rawName, strings.Join(Names(), ", "))
		}
		key := normalizeKey(rawKey)
		if key == "" {
			return Map{}, fmt.Errorf("action %q: empty key", rawName)
		}
		if reserved[key] {
			return Map{}, fmt.Errorf("action %q: key %q is reserved (fixed: enter, esc, q, ctrl+c)", rawName, key)
		}
		byAction[name] = key
	}
	if err := checkConflicts(byAction); err != nil {
		return Map{}, err
	}
	return build(byAction), nil
}

// checkConflicts reports the first key bound to more than one action.
func checkConflicts(byAction map[Action]string) error {
	seen := make(map[string]Action, len(byAction))
	// Iterate in stable order so the reported conflict is deterministic.
	for _, a := range actionOrder {
		key := byAction[a]
		if other, dup := seen[key]; dup {
			return fmt.Errorf("key %q is bound to both %q and %q", display(key), other, a)
		}
		seen[key] = a
	}
	return nil
}

// normalizeKey trims a configured key and maps the "space" alias to the
// bubbletea space key string. Other keys are passed through verbatim (they must
// match bubbletea key names, e.g. "ctrl+d", "f5", "/").
func normalizeKey(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "space") {
		return " "
	}
	return s
}

// display renders a key for messages, showing the space key as "space".
func display(key string) string {
	if key == " " {
		return "space"
	}
	return key
}

// Display returns a human-readable label for the key bound to an action (e.g.
// "space" for the space key), for use in the help screen.
func (m Map) Display(a Action) string { return display(m.byAction[a]) }
