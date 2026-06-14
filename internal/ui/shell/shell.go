// Package shell renders an interactive container exec session as a panel inside
// the TUI. It feeds the remote PTY's byte stream through a virtual-terminal
// emulator (vt10x) and draws the emulator's screen grid each frame, forwarding
// keystrokes back to the session. Unlike a terminal hand-off, the app's header
// and footer stay on screen for the lifetime of the shell.
package shell

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"d9c/internal/docker"
	"d9c/internal/ui/styles"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hinshun/vt10x"
)

// vt10x text-attribute bits (Glyph.Mode). They are unexported by vt10x, so we
// mirror the ones we render here. Reverse is intentionally omitted: vt10x has
// already swapped FG/BG into the stored cell, so honouring it would double-swap.
const (
	attrUnderline = 1 << 1
	attrBold      = 1 << 2
	attrItalic    = 1 << 4
)

// OutputMsg carries a chunk of remote output read from the session.
type OutputMsg struct{ Data []byte }

// ClosedMsg signals the session ended (remote process exited or the connection
// dropped). A non-EOF error is surfaced to the user.
type ClosedMsg struct{ Err error }

// Model is the embedded-terminal panel.
type Model struct {
	term    vt10x.Terminal
	session docker.ExecSession
	title   string

	// input feeds keystrokes to a single pump goroutine so they reach the remote
	// TTY in the exact order they were typed (per-key commands would race).
	input chan []byte

	width, height int // panel area (including border), in cells
	cols, rows    int // inner terminal grid (panel minus border)

	closed  bool
	exitErr error
}

// New returns an empty shell panel; call Open to attach a session.
func New() Model { return Model{} }

// SetSize records the panel area (header/footer-excluded body) and resizes the
// inner terminal grid to fit inside the border.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.cols = max1(width - 2)
	m.rows = max1(height - 2)
	if m.term != nil {
		m.term.Resize(m.cols, m.rows)
	}
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// Open attaches an exec session and starts pumping its output. The terminal is
// sized to the current panel dimensions (set by a prior SetSize). It returns the
// commands that read output and push the initial window size to the remote TTY.
func (m *Model) Open(session docker.ExecSession, title string) tea.Cmd {
	cols, rows := m.cols, m.rows
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	m.cols, m.rows = cols, rows
	m.session = session
	m.title = title
	m.closed = false
	m.exitErr = nil
	// Terminal replies (e.g. cursor reports) are discarded: a shell MVP doesn't
	// need them, and routing them back would mean network I/O on the event loop.
	m.term = vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(io.Discard))

	// A single pump preserves keystroke order; SendKey enqueues from the event
	// loop, this goroutine performs the (blocking) network writes.
	m.input = make(chan []byte, 256)
	go func(in <-chan []byte, s docker.ExecSession) {
		for b := range in {
			if _, err := s.Write(b); err != nil {
				return // session gone; the read side will report it
			}
		}
	}(m.input, session)

	return tea.Batch(ReadCmd(session), ResizeCmd(session, rows, cols))
}

// Closed reports whether the session has ended.
func (m Model) Closed() bool { return m.closed }

// Title returns the display title (container name) of the active session.
func (m Model) Title() string { return m.title }

// ExitErr returns the error that ended the session, if it ended abnormally
// (a clean exit or EOF returns nil).
func (m Model) ExitErr() error { return m.exitErr }

// CloseSession ends the underlying session (used when the user detaches). The
// input pump is stopped first so it can't write to a closing connection.
func (m *Model) CloseSession() {
	if m.input != nil {
		close(m.input)
		m.input = nil
	}
	if m.session != nil {
		_ = m.session.Close()
	}
	m.closed = true
}

// ResizeRemoteCmd pushes the current grid size to the remote TTY; used after a
// window resize. It returns nil when there is nothing to resize.
func (m Model) ResizeRemoteCmd() tea.Cmd {
	if m.session == nil || m.closed {
		return nil
	}
	return ResizeCmd(m.session, m.rows, m.cols)
}

// SendKey encodes a key event and enqueues it for the input pump, preserving
// typing order. It returns the bytes enqueued (nil when the key has no terminal
// representation, the session is gone, or the buffer is full).
func (m Model) SendKey(msg tea.KeyMsg) []byte {
	if m.closed || m.input == nil {
		return nil
	}
	data := encodeKey(msg)
	if len(data) == 0 {
		return nil
	}
	select {
	case m.input <- data:
		return data
	default: // buffer full: drop rather than block the event loop
		return nil
	}
}

// Update applies streamed output and session-closed events.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case OutputMsg:
		if m.term != nil && len(msg.Data) > 0 {
			_, _ = m.term.Write(msg.Data)
		}
		if m.closed {
			return m, nil
		}
		return m, ReadCmd(m.session)
	case ClosedMsg:
		m.closed = true
		if msg.Err != nil && !errors.Is(msg.Err, io.EOF) {
			m.exitErr = msg.Err
		}
		return m, nil
	}
	return m, nil
}

// ── commands ────────────────────────────────────────────────────────────────

