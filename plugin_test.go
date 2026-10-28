package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/version"
)

// pluginDir is the repository's Claude Code plugin; the repository root is
// its marketplace.
const pluginDir = "plugins/d9c"

// pluginNameRE is the kebab-case form plugin, marketplace and skill names use.
var pluginNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// marketplaceFile is .claude-plugin/marketplace.json.
type marketplaceFile struct {
	Name  string `json:"name"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Plugins []struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
	} `json:"plugins"`
}

// pluginManifest is plugins/d9c/.claude-plugin/plugin.json.
type pluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
	UserConfig map[string]struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"userConfig"`
}

// mcpConfig is plugins/d9c/.mcp.json.
type mcpConfig struct {
	MCPServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
}

// readJSON decodes path into v; fields the test does not model are left to
// `claude plugin validate`.
func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// knownKeys fails for a top-level key of the JSON object at path that is not
// in keys: Claude Code strips unknown keys silently, so a typo would just
// disappear.
func knownKeys(t *testing.T, path string, keys ...string) {
	t.Helper()
	var m map[string]json.RawMessage
	readJSON(t, path, &m)
	for k := range m {
		if !slices.Contains(keys, k) {
			t.Errorf("%s: unknown key %q", path, k)
		}
	}
}

func TestManifestKeys(t *testing.T) {
	knownKeys(t, ".claude-plugin/marketplace.json", "$schema", "name", "description", "owner", "metadata", "plugins")
	knownKeys(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), "$schema", "name", "displayName", "version",
		"description", "author", "homepage", "repository", "license", "keywords", "metadata", "icon", "userConfig")
	knownKeys(t, filepath.Join(pluginDir, ".mcp.json"), "mcpServers")
}

func TestMarketplaceManifest(t *testing.T) {
	var m marketplaceFile
	readJSON(t, ".claude-plugin/marketplace.json", &m)
	if !pluginNameRE.MatchString(m.Name) || m.Owner.Name == "" {
		t.Errorf("marketplace name %q / owner %q", m.Name, m.Owner.Name)
	}
	if len(m.Plugins) != 1 {
		t.Fatalf("plugins = %+v, want the d9c plugin", m.Plugins)
	}
	p := m.Plugins[0]
	if p.Source != "./"+pluginDir || strings.Contains(p.Source, "..") || p.Description == "" {
		t.Errorf("plugin entry = %+v", p)
	}
	var manifest pluginManifest
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &manifest)
	if p.Name != manifest.Name {
		t.Errorf("marketplace entry %q and plugin.json %q must share the name", p.Name, manifest.Name)
	}
}

func TestPluginManifest(t *testing.T) {
	var m pluginManifest
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &m)
	if !pluginNameRE.MatchString(m.Name) || m.Description == "" || m.Author.Name == "" {
		t.Errorf("manifest = %+v", m)
	}
	// The plugin ships with the binary: one version for both.
	if m.Version != version.Version {
		t.Errorf("plugin.json version %q, binary %q: bump them together", m.Version, version.Version)
	}
	for key, opt := range m.UserConfig {
		if opt.Type == "" || opt.Title == "" || opt.Description == "" {
			t.Errorf("userConfig %s = %+v: type, title and description are required", key, opt)
		}
	}
}

// The plugin starts `d9c mcp`, and every ${user_config.KEY} it passes is
// declared in the manifest.
func TestPluginMCPConfig(t *testing.T) {
	var c mcpConfig
	readJSON(t, filepath.Join(pluginDir, ".mcp.json"), &c)
	srv, ok := c.MCPServers["d9c"]
	if !ok || srv.Command != "d9c" || len(srv.Args) == 0 || srv.Args[0] != mcpCommand {
		t.Fatalf("mcpServers = %+v, want d9c running `d9c mcp`", c.MCPServers)
	}
	var m pluginManifest
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &m)
	ref := regexp.MustCompile(`\$\{user_config\.([A-Za-z_][A-Za-z0-9_]*)\}`)
	for _, a := range srv.Args {
		for _, k := range ref.FindAllStringSubmatch(a, -1) {
			if _, ok := m.UserConfig[k[1]]; !ok {
				t.Errorf("arg %q references the undeclared option %q", a, k[1])
			}
		}
	}
	if !slices.ContainsFunc(srv.Args, func(a string) bool { return strings.HasPrefix(a, "--host=") }) {
		t.Errorf("args %v: the host option must be one --host=<value> argument, so an empty value still parses", srv.Args)
	}
	// An empty option (the default) means "no saved host".
	cfg := &config.Config{}
	if _, err := cli.ParseGlobals(cfg, []string{"--host="}); err != nil || cfg.SavedHost != "" {
		t.Errorf("--host= gives %q, %v; want no saved host", cfg.SavedHost, err)
	}
}
