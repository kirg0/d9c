package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// waitFor waits until the rendered output contains every one of subs.
//
// NOTE: teatest.WaitFor drains the shared Output() buffer as it reads, so each
// call only sees bytes written after the previous call returned. Substrings
// that appear together in the same rendered frame must therefore be asserted in
// a SINGLE waitFor call; a separate call would see an already-consumed frame.
func waitFor(t *testing.T, tm *teatest.TestModel, subs ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, s := range subs {
			if !bytes.Contains(b, []byte(s)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
}

func newTestModel(t *testing.T) *teatest.TestModel {
	t.Helper()
	return newTestModelStore(t, &hosts.Store{})
}

func newTestModelStore(t *testing.T, store *hosts.Store) *teatest.TestModel {
	t.Helper()
	// Demo:true stubs the saved-host reachability probes (the hosts view would
	// otherwise dial real TCP/SSH connections from inside the test).
	m := NewModel(&config.Config{Demo: true}, docker.NewFakeBackend(), store, nil, false)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))
	// Nudge the renderer: with an empty input buffer the standard renderer does
	// not reliably flush a frame until an event triggers a repaint after the
	// initial data has loaded. A refresh keystroke ('r') does exactly that.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	return tm
}

// TestDemo_ContainersRender checks the app boots and lists demo containers.
func TestDemo_ContainersRender(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web", "nginx:1.25", "api")
	tm.Quit()
}

// TestDemo_ContainerStats verifies the CPU%/MEM columns populate from the
// Stats API: the header is present and the running "web" container shows its
// sampled figures once the async stats fetch completes.
func TestDemo_ContainerStats(t *testing.T) {
	tm := newTestModel(t)
	// Header + sampled values for the web container (CPU 2.5%, 48 MiB) in one frame.
	waitFor(t, tm, "CPU %", "MEM", "2.5%", "48.0 MB")
	tm.Quit()
}

// TestDemo_ContainerStatsView toggles the `docker stats`-style column layout
// with 's' and checks NET I/O / BLOCK I/O columns and values appear.
func TestDemo_ContainerStatsView(t *testing.T) {
	tm := newTestModel(t)
	// Ensure samples are loaded (default layout shows CPU%).
	waitFor(t, tm, "2.5%")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	waitFor(t, tm, "NET I/O", "BLOCK I/O", "1.0 MB / 512.0 KB", "8.0 MB / 2.0 MB")
	tm.Quit()
}

// TestDemo_ResourceAlerts sets a low CPU threshold via :alert and checks the
// breaching demo container is flagged with the ⚠ marker and the header count.
func TestDemo_ResourceAlerts(t *testing.T) {
	tm := newTestModel(t)
	// Wait for stats so the threshold has something to evaluate (web CPU 2.5%).
	waitFor(t, tm, "2.5%")

	tm.Type(":alert cpu 2")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	// The header alert chip (⚠ N) and the flagged web row appear together.
	waitFor(t, tm, "⚠")
	tm.Quit()
}

// TestDemo_ContainerLogs opens a container's logs with 'l' and checks a streamed
// line renders (exercises the LogOptions path through the fake backend).
func TestDemo_ContainerLogs(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web", "api")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	waitFor(t, tm, "server started")
	tm.Quit()
}

// TestDemo_ContainerRemoveError runs :rm on a running container and checks the
// friendly "running" error surfaces in the footer.
func TestDemo_ContainerRemoveError(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web", "api")
	tm.Type(":rm")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "контейнер запущен")
	tm.Quit()
}

// TestDemo_SwitchToImages drives the command line to switch resource views.
func TestDemo_SwitchToImages(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":images")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	waitFor(t, tm, "hello-world:latest", "postgres:16")
	tm.Quit()
}

