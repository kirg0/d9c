package ui

import (
	"strings"
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/plugins"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBuildHelpContentContainers(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	got := m.buildHelpContent()
	for _, want := range []string{
		"Навигация", "Containers", "Shell в контейнере",
		":logs", "Разделы", "Плагины не настроены",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help content missing %q", want)
		}
	}
}

func TestBuildHelpContentWithPlugins(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.SetPlugins(plugins.New([]plugins.Plugin{
		{Name: "dive", Key: "ctrl+d", Scope: "containers", Description: "слои", Command: "dive"},
	}))
	got := m.buildHelpContent()
	if !strings.Contains(got, "dive") || !strings.Contains(got, "ctrl+d") {
		t.Errorf("help should list the plugin and its key:\n%s", got)
	}
	if strings.Contains(got, "Плагины не настроены") {
		t.Error("should not show the no-plugins note when plugins exist")
	}
}

func TestBuildHelpContentCompose(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.resource = ViewCompose
	got := m.buildHelpContent()
	if !strings.Contains(got, "Открыть контейнеры проекта") {
		t.Error("compose help missing the drill-down key")
	}
	if !strings.Contains(got, ":backups") {
		t.Error("compose help should list compose commands like :backups")
	}
}

// TestBuildHelpContentAlertGlyphSpacing guards the :alert hint: the ⚠ glyph is
// ambiguous-width (terminals draw it 2 cells wide) so it must keep a space on
// each side, otherwise it overdraws the adjacent bracket. See the header ↻ fix.
func TestBuildHelpContentAlertGlyphSpacing(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	got := m.buildHelpContent()
	if !strings.Contains(got, "⚠") {
		t.Fatal("help content should mention the ⚠ marker")
	}
	for _, tight := range []string{"(⚠", "⚠)"} {
		if strings.Contains(got, tight) {
			t.Errorf("⚠ glyph must not touch a bracket (found %q) — it overdraws the bracket", tight)
		}
	}
}

// TestHelpOpenClose drives '?' to open and q/esc to close the help overlay.
func TestHelpOpenClose(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 100, Height: 30})
	cs, _ := fb.ListContainers(false)
	step(containersUpdatedMsg{cs})

	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if tm.(Model).mode != ModeHelp {
		t.Fatalf("mode = %v, want ModeHelp", tm.(Model).mode)
	}
	if !strings.Contains(tm.(Model).View(), "Навигация") {
		t.Error("help view should render the reference")
	}

	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}) // q closes, doesn't quit
	if tm.(Model).mode != ModeNormal {
		t.Errorf("after q mode = %v, want ModeNormal", tm.(Model).mode)
	}

	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if tm.(Model).mode != ModeNormal {
		t.Errorf("after esc mode = %v, want ModeNormal", tm.(Model).mode)
	}
}
