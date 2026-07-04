package fsbrowser

import (
	"strings"
	"testing"

	"d9c/internal/docker"

	tea "github.com/charmbracelet/bubbletea"
)

func manyEntries(n int) []docker.FileEntry {
	out := make([]docker.FileEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, docker.FileEntry{Name: string(rune('a' + i))})
	}
	return out
}

func TestFsbrowserNavigation(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.Show("c1", "web", "/var", manyEntries(20))

	key := func(msg tea.Msg) { m, _ = m.Update(msg) }

	key(tea.KeyMsg{Type: tea.KeyUp}) // clamp at top
	if m.cursor != 0 {
		t.Errorf("cursor = %d", m.cursor)
	}
	key(tea.KeyMsg{Type: tea.KeyDown})
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	key(tea.KeyMsg{Type: tea.KeyEnd})
	if m.cursor != 19 {
		t.Errorf("cursor after end = %d", m.cursor)
	}
	key(tea.KeyMsg{Type: tea.KeyHome})
	if m.cursor != 0 {
		t.Errorf("cursor after home = %d", m.cursor)
	}
	key(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor == 0 {
		t.Error("pgdown should move")
	}
	key(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.cursor != 0 {
		t.Errorf("cursor after pgup = %d", m.cursor)
	}
	// Non-key message is a no-op.
	key(tea.MouseMsg{})
}

func TestFsbrowserViewStates(t *testing.T) {
	m := New()
	m.SetSize(80, 10)

	// Empty listing.
	m.Show("c1", "web", "", nil)
	if v := m.View(); !strings.Contains(v, "web:/") {
		t.Error("view should render the name:path header")
	}

	m.Show("c1", "web", "var/log", []docker.FileEntry{{Name: "app", IsDir: true}, {Name: "x.log"}})
	if m.CurrentPath() != "/var/log" {
		t.Errorf("path = %q, want cleaned /var/log", m.CurrentPath())
	}
	v := m.View()
	if !strings.Contains(v, "app/") || !strings.Contains(v, "x.log") || !strings.Contains(v, "▶") {
		t.Error("view should render entries with a cursor")
	}

	m.SetError("permission denied")
	if v := m.View(); !strings.Contains(v, "permission denied") {
		t.Error("view should render the error")
	}

	if got := m.ContainerID(); got != "c1" {
		t.Errorf("ContainerID = %q", got)
	}
	if got := m.Name(); got != "web" {
		t.Errorf("Name = %q", got)
	}
	if got := m.Selected(); got.Name != "app" {
		t.Errorf("Selected = %+v", got)
	}
}

func TestFsbrowserTinyHeight(t *testing.T) {
	m := New()
	m.SetSize(80, 0)
	if got := m.listHeight(); got != 1 {
		t.Errorf("listHeight = %d, want floor 1", got)
	}
	if got := m.pageSize(); got != 1 {
		t.Errorf("pageSize = %d, want 1", got)
	}
}
