package ui

import (
	"d9c/internal/alerts"
	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/keymap"
	"d9c/internal/plugins"

	tea "github.com/charmbracelet/bubbletea"
)

func Run(cfg *config.Config, backend docker.Backend, store *hosts.Store, pluginSet *plugins.Set, keys keymap.Map, alertThresholds alerts.Thresholds, connectErr error, startInHosts bool) error {
	m := NewModel(cfg, backend, store, connectErr, startInHosts)
	m.SetPlugins(pluginSet)
	m.SetKeymap(keys)
	m.SetAlerts(alertThresholds)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
