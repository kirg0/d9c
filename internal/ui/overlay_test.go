package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestOverlayCenter_KeepsBackground checks the panel is composited over the
// middle of the background while the surrounding rows and columns stay intact.
func TestOverlayCenter_KeepsBackground(t *testing.T) {
	bg := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbbbbbbbb",
		"cccccccccc",
		"dddddddddd",
		"eeeeeeeeee",
	}, "\n")
	out := overlayCenter(bg, "XX\nXX", 10, 5)
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5", len(lines))
	}
	// Top and bottom rows are untouched.
	if got := ansi.Strip(lines[0]); got != "aaaaaaaaaa" {
		t.Errorf("top row = %q, want unchanged", got)
	}
	if got := ansi.Strip(lines[4]); got != "eeeeeeeeee" {
		t.Errorf("bottom row = %q, want unchanged", got)
	}
	// Panel (2×2) centers at left=4, top=1: the bg cells around it survive.
	if got := ansi.Strip(lines[1]); got != "bbbbXXbbbb" {
		t.Errorf("overlaid row = %q, want bbbbXXbbbb", got)
	}
	if got := ansi.Strip(lines[2]); got != "ccccXXcccc" {
		t.Errorf("overlaid row = %q, want ccccXXcccc", got)
	}
}

// TestOverlayCenter_PadsShortBackground checks a background with fewer lines than
// the area is padded so the panel still lands at the centered row.
func TestOverlayCenter_PadsShortBackground(t *testing.T) {
	out := overlayCenter("short", "P", 10, 5)
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5 (padded)", len(lines))
	}
	if got := ansi.Strip(lines[0]); got != "short" {
		t.Errorf("row 0 = %q, want short", got)
	}
}