// TestDemo_RemoveImageFriendlyError verifies that a daemon conflict surfaces as
// the friendly hint in the footer rather than the raw error text.
func TestDemo_RemoveImageFriendlyError(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	// Switch to the images view. Wait for the images-only column header:
	// "nginx:1.25" alone also matches the IMAGE column of a late containers frame,
	// and firing :rm before the async view switch lands would remove a container
	// instead.
	tm.Type(":images")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "REPOSITORY:TAG")

	// Rows are sorted by tag, so the default selection is the dangling <none>
	// image; filter down to nginx:1.25 (which the fake backend reports as having a
	// dependent child) so it's the only — and therefore selected — row.
	tm.Type("/nginx")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "nginx:1.25")

	tm.Type(":rm")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	waitFor(t, tm, "зависимые образы")
	tm.Quit()
}

// TestModel_DataBeforeResizeNoPanic reproduces the crash where a fast in-memory
// hosts update arrived before the first WindowSizeMsg, rendering rows against an
// empty column set. NewModel must seed columns so this cannot panic.
func TestModel_DataBeforeResizeNoPanic(t *testing.T) {
	store := &hosts.Store{}
	_ = store.Add("prod", "ssh://user@host")
	connErr := errors.New("could not connect")

	var m tea.Model = NewModel(&config.Config{}, docker.NewDisconnected(connErr), store, connErr, true)
	// Deliver the hosts update before any WindowSizeMsg — must not panic.
	m, _ = m.Update(hostsUpdatedMsg{store.List()})
	_ = m.View()
}

// TestModel_NoHostStartsInHostsWithoutError verifies that when no host is
// configured the app opens in the hosts view with the saved hosts and NO error
// in the footer (no connection is attempted).
func TestModel_NoHostStartsInHostsWithoutError(t *testing.T) {
	store := &hosts.Store{}
	_ = store.Add("prod", "ssh://user@host")

	var m tea.Model = NewModel(&config.Config{}, docker.NewDisconnected(nil), store, nil, true)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(hostsUpdatedMsg{store.List()})

	out := m.View()
	if !strings.Contains(out, "prod") {
		t.Errorf("expected saved host listed, got:\n%s", out)
	}
	if strings.Contains(out, "✖") {
		t.Errorf("expected no error in footer, got:\n%s", out)
	}
}

// TestDemo_StartsInHostsOnConnectError verifies the app opens in the hosts view
// (showing the connection error and saved hosts) instead of exiting when the
// initial Docker connection fails.
func TestDemo_StartsInHostsOnConnectError(t *testing.T) {
	store, err := hosts.Load(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Add("prod", "ssh://user@example")

	connErr := errors.New("dial tcp: connection refused")
	// Demo:true keeps the hosts-view reachability probes off the network.
	m := NewModel(&config.Config{Demo: true}, docker.NewDisconnected(connErr), store, connErr, true)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}) // nudge a render

	// "Connect" is the hosts-view footer hint, "prod" the saved host, and the
	// error text confirms the failed connection is surfaced.
	waitFor(t, tm, "prod", "connection refused", "Connect")
	tm.Quit()
}

// TestDemo_SwitchToVolumes guards against the row/column count mismatch that
// previously panicked the bubbles table when rendering the volumes view.
func TestDemo_SwitchToVolumes(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":volumes")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	waitFor(t, tm, "pgdata", "cache")
	tm.Quit()
}

// TestDemo_ComposeListAndLifecycle switches to the compose view, checks projects
// are listed, then runs a lifecycle command and verifies the status updates.
func TestDemo_ComposeListAndLifecycle(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp", "monitoring", "/srv/webapp")

	// Pause the first project (webapp); none are paused initially, so the word
	// "paused" appearing confirms the lifecycle command took effect.
	tm.Type(":pause")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "paused")

	tm.Quit()
}

// TestDemo_ComposeUp moves to the stopped "legacy" project and runs :up, which
// opens the streaming progress console; the live output then the refreshed
// "running" status after closing the console confirm the operation.
func TestDemo_ComposeUp(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "legacy", "stopped 0/2")

	// Rows are sorted by PROJECT, so legacy is the first row; bring it up.
	tm.Type(":up")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	// Progress console shows the title, a streamed line, and the completion mark.
	waitFor(t, tm, "compose up: legacy", "Started", "готово")
	// Close the console; the project list now reflects the running status.
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	waitFor(t, tm, "running 2/2")
	tm.Quit()
}

