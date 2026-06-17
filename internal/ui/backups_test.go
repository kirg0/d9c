package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/ui/cmdline"

	tea "github.com/charmbracelet/bubbletea"
)

func writeBackup(t *testing.T, dir, name string, mod time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIsBackupFile(t *testing.T) {
	tests := []struct {
		name, prefix string
		want         bool
	}{
		{"webapp-20260101-120000.tar.gz", "webapp-", true},
		{"webapp-20260101-120000.tar", "webapp-", false},      // wrong suffix
		{"webapp-2026-120000.tar.gz", "webapp-", false},       // bad stamp
		{"web-api-20260101-120000.tar.gz", "web-", false},     // remainder isn't a bare stamp
		{"other-20260101-120000.tar.gz", "webapp-", false},    // wrong project
		{"webapp-20260101-120000-x.tar.gz", "webapp-", false}, // trailing junk
	}
	for _, tt := range tests {
		if got := isBackupFile(tt.name, tt.prefix); got != tt.want {
			t.Errorf("isBackupFile(%q,%q) = %v, want %v", tt.name, tt.prefix, got, tt.want)
		}
	}
}

func TestListBackups(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	writeBackup(t, dir, "webapp-20260101-120000.tar.gz", base)
	writeBackup(t, dir, "webapp-20260103-120000.tar.gz", base.Add(48*time.Hour)) // newest
	writeBackup(t, dir, "webapp-20260102-120000.tar.gz", base.Add(24*time.Hour))
	writeBackup(t, dir, "other-20260101-120000.tar.gz", base) // different project
	writeBackup(t, dir, "notes.txt", base)                    // not a backup

	got, err := listBackups(dir, "webapp")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d backups, want 3: %+v", len(got), got)
	}
	if got[0].name != "webapp-20260103-120000.tar.gz" {
		t.Errorf("first = %q, want the newest archive", got[0].name)
	}
	if got[2].name != "webapp-20260101-120000.tar.gz" {
		t.Errorf("last = %q, want the oldest archive", got[2].name)
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
	}
	for _, tt := range tests {
		if got := humanSize(tt.n); got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// TestBackupsListedOpensPicker checks the message handler opens the catalog when
// archives exist and reports an error (staying in normal mode) when none do.
func TestBackupsListedOpensPicker(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, _ = tm.Update(backupsListedMsg{project: "webapp", items: []backupEntry{
		{name: "webapp-20260101-120000.tar.gz", path: "webapp-20260101-120000.tar.gz"},
	}})
	m := tm.(Model)
	if m.mode != ModeBackupPicker || m.backupProject != "webapp" || len(m.backupItems) != 1 {
		t.Fatalf("picker not open: mode=%v project=%q items=%d", m.mode, m.backupProject, len(m.backupItems))
	}

	tm, _ = tm.Update(backupsListedMsg{project: "webapp", items: nil})
	m = tm.(Model)
	if m.mode != ModeNormal || m.err == "" {
		t.Errorf("empty catalog: mode=%v err=%q, want normal mode + error", m.mode, m.err)
	}
}

// TestBackupPickerRestore drives Enter in the catalog to a streaming restore.
func TestBackupPickerRestore(t *testing.T) {
	dir := t.TempDir()
	p := writeBackup(t, dir, "webapp-20260101-120000.tar.gz", time.Now())

	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	var lastCmd tea.Cmd
	step := func(msg tea.Msg) { tm, lastCmd = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(backupsListedMsg{project: "/srv/webapp", name: "webapp", items: []backupEntry{{name: filepath.Base(p), path: p}}})

	step(tea.KeyMsg{Type: tea.KeyEnter})
	if tm.(Model).mode != ModeNormal {
		t.Errorf("after restore mode = %v, want normal", tm.(Model).mode)
	}
	if lastCmd == nil {
		t.Fatal("enter produced no command")
	}
	if _, ok := lastCmd().(opStartedMsg); !ok {
		t.Errorf("restore msg = %#v, want opStartedMsg", lastCmd())
	}
}

// TestBackupPickerDelete confirms the two-step delete removes the archive.
func TestBackupPickerDelete(t *testing.T) {
	dir := t.TempDir()
	p := writeBackup(t, dir, "webapp-20260101-120000.tar.gz", time.Now())

	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(backupsListedMsg{project: "webapp", items: []backupEntry{{name: filepath.Base(p), path: p}}})

	// First 'd' arms the confirmation; the file must still exist.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if tm.(Model).backupConfirmDelete != filepath.Base(p) {
		t.Fatalf("confirm not armed: %q", tm.(Model).backupConfirmDelete)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file removed too early: %v", err)
	}
	// Second 'd' deletes it.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("backup not deleted: stat err = %v", err)
	}
}

// In the compose view the row identity must be the deployment's working_dir
// (not the PROJECT column, which can repeat across deployments), so lifecycle
// ops target exactly one deployment.
func TestComposeSelectedIDIsWorkingDir(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{resource: ViewCompose})
	projects, _ := fb.ListComposeProjects()
	step(composeUpdatedMsg{projects})

	// Rows are sorted by PROJECT, so the cursor lands on legacy (/opt/legacy).
	m := tm.(Model)
	if got := m.selectedID(); got != "/opt/legacy" {
		t.Errorf("selectedID = %q, want the working_dir /opt/legacy", got)
	}
	if got := m.composeNameFor("/opt/legacy"); got != "legacy" {
		t.Errorf("composeNameFor = %q, want legacy", got)
	}
}

// TestBackupsDispatchOpensCatalog checks :backups and :restore (no arg) route to
// the catalog, while :restore <file> goes straight to a restore.
func TestBackupsDispatchOpensCatalog(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{resource: ViewCompose})
	projects, _ := fb.ListComposeProjects()
	step(composeUpdatedMsg{projects})

	m := tm.(Model)
	if m.selectedID() == "" {
		t.Fatal("no compose project selected")
	}
	for _, name := range []string{"backups", "restore"} {
		cmd, err := m.dispatchComposeCommand(&cmdline.CommandMsg{Name: name})
		if err != nil || cmd == nil {
			t.Errorf("dispatch %q: cmd=%v err=%v, want non-nil cmd", name, cmd, err)
		}
	}
	cmd, err := m.dispatchComposeCommand(&cmdline.CommandMsg{Name: "restore", Args: []string{"x.tar.gz"}})
	if err != nil || cmd == nil {
		t.Errorf("dispatch restore <file>: cmd=%v err=%v", cmd, err)
	}
}
