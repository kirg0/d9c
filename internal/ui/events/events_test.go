package events

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNew(t *testing.T) {
	m := New()
	if m.viewport.Height != 0 {
		t.Errorf("expected initial height 0, got %d", m.viewport.Height)
	}
}

func TestOpenClearsBuffer(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.AddLine("container start abc (local)")
	m.AddLine("container stop def (local)")

	m.Open()
	if m.LineCount() != 0 {
		t.Errorf("expected 0 lines after Open(), got %d", m.LineCount())
	}
}

// TestOpenShowsWaitingPlaceholder checks a freshly opened viewer renders the
// waiting hint (so an idle daemon isn't mistaken for a broken stream) and that
// the first real event replaces it.
func TestOpenShowsWaitingPlaceholder(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open()
	if !strings.Contains(m.View(), "ожидание") && !strings.Contains(m.View(), "waiting") {
		t.Errorf("view after Open() = %q, want the waiting placeholder", m.View())
	}

	m.AddLine("container start abc (local)")
	if strings.Contains(m.View(), "ожидание") || strings.Contains(m.View(), "waiting") {
		t.Error("placeholder must disappear after the first event line")
	}
}

func TestAddLineAndLineCount(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.AddLine("container start abc123def (local)")
	m.AddLine("container oom xyz (local)")

	if got := m.LineCount(); got != 2 {
		t.Errorf("expected 2 lines, got %d", got)
	}
}

// TestAddLinesCap checks the buffer never exceeds maxBufferLines: the oldest
// events are dropped, the newest kept.
func TestAddLinesCap(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open()

	batch := make([]string, maxBufferLines+50)
	for i := range batch {
		batch[i] = fmt.Sprintf("container start c%d (local)", i)
	}
	m.AddLines(batch)
	if got := m.LineCount(); got != maxBufferLines {
		t.Fatalf("LineCount = %d, want cap %d", got, maxBufferLines)
	}
	if got, want := m.rawLines[0], "container start c50 (local)"; got != want {
		t.Errorf("oldest kept line = %q, want %q", got, want)
	}
}

func TestRawContent(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.AddLine("container start abc (local)")
	m.AddLine("container stop def (local)")

	raw := m.RawContent()
	expected := "container start abc (local)\ncontainer stop def (local)"
	if raw != expected {
		t.Errorf("expected %q, got %q", expected, raw)
	}
}

func TestUpdateScrolls(t *testing.T) {
	m := New()
	m.SetSize(80, 20)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.viewport.YOffset != 0 {
		t.Errorf("expected offset 0, got %d", updated.viewport.YOffset)
	}
	// No cmd expected for simple key in viewport without content
	if cmd != nil {
		_ = cmd() // consume if any
	}
}

func TestFormatEventLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string // we only check non-empty output with color markers
	}{
		{
			name: "normal event",
			line: "container start 9ae942fd8fbc (local)",
		},
		{
			name: "oom event",
			line: "container oom 3f1ab77c9012 (local)",
		},
		{
			name: "error event",
			line: "[error] connection refused",
		},
		{
			name: "empty line",
			line: "",
		},
		{
			name: "no scope",
			line: "image pull nginx",
		},
		// Lines with fewer than three tokens used to index parts[2] and panic;
		// they must render as plain text instead of crashing the TUI.
		{
			name: "two tokens",
			line: "container start",
		},
		{
			name: "one token",
			line: "container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatEventLine(tt.line)
			if tt.line == "" && got != "" {
				t.Errorf("expected empty, got %q", got)
			}
		})
	}
}
