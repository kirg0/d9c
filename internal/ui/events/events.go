// Package events provides a live-events viewer component for the d9c TUI.
// It displays a scrolling feed of Docker daemon events (create, start,
// stop, die, oom, health_status, …) with type-aware colorization.
package events

import (
	"fmt"
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── styles ────────────────────────────────────────────────────────────────────

var (
	typeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#BB9AF7")).Bold(true)
	actionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A")).Bold(true)
	scopeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#565F89")).Italic(true)
	infoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#C0CAF5"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
)

// ── model ─────────────────────────────────────────────────────────────────────

// Model displays a scrolling feed of Docker daemon events.
type Model struct {
	viewport viewport.Model
	lines    []string // colorized
	rawLines []string // uncolorized, for export
	width    int
	height   int
}

// New creates an events viewer with default settings.
func New() Model {
	return Model{viewport: viewport.New(0, 0)}
}

// SetSize configures the viewer dimensions.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width
	m.viewport.Height = m.vpHeight()
}

func (m Model) vpHeight() int {
	h := m.height - 1 // scrollbar
	if h < 1 {
		return 1
	}
	return h
}

// Open clears the buffer and prepares for a fresh event stream.
func (m *Model) Open() {
	m.lines = nil
	m.rawLines = nil
	m.viewport.SetContent("")
	m.viewport.GotoTop()
}

// AddLine appends a formatted event line and auto-scrolls.
func (m *Model) AddLine(line string) {
	m.lines = append(m.lines, formatEventLine(line))
	m.rawLines = append(m.rawLines, line)
	m.viewport.SetContent(m.renderContent())
	m.viewport.GotoBottom()
}

// LineCount reports the number of buffered event lines.
func (m Model) LineCount() int { return len(m.rawLines) }

// Width reports the current panel width (0 until SetSize is called).
func (m Model) Width() int { return m.width }

// RawContent returns the uncolorized buffer for export to a file.
func (m Model) RawContent() string { return strings.Join(m.rawLines, "\n") }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	pctStr := fmt.Sprintf(" %3.0f%% ", m.viewport.ScrollPercent()*100)
	if m.viewport.ScrollPercent() >= 1.0 {
		pctStr = " END "
	}
	dashLen := m.viewport.Width - lipgloss.Width(pctStr)
	dashLen = max(0, dashLen)
	scrollBar := styles.StatusBar.Render(strings.Repeat("─", dashLen)) +
		lipgloss.NewStyle().
			Background(lipgloss.Color("#1A1B26")).
			Foreground(lipgloss.Color("#565F89")).
			Render(pctStr)

	return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), scrollBar)
}

// renderContent joins buffered lines with type-aware colorization.
func (m Model) renderContent() string {
	out := make([]string, len(m.lines))
	copy(out, m.lines)
	return strings.Join(out, "\n")
}

// formatEventLine colorizes a single event line.
func formatEventLine(line string) string {
	if line == "" {
		return line
	}

	// Format: "[type] action name (scope)" or "[error] message"
	if strings.HasPrefix(line, "[error]") {
		return errorStyle.Render(line)
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 { // too few tokens to colorize structurally
		return infoStyle.Render(line)
	}

	typ := typeStyle.Render(parts[0])
	action := actionStyle.Render(parts[1])
	rest := parts[2]

	// Scope detection: the trailing "(scope)" segment, if present.
	if idx := strings.LastIndex(rest, "("); idx >= 0 {
		name := strings.TrimSpace(rest[:idx])
		scope := scopeStyle.Render(rest[idx:])
		return fmt.Sprintf("%s  %s  %s %s", typ, action, name, scope)
	}
	return fmt.Sprintf("%s  %s  %s", typ, action, rest)
}