// TestDemo_ComposePull runs :pull and verifies the streamed pull progress shows
// in the console.
func TestDemo_ComposePull(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	// Rows are sorted by PROJECT (legacy, monitoring, webapp); move onto webapp.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Type(":pull")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "compose pull: webapp", "Pull complete", "готово")
	tm.Quit()
}

// TestDemo_ComposeDown runs :down on the running "webapp" project: the console
// streams teardown progress, and after closing it the status reads stopped.
func TestDemo_ComposeDown(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp", "running 3/3")

	// Rows are sorted by PROJECT (legacy, monitoring, webapp); move onto webapp.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Type(":down")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "compose down: webapp", "Removed", "готово")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	waitFor(t, tm, "stopped 0/3")
	tm.Quit()
}

// TestDemo_ComposeCreate authors a new project with :create <dir>, then Ctrl+S
// writes the file and brings it up, streaming progress in the console.
func TestDemo_ComposeCreate(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")
	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	tm.Type(":create /srv/newapp")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// Editor opens in create mode pre-filled with the starter template.
	waitFor(t, tm, "create: /srv/newapp", "services:")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlS})
	waitFor(t, tm, "compose create: /srv/newapp", "Started", "готово")
	tm.Quit()
}

// TestDemo_ComposeBackup runs :backup on a project and checks the saved-path
// notification appears (in a temp dir so the archive doesn't litter the repo).
func TestDemo_ComposeBackup(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	tm := newTestModel(t)
	waitFor(t, tm, "web")
	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")
	// Rows are sorted by PROJECT (legacy, monitoring, webapp); move onto webapp.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Type(":backup")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "backup saved:", "webapp-")
	tm.Quit()
}

// TestDemo_ComposeRestore uploads a backup archive with :restore <file> and
// checks the project is brought back up, streaming progress in the console.
func TestDemo_ComposeRestore(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	if err := os.WriteFile("webapp.tar.gz", []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}

	tm := newTestModel(t)
	waitFor(t, tm, "web")
	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	// Rows are sorted by PROJECT (legacy, monitoring, webapp); move onto webapp.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Type(":restore webapp.tar.gz")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "compose restore: webapp", "Started", "готово")
	tm.Quit()
}

// TestDemo_BackupCatalog opens the backup catalog for the selected project and
// checks an existing archive is listed.
func TestDemo_BackupCatalog(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	if err := os.WriteFile("webapp-20260101-120000.tar.gz", []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}

	tm := newTestModel(t)
	waitFor(t, tm, "web")
	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	// Rows are sorted by PROJECT (legacy, monitoring, webapp); move onto webapp.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Type(":backups")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Backups", "webapp-20260101-120000.tar.gz")
	tm.Quit()
}

// TestDemo_ComposeConfig runs :config and checks the merged config opens in the
// detail viewer.
func TestDemo_ComposeConfig(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	tm.Type(":config")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "services:", "nginx:1.25")
	tm.Quit()
}

// TestDemo_ComposeEdit opens the compose-file editor, then saves with Ctrl+S
// (valid YAML) and returns to the project list.
func TestDemo_ComposeEdit(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	// Rows are sorted by PROJECT (legacy, monitoring, webapp); move onto webapp.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Type(":edit")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "image: nginx:1.25") // editor loaded the file

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlS})
	waitFor(t, tm, "monitoring") // saved → back to the compose list
	tm.Quit()
}

// TestDemo_ComposeDrillIntoContainers checks Enter opens the selected project's
// containers and Esc returns to the project list.
func TestDemo_ComposeDrillIntoContainers(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp", "monitoring")

	// Enter drills into the selected project's (webapp) containers.
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "api", "postgres:16")

	// Esc pops back to the compose project list.
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	waitFor(t, tm, "monitoring", "/srv/webapp")

	tm.Quit()
}

// TestDemo_ComposeDetail opens the project detail view (i) and checks it shows
// the project's services and images.
func TestDemo_ComposeDetail(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	// Rows are sorted by PROJECT (legacy, monitoring, webapp); move onto webapp.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	waitFor(t, tm, "project: webapp", "nginx:1.25")
	tm.Quit()
}

