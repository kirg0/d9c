package fsbrowser

import (
	"strings"
	"testing"

	"d9c/internal/docker"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPathJoin(t *testing.T) {
	tests := []struct {
		base, name, want string
	}{
		{"/", "app", "/app"},
		{"/app", "data", "/app/data"},
		{"/app/", "data", "/app/data"},
		{"", "x", "/x"},
		{"/a/b", "..", "/a"},
		{"/a", ".", "/a"},
	}
	for _, tt := range tests {
		if got := PathJoin(tt.base, tt.name); got != tt.want {
			t.Errorf("PathJoin(%q,%q) = %q, want %q", tt.base, tt.name, got, tt.want)
		}
	}
}

func TestPathParent(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/", "/"},
		{"/app", "/"},
		{"/app/data", "/app"},
		{"/app/data/", "/app"},
		{"", "/"},
	}
	for _, tt := range tests {
		if got := PathParent(tt.in); got != tt.want {
			t.Errorf("PathParent(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestShowAndSelected(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	entries := []docker.FileEntry{
		{Name: "bin", IsDir: true},
		{Name: "hello.txt", IsDir: false},
	}
	m.Show("abc123", "web", "/", entries)

	if m.ContainerID() != "abc123" || m.Name() != "web" || m.CurrentPath() != "/" {
		t.Fatalf("Show state wrong: id=%q name=%q path=%q", m.ContainerID(), m.Name(), m.CurrentPath())
	}
	if got := m.Selected(); got.Name != "bin" {
		t.Fatalf("Selected at cursor 0 = %q, want bin", got.Name)
	}
}

func TestCursorMovementClamped(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Show("abc", "web", "/", []docker.FileEntry{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	})

	// Up at the top stays at 0.
	m, _ = m.Update(key("up"))
	if m.Selected().Name != "a" {
		t.Errorf("up at top: %q", m.Selected().Name)
	}
	// Down moves.
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("down"))
	if m.Selected().Name != "c" {
		t.Errorf("after two downs: %q", m.Selected().Name)
	}
	// Down past the end clamps to last.
	m, _ = m.Update(key("down"))
	if m.Selected().Name != "c" {
		t.Errorf("down past end: %q", m.Selected().Name)
	}
	// G jumps to bottom, g to top.
	m, _ = m.Update(key("g"))
	if m.Selected().Name != "a" {
		t.Errorf("g to top: %q", m.Selected().Name)
	}
}

func TestSelectedEmpty(t *testing.T) {
	m := New()
	m.Show("abc", "web", "/", nil)
	if got := m.Selected(); got.Name != "" {
		t.Fatalf("Selected on empty listing = %q, want empty", got.Name)
	}
}

func TestViewShowsEntriesAndPath(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Show("abc", "web", "/app", []docker.FileEntry{
		{Name: "data", IsDir: true},
		{Name: "main.go", IsDir: false},
	})
	out := m.View()
	for _, want := range []string{"web:/app", "data", "main.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\n%s", want, out)
		}
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
