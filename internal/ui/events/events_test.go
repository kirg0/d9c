package events

import (
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

func TestAddLineAndLineCount(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.AddLine("container start abc123def (local)")
	m.AddLine("container oom xyz (local)")

	if got := m.LineCount(); got != 2 {
		t.Errorf("expected 2 lines, got %d", got)
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