// TestDemo_ComposeLogs opens aggregated project logs (l) and checks lines are
// streamed with a service prefix.
func TestDemo_ComposeLogs(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web")

	tm.Type(":compose")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "webapp")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	waitFor(t, tm, "web |", "server started")
	tm.Quit()
}

// TestDemo_HostsAddViaForm switches to the hosts view, opens the add form,
// types a name (keeping the default host), saves, and checks the host is listed
// and persisted.
func TestDemo_HostsAddViaForm(t *testing.T) {
	store, err := hosts.Load(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	tm := newTestModelStore(t, store)
	waitFor(t, tm, "web")

	tm.Type(":hosts")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Hosts")

	tm.Type(":add")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Add host") // modal opened, pre-filled

	tm.Type("prod") // into the focused Name field; Host keeps its default
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// One condition: the saved host row plus its probed STATUS cell ("up" —
	// probes are stubbed in demo mode) must appear in the same frame.
	waitFor(t, tm, "prod", "ssh://user@host", "● up")

	tm.Quit()

	if h, ok := store.Find("prod"); !ok || h.Host != "ssh://user@host" {
		t.Errorf("host not persisted correctly: %+v ok=%v", h, ok)
	}
}

// TestDemo_HostsDashboard switches to the unified hosts view (which now folds in
// the dashboard) and checks the saved host is listed with its aggregated daemon
// summary — STATUS, plus the demo daemon version (27.4.0). The :dashboard alias
// opens the same view. Counts come from the stubbed summarizer in demo mode, so
// no network is touched.
func TestDemo_HostsDashboard(t *testing.T) {
	store, err := hosts.Load(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Add("prod", "ssh://user@example")

	tm := newTestModelStore(t, store)
	waitFor(t, tm, "web")

	tm.Type(":dashboard") // alias for :hosts
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// The Hosts header, the host row, its "up" status and the demo daemon
	// version (27.4.0) must appear once the summary batch lands.
	waitFor(t, tm, "Hosts", "prod", "● up", "27.4.0")

	tm.Quit()
}

// TestDemo_HostsAddViaKey opens the add form with the `a` key (host management
// is first-class in the merged hosts/dashboard view), saves a host and checks it
// is listed and persisted.
func TestDemo_HostsAddViaKey(t *testing.T) {
	store, err := hosts.Load(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	tm := newTestModelStore(t, store)
	waitFor(t, tm, "web")

	tm.Type(":hosts")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Hosts")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // open add form
	waitFor(t, tm, "Add host")

	tm.Type("prod") // Name; Host keeps its default
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "prod", "ssh://user@host")

	tm.Quit()

	if _, ok := store.Find("prod"); !ok {
		t.Error("host added via key not persisted")
	}
}

// TestDemo_HostsDeleteViaKey removes the selected host with the `d` key and the
// confirmation overlay, then checks it is gone and not persisted.
func TestDemo_HostsDeleteViaKey(t *testing.T) {
	store, err := hosts.Load(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Add("prod", "ssh://user@example")

	tm := newTestModelStore(t, store)
	waitFor(t, tm, "web")

	tm.Type(":hosts")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "prod")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}) // arm delete
	waitFor(t, tm, "Удалить хост prod")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}) // confirm
	waitFor(t, tm, "Hosts")

	tm.Quit()

	if _, ok := store.Find("prod"); ok {
		t.Error("host deleted via key should not be persisted")
	}
}

// TestDemo_HostsEditViaForm opens the edit form for the selected host (pre-filled),
// appends to the name, saves, and verifies the rename persisted.
func TestDemo_HostsEditViaForm(t *testing.T) {
	store, err := hosts.Load(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Add("prod", "ssh://user@example")

	tm := newTestModelStore(t, store)
	waitFor(t, tm, "web")

	tm.Type(":hosts")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "prod")

	tm.Type(":edit")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "Edit host", "prod") // form pre-filled with the selected host

	tm.Type("-eu") // appended to the pre-filled Name (cursor at end)
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "prod-eu")

	tm.Quit()

	if _, ok := store.Find("prod-eu"); !ok {
		t.Error("rename was not persisted")
	}
	if _, ok := store.Find("prod"); ok {
		t.Error("old name should be gone after edit")
	}
}
