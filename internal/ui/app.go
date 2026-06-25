package ui

import (
	"d9c/internal/alerts"
	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/keymap"
	"d9c/internal/plugins"
	"d9c/internal/settings"

	tea "github.com/charmbracelet/bubbletea"
)

func Run(cfg *config.Config, backend docker.Backend, store *hosts.Store, set *settings.Store, pluginSet *plugins.Set, keys keymap.Map, alertThresholds alerts.Thresholds, connectErr error, startInHosts bool) error {
	m := NewModel(cfg, backend, store, connectErr, startInHosts)
	m.SetSettings(set)
	m.SetPlugins(pluginSet)
	m.SetKeymap(keys)
	m.SetAlerts(alertThresholds)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
