package ui

import (
	"github.com/kirg0/d9c/internal/alerts"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/keymap"
	"github.com/kirg0/d9c/internal/plugins"
	"github.com/kirg0/d9c/internal/settings"

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
