package ui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/theme"
	"d9c/internal/ui/cmdline"
	"d9c/internal/ui/logs"
	"d9c/internal/ui/shell"
	"d9c/internal/ui/styles"
	uitbl "d9c/internal/ui/table"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseLogOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want docker.LogOptions
	}{
		{"defaults", nil, docker.LogOptions{Tail: 100}},
		{"tail", []string{"--tail", "50"}, docker.LogOptions{Tail: 50}},
		{"since", []string{"--since", "1h"}, docker.LogOptions{Tail: 100, Since: "1h"}},
		{"all flags", []string{"--tail", "10", "--since", "2h", "--until", "30m"}, docker.LogOptions{Tail: 10, Since: "2h", Until: "30m"}},
		{"bad tail kept default", []string{"--tail", "abc"}, docker.LogOptions{Tail: 100}},
		{"flag without value ignored", []string{"--since"}, docker.LogOptions{Tail: 100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLogOptions(tt.args); got != tt.want {
				t.Errorf("parseLogOptions(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"web", "web"},
		{"compose up: webapp", "compose-up--webapp"},
		{"/srv/app", "srv-app"},
		{"a/b\\c:d", "a-b-c-d"},
		{"keep.dots_and-dashes", "keep.dots_and-dashes"},
	}
	for _, tt := range tests {
		if got := sanitizeFileName(tt.in); got != tt.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSortKeyField(t *testing.T) {
	tests := []struct {
		key   string
		want  uitbl.SortField
		found bool
	}{
		{"N", uitbl.SortName, true},
		{"S", uitbl.SortStatus, true},
		{"C", uitbl.SortCPU, true},
		{"M", uitbl.SortMem, true},
		{"n", uitbl.SortNone, false}, // lowercase is not a sort key
		{"x", uitbl.SortNone, false},
	}
	for _, tt := range tests {
		got, ok := sortKeyField(tt.key)
		if ok != tt.found || got != tt.want {
			t.Errorf("sortKeyField(%q) = (%v,%v), want (%v,%v)", tt.key, got, ok, tt.want, tt.found)
		}
	}
}

// cycleSort selects a new column (CPU/MEM default to descending) and reverses
// the direction when the active column is chosen again.
func TestCycleSort(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.resource = ViewContainers

	m.cycleSort(uitbl.SortName)
	if m.sortField != uitbl.SortName || m.sortDesc {
		t.Fatalf("name: field=%v desc=%v, want SortName asc", m.sortField, m.sortDesc)
	}
	m.cycleSort(uitbl.SortName) // same column → reverse
	if m.sortField != uitbl.SortName || !m.sortDesc {
		t.Fatalf("name reverse: field=%v desc=%v, want SortName desc", m.sortField, m.sortDesc)
	}
	m.cycleSort(uitbl.SortCPU) // new column → default desc for usage
	if m.sortField != uitbl.SortCPU || !m.sortDesc {
		t.Fatalf("cpu: field=%v desc=%v, want SortCPU desc", m.sortField, m.sortDesc)
	}
	m.cycleSort(uitbl.SortCPU) // reverse → asc
	if m.sortField != uitbl.SortCPU || m.sortDesc {
		t.Fatalf("cpu reverse: field=%v desc=%v, want SortCPU asc", m.sortField, m.sortDesc)
	}
}

func TestSaveLogsViaKey(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	ch := make(chan string, 2)
	close(ch)
	step(logsOpenedMsg{ch: ch, containerID: "web"})
	step(logs.LineMsg{ContainerID: "web", Line: "hello"})
	step(logs.LineMsg{ContainerID: "web", Line: "world"})

	cmd := step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatal("expected a save command")
	}
	msg, ok := cmd().(logsSavedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("save result = %#v, want success", msg)
	}
	if !strings.HasPrefix(msg.path, "web-") || !strings.HasSuffix(msg.path, ".log") {
		t.Errorf("path = %q, want web-*.log", msg.path)
	}
	data, err := os.ReadFile(msg.path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != "hello\nworld" {
		t.Errorf("content = %q, want hello\\nworld", data)
	}
}

func TestEventsFlow(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	// :events opens the stream and switches to the events view.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	for _, r := range "events" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected an openEvents command")
	}
	// Drive the openEventsMsg then one event line.
	opened, ok := cmd().(openEventsMsg)
	if !ok {
		t.Fatalf("expected openEventsMsg, got %#v", cmd())
	}
	step(opened)
	if m := tm.(Model); m.mode != ModeEvents {
		t.Fatalf("mode = %v, want ModeEvents", m.mode)
	}
	// The panel must be sized when opened, otherwise it renders empty.
	if got := tm.(Model).eventsModel.Width(); got == 0 {
		t.Fatal("events panel width is 0 — relayout missing on open")
	}

	step(eventsLineMsg{line: "container start 9ae942fd8fbc (local)"})
	if got := tm.(Model).eventsModel.LineCount(); got != 1 {
		t.Errorf("event line count = %d, want 1", got)
	}

	// q closes the events view.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m := tm.(Model); m.mode != ModeNormal {
		t.Fatalf("mode after q = %v, want ModeNormal", m.mode)
	}
}

func TestMergeStats(t *testing.T) {
	st := func(id string, cpu float64) docker.ContainerStats {
		return docker.ContainerStats{ID: id, CPUPerc: cpu}
	}
	containers := []docker.Container{
		{ID: "aaa", State: "running"},
		{ID: "bbb", State: "running"},
		{ID: "ccc", State: "exited"},
	}
	tests := []struct {
		name       string
		old, fresh map[string]docker.ContainerStats
		wantIDs    map[string]float64 // id -> expected CPUPerc
	}{
		{
			"fresh sample wins over old",
			map[string]docker.ContainerStats{"aaa": st("aaa", 1)},
			map[string]docker.ContainerStats{"aaa": st("aaa", 9)},
			map[string]float64{"aaa": 9},
		},
		{
			"running container missing from batch keeps old figures",
			map[string]docker.ContainerStats{"aaa": st("aaa", 1), "bbb": st("bbb", 2)},
			map[string]docker.ContainerStats{"aaa": st("aaa", 9)},
			map[string]float64{"aaa": 9, "bbb": 2},
		},
		{
			"stopped container is dropped even with old figures",
			map[string]docker.ContainerStats{"ccc": st("ccc", 3)},
			nil,
			map[string]float64{},
		},
		{
			"removed container is dropped",
			map[string]docker.ContainerStats{"zzz": st("zzz", 4)},
			nil,
			map[string]float64{},
		},
		{
			"nil old map",
			nil,
			map[string]docker.ContainerStats{"bbb": st("bbb", 5)},
			map[string]float64{"bbb": 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeStats(tt.old, tt.fresh, containers)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("merged %d entries %v, want %d", len(got), got, len(tt.wantIDs))
			}
			for id, cpu := range tt.wantIDs {
				if got[id].CPUPerc != cpu {
					t.Errorf("merged[%s].CPUPerc = %v, want %v", id, got[id].CPUPerc, cpu)
				}
			}
		})
	}
}

// Leaving a compose drill-down (Esc) must return the cursor to the deployment
// it was opened from, not jump to the top of the list.
func TestComposeDrillDownRestoresSelection(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	// exec drives a command and every message it (transitively) produces.
	exec := func(c tea.Cmd) {
		for c != nil {
			c = step(c())
		}
	}
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Open the compose list and select the last deployment (legacy, /opt/legacy)
	// so a reset-to-top would be visible.
	exec(step(switchResourceMsg{ViewCompose}))
	m := tm.(Model)
	m.table.InnerTable().SetCursor(len(m.composes) - 1)
	tm = m
	if got := tm.(Model).selectedID(); got != "/opt/legacy" {
		t.Fatalf("precondition: selectedID = %q, want /opt/legacy", got)
	}

	// Drill in (Enter), then leave (Esc).
	exec(step(tea.KeyMsg{Type: tea.KeyEnter}))
	if m := tm.(Model); m.resource != ViewContainers || m.composeFilter != "/opt/legacy" {
		t.Fatalf("after Enter: resource=%v composeFilter=%q, want Containers /opt/legacy", m.resource, m.composeFilter)
	}
	exec(step(tea.KeyMsg{Type: tea.KeyEsc}))

	m = tm.(Model)
	if m.resource != ViewCompose {
		t.Fatalf("after Esc: resource = %v, want Compose", m.resource)
	}
	if got := m.selectedID(); got != "/opt/legacy" {
		t.Errorf("selection after returning = %q, want /opt/legacy (stayed on the same row)", got)
	}
	if m.composeReselect != "" {
		t.Errorf("composeReselect = %q, want cleared after applying", m.composeReselect)
	}
}

// While a stats batch is in flight, refresh ticks must not launch another one;
// the next batch starts only after the current one reports back.
func TestStatsInFlightGuard(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	cs, _ := fb.ListContainers(false)
	if cmd := step(containersUpdatedMsg{cs}); cmd == nil {
		t.Fatal("first refresh must launch a stats batch")
	}
	if cmd := step(containersUpdatedMsg{cs}); cmd != nil {
		t.Fatal("second refresh must not overlap the in-flight batch")
	}
	step(statsUpdatedMsg{}) // batch (even a failed one) reports back
	if cmd := step(containersUpdatedMsg{cs}); cmd == nil {
		t.Fatal("after the batch lands the next refresh must fetch stats again")
	}
}

// Closing the logs view (q) must release the backend stream via its stop handle.
func TestLogsStopOnClose(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	stopped := false
	step(logsOpenedMsg{ch: make(chan string), containerID: "web", stop: func() { stopped = true }})
	if m := tm.(Model); m.mode != ModeLogs {
		t.Fatalf("mode = %v, want ModeLogs", m.mode)
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !stopped {
		t.Error("q must call the stream's stop handle")
	}
	if m := tm.(Model); m.logCh != nil || m.logStop != nil {
		t.Error("logCh/logStop must be cleared after close")
	}
}

// Esc closes the logs view through the global key handler — the stream must be
// released on that path too.
func TestLogsStopOnEsc(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	stopped := false
	step(logsOpenedMsg{ch: make(chan string), containerID: "web", stop: func() { stopped = true }})
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if !stopped {
		t.Error("esc must call the stream's stop handle")
	}
	if m := tm.(Model); m.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal", m.mode)
	}
}

// The progress console (compose up/pull/down, image build/push) reuses the logs
// view, so closing it with q must release the operation stream via its stop
// handle — otherwise the SSH session / daemon request behind it leaks.
func TestProgressConsoleStopOnClose(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	stopped := false
	step(opStartedMsg{ch: make(chan string), stop: func() { stopped = true }, title: "compose up: web"})
	if m := tm.(Model); m.mode != ModeLogs {
		t.Fatalf("mode = %v, want ModeLogs", m.mode)
	}
	if m := tm.(Model); m.logStop == nil {
		t.Fatal("opStartedMsg must store the stream's stop handle")
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !stopped {
		t.Error("q must call the operation stream's stop handle")
	}
	if m := tm.(Model); m.logCh != nil || m.logStop != nil {
		t.Error("logCh/logStop must be cleared after close")
	}
}

// Esc closes the progress console through the global key handler — the operation
// stream must be released on that path too.
func TestProgressConsoleStopOnEsc(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	stopped := false
	step(opStartedMsg{ch: make(chan string), stop: func() { stopped = true }, title: "build: ."})
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if !stopped {
		t.Error("esc must call the operation stream's stop handle")
	}
	if m := tm.(Model); m.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal", m.mode)
	}
}

// Refreshing (r) and closing (esc) the events view must stop the abandoned
// subscription — each refresh used to leak one.
func TestEventsStopOnRefreshAndClose(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	stopped1, stopped2 := false, false
	step(openEventsMsg{ch: make(chan string), stop: func() { stopped1 = true }})
	if m := tm.(Model); m.mode != ModeEvents {
		t.Fatalf("mode = %v, want ModeEvents", m.mode)
	}

	// r tears down the old subscription before opening a new one.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !stopped1 {
		t.Error("r must stop the previous subscription")
	}
	step(openEventsMsg{ch: make(chan string), stop: func() { stopped2 = true }})

	// esc (global handler) closes the view and stops the live subscription.
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if !stopped2 {
		t.Error("esc must stop the active subscription")
	}
	if m := tm.(Model); m.eventCh != nil || m.eventStop != nil {
		t.Error("eventCh/eventStop must be cleared after close")
	}
}

// A live :connect that fails because the host key changed must open the
// dedicated notice modal (with a hint to clean known_hosts) instead of dumping
// the raw error into the footer.
func TestConnectHostKeyErrorOpensNotice(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	hostKeyErr := errors.New("SSH tunnel: ssh: handshake failed: knownhosts: key mismatch")
	cmd := step(connectResultMsg{host: "ssh://user@host", err: hostKeyErr})
	if cmd == nil {
		t.Fatal("connectResultMsg with host-key error must produce an openNoticeMsg cmd")
	}
	notice, ok := cmd().(openNoticeMsg)
	if !ok {
		t.Fatalf("emitted msg = %T, want openNoticeMsg", cmd())
	}
	if !strings.Contains(notice.body, "known_hosts") {
		t.Errorf("notice body missing 'known_hosts' hint:\n%s", notice.body)
	}
	if !strings.Contains(notice.body, "ssh://user@host") {
		t.Errorf("notice body missing the failing host URL:\n%s", notice.body)
	}

	// Deliver the message and verify the model enters notice mode with no
	// footer-error noise.
	step(notice)
	m := tm.(Model)
	if m.mode != ModeNotice {
		t.Errorf("mode = %v, want ModeNotice", m.mode)
	}
	if m.err != "" {
		t.Errorf("footer err = %q, want empty (notice replaces it)", m.err)
	}
	if !strings.Contains(m.View(), "known_hosts") {
		t.Errorf("view missing notice body")
	}

	// Esc closes the notice.
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if m2 := tm.(Model); m2.mode != ModeNormal || m2.noticeBody != "" {
		t.Errorf("after esc: mode=%v body=%q, want ModeNormal/empty", m2.mode, m2.noticeBody)
	}
}

// A startup connect failure caused by a changed host key seeds the same notice
// from Init, so the user sees the modal as soon as the app paints.
func TestStartupHostKeyErrorOpensNotice(t *testing.T) {
	hostKeyErr := errors.New("SSH tunnel: ssh: handshake failed: knownhosts: key mismatch")
	m := NewModel(&config.Config{Host: "ssh://user@h"}, docker.NewDisconnected(hostKeyErr), nil, hostKeyErr, true)
	if m.err != "" {
		t.Errorf("startup footer err = %q, want empty (notice replaces it)", m.err)
	}
	if m.startupNotice == nil {
		t.Fatal("expected startupNotice to be seeded")
	}
	if !strings.Contains(m.startupNotice.body, "known_hosts") {
		t.Errorf("startupNotice body missing hint:\n%s", m.startupNotice.body)
	}
}

// A live :connect that fails because the host name cannot be resolved opens the
// dedicated "host not found" notice instead of a raw footer error.
func TestConnectHostNotFoundOpensNotice(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	dnsErr := errors.New("dial tcp: lookup typo.invalid: no such host")
	cmd := step(connectResultMsg{host: "tcp://typo.invalid:2375", err: dnsErr})
	if cmd == nil {
		t.Fatal("connectResultMsg with no-such-host error must produce an openNoticeMsg cmd")
	}
	notice, ok := cmd().(openNoticeMsg)
	if !ok {
		t.Fatalf("emitted msg = %T, want openNoticeMsg", cmd())
	}
	if !strings.Contains(notice.body, "no such host") {
		t.Errorf("notice body missing 'no such host' hint:\n%s", notice.body)
	}
	if !strings.Contains(notice.body, "tcp://typo.invalid:2375") {
		t.Errorf("notice body missing the failing host URL:\n%s", notice.body)
	}

	step(notice)
	m := tm.(Model)
	if m.mode != ModeNotice {
		t.Errorf("mode = %v, want ModeNotice", m.mode)
	}
	if m.err != "" {
		t.Errorf("footer err = %q, want empty (notice replaces it)", m.err)
	}

	step(tea.KeyMsg{Type: tea.KeyEsc})
	if m2 := tm.(Model); m2.mode != ModeNormal || m2.noticeBody != "" {
		t.Errorf("after esc: mode=%v body=%q, want ModeNormal/empty", m2.mode, m2.noticeBody)
	}
}

// A startup connect failure caused by an unresolvable host seeds the same
// "host not found" notice from Init.
func TestStartupHostNotFoundOpensNotice(t *testing.T) {
	dnsErr := errors.New("dial tcp: lookup typo.invalid: no such host")
	m := NewModel(&config.Config{Host: "tcp://typo.invalid:2375"}, docker.NewDisconnected(dnsErr), nil, dnsErr, true)
	if m.err != "" {
		t.Errorf("startup footer err = %q, want empty (notice replaces it)", m.err)
	}
	if m.startupNotice == nil {
		t.Fatal("expected startupNotice to be seeded")
	}
	if !strings.Contains(m.startupNotice.body, "no such host") {
		t.Errorf("startupNotice body missing hint:\n%s", m.startupNotice.body)
	}
}

// A live :connect must release streams owned by the old backend.
func TestConnectStopsStreams(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{Host: "tcp://old:2375"}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	stopped := false
	step(logsOpenedMsg{ch: make(chan string), containerID: "web", stop: func() { stopped = true }})
	step(connectResultMsg{backend: docker.NewFakeBackend(), host: "tcp://new:2375"})
	if !stopped {
		t.Error("connect must stop streams of the old backend")
	}
}

func TestHeaderShowsSelectionCount(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	step(containersUpdatedMsg{cs})
	step(tea.KeyMsg{Type: tea.KeySpace})
	step(tea.KeyMsg{Type: tea.KeyDown})
	step(tea.KeyMsg{Type: tea.KeySpace})

	view := tm.(Model).View()
	if !strings.Contains(view, "2 selected") {
		t.Errorf("view missing '2 selected'\n%s", view)
	}
	if !strings.Contains(view, "● web") {
		t.Errorf("view missing '● web' marker")
	}
}

// TestLogsSearchEscFlow verifies esc clears an active log search before it
// closes the logs view, and that the global esc handler doesn't pre-empt it.
func TestLogsSearchEscFlow(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	ch := make(chan string)
	close(ch)
	step(logsOpenedMsg{ch: ch, containerID: "web"})
	step(logs.LineMsg{ContainerID: "web", Line: "INFO a"})
	step(logs.LineMsg{ContainerID: "web", Line: "ERROR b"})

	// Start a search and type a query.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "error" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if tm.(Model).mode != ModeLogs {
		t.Fatalf("mode = %v, want ModeLogs while searching", tm.(Model).mode)
	}

	// First esc clears the search but stays in the logs view.
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if got := tm.(Model); got.mode != ModeLogs {
		t.Errorf("after clearing search mode = %v, want ModeLogs", got.mode)
	}
	if tm.(Model).logs.HasSearch() {
		t.Error("search should be cleared after first esc")
	}

	// Second esc closes the logs view.
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if got := tm.(Model); got.mode != ModeNormal {
		t.Errorf("after second esc mode = %v, want ModeNormal", got.mode)
	}
}

