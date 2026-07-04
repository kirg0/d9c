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

	// Stream-viewer / colorization styles (P2: these lived as hardcoded hex in
	// ui/logs, ui/detail, ui/events and ui/cmdline and ignored re-theming).
	checks := []struct {
		name string
		got  lipgloss.TerminalColor
		want lipgloss.Color
	}{
		{"LogTimestamp fg", LogTimestamp.GetForeground(), p.Muted},
		{"LogBadgeError fg", LogBadgeError.GetForeground(), p.Danger},
		{"LogBadgeWarn fg", LogBadgeWarn.GetForeground(), p.Warning},
		{"LogBadgeInfo fg", LogBadgeInfo.GetForeground(), p.Success},
		{"LogBadgeDebug fg", LogBadgeDebug.GetForeground(), p.Muted},
		{"LogLineError fg", LogLineError.GetForeground(), p.Danger},
		{"LogLineInfo fg", LogLineInfo.GetForeground(), p.Fg},
		{"LogFollowBadge fg", LogFollowBadge.GetForeground(), p.Success},
		{"LogFollowBadge bg", LogFollowBadge.GetBackground(), p.Bg},
		{"MatchLine bg", MatchLine.GetBackground(), p.BgAlt},
		{"MatchCurrent bg", MatchCurrent.GetBackground(), p.Secondary},
		{"MatchCurrent fg", MatchCurrent.GetForeground(), p.Bg},
		{"ScrollInfo bg", ScrollInfo.GetBackground(), p.Bg},
		{"ScrollInfo fg", ScrollInfo.GetForeground(), p.Muted},
		{"SearchCount bg", SearchCount.GetBackground(), p.BgAlt},
		{"YAMLKey fg", YAMLKey.GetForeground(), p.Primary},
		{"YAMLString fg", YAMLString.GetForeground(), p.Success},
		{"YAMLBool fg", YAMLBool.GetForeground(), p.Secondary},
		{"YAMLNull fg", YAMLNull.GetForeground(), p.Muted},
		{"YAMLNumber fg", YAMLNumber.GetForeground(), p.Warning},
		{"EventType fg", EventType.GetForeground(), p.Secondary},
		{"EventAction fg", EventAction.GetForeground(), p.Success},
		{"EventScope fg", EventScope.GetForeground(), p.Muted},
		{"EventInfo fg", EventInfo.GetForeground(), p.Fg},
		{"EventError fg", EventError.GetForeground(), p.Danger},
		{"CmdGhost fg", CmdGhost.GetForeground(), p.Muted},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestSelectionOverride checks that SelectBg/SelectFg, when set, drive the
// cursor-row highlight (a bright inverse bar) instead of the default
// BgAlt/Primary scheme, and that empty overrides fall back to BgAlt/Primary.
func TestSelectionOverride(t *testing.T) {
	t.Cleanup(func() { Apply(DefaultPalette()) })

	// Override set: selection uses SelectBg/SelectFg, not BgAlt/Primary.
	Apply(Palette{
		Primary: "#00E5FF", BgAlt: "#1A1A1A", Bg: "#000000",
		SelectBg: "#00E5FF", SelectFg: "#000000",
	})
	if SelectedBg != lipgloss.Color("#00E5FF") {
		t.Errorf("SelectedBg = %v, want SelectBg #00E5FF", SelectedBg)
	}
	if got := TableSelected.GetBackground(); got != lipgloss.Color("#00E5FF") {
		t.Errorf("TableSelected bg = %v, want SelectBg #00E5FF", got)
	}
	if got := TableSelected.GetForeground(); got != lipgloss.Color("#000000") {
		t.Errorf("TableSelected fg = %v, want SelectFg #000000", got)
	}

	// No override: selection falls back to BgAlt background / Primary foreground.
	Apply(Palette{Primary: "#00E5FF", BgAlt: "#1A1A1A", Bg: "#000000"})
	if SelectedBg != lipgloss.Color("#1A1A1A") {
		t.Errorf("SelectedBg = %v, want BgAlt #1A1A1A fallback", SelectedBg)
	}
	if got := TableSelected.GetForeground(); got != lipgloss.Color("#00E5FF") {
		t.Errorf("TableSelected fg = %v, want Primary #00E5FF fallback", got)
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
