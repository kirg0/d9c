package logs

import (
	"fmt"
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// LinesMsg carries a batch of log lines from the streaming goroutine. Lines
// arrive batched (see ui.streamLogs): one message per channel drain rather than
// one per line, so a chatty stream costs one re-render per batch.
type LinesMsg struct {
	ContainerID string
	Lines       []string
}

// maxBufferLines caps the log buffer: past it the oldest lines are dropped.
// Without a cap a follow stream on a chatty container grows the buffer — and
// the per-batch re-render, which joins the whole buffer — without bound.
const maxBufferLines = 10000

// ── level colorization ────────────────────────────────────────────────────────

// levelDef points at the styles package vars rather than copying them: Apply
// reassigns those vars on a re-theme, so dereferencing at render time always
// picks up the active palette.
type levelDef struct {
	words []string
	badge *lipgloss.Style
	line  *lipgloss.Style
}

// Ordered from most to least severe; first match wins.
var levels = []levelDef{
	{[]string{"ERROR", "FATAL", "PANIC", "CRITICAL", "CRIT", "ERR"}, &styles.LogBadgeError, &styles.LogLineError},
	{[]string{"WARN", "WARNING"}, &styles.LogBadgeWarn, &styles.LogLineWarn},
	{[]string{"INFO", "INFORMATION", "NOTICE"}, &styles.LogBadgeInfo, &styles.LogLineInfo},
	{[]string{"DEBUG", "TRACE", "VERBOSE"}, &styles.LogBadgeDebug, &styles.LogLineDebug},
}

// ── model ─────────────────────────────────────────────────────────────────────

type Model struct {
	viewport    viewport.Model
	lines       []string // colorized, for display
	rawLines    []string // uncolorized, for export to file and search
	containerID string
	autoScroll  bool
	width       int
	height      int

	searching   bool
	searchInput textinput.Model
	searchQuery string
	matches     []int // indices into rawLines
	matchIdx    int
}

func New() Model {
	ti := textinput.New()
	ti.Placeholder = "search…"
	ti.CharLimit = 128
	return Model{viewport: viewport.New(0, 0), autoScroll: true, searchInput: ti}
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width
	m.viewport.Height = m.vpHeight()
}

// vpHeight reserves a row for the scrollbar and, while searching, the search bar.
func (m Model) vpHeight() int {
	h := m.height - 1 // scrollbar
	if m.searching {
		h-- // search bar
	}
	if h < 1 {
		return 1
	}
	return h
}

func (m *Model) Open(containerID string) {
	m.containerID = containerID
	m.lines = nil
	m.rawLines = nil
	m.autoScroll = true
	m.resetSearch()
	m.viewport.SetContent("")
	m.viewport.GotoTop()
}

func (m *Model) resetSearch() {
	m.searching = false
	m.searchQuery = ""
	m.searchInput.Reset()
	m.matches = nil
	m.matchIdx = 0
}

// AddLine appends a single log line (convenience wrapper over AddLines).
func (m *Model) AddLine(line string) { m.AddLines([]string{line}) }

// AddLines appends a batch of log lines, trimming the buffer to maxBufferLines,
// keeping search matches current and the view scrolled. Rebuilding the viewport
// content costs O(buffer), so it runs once per batch — never per line.
func (m *Model) AddLines(batch []string) {
	if len(batch) == 0 {
		return
	}
	oldLen := len(m.rawLines)
	for _, line := range batch {
		m.lines = append(m.lines, colorizeLogLine(line))
		m.rawLines = append(m.rawLines, line)
	}
	trimmed := m.trimToCap()
	if m.searchQuery != "" {
		// Incremental: rebase the existing matches past the trim, then scan only
		// the appended lines — a full computeMatches rescan per batch would put
		// the quadratic cost right back.
		m.shiftMatches(trimmed)
		m.appendMatches(max(oldLen-trimmed, 0))
	}
	m.viewport.SetContent(m.renderContent())
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

// trimToCap drops the oldest lines once the buffer exceeds maxBufferLines and
// reports how many were dropped. The tails are copied down in place, so the
// backing arrays are reused and the dropped strings become collectable.
func (m *Model) trimToCap() int {
	over := len(m.rawLines) - maxBufferLines
	if over <= 0 {
		return 0
	}
	m.lines = m.lines[:copy(m.lines, m.lines[over:])]
	m.rawLines = m.rawLines[:copy(m.rawLines, m.rawLines[over:])]
	return over
}

// shiftMatches rebases match indices after n lines were dropped off the front,
// discarding matches that slid out of the buffer and keeping the current-match
// cursor on the same line where possible.
func (m *Model) shiftMatches(n int) {
	if n <= 0 || len(m.matches) == 0 {
		return
	}
	dropped := 0
	kept := m.matches[:0]
	for _, idx := range m.matches {
		if idx < n {
			dropped++
			continue
		}
		kept = append(kept, idx-n)
	}
	m.matches = kept
	m.matchIdx = max(m.matchIdx-dropped, 0)
	if m.matchIdx >= len(m.matches) {
		m.matchIdx = 0
	}
}

// appendMatches scans lines from index `from` on for the active query — the
// incremental complement of computeMatches for freshly appended lines.
func (m *Model) appendMatches(from int) {
	q := strings.ToLower(m.searchQuery)
	for i := from; i < len(m.rawLines); i++ {
		if strings.Contains(strings.ToLower(m.rawLines[i]), q) {
			m.matches = append(m.matches, i)
		}
	}
}

func (m Model) ContainerID() string { return m.containerID }

// RawContent returns the uncolorized log buffer for export to a file.
func (m Model) RawContent() string { return strings.Join(m.rawLines, "\n") }

// LineCount reports how many log lines are currently buffered.
func (m Model) LineCount() int { return len(m.rawLines) }

// IsSearching reports whether the search input is focused (capturing keys).
func (m Model) IsSearching() bool { return m.searching }

// HasSearch reports whether a search query is active (highlights shown).
func (m Model) HasSearch() bool { return m.searchQuery != "" }

// IsFollowing reports whether new log lines auto-scroll the view (follow mode).
func (m Model) IsFollowing() bool { return m.autoScroll }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		return m.handleKey(key)
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()

	if m.searching {
		switch key {
		case "enter":
			m.searching = false
			m.viewport.Height = m.vpHeight()
			return m, nil
		case "esc":
			m.resetSearch()
			m.viewport.Height = m.vpHeight()
			m.viewport.SetContent(m.renderContent())
			return m, nil
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.searchQuery = m.searchInput.Value()
			m.recomputeMatches()
			return m, cmd
		}
	}

	switch key {
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.viewport.Height = m.vpHeight()
		return m, nil
	case "f":
		// Toggle follow (auto-scroll): when re-enabled, jump to the latest line.
		m.autoScroll = !m.autoScroll
		if m.autoScroll {
			m.viewport.GotoBottom()
		}
		return m, nil
	case "n":
		m.autoScroll = false
		m.stepMatch(+1)
		return m, nil
	case "N":
		m.autoScroll = false
		m.stepMatch(-1)
		return m, nil
	case "esc":
		if m.searchQuery != "" {
			m.resetSearch()
			m.viewport.SetContent(m.renderContent())
			return m, nil
		}
	}

	m.autoScroll = false
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// computeMatches rescans the buffer for the current query without moving the view.
func (m *Model) computeMatches() {
	m.matches = nil
	if m.searchQuery == "" {
		return
	}
	q := strings.ToLower(m.searchQuery)
	for i, line := range m.rawLines {
		if strings.Contains(strings.ToLower(line), q) {
			m.matches = append(m.matches, i)
		}
	}
	if m.matchIdx >= len(m.matches) {
		m.matchIdx = 0
	}
}

// recomputeMatches rescans and jumps to the first match (used while typing).
func (m *Model) recomputeMatches() {
	m.matchIdx = 0
	m.computeMatches()
	m.viewport.SetContent(m.renderContent())
	if len(m.matches) > 0 {
		m.autoScroll = false
		m.viewport.SetYOffset(m.matches[0])
	}
}

func (m *Model) stepMatch(dir int) {
	if len(m.matches) == 0 {
		return
	}
	m.matchIdx = (m.matchIdx + dir + len(m.matches)) % len(m.matches)
	m.viewport.SetContent(m.renderContent())
	m.viewport.SetYOffset(m.matches[m.matchIdx])
}

func (m Model) View() string {
	pctStr := fmt.Sprintf(" %3.0f%% ", m.viewport.ScrollPercent()*100)
	if m.viewport.ScrollPercent() >= 1.0 {
		pctStr = " END "
	}
	followTag := ""
	if m.autoScroll {
		followTag = " FOLLOW "
	}
	dashLen := m.viewport.Width - lipgloss.Width(pctStr) - lipgloss.Width(followTag)
	if dashLen < 0 {
		dashLen = 0
	}
	scrollBar := styles.LogFollowBadge.Render(followTag) +
		styles.StatusBar.Render(strings.Repeat("─", dashLen)) +
		styles.ScrollInfo.Render(pctStr)

	parts := []string{m.viewport.View()}

	if m.searching {
		matchInfo := ""
		if m.searchQuery != "" {
			if len(m.matches) > 0 {
				matchInfo = fmt.Sprintf("  %d/%d  ", m.matchIdx+1, len(m.matches))
			} else {
				matchInfo = "  no match  "
			}
		}
		infoRendered := styles.SearchCount.Render(matchInfo)
		prefix := styles.BottomBarPrefix.Render("/")
		inputW := m.viewport.Width - lipgloss.Width(prefix) - lipgloss.Width(infoRendered)
		if inputW < 0 {
			inputW = 0
		}
		inputView := styles.BottomBar.Width(inputW).Render(m.searchInput.View())
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Left, prefix, inputView, infoRendered))
	}

	parts = append(parts, scrollBar)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderContent joins the buffered lines, applying search highlights when a
// query is active: matched lines get a subtle background, the current match a
// bright one.
func (m Model) renderContent() string {
	if len(m.matches) == 0 {
		return strings.Join(m.lines, "\n")
	}
	matchSet := make(map[int]bool, len(m.matches))
	for _, idx := range m.matches {
		matchSet[idx] = true
	}
	current := m.matches[m.matchIdx]

	out := make([]string, len(m.lines))
	for i := range m.lines {
		switch {
		case i == current:
			out[i] = styles.MatchCurrent.Width(m.width).Render(m.rawLines[i])
		case matchSet[i]:
			out[i] = styles.MatchLine.Width(m.width).Render(m.rawLines[i])
		default:
			out[i] = m.lines[i]
		}
	}
	return strings.Join(out, "\n")
}

// ── colorization ──────────────────────────────────────────────────────────────

func colorizeLogLine(line string) string {
	if line == "" {
		return line
	}

	// Separate Docker-prepended RFC3339 timestamp.
	// Format: "2024-01-15T10:23:45.123456789Z <message>"
	ts, content := splitTimestamp(line)

	lv := matchLevel(content)

	var sb strings.Builder
	if ts != "" {
		sb.WriteString(styles.LogTimestamp.Render(ts))
		sb.WriteByte(' ')
	}
	if lv != nil {
		sb.WriteString(highlightLevel(content, lv))
	} else {
		sb.WriteString(styles.LogLineInfo.Render(content))
	}
	return sb.String()
}

// splitTimestamp splits a Docker-timestamped line into (ts, rest).
func splitTimestamp(line string) (string, string) {
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return "", line
	}
	ts := line[:sp]
	if len(ts) >= 19 && ts[4] == '-' && ts[7] == '-' && ts[10] == 'T' {
		return ts, line[sp+1:]
	}
	return "", line
}