func TestCreateComposeViaEditor(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	var lastCmd tea.Cmd
	step := func(msg tea.Msg) { tm, lastCmd = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Open the create editor for a new directory.
	step(openComposeCreateMsg{dir: "/srv/newapp"})
	mdl := tm.(Model)
	if mdl.mode != ModeComposeEdit || !mdl.composeEdit.IsCreate() {
		t.Fatalf("editor not in create mode (mode=%v, create=%v)", mdl.mode, mdl.composeEdit.IsCreate())
	}

	// Ctrl+S validates, writes the file, and brings the project up.
	step(tea.KeyMsg{Type: tea.KeyCtrlS})
	if lastCmd == nil {
		t.Fatal("expected a create command from ctrl+s")
	}
	msg := lastCmd()
	if op, ok := msg.(opStartedMsg); !ok {
		t.Fatalf("ctrl+s msg = %#v, want opStartedMsg", msg)
	} else if !strings.Contains(op.title, "/srv/newapp") {
		t.Errorf("op title = %q, want it to mention the dir", op.title)
	}

	// The new project is now known to the backend.
	projects, _ := fb.ListComposeProjects()
	found := false
	for _, p := range projects {
		if p.Name == "newapp" && p.WorkingDir == "/srv/newapp" {
			found = true
		}
	}
	if !found {
		t.Errorf("project 'newapp' not created; have %+v", projects)
	}
}

func TestBackupComposeCmd(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	fb := docker.NewFakeBackend()
	msg, ok := backupComposeCmd(fb, "/srv/webapp")().(composeBackupMsg)
	if !ok || msg.err != nil {
		t.Fatalf("backup result = %#v, want success", msg)
	}
	if !strings.HasPrefix(msg.path, "webapp-") || !strings.HasSuffix(msg.path, ".tar.gz") {
		t.Errorf("path = %q, want webapp-*.tar.gz", msg.path)
	}
	if _, err := os.Stat(msg.path); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
}

// TestRestoreComposeCmd checks the restore command streams an operation console
// (opStartedMsg) for an existing backup file.
func TestRestoreComposeCmd(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, "webapp.tar.gz")
	if err := os.WriteFile(backup, []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	fb := docker.NewFakeBackend()
	msg := restoreComposeCmd(fb, "/srv/webapp", "webapp", backup)()
	op, ok := msg.(opStartedMsg)
	if !ok {
		t.Fatalf("restore msg = %#v, want opStartedMsg", msg)
	}
	if !strings.Contains(op.title, "webapp") {
		t.Errorf("op title = %q, want it to mention the project", op.title)
	}
}

// TestExecDispatchRunning checks :exec on a running container yields an execMsg
// carrying a ready session.
func TestExecDispatchRunning(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	step(containersUpdatedMsg{cs})

	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "exec"})
	if err != nil {
		t.Fatalf("dispatch exec: unexpected err %v", err)
	}
	if cmd == nil {
		t.Fatal("dispatch exec returned nil cmd")
	}
	op, ok := cmd().(execMsg)
	if !ok {
		t.Fatalf("exec cmd msg = %#v, want execMsg", cmd())
	}
	if op.session == nil {
		t.Error("execMsg.session is nil")
	}
}

