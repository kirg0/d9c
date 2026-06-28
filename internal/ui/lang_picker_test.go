package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/i18n"
	"d9c/internal/settings"
	"d9c/internal/ui/cmdline"

	tea "github.com/charmbracelet/bubbletea"
)

// :lang with no args opens the picker; an explicit code switches and notifies;
// an unknown code errors without changing the active language.
func TestLangCommand(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.RU) })

	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m := tm.(Model)

	// no args → opens the picker.
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "lang"})
	if err != nil {
		t.Fatalf("lang without args should not error: %v", err)
	}
	if cmd == nil {
		t.Fatal("lang without args should return a command opening the picker")
	}
	if _, ok := cmd().(openLangPickerMsg); !ok {
		t.Error("lang without args should open the language picker")
	}

	// unknown code → error, language unchanged.
	i18n.Set(i18n.RU)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "lang", Args: []string{"fr"}}); err == nil {
		t.Error("unknown language should error")
	}
	if i18n.Current() != i18n.RU {
		t.Error("failed language switch must not change the active language")
	}

	// known code → applied, footer notified.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "lang", Args: []string{"en"}}); err != nil {
		t.Fatalf("dispatch lang en: %v", err)
	}
	if i18n.Current() != i18n.EN {
		t.Error("active language should be EN after switch")
	}
}

// The language picker previews the highlighted language live as the cursor
// moves, keeps it on Enter, and rolls back on cancel.
func TestLangPicker(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.RU) })

	i18n.Set(i18n.RU)
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Open the picker (as the no-arg :lang command does).
	tm, _ = tm.Update(openLangPickerMsg{})
	m := tm.(Model)
	if m.mode != ModeLangPicker {
		t.Fatalf("mode = %v, want ModeLangPicker", m.mode)
	}
	if len(m.langNames) == 0 {
		t.Fatal("picker should be populated with language names")
	}

	// Moving the cursor to English previews it live.
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = tm.(Model)
	if i18n.Current() != m.langNames[m.langCursor] {
		t.Error("moving the cursor should apply the highlighted language as a preview")
	}

	// Esc cancels: the original language is restored and the modal closes.
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(Model)
	if m.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after cancel", m.mode)
	}
	if i18n.Current() != i18n.RU {
		t.Error("cancel should restore the original language")
	}

	// Reopen, move to English, confirm with Enter: the preview is kept and the
	// help screen now renders in English.
	tm, _ = tm.Update(openLangPickerMsg{})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = tm.(Model)
	chosen := m.langNames[m.langCursor]
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if m.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after apply", m.mode)
	}
	if i18n.Current() != chosen {
		t.Error("Enter should keep the previewed language")
	}
	if chosen == i18n.EN && !strings.Contains(m.buildHelpContent(), "Navigation") {
		t.Error("help should render in English after switching to EN")
	}
}

// TestLangPickerPersists verifies that confirming a language writes it to the
// unified config store, so the choice survives a restart.
func TestLangPickerPersists(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.RU) })

	path := filepath.Join(t.TempDir(), "d9c-config.yaml")
	set, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	fb := docker.NewFakeBackend()
	m := NewModel(&config.Config{}, fb, nil, nil, false)
	m.SetSettings(set)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, _ = tm.Update(openLangPickerMsg{})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown}) // move to English
	mm := tm.(Model)
	chosen := mm.langNames[mm.langCursor]
	_, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	reloaded, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.File.Lang != string(chosen) {
		t.Errorf("persisted lang = %q, want %q", reloaded.File.Lang, string(chosen))
	}
}

// lang is recognised as a builtin command in every view (it is global).
func TestLangIsBuiltinEverywhere(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, res := range []ResourceView{ViewContainers, ViewImages, ViewHosts, ViewCompose} {
		tm, _ = tm.Update(switchResourceMsg{res})
		if !tm.(Model).cmdline.IsBuiltin("lang") {
			t.Errorf("lang should be builtin in %v view", res)
		}
	}
}