// matchLevel returns the first levelDef whose keywords appear in content.
func matchLevel(content string) *levelDef {
	up := strings.ToUpper(content)
	for i := range levels {
		for _, w := range levels[i].words {
			if wordIn(up, w) {
				return &levels[i]
			}
		}
	}
	return nil
}

// wordIn returns true when word appears in s as a standalone token
// (not as part of a longer word).
func wordIn(s, word string) bool {
	idx := strings.Index(s, word)
	if idx < 0 {
		return false
	}
	if idx > 0 {
		c := s[idx-1]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return false
		}
	}
	end := idx + len(word)
	if end < len(s) {
		c := s[end]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// highlightLevel renders content with the matched level keyword in bold
// and the rest in the level's line colour.
func highlightLevel(content string, lv *levelDef) string {
	up := strings.ToUpper(content)
	// Byte offsets found in `up` are only valid in `content` when the case
	// mapping didn't change any rune's byte width (e.g. 'ɱ'→'Ɱ' grows); with a
	// length mismatch slicing `content` at `up` offsets would corrupt the line
	// or panic, so fall back to colouring the whole line.
	if len(up) != len(content) {
		return lv.line.Render(content)
	}
	for _, w := range lv.words {
		idx := strings.Index(up, w)
		if idx < 0 || !wordIn(up, w) {
			continue
		}
		before := content[:idx]
		word := content[idx : idx+len(w)]
		after := content[idx+len(w):]
		return lv.line.Render(before) + lv.badge.Render(word) + lv.line.Render(after)
	}
	return lv.line.Render(content)
}