// TestExecDispatchNotRunning checks :exec is refused for a non-running container.
func TestExecDispatchNotRunning(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(containersUpdatedMsg{fb.Containers}) // all states, incl. exited "db"
	step(tea.KeyMsg{Type: tea.KeyDown})
	step(tea.KeyMsg{Type: tea.KeyDown}) // cursor on the exited container

	m := tm.(Model)
	if got := m.selectedID(); got != "3f1ab77c9012" {
		t.Fatalf("selected = %q, want exited db id", got)
	}
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "exec"}); err == nil ||
		!strings.Contains(err.Error(), "not running") {
		t.Errorf("dispatch exec on exited: err = %v, want 'not running'", err)
	}
}

// TestExecKeyShortcut verifies the 'x' key starts an exec on a running container.
func TestExecKeyShortcut(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	var lastCmd tea.Cmd
	step := func(msg tea.Msg) { tm, lastCmd = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	step(containersUpdatedMsg{cs})

	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if lastCmd == nil {
		t.Fatal("'x' produced no command")
	}
	if _, ok := lastCmd().(execMsg); !ok {
		t.Errorf("'x' msg = %#v, want execMsg", lastCmd())
	}
}

// TestShellModeFlow exercises the embedded terminal end to end: 'x' opens the
// panel, output renders inside it with the app chrome intact, and once the
// session closes 'q' returns to the table.
func TestShellModeFlow(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{Host: "tcp://h:2375"}, fb, nil, nil, false)
	var lastCmd tea.Cmd
	step := func(msg tea.Msg) { tm, lastCmd = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	step(containersUpdatedMsg{cs})

	// 'x' yields an execCmd; running it opens the session.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	step(lastCmd()) // execMsg → enters ModeShell

	m := tm.(Model)
	if m.mode != ModeShell {
		t.Fatalf("mode = %v, want ModeShell", m.mode)
	}
	if !strings.Contains(m.View(), "shell:") {
		t.Errorf("header missing shell breadcrumb:\n%s", m.View())
	}

	// Output is rendered inside the panel.
	step(shell.OutputMsg{Data: []byte("hello-from-shell")})
	if !strings.Contains(tm.(Model).View(), "hello-from-shell") {
		t.Errorf("view missing shell output:\n%s", tm.(Model).View())
	}

	// A printable key is forwarded to the session, not handled by the app.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if tm.(Model).mode != ModeShell {
		t.Error("'q' while shell live should be forwarded, not quit")
	}

	// Once the session ends, 'q' closes the panel and returns to the table.
	step(shell.ClosedMsg{Err: io.EOF})
	if !tm.(Model).shell.Closed() {
		t.Fatal("shell not marked closed after ClosedMsg")
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if tm.(Model).mode != ModeNormal {
		t.Errorf("mode after closing shell = %v, want ModeNormal", tm.(Model).mode)
	}
}

// TestExecDoneError surfaces a failed session in the footer error.
func TestExecDoneError(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	tm, _ = tm.Update(execDoneMsg{err: errors.New("boom")})
	if m := tm.(Model); !strings.Contains(m.err, "boom") {
		t.Errorf("err = %q, want it to contain 'boom'", m.err)
	}
}

// TestEscClearsSelection verifies Esc drops a pending bulk selection.
func TestEscClearsSelection(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	step(containersUpdatedMsg{cs})

	step(tea.KeyMsg{Type: tea.KeySpace})
	step(tea.KeyMsg{Type: tea.KeyDown})
	step(tea.KeyMsg{Type: tea.KeySpace})
	if len(tm.(Model).selected) != 2 {
		t.Fatalf("selected = %d, want 2", len(tm.(Model).selected))
	}
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if len(tm.(Model).selected) != 0 {
		t.Errorf("after esc selected = %d, want 0", len(tm.(Model).selected))
	}
}

// TestBulkStopViaKeys drives the full keystroke path — Space to multi-select two
// containers, then :stop — through the Update loop and asserts both containers
// are actually stopped in the backend.
func TestBulkStopViaKeys(t *testing.T) {
	fb := docker.NewFakeBackend()
	m := NewModel(&config.Config{}, fb, nil, nil, false)
	var tm tea.Model = m
	run := func(msg tea.Msg) {
		var cmd tea.Cmd
		tm, cmd = tm.Update(msg)
		// Drain the command chain synchronously (depth-limited) so actions apply.
		for i := 0; cmd != nil && i < 10; i++ {
			msg := cmd()
			if msg == nil {
				break
			}
			tm, cmd = tm.Update(msg)
		}
	}
	run(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	run(containersUpdatedMsg{cs})

	run(tea.KeyMsg{Type: tea.KeySpace})
	run(tea.KeyMsg{Type: tea.KeyDown})
	run(tea.KeyMsg{Type: tea.KeySpace})
	if n := len(tm.(Model).selected); n != 2 {
		t.Fatalf("selected = %d, want 2", n)
	}

	run(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	for _, r := range "stop" {
		run(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	run(tea.KeyMsg{Type: tea.KeyEnter})

	cs, _ = fb.ListContainers(true)
	for _, c := range cs {
		if (c.ID == "9ae942fd8fbc" || c.ID == "d2c94e258dcb") && c.State != "exited" {
			t.Errorf("%s state = %q, want exited", c.Name, c.State)
		}
	}
}

func TestDispatchBulkStop(t *testing.T) {
	fb := docker.NewFakeBackend()
	m := Model{
		backend:  fb,
		resource: ViewContainers,
		selected: map[string]bool{"9ae942fd8fbc": true, "d2c94e258dcb": true},
	}
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "stop"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	msg, ok := cmd().(actionResultMsg)
	if !ok || msg.err != nil {
		t.Fatalf("bulk stop result = %#v, want success", msg)
	}
	cs, _ := fb.ListContainers(true)
	for _, c := range cs {
		if (c.ID == "9ae942fd8fbc" || c.ID == "d2c94e258dcb") && c.State != "exited" {
			t.Errorf("%s state = %q, want exited", c.Name, c.State)
		}
	}
}

// imagesModel returns a Model switched to the Images view with the table
// populated from the fake backend (cursor on the first image).
func imagesModel(t *testing.T) (tea.Model, *docker.FakeBackend) {
	t.Helper()
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{ViewImages})
	step(imagesUpdatedMsg{fb.Images})
	return tm, fb
}

func TestDispatchImageBuild(t *testing.T) {
	tm, _ := imagesModel(t)
	m := tm.(Model)

	// build with no args errors.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "build"}); err == nil {
		t.Error("build with no dir should error")
	}

	// build <dir> opens the streaming operation console.
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "build", Args: []string{"/ctx", "myapp:1"}})
	if err != nil {
		t.Fatalf("dispatch build: %v", err)
	}
	op, ok := cmd().(opStartedMsg)
	if !ok {
		t.Fatalf("build msg = %#v, want opStartedMsg", cmd())
	}
	if !strings.Contains(op.title, "build: /ctx") {
		t.Errorf("title = %q, want it to contain 'build: /ctx'", op.title)
	}
}

func TestDispatchImageTag(t *testing.T) {
	tm, fb := imagesModel(t)
	m := tm.(Model)

	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "tag"}); err == nil {
		t.Error("tag with no target should error")
	}

	before := len(fb.Images)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "tag", Args: []string{"nginx:copy"}})
	if err != nil {
		t.Fatalf("dispatch tag: %v", err)
	}
	res, ok := cmd().(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("tag result = %#v, want success", res)
	}
	if len(fb.Images) != before+1 {
		t.Errorf("image count = %d, want %d", len(fb.Images), before+1)
	}
}

