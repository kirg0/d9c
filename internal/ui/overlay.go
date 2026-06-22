package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// reset clears any active SGR styling, inserted at slice boundaries so a
// background style truncated mid-cell doesn't bleed into the overlaid panel.
const reset = "\x1b[0m"

// overlayCenter draws panel centered on top of bg within a width×height area,
// keeping the surrounding bg content (e.g. the resource table) visible. Both
// layers may carry ANSI styling, which is preserved. bg is normalized to exactly
// height lines first.
func overlayCenter(bg, panel string, width, height int) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < height {
		bgLines = append(bgLines, "")
	}
	bgLines = bgLines[:height]

	panelLines := strings.Split(panel, "\n")
	panelW := 0
	for _, l := range panelLines {
		if w := ansi.StringWidth(l); w > panelW {
			panelW = w
		}
	}

	top := max((height-len(panelLines))/2, 0)
	left := max((width-panelW)/2, 0)

	for i, pl := range panelLines {
		row := top + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		bgLines[row] = overlayLine(bgLines[row], pl, left)
	}
	return strings.Join(bgLines, "\n")
}

// overlayLine draws fg onto bg starting at column `left`, keeping the bg cells to
// the left and right of the fg span. Slice boundaries are reset so neither layer's
// styling leaks into the other.
func overlayLine(bg, fg string, left int) string {
	fgW := ansi.StringWidth(fg)

	leftPart := ansi.Truncate(bg, left, "")
	if pad := left - ansi.StringWidth(leftPart); pad > 0 {
		leftPart += strings.Repeat(" ", pad)
	}
	rightPart := ansi.TruncateLeft(bg, left+fgW, "")

	return leftPart + reset + fg + reset + rightPart
}
