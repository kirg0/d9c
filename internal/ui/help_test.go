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

// Over a tcp:// connection the SSH-only compose commands (and the edit key row)
// must be absent from the help screen, while the API-driven ops remain.
func TestBuildHelpContentComposeOverTCP(t *testing.T) {
	m := NewModel(&config.Config{}, &docker.FakeBackend{NoHostCompose: true}, nil, nil, false)
	m.resource = ViewCompose
	got := m.buildHelpContent()

	if !strings.Contains(got, ":backups") {
		t.Error("tcp compose help should still list the local :backups catalog")
	}
	if !strings.Contains(got, ":start") {
		t.Error("tcp compose help should still list API lifecycle ops like :start")
	}
	for _, hidden := range []string{":up", ":down", ":pull", ":config", ":edit", ":backup ", ":restore", ":create"} {
		if strings.Contains(got, hidden) {
			t.Errorf("tcp compose help must NOT list SSH-only command %q:\n%s", hidden, got)
		}
	}
	if strings.Contains(got, "Редактировать compose-файл") {
		t.Error("tcp compose help must not show the edit key row")
	}
}

// TestBuildHelpContentNoGluedWideGlyph guards against the (⚠) overlap class:
// emoji-presentation glyphs (⚠ ⏸ ✖ ✔ ▶) are drawn 2 cells wide by many
// terminals while runewidth counts them as 1, so a glyph glued to the next
// visible rune overdraws it. Keep such glyphs clear of adjacent text in help.
func TestBuildHelpContentNoGluedWideGlyph(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	wide := map[rune]bool{'⚠': true, '⏸': true, '✖': true, '✔': true, '▶': true}
	rs := []rune(m.buildHelpContent())
	for i, r := range rs {
		if !wide[r] || i+1 >= len(rs) {
			continue
		}
		if next := rs[i+1]; next != ' ' && next != '\n' {
			t.Errorf("wide glyph %q is glued to %q in help — it overdraws the next char", string(r), string(next))
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