// :push opens the credentials modal rather than pushing immediately.
func TestDispatchImagePushOpensForm(t *testing.T) {
	tm, _ := imagesModel(t)
	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "push"})
	if err != nil {
		t.Fatalf("dispatch push: %v", err)
	}
	if _, ok := cmd().(openPushFormMsg); !ok {
		t.Fatalf("push msg = %#v, want openPushFormMsg", cmd())
	}
}

// Submitting the push form pushes with the entered credentials and remembers
// them for the session.
func TestPushFormSubmit(t *testing.T) {
	tm, _ := imagesModel(t)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }

	// Open the form for a private-registry ref.
	step(openPushFormMsg{ref: "myreg:5000/app:1"})
	if m := tm.(Model); m.mode != ModePushForm {
		t.Fatalf("mode = %v, want ModePushForm", m.mode)
	}
	// The registry should be pre-inferred from the ref.
	if got := tm.(Model).pushForm.Registry(); got != "myreg:5000" {
		t.Errorf("inferred registry = %q, want myreg:5000", got)
	}

	// Type a username (focus starts on registry; tab twice would reach password —
	// instead drive the fields directly via the form for determinism).
	m := tm.(Model)
	m.pushForm.Open("myreg:5000/app:1", "myreg:5000", "alice", "s3cret")
	tm = m

	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if m := tm.(Model); m.mode != ModeNormal {
		t.Fatalf("mode after submit = %v, want ModeNormal", m.mode)
	}
	// Credentials remembered for the session.
	if got := tm.(Model).pushAuth["myreg:5000"]; got.Username != "alice" || got.Password != "s3cret" {
		t.Errorf("remembered auth = %+v, want alice/s3cret", got)
	}
	// The submit returns a streaming push op.
	if cmd == nil {
		t.Fatal("expected a push command")
	}
	if _, ok := cmd().(opStartedMsg); !ok {
		t.Fatalf("submit msg = %#v, want opStartedMsg", cmd())
	}
}

