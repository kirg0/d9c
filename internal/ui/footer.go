package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/kirg0/d9c/internal/ui/styles"
)

// footerEllipsis marks a footer cut short: more hints than maxFooterLines hold.
const footerEllipsis = " …"

// maxFooterLines caps how many lines the wrapped footer may take, leaving the
// body (table, logs…) most of a short window.
func (m Model) maxFooterLines() int {
	switch {
	case m.height >= 20:
		return 3
	case m.height >= 12:
		return 2
	default:
		return 1
	}
}

// bodyHeight is the number of lines left between the header and the footer.
func (m Model) bodyHeight() int {
	return max(m.height-1-max(m.footerH, 1), 1)
}

// cmdHints renders the `:` command names for the command-mode footer as
// separate segments ("run ·", "start ·" … "rm"), so a long list wraps between
// commands instead of being cut off at the window edge.
func cmdHints(names ...string) []string {
	segs := make([]string, 0, len(names))
	for i, n := range names {
		s := " " + n + " ·"
		if i == 0 {
			s = " " + s
		}
		if i == len(names)-1 {
			s = strings.TrimSuffix(s, " ·") + "  "
		}
		segs = append(segs, styles.FooterDesc.Render(s))
	}
	return segs
}

// wrapSegments packs segs greedily into lines at most w cells wide, breaking
// only between segments. A segment wider than w alone is truncated with "…".
// When the segments need more than maxLines lines, the last line ends with
// footerEllipsis. A non-positive w (size not known yet) yields one line.
func wrapSegments(segs []string, w, maxLines int) []string {
	if w <= 0 {
		return []string{strings.Join(segs, "")}
	}
	maxLines = max(maxLines, 1)
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, s := range segs {
		sw := lipgloss.Width(s)
		if sw > w {
			s = ansi.Truncate(s, w, "…")
			sw = lipgloss.Width(s)
		}
		if curW > 0 && curW+sw > w {
			lines = append(lines, cur.String())
			if len(lines) == maxLines {
				lines[maxLines-1] = withEllipsis(lines[maxLines-1], w)
				return lines
			}
			cur.Reset()
			curW = 0
		}
		cur.WriteString(s)
		curW += sw
	}
	return append(lines, cur.String())
}

// withEllipsis appends footerEllipsis to line, trimming the line first when
// the marker would not fit within w cells.
func withEllipsis(line string, w int) string {
	ew := lipgloss.Width(footerEllipsis)
	if lipgloss.Width(line)+ew > w {
		line = ansi.Truncate(line, max(w-ew, 0), "")
	}
	return line + styles.FooterDesc.Render(footerEllipsis)
}
