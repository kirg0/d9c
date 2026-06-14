package main

import (
	"fmt"
	"os"

	"d9c/internal/alerts"
	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/keymap"
	"d9c/internal/plugins"
	"d9c/internal/theme"
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

	hostsPath := cfg.HostsFile
	if hostsPath == "" {
		hostsPath = hosts.DefaultPath()
	}
	store, err := hosts.Load(hostsPath)
	if err != nil {
		return fmt.Errorf("loading saved hosts: %w", err)
	}

	pluginsPath := cfg.PluginsFile
	if pluginsPath == "" {
		pluginsPath = plugins.DefaultPath()
	}
	pluginSet, err := plugins.Load(pluginsPath)
	if err != nil {
		return fmt.Errorf("loading plugins: %w", err)
	}

	configPath := cfg.ConfigFile
	if configPath == "" {
		configPath = theme.DefaultPath()
	}
	palette, err := theme.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	styles.Apply(palette)

	keys, err := keymap.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading keybindings: %w", err)
	}

	alertThresholds, err := alerts.Load(configPath)
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

	return ui.Run(cfg, backend, store, pluginSet, keys, alertThresholds, connectErr, startInHosts)
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