// :create in the networks view opens the create-network modal.
func TestDispatchNetworkCreateOpensForm(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{ViewNetworks})

	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "create"})
	if err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, ok := cmd().(openNetFormMsg); !ok {
		t.Fatalf("create msg = %#v, want openNetFormMsg", cmd())
	}
}

// Submitting the network form creates the network and returns to the list.
func TestNetFormSubmit(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{ViewNetworks})

	step(openNetFormMsg{})
	if m := tm.(Model); m.mode != ModeNetForm {
		t.Fatalf("mode = %v, want ModeNetForm", m.mode)
	}

	// Type a name into the focused (first) field, then submit.
	for _, r := range "app-tier" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	before := len(fb.Networks)
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a create command")
	}
	res, ok := cmd().(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("create result = %#v, want success", res)
	}
	if len(fb.Networks) != before+1 {
		t.Errorf("network count = %d, want %d", len(fb.Networks), before+1)
	}
	if got := fb.Networks[len(fb.Networks)-1].Name; got != "app-tier" {
		t.Errorf("created network name = %q, want app-tier", got)
	}
	// The successful action result returns the model to the table.
	step(res)
	if m := tm.(Model); m.mode != ModeNormal {
		t.Errorf("mode after success = %v, want ModeNormal", m.mode)
	}
}

