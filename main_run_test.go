package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/settings"
	"github.com/kirg0/d9c/internal/version"
)

// runWithArgs executes run() with the given command line on a fresh flag set,
// so repeated calls don't hit "flag redefined" panics.
func runWithArgs(t *testing.T, args ...string) error {
	t.Helper()
	oldCmdLine, oldArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = oldCmdLine, oldArgs })
	flag.CommandLine = flag.NewFlagSet("d9c-test", flag.ContinueOnError)
	os.Args = append([]string{"d9c"}, args...)
	t.Setenv("DOCKER_HOST", "")
	return run()
}

func TestRunPrintsVersion(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	runErr := runWithArgs(t, "-version")
	os.Stdout = oldStdout
	_ = w.Close()
	out, _ := io.ReadAll(r)

	if runErr != nil {
		t.Fatalf("run -version: %v", runErr)
	}
	if want := "d9c " + version.String(); strings.TrimSpace(string(out)) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// Each startup stage reports a broken section with its own context before the
// TUI is ever started.
func TestRunReportsBrokenConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	missing := filepath.Join(dir, "missing.yaml")
	badHosts := write("hosts.json", "{not json")
	badPlugins := write("plugins.yaml", "plugins: [")

	tests := []struct {
		name    string
		config  string
		hosts   string
		plugins string
		want    string
	}{
		{"malformed config", "lang: [", missing, missing, "loading config"},
		{"malformed legacy hosts", "", badHosts, missing, "migrating saved hosts"},
		{"malformed plugins", "", missing, badPlugins, "loading plugins"},
		{"unknown language", "lang: klingon\n", missing, missing, "loading language"},
		{"unknown theme", "theme: no-such-theme\n", missing, missing, "loading theme"},
		{"unknown key action", "keys:\n  no-such-action: z\n", missing, missing, "loading keybindings"},
		{"negative alert", "alerts:\n  cpu: -5\n  mem: 10\n", missing, missing, "loading alerts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := write(strings.ReplaceAll(tt.name, " ", "-")+".yaml", tt.config)
			err := runWithArgs(t, "-config", cfg, "-hosts-file", tt.hosts, "-plugins-file", tt.plugins)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMigrateLegacyHosts(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "d9c-hosts.json")
	data, err := json.Marshal(struct {
		Hosts []hosts.Host `json:"hosts"`
	}{[]hosts.Host{{Name: "prod", Host: "ssh://ops@prod"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "d9c-config.yaml")
	set, err := settings.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyHosts(set, &config.Config{HostsFile: legacy}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !set.HasHosts() || set.File.Hosts[0].Name != "prod" {
		t.Errorf("hosts after migration = %+v", set.File.Hosts)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("migrated hosts were not saved: %v", err)
	}
	if _, err := os.Stat(legacy + ".migrated"); err != nil {
		t.Errorf("legacy file was not renamed: %v", err)
	}

	// Already migrated: the (now renamed) legacy file is never consulted again.
	if err := migrateLegacyHosts(set, &config.Config{HostsFile: filepath.Join(dir, "garbage.json")}); err != nil {
		t.Errorf("second migration = %v, want no-op", err)
	}

	// Nothing to migrate: no hosts, no file written.
	empty, _ := settings.Load(filepath.Join(dir, "empty.yaml"))
	if err := migrateLegacyHosts(empty, &config.Config{HostsFile: filepath.Join(dir, "absent.json")}); err != nil {
		t.Errorf("migration without a legacy file = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "empty.yaml")); !os.IsNotExist(err) {
		t.Error("nothing to migrate must not create the config file")
	}
}

func TestRememberHost(t *testing.T) {
	var saved [][]hosts.Host
	store := hosts.NewStore(nil, func(list []hosts.Host) error {
		saved = append(saved, list)
		return nil
	})

	rememberHost(store, "")
	rememberHost(store, config.DefaultHost)
	if len(saved) != 0 {
		t.Fatalf("implicit hosts must not be saved, got %v", saved)
	}
	rememberHost(store, "tcp://10.0.0.5:2375")
	rememberHost(store, "tcp://10.0.0.5:2375") // already known
	if len(saved) != 1 || len(saved[0]) != 1 || saved[0][0].Host != "tcp://10.0.0.5:2375" {
		t.Errorf("saves = %+v, want one save with the new host", saved)
	}

	// A failing save only warns; startup continues.
	failing := hosts.NewStore(nil, func([]hosts.Host) error { return errors.New("disk full") })
	rememberHost(failing, "ssh://ops@box")
}
