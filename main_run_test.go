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
	"github.com/kirg0/d9c/internal/dockerctx"
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
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
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

// writeDockerContext creates a minimal Docker CLI context store entry.
func writeDockerContext(t *testing.T, dir, name, host string, tlsFiles ...string) {
	t.Helper()
	metaDir := filepath.Join(dir, "contexts", "meta", dockerctx.ID(name))
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{
		"Name":      name,
		"Endpoints": map[string]any{"docker": map[string]any{"Host": host, "SkipTLSVerify": len(tlsFiles) > 0}},
	})
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), meta, 0o600); err != nil {
		t.Fatal(err)
	}
	tlsDir := filepath.Join(dir, "contexts", "tls", dockerctx.ID(name), "docker")
	for _, f := range tlsFiles {
		if err := os.MkdirAll(tlsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tlsDir, f), []byte("pem"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplyDockerContext(t *testing.T) {
	dir := t.TempDir()
	writeDockerContext(t, dir, "prod", "tcp://prod:2376", "ca.pem", "cert.pem", "key.pem")
	writeDockerContext(t, dir, "lab", "ssh://ops@lab")
	noEnv := func(string) string { return "" }

	// -context points cfg at the endpoint and its TLS files.
	cfg := &config.Config{Context: "prod", Host: config.DefaultHost}
	h, err := applyDockerContext(cfg, dir, noEnv)
	if err != nil || h == nil {
		t.Fatalf("applyDockerContext = %v, %v", h, err)
	}
	tlsDir := filepath.Join(dir, "contexts", "tls", dockerctx.ID("prod"), "docker")
	if cfg.Host != "tcp://prod:2376" || cfg.TLSCACert != filepath.Join(tlsDir, "ca.pem") || cfg.TLSKey == "" {
		t.Errorf("cfg = %+v", cfg)
	}
	if h.Name != "prod" || !h.HasTLS() {
		t.Errorf("host = %+v", h)
	}

	// DOCKER_CONTEXT is honored when neither -H nor DOCKER_HOST is set.
	cfg = &config.Config{Host: config.DefaultHost}
	env := func(k string) string { return map[string]string{"DOCKER_CONTEXT": "lab"}[k] }
	if h, err := applyDockerContext(cfg, dir, env); err != nil || h == nil || cfg.Host != "ssh://ops@lab" || cfg.Context != "lab" || cfg.TLSCACert != "" {
		t.Errorf("DOCKER_CONTEXT: host=%v err=%v cfg=%+v", h, err, cfg)
	}

	// Nothing selected, or demo mode: cfg untouched.
	for _, cfg := range []*config.Config{{Host: "tcp://x"}, {Demo: true, Context: "prod", Host: "tcp://x"}} {
		if h, err := applyDockerContext(cfg, dir, noEnv); h != nil || err != nil || cfg.Host != "tcp://x" {
			t.Errorf("no-op case: host=%v err=%v cfg=%+v", h, err, cfg)
		}
	}

	// Unknown context and -H/-context conflict are startup errors.
	if _, err := applyDockerContext(&config.Config{Context: "nope"}, dir, noEnv); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("unknown context err = %v", err)
	}
	if _, err := applyDockerContext(&config.Config{Context: "prod", HostFlagSet: true}, dir, noEnv); err == nil {
		t.Error("-H with -context must conflict")
	}
}

func TestApplySavedHostTLS(t *testing.T) {
	store := hosts.NewStore([]hosts.Host{{Name: "prod", Host: "tcp://prod:2376", TLSCACert: "ca", TLSCert: "cert", TLSKey: "key"}}, nil)

	cfg := &config.Config{Host: "tcp://prod:2376"}
	applySavedHostTLS(cfg, store)
	if cfg.TLSCACert != "ca" || cfg.TLSCert != "cert" || cfg.TLSKey != "key" {
		t.Errorf("saved TLS not applied: %+v", cfg)
	}

	// Explicit -tls* flags win; unknown hosts stay untouched.
	cfg = &config.Config{Host: "tcp://prod:2376", TLSCACert: "flag-ca"}
	applySavedHostTLS(cfg, store)
	if cfg.TLSCACert != "flag-ca" || cfg.TLSCert != "" {
		t.Errorf("flags overridden: %+v", cfg)
	}
	cfg = &config.Config{Host: "tcp://other:2376"}
	applySavedHostTLS(cfg, store)
	if cfg.TLSCACert != "" {
		t.Errorf("unknown host got TLS: %+v", cfg)
	}
}

func TestRememberContextHost(t *testing.T) {
	saves := 0
	store := hosts.NewStore(nil, func([]hosts.Host) error { saves++; return nil })
	h := hosts.Host{Name: "prod", Host: "tcp://prod:2376", TLSCACert: "ca"}
	rememberContextHost(store, h)
	rememberContextHost(store, h) // same URL: skipped, not saved again
	if saves != 1 || len(store.Hosts) != 1 || store.Hosts[0] != h {
		t.Errorf("saves=%d hosts=%+v", saves, store.Hosts)
	}
	failing := hosts.NewStore(nil, func([]hosts.Host) error { return errors.New("disk full") })
	rememberContextHost(failing, h) // only warns
}

// A -context naming a missing context aborts startup with a clear error, and
// combining it with -H is rejected like the Docker CLI does.
func TestRunContextErrors(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "d9c.yaml")
	if err := runWithArgs(t, "-config", cfgFile, "-context", "ghost"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("missing context err = %v", err)
	}
	if err := runWithArgs(t, "-config", cfgFile, "-H", "tcp://x:2375", "-context", "ghost"); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Errorf("-H + -context err = %v", err)
	}
}