// A backend create failure (duplicate name) keeps the form open so the user
// can correct the input; the error is shown inside the form, not the footer.
func TestNetFormBackendErrorStaysOpen(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{ViewNetworks})
	step(openNetFormMsg{})

	for _, r := range "bridge" { // duplicate of a seeded demo network
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a create command")
	}
	res, ok := cmd().(actionResultMsg)
	if !ok || res.err == nil {
		t.Fatalf("create result = %#v, want duplicate-name error", res)
	}
	step(res)
	m := tm.(Model)
	if m.mode != ModeNetForm {
		t.Errorf("mode = %v, want ModeNetForm (form stays open)", m.mode)
	}
	if m.err != "" {
		t.Errorf("footer err = %q, want empty (error belongs to the form)", m.err)
	}
	if view := m.View(); !strings.Contains(view, "already exists") {
		t.Error("form view should show the backend error")
	}
}

// An empty name keeps the network form open with an inline error.
func TestNetFormRejectsEmptyName(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{ViewNetworks})
	step(openNetFormMsg{})

	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected no command for empty name, got %#v", cmd())
	}
	if m := tm.(Model); m.mode != ModeNetForm {
		t.Errorf("mode = %v, want ModeNetForm (form stays open)", m.mode)
	}
}

// :create in the volumes view opens the create-volume modal and submitting it
// creates the volume.
func TestVolFormSubmit(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{ViewVolumes})

	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "create"})
	if err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, ok := cmd().(openVolFormMsg); !ok {
		t.Fatalf("create msg = %#v, want openVolFormMsg", cmd())
	}

	step(openVolFormMsg{})
	if m := tm.(Model); m.mode != ModeVolForm {
		t.Fatalf("mode = %v, want ModeVolForm", m.mode)
	}
	for _, r := range "scratch" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	before := len(fb.Volumes)
	cmd = step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a create command")
	}
	res, ok := cmd().(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("create result = %#v, want success", res)
	}
	if len(fb.Volumes) != before+1 {
		t.Errorf("volume count = %d, want %d", len(fb.Volumes), before+1)
	}
	if got := fb.Volumes[len(fb.Volumes)-1].Name; got != "scratch" {
		t.Errorf("created volume name = %q, want scratch", got)
	}
}

func TestSplitList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"spaces only", "  ,  , ", nil},
		{"single", "8080:80", []string{"8080:80"}},
		{"multiple with spaces", " 8080:80 , 9443:443/udp ", []string{"8080:80", "9443:443/udp"}},
		{"env pairs", "KEY=value, OTHER=x", []string{"KEY=value", "OTHER=x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitList(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitList(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("item %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// :run in the containers view opens the wizard; in other views (except images)
// it is rejected.
func TestDispatchRunOpensForm(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "run"})
	if err != nil {
		t.Fatalf("dispatch run: %v", err)
	}
	if _, ok := cmd().(openRunFormMsg); !ok {
		t.Fatalf("run msg = %#v, want openRunFormMsg", cmd())
	}

	step(switchResourceMsg{ViewNetworks})
	m = tm.(Model)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "run"}); err == nil {
		t.Error("run in networks view should error")
	}
}

// :run in the images view opens the wizard pre-filled with the selected image.
func TestDispatchRunFromImagesPrefills(t *testing.T) {
	tm, _ := imagesModel(t)
	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "run"})
	if err != nil {
		t.Fatalf("dispatch run: %v", err)
	}
	msg, ok := cmd().(openRunFormMsg)
	if !ok {
		t.Fatalf("run msg = %#v, want openRunFormMsg", cmd())
	}
	if msg.image != "nginx:1.25" {
		t.Errorf("pre-filled image = %q, want nginx:1.25", msg.image)
	}
}

