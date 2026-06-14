package shell

import (
	"bytes"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hinshun/vt10x"
)

// fakeSess is a controllable docker.ExecSession for tests: Read drains a queued
// list of chunks then reports EOF, Write forwards each chunk to the wrote channel
// (when set) so the input pump's output can be observed in order, and
// Resize/Close record the calls.
type fakeSess struct {
	reads      [][]byte
	ridx       int
	wrote      chan []byte // receives each Write, in order; nil to ignore writes
	resizeRows int
	resizeCols int
	closed     bool
}

func (f *fakeSess) Read(p []byte) (int, error) {
	if f.ridx >= len(f.reads) {
		return 0, io.EOF
	}
	n := copy(p, f.reads[f.ridx])
	f.ridx++
	return n, nil
}

func (f *fakeSess) Write(p []byte) (int, error) {
	if f.wrote != nil {
		f.wrote <- append([]byte(nil), p...)
	}
	return len(p), nil
}

func (f *fakeSess) Resize(rows, cols int) error {
	f.resizeRows, f.resizeCols = rows, cols
	return nil
}

func (f *fakeSess) Close() error {
	f.closed = true
	return nil
}

// runCmd executes a command, recursing into batched commands, so tests can
// observe the side effects of the cmds a model returns.
func runCmd(c tea.Cmd) {
	if c == nil {
		return
	}
	switch msg := c().(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			runCmd(sub)
		}
	}
}

func TestEncodeKey(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyMsg
		want []byte
	}{
		{"rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}, []byte("a")},
		{"multi-rune paste", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ls")}, []byte("ls")},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, []byte(" ")},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, []byte{'\r'}},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, []byte{'\t'}},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, []byte{0x7f}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, []byte{0x1b}},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}, []byte{0x03}},
		{"ctrl+d", tea.KeyMsg{Type: tea.KeyCtrlD}, []byte{0x04}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, []byte("\x1b[A")},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, []byte("\x1b[B")},
		{"right", tea.KeyMsg{Type: tea.KeyRight}, []byte("\x1b[C")},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}, []byte("\x1b[D")},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, []byte("\x1b[3~")},
		{"alt+rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true}, []byte{0x1b, 'b'}},
		{"unmapped f1", tea.KeyMsg{Type: tea.KeyF1}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodeKey(tt.msg); !bytes.Equal(got, tt.want) {
				t.Errorf("encodeKey(%v) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestColorValue(t *testing.T) {
	tests := []struct {
		name string
		in   vt10x.Color
		want string
	}{
		{"default fg", vt10x.DefaultFG, ""},
		{"default bg", vt10x.DefaultBG, ""},
		{"ansi red", vt10x.Red, "1"},
		{"256 index", vt10x.Color(200), "200"},
		{"truecolor", vt10x.Color(0xFF8800), "#FF8800"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := colorValue(tt.in); got != tt.want {
				t.Errorf("colorValue(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestReadCmd verifies output chunks become OutputMsg and EOF becomes ClosedMsg.
func TestReadCmd(t *testing.T) {
	s := &fakeSess{reads: [][]byte{[]byte("hello")}}

	msg := ReadCmd(s)()
	out, ok := msg.(OutputMsg)
	if !ok {
		t.Fatalf("first msg = %#v, want OutputMsg", msg)
	}
	if string(out.Data) != "hello" {
		t.Errorf("data = %q, want hello", out.Data)
	}

	msg = ReadCmd(s)()
	closed, ok := msg.(ClosedMsg)
	if !ok {
		t.Fatalf("second msg = %#v, want ClosedMsg", msg)
	}
	if closed.Err != io.EOF {
		t.Errorf("closed err = %v, want io.EOF", closed.Err)
	}
}

// TestOpenAndRender drives a session through the model and checks the rendered
// panel shows the output, the title and exactly fills the requested area.
func TestOpenAndRender(t *testing.T) {
	m := New()
	m.SetSize(40, 12) // inner grid: 38 cols × 10 rows
	s := &fakeSess{}
	runCmd(m.Open(s, "web-1")) // runs the initial read + resize batch

	if s.resizeRows != 10 || s.resizeCols != 38 {
		t.Errorf("initial resize = %dx%d, want 10x38", s.resizeRows, s.resizeCols)
	}

	m, _ = m.Update(OutputMsg{Data: []byte("hi there")})
	view := m.View()

	if !strings.Contains(view, "hi there") {
		t.Errorf("view missing output text:\n%s", view)
	}
	if !strings.Contains(view, "web-1") {
		t.Errorf("view missing title:\n%s", view)
	}

	lines := strings.Split(view, "\n")
	if len(lines) != 12 {
		t.Fatalf("view has %d lines, want 12", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 40 {
			t.Errorf("line %d width = %d, want 40", i, w)
		}
	}
}

// TestSendKey encodes keystrokes and the input pump delivers them to the session
// in typing order.
func TestSendKey(t *testing.T) {
	m := New()
	m.SetSize(40, 12)
	s := &fakeSess{wrote: make(chan []byte, 8)}
	m.Open(s, "web-1")

	if got := m.SendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}); string(got) != "a" {
		t.Fatalf("SendKey enqueued %q, want a", got)
	}
	m.SendKey(tea.KeyMsg{Type: tea.KeyEnter})

	if got := <-s.wrote; string(got) != "a" {
		t.Errorf("pump wrote %q first, want a", got)
	}
	if got := <-s.wrote; string(got) != "\r" {
		t.Errorf("pump wrote %q second, want \\r", got)
	}
}

// TestClosedStopsInput verifies a closed session ignores further input and
// reports Closed.
func TestClosedStopsInput(t *testing.T) {
	m := New()
	m.SetSize(40, 12)
	s := &fakeSess{}
	m.Open(s, "web-1")

	m, _ = m.Update(ClosedMsg{Err: io.EOF})
	if !m.Closed() {
		t.Fatal("model not marked closed after ClosedMsg")
	}
	if got := m.SendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}); got != nil {
		t.Errorf("SendKey should be a no-op after close, got %q", got)
	}
	if cmd := m.ResizeRemoteCmd(); cmd != nil {
		t.Error("ResizeRemoteCmd should be nil after close")
	}
}
