package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestDemo_FilesBrowse opens the filesystem browser with 'f', descends into a
// directory with Enter and climbs back with Backspace, checking the path header
// and listings update through the fake backend.
func TestDemo_FilesBrowse(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web", "api")

	// Cursor starts on "web" (first running container). Open its filesystem.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	waitFor(t, tm, "web:/", "hello.txt", "app")

	// Cursor 0 is the "app" directory; Enter descends into it.
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "web:/app", "config.yaml", "main.go")

	// Backspace climbs back to the root listing (hello.txt only lives there).
	tm.Send(tea.KeyMsg{Type: tea.KeyBackspace})
	waitFor(t, tm, "web:/", "hello.txt")

	tm.Quit()
}

// TestDemo_FilesDownload moves to a file entry and presses 'd', checking the
// download lands in the working directory and the footer confirms it.
func TestDemo_FilesDownload(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	tm := newTestModel(t)
	waitFor(t, tm, "web", "api")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	waitFor(t, tm, "web:/", "hello.txt")

	// Move the cursor down onto "hello.txt" (the 5th entry) and download it.
	for range 4 {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	waitFor(t, tm, "скопировано", "hello.txt")

	tm.Quit()

	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("expected downloaded ./hello.txt: %v", err)
	}
}

// TestDemo_FilesCommandUpload exercises the `:cp` upload command end to end
// through the fake backend (which only validates the local path exists).
func TestDemo_FilesCommandUpload(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(local, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tm := newTestModel(t)
	waitFor(t, tm, "web", "api")

	tm.Type(":cp " + local + " /tmp")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	// A successful upload returns to the table with no error footer; assert the
	// container list still renders (the action completed without surfacing an err).
	waitFor(t, tm, "web", "nginx:1.25")
	tm.Quit()
}
