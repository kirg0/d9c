package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/appdir"
	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/dockerctx"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/i18n"
	"github.com/kirg0/d9c/internal/plugins"
	"github.com/kirg0/d9c/internal/settings"
	"github.com/kirg0/d9c/internal/ui"
	"github.com/kirg0/d9c/internal/ui/styles"
	"github.com/kirg0/d9c/internal/version"
)

func main() {
	if err := run(); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// exitCode is returned by run when the CLI has already reported the outcome
// on stdout/stderr and only the process exit status remains to be set.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

func run() error {
	cfg := config.Load()

	if cfg.ShowVersion {
		fmt.Println("d9c " + version.String())
		return nil
	}

	// A known command word after the global flags selects the CLI; anything
	// else (including no arguments) starts the TUI.
	if args := flag.Args(); len(args) > 0 {
		if args[0] == mcpCommand {
			if code := runMCP(cfg, agent.Builtin(), args[1:], os.Stdin, os.Stdout, os.Stderr); code != 0 {
				return exitCode(code)
			}
			return nil
		}
		if reg := agent.Builtin(); cli.IsCommand(reg, args[0]) {
			if code := runCLI(cfg, reg, args, os.Stdout, os.Stderr); code != 0 {
				return exitCode(code)
			}
			return nil
		}
	}

	set, err := loadSettings(cfg, os.Stderr)
	if err != nil {
		return err
	}
	store := set.Hosts()

	pluginsPath := cfg.PluginsFile
	if pluginsPath == "" {
		pluginsPath = plugins.DefaultPath()
		migrateLegacyFile(pluginsPath, appdir.LegacyPath(plugins.FileName), os.Stderr)
	}
	pluginSet, err := plugins.Load(pluginsPath)
	if err != nil {
		return fmt.Errorf("loading plugins: %w", err)
	}

	lang, err := set.Lang()
	if err != nil {
		return fmt.Errorf("loading language: %w", err)
	}
	i18n.Set(lang)

	palette, err := set.Palette()
	if err != nil {
		return fmt.Errorf("loading theme: %w", err)
	}
	styles.Apply(palette)

	keys, err := set.Keymap()
	if err != nil {
		return fmt.Errorf("loading keybindings: %w", err)
	}

	alertThresholds, err := set.Alerts()
	if err != nil {
		return fmt.Errorf("loading alerts: %w", err)
	}

	var ctxHost *hosts.Host
	if cfg.SavedHost != "" && !cfg.Demo {
		if _, err := applySavedHost(cfg, store); err != nil {
			return err
		}
	} else {
		if ctxHost, err = applyDockerContext(cfg, dockerctx.Dir(), os.Getenv, os.Stderr); err != nil {
			return err
		}
		if ctxHost == nil && !cfg.Demo {
			applySavedHostTLS(cfg, store)
		}
	}

	var backend docker.Backend
	var connectErr error
	var startInHosts bool
	switch {
	case cfg.Demo:
		fb := docker.NewFakeBackend()
		fb.StatsJitter = true // live-looking CPU/MEM graphs in the stats view
		backend = fb

	case !hostConfigured(cfg.Host, cfg.HostFlagSet || cfg.SavedHost != ""):
		// No host specified: don't connect at all. Open the hosts view so the
		// user can pick or add one; connecting happens on :connect / Enter.
		backend = docker.NewDisconnected(nil)
		startInHosts = true

	default:
		b, err := docker.New(cfg)
		if err != nil {
			// Don't exit: start in the hosts view so the user can pick, add, or
			// fix a host and connect. Remember the attempted host for editing.
			connectErr = fmt.Errorf("could not connect to %s: %w", cfg.Host, err)
			backend = docker.NewDisconnected(err)
			startInHosts = true
		} else {
			backend = b
		}
		if ctxHost != nil {
			rememberContextHost(store, *ctxHost)
		} else {
			rememberHost(store, cfg.Host)
		}
	}
	defer backend.Close()

	return ui.Run(cfg, backend, store, set, pluginSet, keys, alertThresholds, connectErr, startInHosts)
}

// loadSettings loads the unified config (migrating the pre-1.31 files on the
// way, with notices written to notices) and imports legacy saved hosts.
func loadSettings(cfg *config.Config, notices io.Writer) (*settings.Store, error) {
	configPath := cfg.ConfigFile
	if configPath == "" {
		configPath = settings.DefaultPath()
		migrateLegacyFile(configPath, appdir.LegacyPath(settings.FileName), notices)
	}
	set, err := settings.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if err := migrateLegacyHosts(set, cfg); err != nil {
		return nil, fmt.Errorf("migrating saved hosts: %w", err)
	}
	return set, nil
}

// applySavedHost points cfg at the saved host named by -host: its URL, SSH
// key and TLS files, as connecting from the hosts view does. The password of
// a password-auth SSH host is left to the caller (-ssh-password in the TUI,
// D9C_SSH_PASSWORD in the CLI). -host replaces -H and -context, so combining
// them is a usage error.
func applySavedHost(cfg *config.Config, store *hosts.Store) (hosts.Host, error) {
	if cfg.HostFlagSet || cfg.Context != "" {
		return hosts.Host{}, agent.Errorf(agent.CodeUsage, "-host cannot be combined with -H or -context").
			WithHint("pass either a saved host name or a host URL")
	}
	h, ok := store.Find(cfg.SavedHost)
	if !ok {
		return hosts.Host{}, agent.Errorf(agent.CodeNotFound, "no saved host named %q", cfg.SavedHost).
			WithHint("run `d9c hosts ls` to list the saved hosts")
	}
	cfg.Host = h.Host
	if h.SSHKeyPath != "" {
		cfg.SSHKeyFile = h.SSHKeyPath
	}
	if !isPasswordHost(h) {
		cfg.SSHPassword = ""
	}
	if h.HasTLS() {
		cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey = h.TLSCACert, h.TLSCert, h.TLSKey
	}
	return h, nil
}

// isPasswordHost reports whether h is an SSH host set up for password auth.
func isPasswordHost(h hosts.Host) bool {
	return hosts.IsSSH(h.Host) && h.SSHAuth == hosts.SSHAuthPassword
}

// migrateLegacyFile copies a file that versions before 1.31 kept next to the
// binary into the per-user directory (~/.d9c), once. A failure is only a
// warning: d9c still starts, using the new (empty) location.
func migrateLegacyFile(dst, legacy string, w io.Writer) {
	copied, err := appdir.Migrate(dst, legacy)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(w, "warning: could not copy %s to %s: %v\n", legacy, dst, err)
	case copied:
		_, _ = fmt.Fprintf(w, "d9c: copied %s to %s (the old file is no longer used)\n", legacy, dst)
	}
}

