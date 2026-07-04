package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestThemePickerFlowAndOverlay(t *testing.T) {
	h := newSizedModel(t)
	m := h.cur()
	m.openThemePicker()
	h.set(m)
	if got := h.cur().mode; got != ModeThemePicker {
		t.Fatalf("mode = %v, want theme picker", got)
	}
	if v := h.cur().View(); !strings.Contains(v, "▶") {
		t.Error("theme overlay should render the cursor")
	}

	// Navigate: down previews the next theme, up goes back.
	h.step(tea.KeyMsg{Type: tea.KeyDown})
	h.step(tea.KeyMsg{Type: tea.KeyUp})
	// q cancels and restores the original palette.
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if got := h.cur().mode; got != ModeNormal {
		t.Errorf("mode after q = %v, want normal", got)
	}

	// Enter applies the highlighted theme.
	m = h.cur()
	m.openThemePicker()
	h.set(m)
	h.step(tea.KeyMsg{Type: tea.KeyDown})
	h.step(tea.KeyMsg{Type: tea.KeyEnter})
	got := h.cur()
	if got.mode != ModeNormal || got.copyNotif == "" {
		t.Errorf("after enter: mode=%v notif=%q", got.mode, got.copyNotif)
	}
}

func TestLangOverlayRenders(t *testing.T) {
	h := newSizedModel(t)
	m := h.cur()
	m.openLangPicker()
	h.set(m)
	if got := h.cur().mode; got != ModeLangPicker {
		t.Fatalf("mode = %v, want lang picker", got)
	}
	if v := h.cur().View(); !strings.Contains(v, "▶") {
		t.Error("lang overlay should render the cursor")
	}
	// Cancel restores the original language.
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if got := h.cur().mode; got != ModeNormal {
		t.Errorf("mode after q = %v, want normal", got)
	}
}

func TestNamespacePickerFlowAndOverlay(t *testing.T) {
	h := newSizedModel(t)
	m := h.cur()
	m.openNamespacePicker([]string{"default", "k8s.io"}, "k8s.io")
	h.set(m)
	if got := h.cur(); got.mode != ModeNamespacePicker || got.nsCursor != 1 {
		t.Fatalf("mode=%v cursor=%d, want picker with cursor on current ns", got.mode, got.nsCursor)
	}
	if v := h.cur().View(); !strings.Contains(v, "k8s.io") {
		t.Error("namespace overlay should list the namespaces")
	}

	// Navigation clamps.
	h.step(tea.KeyMsg{Type: tea.KeyDown})
	if got := h.cur().nsCursor; got != 1 {
		t.Errorf("cursor past end = %d", got)
	}
	h.step(tea.KeyMsg{Type: tea.KeyUp})
	if got := h.cur().nsCursor; got != 0 {
		t.Errorf("cursor after up = %d", got)
	}
	// q cancels.
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if got := h.cur().mode; got != ModeNormal {
		t.Errorf("mode after q = %v", got)
	}

	// Enter on a non-namespaced backend (FakeBackend) still closes the picker.
	m = h.cur()
	m.openNamespacePicker([]string{"default"}, "default")
	h.set(m)
	h.step(tea.KeyMsg{Type: tea.KeyEnter})
	if got := h.cur().mode; got != ModeNormal {
		t.Errorf("mode after enter = %v", got)
	}

	// Empty namespace list renders the placeholder.
	m = h.cur()
	m.openNamespacePicker(nil, "")
	h.set(m)
	if v := h.cur().View(); v == "" {
		t.Error("empty namespace overlay should still render")
	}
}
