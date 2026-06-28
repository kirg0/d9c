package main

import (
	"fmt"
	"os"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/i18n"
	"d9c/internal/plugins"
	"d9c/internal/settings"
	"d9c/internal/ui"
	"d9c/internal/ui/styles"
	"d9c/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()

	if cfg.ShowVersion {
		fmt.Println("d9c " + version.String())
		return nil
	}

	configPath := cfg.ConfigFile
	if configPath == "" {
		configPath = settings.DefaultPath()
	}
	set, err := settings.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := migrateLegacyHosts(set, cfg); err != nil {
		return fmt.Errorf("migrating saved hosts: %w", err)
	}
	store := set.Hosts()

	pluginsPath := cfg.PluginsFile
	if pluginsPath == "" {
		pluginsPath = plugins.DefaultPath()
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

	var backend docker.Backend
	var connectErr error
	var startInHosts bool
	switch {
	case cfg.Demo:
		backend = docker.NewFakeBackend()

	case !hostConfigured(cfg.Host):
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
		rememberHost(store, cfg.Host)
	}
	defer backend.Close()

	return ui.Run(cfg, backend, store, set, pluginSet, keys, alertThresholds, connectErr, startInHosts)
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

// hostConfigured reports whether the user explicitly provided a Docker host
// (via -H or DOCKER_HOST) rather than falling back to the default socket.
func hostConfigured(host string) bool {
	return host != "" && host != config.DefaultHost
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