// ReadCmd reads the next chunk of remote output. A read of zero bytes (or any
// error) closes the panel.
func ReadCmd(s docker.ExecSession) tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 4096) // fresh each call: buf[:n] is safe to hand off
		n, err := s.Read(buf)
		if n > 0 {
			return OutputMsg{Data: buf[:n]}
		}
		return ClosedMsg{Err: err}
	}
}

// ResizeCmd updates the remote TTY window size (rows × cols).
func ResizeCmd(s docker.ExecSession, rows, cols int) tea.Cmd {
	return func() tea.Msg {
		_ = s.Resize(rows, cols)
		return nil
	}
}

// ── view ────────────────────────────────────────────────────────────────────

// View draws the terminal grid inside a titled border filling the panel area.
func (m Model) View() string {
	if m.width < 2 || m.height < 2 {
		return ""
	}
	if m.term == nil {
		return m.box(strings.Repeat(strings.Repeat(" ", m.cols)+"\n", m.rows))
	}
	return m.box(m.renderScreen())
}

// renderScreen turns the emulator's cell grid into a styled, multi-line string
// of exactly m.rows lines, each m.cols cells wide, with the cursor drawn as a
// reverse-video block.
func (m Model) renderScreen() string {
	cur := m.term.Cursor()
	showCursor := m.term.CursorVisible() && !m.closed
	lines := make([]string, m.rows)
	for y := 0; y < m.rows; y++ {
		lines[y] = m.renderLine(y, cur, showCursor)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderLine(y int, cur vt10x.Cursor, showCursor bool) string {
	var sb strings.Builder
	var run strings.Builder
	var runKey cellStyle
	haveRun := false

	flush := func() {
		if haveRun {
			sb.WriteString(runKey.style().Render(run.String()))
			run.Reset()
			haveRun = false
		}
	}

	for x := 0; x < m.cols; x++ {
		g := m.term.Cell(x, y)
		ch := g.Char
		if ch < 0x20 { // empty/control cells render as blanks
			ch = ' '
		}
		cs := glyphStyle(g)
		if showCursor && x == cur.X && y == cur.Y {
			cs.reverse = true
		}
		if haveRun && cs == runKey {
			run.WriteRune(ch)
			continue
		}
		flush()
		runKey = cs
		run.WriteRune(ch)
		haveRun = true
	}
	flush()
	return sb.String()
}

// box wraps content in a rounded border, sized to exactly m.width × m.height
// cells, with the title spliced into the top edge when it fits.
func (m Model) box(content string) string {
	inner := m.width - 2

	title := m.title
	if m.closed {
		title += " [exited — q/esc to close]"
	}

	head := "─ " + title + " "
	var top string
	if lipgloss.Width(head) <= inner {
		dashes := inner - lipgloss.Width(head)
		top = styles.ShellBorder.Render("╭─ ") +
			styles.ShellTitle.Render(title) +
			styles.ShellBorder.Render(" "+strings.Repeat("─", dashes)+"╮")
	} else {
		top = styles.ShellBorder.Render("╭" + strings.Repeat("─", inner) + "╮")
	}

	side := styles.ShellBorder.Render("│")
	var b strings.Builder
	b.WriteString(top)
	b.WriteByte('\n')
	for _, line := range strings.Split(content, "\n") {
		b.WriteString(side)
		b.WriteString(line)
		b.WriteString(side)
		b.WriteByte('\n')
	}
	b.WriteString(styles.ShellBorder.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// ── cell styling ────────────────────────────────────────────────────────────

// cellStyle is a comparable description of a cell's appearance, used to coalesce
// runs of identically-styled cells into a single lipgloss render.
type cellStyle struct {
	fg, bg           string // lipgloss colour value; "" means terminal default
	bold, ul, italic bool
	reverse          bool // set for the cursor cell
}

func (c cellStyle) style() lipgloss.Style {
	s := lipgloss.NewStyle()
	if c.fg != "" {
		s = s.Foreground(lipgloss.Color(c.fg))
	}
	if c.bg != "" {
		s = s.Background(lipgloss.Color(c.bg))
	}
	if c.bold {
		s = s.Bold(true)
	}
	if c.ul {
		s = s.Underline(true)
	}
	if c.italic {
		s = s.Italic(true)
	}
	if c.reverse {
		s = s.Reverse(true)
	}
	return s
}

func glyphStyle(g vt10x.Glyph) cellStyle {
	return cellStyle{
		fg:     colorValue(g.FG),
		bg:     colorValue(g.BG),
		bold:   g.Mode&attrBold != 0,
		ul:     g.Mode&attrUnderline != 0,
		italic: g.Mode&attrItalic != 0,
	}
}

// colorValue maps a vt10x colour to a lipgloss colour string: the default
// markers become "" (use the terminal default), values < 256 are ANSI/256
// palette indices and larger values are packed 24-bit RGB.
func colorValue(c vt10x.Color) string {
	switch {
	case c >= vt10x.DefaultFG: // DefaultFG/BG/Cursor sentinels
		return ""
	case c < 256:
		return strconv.Itoa(int(c))
	default:
		return fmt.Sprintf("#%06X", uint32(c))
	}
}
