package styles

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestApplyRebuildsStyles verifies that Apply re-derives the exported styles
// from the given palette and records it as active. It restores the default
// palette afterwards so it does not bleed into other tests.
func TestApplyRebuildsStyles(t *testing.T) {
	t.Cleanup(func() { Apply(DefaultPalette()) })

	p := Palette{
		Primary:   "#111111",
		Secondary: "#222222",
		Success:   "#333333",
		Warning:   "#444444",
		Danger:    "#555555",
		Muted:     "#666666",
		Bg:        "#777777",
		BgAlt:     "#888888",
		Fg:        "#999999",
		Border:    "#aaaaaa",
	}
	Apply(p)

	if Active() != p {
		t.Errorf("Active() = %+v, want %+v", Active(), p)
	}
	if got := TableCell.GetForeground(); got != lipgloss.Color("#999999") {
		t.Errorf("TableCell fg = %v, want Fg #999999", got)
	}
	if got := StatusRunning.GetForeground(); got != lipgloss.Color("#333333") {
		t.Errorf("StatusRunning fg = %v, want Success #333333", got)
	}
	if got := ErrorStyle.GetForeground(); got != lipgloss.Color("#555555") {
		t.Errorf("ErrorStyle fg = %v, want Danger #555555", got)
	}
	if SelectedBg != lipgloss.Color("#888888") {
		t.Errorf("SelectedBg = %v, want BgAlt #888888", SelectedBg)
	}
}

func TestStateColorTracksPalette(t *testing.T) {
	t.Cleanup(func() { Apply(DefaultPalette()) })

	Apply(Palette{Success: "#00ff00", Danger: "#ff0000", Warning: "#ffff00", Muted: "#888888"})

	if got := StateColor("running").GetForeground(); got != lipgloss.Color("#00ff00") {
		t.Errorf("StateColor(running) fg = %v, want #00ff00", got)
	}
	if got := StateColor("exited").GetForeground(); got != lipgloss.Color("#ff0000") {
		t.Errorf("StateColor(exited) fg = %v, want #ff0000", got)
	}
	if got := StateColor("paused").GetForeground(); got != lipgloss.Color("#ffff00") {
		t.Errorf("StateColor(paused) fg = %v, want #ffff00", got)
	}
}
