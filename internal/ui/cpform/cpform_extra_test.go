package cpform

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func entries(n int) []Entry {
	out := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Entry{Name: string(rune('a' + i))})
	}
	return out
}

func TestOpenAndGetters(t *testing.T) {
	m := New()
	m.SetSize(100, 30)
	m.Open("ctr1", "web")
	m.Show("/home", []Entry{{Name: "docs", IsDir: true}, {Name: "a.txt"}})

	if m.ContainerID() != "ctr1" || m.Name() != "web" {
		t.Errorf("identity = %q/%q", m.ContainerID(), m.Name())
	}
	if m.CurrentDir() != "/home" {
		t.Errorf("dir = %q", m.CurrentDir())
	}
	if !m.OnBrowser() {
		t.Error("focus should start on the browser")
	}
	if got := m.Selected(); got.Name != "docs" || !got.IsDir {
		t.Errorf("selected = %+v", got)
	}
	if got := m.SourcePath(); !strings.HasSuffix(got, "docs") {
		t.Errorf("source path = %q", got)
	}
	if got := m.Dest(); got != "/" {
		t.Errorf("dest = %q", got)
	}
}

func TestUpdateNavigationAndPaging(t *testing.T) {
	m := New()
	m.SetSize(100, 30)
	m.Open("c", "n")
	m.Show("/", entries(20))

	key := func(s string) {
		var msg tea.KeyMsg
		switch s {
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "home":
			msg = tea.KeyMsg{Type: tea.KeyHome}
		case "end":
			msg = tea.KeyMsg{Type: tea.KeyEnd}
		case "pgup":
			msg = tea.KeyMsg{Type: tea.KeyPgUp}
		case "pgdown":
			msg = tea.KeyMsg{Type: tea.KeyPgDown}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		}
		m, _ = m.Update(msg)
	}

	key("up") // clamped at top
	if m.cursor != 0 {
		t.Errorf("cursor = %d", m.cursor)
	}
	key("down")
	key("j")
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	key("end")
	if m.cursor != 19 {
		t.Errorf("cursor after end = %d", m.cursor)
	}
	key("home")
	if m.cursor != 0 {
		t.Errorf("cursor after home = %d", m.cursor)
	}
	key("pgdown")
	if m.cursor == 0 {
		t.Error("pgdown should move the cursor")
	}
	key("pgup")
	if m.cursor != 0 {
		t.Errorf("cursor after pgup = %d", m.cursor)
	}
	key("G")
	if m.cursor != 19 {
		t.Errorf("cursor after G = %d", m.cursor)
	}
	key("g")
	if m.cursor != 0 {
		t.Errorf("cursor after g = %d", m.cursor)
	}

	// Focus on the destination field routes typing there.
	m.ToggleFocus()
	if m.OnBrowser() {
		t.Error("focus should be on the destination")
	}
	key("x")
	if !strings.Contains(m.Dest(), "x") {
		t.Errorf("dest = %q, want typed x", m.Dest())
	}

	// Busy swallows input.
	_ = m.Running()
	key("y")
	if strings.Contains(m.Dest(), "y") {
		t.Error("busy form should ignore input")
	}
	if !m.Busy() {
		t.Error("Busy should report true")
	}
	m, _ = m.Tick(spinner.TickMsg{})

	// Non-key messages are ignored on the browser.
	m.busy = false
	m.focusField(focusBrowser)
	m, _ = m.Update(tea.MouseMsg{})
}

func TestViewStatesCpform(t *testing.T) {
	m := New()
	m.SetSize(100, 30)
	m.Open("c", "web")

	// Empty listing.
	if v := m.View(100, 30); !strings.Contains(v, "Upload to container: web") {
		t.Error("view should render the title")
	}

	m.Show("/", []Entry{{Name: "dir", IsDir: true}, {Name: "f.txt"}})
	v := m.View(100, 30)
	if !strings.Contains(v, "dir/") || !strings.Contains(v, "f.txt") {
		t.Error("view should render entries")
	}
	if !strings.Contains(v, "▶") {
		t.Error("view should mark the cursor row")
	}

	// Cursor marker dims when focus moves to the destination.
	m.ToggleFocus()
	if v := m.View(100, 30); !strings.Contains(v, "●") {
		t.Error("picker cursor should stay visible as ●")
	}

	// Error and busy footers.
	m.SetError("boom")
	if v := m.View(100, 30); !strings.Contains(v, "boom") {
		t.Error("view should render the error")
	}
	_ = m.Running()
	if v := m.View(100, 30); !strings.Contains(v, "uploading…") {
		t.Error("view should render the busy footer")
	}
}

func TestListHeightBounds(t *testing.T) {
	m := New()
	m.SetSize(100, 5) // tiny: floor of 3
	if got := m.listHeight(); got != 3 {
		t.Errorf("listHeight = %d, want floor 3", got)
	}
	m.SetSize(100, 100) // huge: cap of 14
	if got := m.listHeight(); got != 14 {
		t.Errorf("listHeight = %d, want cap 14", got)
	}
	if m.pageSize() != 13 {
		t.Errorf("pageSize = %d", m.pageSize())
	}
}

func TestReadLocalDirAndParent(t *testing.T) {
	dir := t.TempDir()
	abs, es, err := ReadLocalDir(dir)
	if err != nil || abs == "" || len(es) != 0 {
		t.Errorf("empty dir = %q / %v / %v", abs, es, err)
	}
	if _, _, err := ReadLocalDir(dir + "\\ghost"); err == nil {
		t.Error("missing dir should fail")
	}
	if got := Parent(dir); got == dir {
		t.Errorf("Parent(%q) = %q", dir, got)
	}
}