// migrateLegacyHosts imports hosts from the old standalone d9c-hosts.json into
// the unified config, once: only when the config has no hosts yet. The legacy
// file is renamed to *.migrated so it is read at most once and the user can see
// where the data came from. Honors -hosts-file as the legacy source override.
func migrateLegacyHosts(set *settings.Store, cfg *config.Config) error {
	if set.HasHosts() {
		return nil
	}
	legacyPath := cfg.HostsFile
	if legacyPath == "" {
		legacyPath = hosts.LegacyDefaultPath()
	}
	list, err := hosts.LoadLegacy(legacyPath)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	set.SetHosts(list)
	if err := set.Save(); err != nil {
		return err
	}
	if err := os.Rename(legacyPath, legacyPath+".migrated"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: migrated hosts into %s but could not rename %s: %v\n", set.Path(), legacyPath, err)
	}
	return nil
}

// applyDockerContext resolves the Docker CLI context to start with (-context,
// else DOCKER_CONTEXT unless -H/DOCKER_HOST is set) and points cfg at its
// endpoint and TLS files; warnings about unsupported context settings go to
// warn. It returns the context as a saved-host entry to
// remember, or nil when no context applies. Demo mode ignores contexts.
func applyDockerContext(cfg *config.Config, dir string, getenv func(string) string, warn io.Writer) (*hosts.Host, error) {
	if cfg.Demo {
		return nil, nil
	}
	name, err := dockerctx.Select(cfg.Context, cfg.HostFlagSet, getenv)
	if err != nil || name == "" {
		return nil, err
	}
	c, err := dockerctx.Find(dir, name)
	if err != nil {
		return nil, err
	}
	cfg.Context = c.Name
	cfg.Host = c.Host
	if c.HasTLS() {
		cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey = c.TLSCACert, c.TLSCert, c.TLSKey
	}
	if c.SkipTLSVerify {
		_, _ = fmt.Fprintf(warn, "warning: docker context %q sets SkipTLSVerify, which d9c does not support; the server certificate will be verified\n", c.Name)
	}
	h := c.SavedHost()
	return &h, nil
}

// applySavedHostTLS reuses the TLS files of the saved host matching cfg.Host
// (e.g. an imported context started with -H) when no -tls* flags were given.
func applySavedHostTLS(cfg *config.Config, store *hosts.Store) {
	if cfg.TLSCACert != "" || cfg.TLSCert != "" || cfg.TLSKey != "" {
		return
	}
	if h, ok := store.FindByURL(cfg.Host); ok && h.HasTLS() {
		cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey = h.TLSCACert, h.TLSCert, h.TLSKey
	}
}

// rememberContextHost saves the host of the startup Docker context under the
// context's name (unless a host with that URL is already saved).
func rememberContextHost(store *hosts.Store, h hosts.Host) {
	if res, _ := store.Import(h); res != hosts.Imported {
		return
	}
	if err := store.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save host: %v\n", err)
	}
}

// hostConfigured reports whether the user explicitly provided a Docker host
// (via -H or DOCKER_HOST) rather than falling back to the default socket.
// explicit is true when -H was given on the command line: then even the
// default socket counts (e.g. the container image run with a mounted
// /var/run/docker.sock and -H unix:///var/run/docker.sock).
func hostConfigured(host string, explicit bool) bool {
	if host == "" {
		return false
	}
	return explicit || host != config.DefaultHost
}

// rememberHost saves an explicitly provided host to the store for next time.
func rememberHost(store *hosts.Store, host string) {
	if host == "" || host == config.DefaultHost {
		return
	}
	if store.UpsertByHost(host) {
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save host: %v\n", err)
		}
	}
}
