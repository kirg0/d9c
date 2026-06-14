// Package plugins loads user-defined commands from a small YAML file, letting
// d9c be extended with custom actions (like k9s plugins). Each plugin binds a
// name (invoked as ":name") and/or a key to a local command, scoped to a
// resource view, with ${PLACEHOLDER} substitution from the selected row.
//
// Example d9c-plugins.yaml:
//
//	plugins:
//	  - name: dive
//	    key: ctrl+d
//	    scope: images
//	    description: Explore image layers with dive
//	    command: dive
//	    args: ["${ID}"]
//	  - name: htop
//	    scope: containers
//	    command: docker
//	    args: ["-H", "${HOST}", "exec", "-it", "${ID}", "htop"]
//	  - name: df
//	    scope: "*"
//	    background: true          # stream output to the console instead of a TTY
//	    command: docker
//	    args: ["-H", "${HOST}", "system", "df"]
package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Plugin is a single user-defined action.
type Plugin struct {
	Name        string   `yaml:"name"`
	Key         string   `yaml:"key"`
	Scope       string   `yaml:"scope"` // containers|images|networks|volumes|compose|hosts|* (default *)
	Description string   `yaml:"description"`
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args"`
	// Background runs the command detached, streaming its output to the operation
	// console; the default (false) hands the terminal over for an interactive TTY.
	Background bool `yaml:"background"`
}

// Set is an immutable collection of loaded plugins. All methods are nil-safe so
// callers can hold a nil *Set when no plugins are configured.
type Set struct {
	plugins []Plugin
}

type fileFormat struct {
	Plugins []Plugin `yaml:"plugins"`
}

var validScopes = map[string]bool{
	"*": true, "containers": true, "images": true,
	"networks": true, "volumes": true, "compose": true, "hosts": true,
}

// DefaultPath returns the plugins file location next to the running binary,
// falling back to the current directory if the executable path is unavailable.
func DefaultPath() string {
	const name = "d9c-plugins.yaml"
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exe), name)
}

// Load reads and validates the plugins file at path. A missing file yields an
// empty set (not an error); malformed YAML or an invalid plugin is an error.
func Load(path string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Set{}, nil
		}
		return nil, fmt.Errorf("read plugins file: %w", err)
	}
	var ff fileFormat
	if err := yaml.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parse plugins file %s: %w", path, err)
	}
	for i := range ff.Plugins {
		p := &ff.Plugins[i]
		p.Name = strings.TrimSpace(p.Name)
		p.Key = strings.ToLower(strings.TrimSpace(p.Key))
		p.Scope = strings.ToLower(strings.TrimSpace(p.Scope))
		if p.Scope == "" {
			p.Scope = "*"
		}
		if p.Name == "" {
			return nil, fmt.Errorf("plugin #%d: name is required", i+1)
		}
		if p.Command == "" {
			return nil, fmt.Errorf("plugin %q: command is required", p.Name)
		}
		if !validScopes[p.Scope] {
			return nil, fmt.Errorf("plugin %q: invalid scope %q", p.Name, p.Scope)
		}
	}
	return &Set{plugins: ff.Plugins}, nil
}

// New builds a Set directly from already-validated plugins. Load is the usual
// entry point; New is handy for tests and programmatic configuration.
func New(ps []Plugin) *Set { return &Set{plugins: ps} }

// All returns every loaded plugin.
func (s *Set) All() []Plugin {
	if s == nil {
		return nil
	}
	return s.plugins
}

// inScope reports whether a plugin applies to the given resource scope.
func inScope(p Plugin, scope string) bool {
	return p.Scope == "*" || p.Scope == scope
}

// ForScope returns the plugins applicable in the given resource view (its own
// scope plus the wildcard "*").
func (s *Set) ForScope(scope string) []Plugin {
	if s == nil {
		return nil
	}
	var out []Plugin
	for _, p := range s.plugins {
		if inScope(p, scope) {
			out = append(out, p)
		}
	}
	return out
}

// Lookup finds a plugin by name applicable in the given scope.
func (s *Set) Lookup(scope, name string) (Plugin, bool) {
	if s == nil {
		return Plugin{}, false
	}
	for _, p := range s.plugins {
		if p.Name == name && inScope(p, scope) {
			return p, true
		}
	}
	return Plugin{}, false
}

// ByKey finds a plugin bound to key in the given scope.
func (s *Set) ByKey(scope, key string) (Plugin, bool) {
	if s == nil || key == "" {
		return Plugin{}, false
	}
	for _, p := range s.plugins {
		if p.Key == key && inScope(p, scope) {
			return p, true
		}
	}
	return Plugin{}, false
}

// Substitute expands ${VAR} placeholders in the plugin's command and args using
// vars; unknown placeholders are left untouched so typos stay visible.
func Substitute(p Plugin, vars map[string]string) (command string, args []string) {
	command = expand(p.Command, vars)
	args = make([]string, len(p.Args))
	for i, a := range p.Args {
		args[i] = expand(a, vars)
	}
	return command, args
}

func expand(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}
