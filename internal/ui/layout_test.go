package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
)

// TestViewFitsWindowHeight guards against the body rendering one line taller
// than the window: the terminal then scrolls and the header row disappears.
// It happened on the first Containers layout because the table height was set
// while the columns still had their width-0 placeholders.
func TestViewFitsWindowHeight(t *testing.T) {
	const w, h = 120, 38

	tests := []struct {
		name  string
		setup func(m Model) Model
	}{
		{"containers on start", func(m Model) Model { return m }},
		{"containers stats layout", func(m Model) Model {
			m.statsView = true
			m.applyColumns(m.width)
			return m
		}},
		{"images", func(m Model) Model { m.resource = ViewImages; m.relayout(); return m }},
		{"networks", func(m Model) Model { m.resource = ViewNetworks; m.relayout(); return m }},
		{"volumes", func(m Model) Model { m.resource = ViewVolumes; m.relayout(); return m }},
		{"compose", func(m Model) Model { m.resource = ViewCompose; m.relayout(); return m }},
		{"hosts", func(m Model) Model { m.resource = ViewHosts; m.relayout(); return m }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(&config.Config{Demo: true}, docker.NewFakeBackend(), &hosts.Store{}, nil, false)
			nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
			m = tt.setup(nm.(Model))

			if got := strings.Count(m.View(), "\n") + 1; got != h {
				t.Errorf("View() has %d lines, want %d (window height)", got, h)
			}
		})
	}
}
