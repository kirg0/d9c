package ui

import (
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"

	tea "github.com/charmbracelet/bubbletea"
)

// modelHarness drives a demo-backed model with a laid-out window: step feeds a
// message, cur reads the current model and set replaces it.
type modelHarness struct {
	tm tea.Model
	fb *docker.FakeBackend
}

func newSizedModel(t *testing.T) *modelHarness {
	t.Helper()
	fb := docker.NewFakeBackend()
	h := &modelHarness{tm: NewModel(&config.Config{}, fb, nil, nil, false), fb: fb}
	h.step(tea.WindowSizeMsg{Width: 120, Height: 30})
	return h
}

func (h *modelHarness) step(msg tea.Msg) { h.tm, _ = h.tm.Update(msg) }
func (h *modelHarness) cur() Model       { return h.tm.(Model) }
func (h *modelHarness) set(m Model)      { h.tm = m }

func TestBuildCopyItemsPerResource(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	step := func(msg tea.Msg) { tm, _ = tm.Update(msg) }
	step(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Containers (with stats present for the selected one).
	step(containersUpdatedMsg{fb.Containers})
	stats, _ := fb.ContainerStats([]string{"9ae942fd8fbc"})
	step(statsUpdatedMsg{stats: stats})
	m := tm.(Model)
	items := m.buildCopyItems()
	if len(items) < 6 {
		t.Fatalf("container copy items = %d, want name/image/status/id/health/ports(+stats)", len(items))
	}
	labels := map[string]bool{}
	for _, it := range items {
		labels[it.Label] = true
	}
	for _, want := range []string{"Name", "Image", "Status", "ID", "Health", "Ports", "CPU", "Memory"} {
		if !labels[want] {
			t.Errorf("container items missing %q: %+v", want, items)
		}
	}

	// Images.
	m.resource = ViewImages
	m.images = fb.Images
	m.refreshTableRows()
	if items := m.buildCopyItems(); len(items) != 3 {
		t.Errorf("image copy items = %+v, want 3", items)
	}

	// Networks (bridge has a subnet).
	m.resource = ViewNetworks
	m.networks = fb.Networks
	m.refreshTableRows()
	if items := m.buildCopyItems(); len(items) != 4 {
		t.Errorf("network copy items = %+v, want 4 incl. subnet", items)
	}

	// Volumes.
	m.resource = ViewVolumes
	m.volumes = fb.Volumes
	m.refreshTableRows()
	if items := m.buildCopyItems(); len(items) != 3 {
		t.Errorf("volume copy items = %+v, want 3", items)
	}

	// Hosts (with a reachable summary).
	m.resource = ViewHosts
	m.hosts = []hosts.Host{{Name: "lab", Host: "ssh://me@lab"}}
	m.summaries = map[string]docker.HostSummary{
		"ssh://me@lab": {Reachable: true, Version: "27.0", Containers: 3, Running: 2, Images: 4},
	}
	m.refreshTableRows()
	if items := m.buildCopyItems(); len(items) != 5 {
		t.Errorf("host copy items = %+v, want 5 incl. summary", items)
	}

	// Compose.
	m.resource = ViewCompose
	m.composes = fb.Composes
	m.refreshTableRows()
	if items := m.buildCopyItems(); len(items) < 5 {
		t.Errorf("compose copy items = %+v, want project/name/path/status/command", items)
	}
}

func TestCopyModeFlow(t *testing.T) {
	h := newSizedModel(t)
	h.step(containersUpdatedMsg{h.fb.Containers})

	// 'y' opens the copy menu.
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m := h.cur(); m.mode != ModeCopy {
		t.Fatalf("mode = %v, want ModeCopy", m.mode)
	}

	// Navigation clamps at both ends.
	h.step(tea.KeyMsg{Type: tea.KeyUp})
	if m := h.cur(); m.copyCursor != 0 {
		t.Errorf("cursor above top = %d", m.copyCursor)
	}
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m := h.cur(); m.copyCursor != 0 {
		t.Errorf("cursor after j/k = %d, want 0", m.copyCursor)
	}

	// The overlay renders the items.
	if v := h.cur().View(); !strings.Contains(v, "Copy to clipboard") || !strings.Contains(v, "Name") {
		t.Error("copy overlay should render title and items")
	}

	// esc closes without copying.
	h.step(tea.KeyMsg{Type: tea.KeyEsc})
	if m := h.cur(); m.mode != ModeNormal {
		t.Errorf("mode after esc = %v, want normal", m.mode)
	}

	// enter copies and closes (clipboard write is best-effort).
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	h.step(tea.KeyMsg{Type: tea.KeyEnter})
	if m := h.cur(); m.mode != ModeNormal || m.copyNotif == "" {
		t.Errorf("after enter: mode=%v notif=%q, want normal mode + notification", m.mode, m.copyNotif)
	}
}

func TestNoticeClosesOnAnyKey(t *testing.T) {
	h := newSizedModel(t)
	m := h.cur()
	m.mode = ModeNotice
	m.noticeTitle = "SSH host key"
	m.noticeBody = "the key changed"
	h.set(m)
	if v := h.cur().View(); !strings.Contains(v, "SSH host key") {
		t.Error("notice overlay should render the title")
	}
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = h.cur()
	if m.mode != ModeNormal || m.noticeTitle != "" || m.noticeBody != "" {
		t.Errorf("notice should close and clear: mode=%v title=%q", m.mode, m.noticeTitle)
	}
}

func TestDetailModeKeys(t *testing.T) {
	h := newSizedModel(t)
	m := h.cur()
	m.mode = ModeDetail
	m.detail.SetContent(&docker.InspectResult{Name: "web", RawYAML: "a: 1\nb: 2\n"})
	h.set(m)

	// A scroll key stays inside detail.
	h.step(tea.KeyMsg{Type: tea.KeyDown})
	if m := h.cur(); m.mode != ModeDetail {
		t.Fatalf("mode after scroll = %v, want detail", m.mode)
	}
	// 'q' exits when no search is active.
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m := h.cur(); m.mode != ModeNormal {
		t.Errorf("mode after q = %v, want normal", m.mode)
	}

	// With an active search, 'q' goes to the search input instead of exiting.
	m = h.cur()
	m.mode = ModeDetail
	m.detail.SetContent(&docker.InspectResult{Name: "web", RawYAML: "a: 1\n"})
	h.set(m)
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m := h.cur(); m.mode != ModeDetail {
		t.Errorf("mode while searching = %v, want detail", m.mode)
	}
}

func TestHelpModeKeys(t *testing.T) {
	h := newSizedModel(t)
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m := h.cur(); m.mode != ModeHelp {
		t.Fatalf("mode = %v, want help", m.mode)
	}
	// A scroll key stays in help.
	h.step(tea.KeyMsg{Type: tea.KeyDown})
	if m := h.cur(); m.mode != ModeHelp {
		t.Fatalf("mode after scroll = %v, want help", m.mode)
	}
	h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m := h.cur(); m.mode != ModeNormal {
		t.Errorf("mode after q = %v, want normal", m.mode)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("firstNonEmpty(a,b) = %q", got)
	}
	if got := firstNonEmpty("", "b"); got != "b" {
		t.Errorf("firstNonEmpty(,b) = %q", got)
	}
}