// Submitting the run wizard creates and starts a container from the form data.
func TestRunFormSubmit(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	step(openRunFormMsg{image: "nginx:1.25"})
	if m := tm.(Model); m.mode != ModeRunForm {
		t.Fatalf("mode = %v, want ModeRunForm", m.mode)
	}
	// Image is pre-filled; tab to Name and type one.
	step(tea.KeyMsg{Type: tea.KeyTab})
	for _, r := range "wizard-app" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	before := len(fb.Containers)
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a run command")
	}
	res, ok := cmd().(actionResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("run result = %#v, want success", res)
	}
	if len(fb.Containers) != before+1 {
		t.Fatalf("container count = %d, want %d", len(fb.Containers), before+1)
	}
	got := fb.Containers[len(fb.Containers)-1]
	if got.Name != "wizard-app" || got.Image != "nginx:1.25" {
		t.Errorf("created = %+v, want wizard-app/nginx:1.25", got)
	}
	step(res)
	if m := tm.(Model); m.mode != ModeNormal {
		t.Errorf("mode after success = %v, want ModeNormal", m.mode)
	}
}

// A backend failure (missing image) keeps the wizard open with the error inside.
func TestRunFormBackendErrorStaysOpen(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	step(openRunFormMsg{image: "ghost:0.0"})
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a run command")
	}
	res, ok := cmd().(actionResultMsg)
	if !ok || res.err == nil {
		t.Fatalf("run result = %#v, want missing-image error", res)
	}
	step(res)
	m := tm.(Model)
	if m.mode != ModeRunForm {
		t.Errorf("mode = %v, want ModeRunForm (form stays open)", m.mode)
	}
	if m.err != "" {
		t.Errorf("footer err = %q, want empty (error belongs to the form)", m.err)
	}
}

// :exec in the images view opens the one-off run wizard pre-filled with the
// selected image (in the containers view :exec keeps its attach semantics).
func TestDispatchExecFromImagesOpensForm(t *testing.T) {
	tm, _ := imagesModel(t)
	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "exec"})
	if err != nil {
		t.Fatalf("dispatch exec: %v", err)
	}
	msg, ok := cmd().(openExecFormMsg)
	if !ok {
		t.Fatalf("exec msg = %#v, want openExecFormMsg", cmd())
	}
	if msg.image != "nginx:1.25" {
		t.Errorf("pre-filled image = %q, want nginx:1.25", msg.image)
	}
}

// Submitting the exec wizard starts a one-off session and opens the embedded
// terminal panel.
func TestExecFormSubmitOpensShell(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	step(openExecFormMsg{image: "nginx:1.25"})
	if m := tm.(Model); m.mode != ModeExecForm {
		t.Fatalf("mode = %v, want ModeExecForm", m.mode)
	}
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a run-interactive command")
	}
	msg, ok := cmd().(execMsg)
	if !ok {
		t.Fatalf("submit msg = %#v, want execMsg", cmd())
	}
	if !strings.Contains(msg.title, "nginx:1.25") {
		t.Errorf("session title = %q, want it to mention the image", msg.title)
	}
	step(msg)
	if m := tm.(Model); m.mode != ModeShell {
		t.Errorf("mode after execMsg = %v, want ModeShell", m.mode)
	}
}

// A failed one-off run (missing image) keeps the wizard open with the error
// shown inside the form, not in the footer.
func TestExecFormBackendErrorStaysOpen(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	step(openExecFormMsg{image: "ghost:0.0"})
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a run-interactive command")
	}
	res, ok := cmd().(errMsg)
	if !ok || res.err == nil {
		t.Fatalf("submit msg = %#v, want errMsg", cmd())
	}
	step(res)
	m := tm.(Model)
	if m.mode != ModeExecForm {
		t.Errorf("mode = %v, want ModeExecForm (form stays open)", m.mode)
	}
	if m.err != "" {
		t.Errorf("footer err = %q, want empty (error belongs to the form)", m.err)
	}
	if view := m.View(); !strings.Contains(view, "pull") {
		t.Error("form view should show the missing-image hint")
	}

	// An empty image is rejected locally without calling the backend.
	step(openExecFormMsg{})
	if cmd := step(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatalf("expected no command for empty image, got %#v", cmd())
	}
	if m := tm.(Model); m.mode != ModeExecForm {
		t.Errorf("mode = %v, want ModeExecForm", m.mode)
	}
}

// :system df opens the disk-usage report in the detail viewer from any view.
func TestDispatchSystemDF(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	step(switchResourceMsg{ViewVolumes}) // works outside containers too

	m := tm.(Model)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "system"}); err == nil {
		t.Error("system without args should error with usage")
	}
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "system", Args: []string{"df"}})
	if err != nil {
		t.Fatalf("dispatch system df: %v", err)
	}
	msg, ok := cmd().(showDetailMsg)
	if !ok {
		t.Fatalf("df msg = %#v, want showDetailMsg", cmd())
	}
	if !strings.Contains(msg.result.RawYAML, "RECLAIMABLE") {
		t.Error("df detail should contain the usage table")
	}
}

// :theme switches the color scheme on the fly: a known name re-themes styles
// and notifies the footer; an unknown name errors and leaves styles untouched.
func TestThemeCommand(t *testing.T) {
	t.Cleanup(func() { styles.Apply(styles.DefaultPalette()) })

	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m := tm.(Model)

	// no args → usage error, styles unchanged
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "theme"}); err == nil {
		t.Error("theme without args should error with usage")
	}

	// unknown name → error, styles unchanged
	before := styles.Active()
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "theme", Args: []string{"monokai"}}); err == nil {
		t.Error("unknown theme should error")
	}
	if styles.Active() != before {
		t.Error("failed theme switch must not change active palette")
	}

	// known name → palette applied, footer notified
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "theme", Args: []string{"Dracula"}})
	if err != nil {
		t.Fatalf("dispatch theme dracula: %v", err)
	}
	want, _ := theme.ByName("dracula")
	if styles.Active() != want {
		t.Error("active palette should be dracula after switch")
	}
	if m.copyNotif != "тема: dracula" {
		t.Errorf("copyNotif = %q, want %q", m.copyNotif, "тема: dracula")
	}
	if cmd == nil {
		t.Error("theme switch should return a clear-notif command")
	}
}

