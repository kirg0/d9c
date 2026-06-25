package cpform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReadLocalDirSortsDirsFirst(t *testing.T) {
	tmp := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Mkdir(filepath.Join(tmp, "zsub"), 0o755))
	must(os.WriteFile(filepath.Join(tmp, "b.txt"), []byte("b"), 0o644))
	must(os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("a"), 0o644))

	abs, entries, err := ReadLocalDir(tmp)
	if err != nil {
		t.Fatalf("ReadLocalDir: %v", err)
	}
	if !filepath.IsAbs(abs) {
		t.Errorf("dir %q is not absolute", abs)
	}
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name
	}
	want := []string{"zsub", "a.txt", "b.txt"} // dir first, then files alphabetical
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	if !entries[0].IsDir {
		t.Error("first entry should be the directory")
	}
}

func TestReadLocalDirError(t *testing.T) {
	if _, _, err := ReadLocalDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for a missing directory")
	}
}

func TestParent(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "child")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Parent(sub); got != tmp {
		t.Errorf("Parent(%q) = %q, want %q", sub, got, tmp)
	}
}

func TestSourcePathJoinsCursorEntry(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	m.Show("/data", []Entry{{Name: "x.txt"}, {Name: "y.txt"}})
	if got := m.SourcePath(); got != filepath.Join("/data", "x.txt") {
		t.Errorf("SourcePath = %q, want %q", got, filepath.Join("/data", "x.txt"))
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SourcePath(); got != filepath.Join("/data", "y.txt") {
		t.Errorf("SourcePath after down = %q, want y.txt path", got)
	}
}

func TestSourcePathEmptyListing(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	if got := m.SourcePath(); got != "" {
		t.Errorf("SourcePath on empty listing = %q, want empty", got)
	}
}

func TestToggleFocusAndDest(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	if !m.OnBrowser() {
		t.Fatal("form should open focused on the picker")
	}
	m.ToggleFocus()
	if m.OnBrowser() {
		t.Fatal("ToggleFocus should move focus to the destination field")
	}
	// Default destination is the container root.
	if got := m.Dest(); got != "/" {
		t.Errorf("default Dest = %q, want /", got)
	}
	for _, r := range "tmp" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Dest(); got != "/tmp" {
		t.Errorf("Dest after typing = %q, want /tmp", got)
	}
}

func TestBrowserKeysIgnoredWhileDestFocused(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	m.Show("/data", []Entry{{Name: "a"}, {Name: "b"}})
	m.ToggleFocus() // dest focused
	// 'j' should type into the field, not move the picker cursor.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := m.SourcePath(); got != filepath.Join("/data", "a") {
		t.Errorf("cursor moved while dest focused: SourcePath = %q", got)
	}
}

func TestSetErrorClearsBusy(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	m.Running()
	if !m.Busy() {
		t.Fatal("Running should set busy")
	}
	m.SetError("denied")
	if m.Busy() {
		t.Error("SetError should clear busy so the user can retry")
	}
}

func TestSelectedFileMarkedWhenDestFocused(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	m.Show("/data", []Entry{{Name: "a.txt"}, {Name: "b.txt"}})
	m.ToggleFocus() // move focus to the destination field
	v := m.View(80, 24)
	// The chosen source stays marked (●) and named in the Selected line.
	if !strings.Contains(v, "●") {
		t.Errorf("view should mark the chosen source while dest focused:\n%s", v)
	}
	if !strings.Contains(v, "Selected: ") || !strings.Contains(v, "a.txt") {
		t.Errorf("view should name the selected source a.txt:\n%s", v)
	}
}

func TestSourceLabelEmptyListing(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	if got := m.sourceLabel(); got != "(none)" {
		t.Errorf("sourceLabel on empty listing = %q, want (none)", got)
	}
}

func TestViewShowsContainerAndError(t *testing.T) {
	m := New()
	m.Open("abc123", "web")
	m.SetError("boom")
	v := m.View(80, 24)
	if !strings.Contains(v, "web") {
		t.Error("view should show the container name")
	}
	if !strings.Contains(v, "boom") {
		t.Error("view should render the error message")
	}
}