// theme is recognised as a builtin command in every view (it is global).
func TestThemeIsBuiltinEverywhere(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, res := range []ResourceView{ViewContainers, ViewImages, ViewHosts, ViewCompose} {
		tm, _ = tm.Update(switchResourceMsg{res})
		if !tm.(Model).cmdline.IsBuiltin("theme") {
			t.Errorf("theme should be builtin in %v view", res)
		}
	}
}

// :system prune asks for confirmation; y runs the prune (demo data shrinks)
// and the summary lands in the footer notification.
func TestSystemPruneConfirmFlow(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "system", Args: []string{"prune"}})
	if err != nil {
		t.Fatalf("dispatch system prune: %v", err)
	}
	confirm, ok := cmd().(openConfirmMsg)
	if !ok {
		t.Fatalf("prune msg = %#v, want openConfirmMsg", cmd())
	}
	step(confirm)
	if got := tm.(Model); got.mode != ModeConfirm {
		t.Fatalf("mode = %v, want ModeConfirm", got.mode)
	}
	if view := tm.(Model).View(); !strings.Contains(view, "Подтверждение") {
		t.Error("confirm overlay should be rendered")
	}

	stoppedBefore := len(fb.Containers)
	pruneCmd := step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if pruneCmd == nil {
		t.Fatal("y must run the confirmed action")
	}
	if got := tm.(Model); got.mode != ModeNormal || got.confirmAction != nil {
		t.Error("confirm overlay must close and clear its action on y")
	}
	res, ok := pruneCmd().(systemPruneMsg)
	if !ok || res.err != nil {
		t.Fatalf("prune result = %#v, want success", res)
	}
	if len(fb.Containers) >= stoppedBefore {
		t.Error("prune should remove stopped demo containers")
	}
	step(res)
	if got := tm.(Model); !strings.Contains(got.copyNotif, "prune:") {
		t.Errorf("notification = %q, want prune summary", got.copyNotif)
	}
}

// Declining the confirmation (esc) must not run the pending action.
func TestSystemPruneConfirmCancel(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) tea.Cmd { var c tea.Cmd; tm, c = tm.Update(msg); return c }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	ran := false
	step(openConfirmMsg{prompt: "точно?", action: func() tea.Msg { ran = true; return nil }})
	if cmd := step(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		cmd()
	}
	m := tm.(Model)
	if m.mode != ModeNormal || m.confirmAction != nil {
		t.Error("esc must close the overlay and drop the action")
	}
	if ran {
		t.Error("cancelled action must not run")
	}

	// 'n' also cancels.
	step(openConfirmMsg{prompt: "точно?", action: func() tea.Msg { ran = true; return nil }})
	if cmd := step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}); cmd != nil {
		cmd()
	}
	if ran {
		t.Error("'n' must cancel the action")
	}
}

// create is rejected in views that don't support it (e.g. containers).
func TestDispatchCreateUnavailable(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	m := tm.(Model)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "create"}); err == nil {
		t.Error("create in containers view should error")
	}
}

func TestDispatchImageHistory(t *testing.T) {
	tm, _ := imagesModel(t)
	m := tm.(Model)
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "history"})
	if err != nil {
		t.Fatalf("dispatch history: %v", err)
	}
	msg, ok := cmd().(showDetailMsg)
	if !ok {
		t.Fatalf("history msg = %#v, want showDetailMsg", cmd())
	}
	if msg.result == nil || msg.result.RawYAML == "" {
		t.Error("history detail content is empty")
	}
}

func TestImageRefFromTags(t *testing.T) {
	tests := []struct {
		name string
		tags string
		id   string
		want string
	}{
		{"single tag", "nginx:latest", "abc123", "nginx:latest"},
		{"first of multiple tags", "nginx:latest, nginx:1.25", "abc123", "nginx:latest"},
		{"dangling image falls back to id", "<none>:<none>", "abc123", "abc123"},
		{"literal none falls back to id", "<none>", "abc123", "abc123"},
		{"empty tags falls back to id", "", "abc123", "abc123"},
		{"skips none, picks real tag", "<none>:<none>, app:v2", "abc123", "app:v2"},
		{"trims spaces", "  redis:7  ", "abc123", "redis:7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageRefFromTags(tt.tags, tt.id); got != tt.want {
				t.Errorf("imageRefFromTags(%q, %q) = %q, want %q", tt.tags, tt.id, got, tt.want)
			}
		})
	}
}

func TestTargetContainerIDs(t *testing.T) {
	// With a bulk selection, all selected IDs are returned (cursor ignored).
	m := Model{selected: map[string]bool{"a": true, "b": true}}
	got := m.targetContainerIDs()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("with selection = %v, want [a b]", got)
	}

	// With no selection and no table rows, there is nothing to target.
	empty := Model{}
	if ids := empty.targetContainerIDs(); len(ids) != 0 {
		t.Errorf("empty = %v, want none", ids)
	}
}

func TestBulkAction(t *testing.T) {
	t.Run("all succeed", func(t *testing.T) {
		var calls []string
		cmd := bulkAction([]string{"a", "b"}, func(id string) error {
			calls = append(calls, id)
			return nil
		})
		msg, ok := cmd().(actionResultMsg)
		if !ok || msg.err != nil {
			t.Fatalf("msg = %#v, want actionResultMsg{nil}", msg)
		}
		if len(calls) != 2 {
			t.Errorf("fn called %d times, want 2", len(calls))
		}
	})

	t.Run("partial failure aggregates", func(t *testing.T) {
		cmd := bulkAction([]string{"a", "b", "c"}, func(id string) error {
			if id == "b" {
				return errString("boom")
			}
			return nil
		})
		msg := cmd().(actionResultMsg)
		if msg.err == nil || !strings.Contains(msg.err.Error(), "1 of 3 failed") {
			t.Errorf("err = %v, want '1 of 3 failed'", msg.err)
		}
	})
}

type errString string

func (e errString) Error() string { return string(e) }
