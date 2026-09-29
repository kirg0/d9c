package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/ui/cmdline"

	tea "github.com/charmbracelet/bubbletea"
)

// goneHarness drives a Model through Update with the command chain drained
// synchronously (depth-limited), like TestBulkStopViaKeys.
type goneHarness struct {
	t  *testing.T
	tm tea.Model
	fb *docker.FakeBackend
}

func newGoneHarness(t *testing.T) *goneHarness {
	t.Helper()
	fb := docker.NewFakeBackend()
	h := &goneHarness{t: t, tm: NewModel(&config.Config{}, fb, nil, nil, false), fb: fb}
	h.run(tea.WindowSizeMsg{Width: 160, Height: 30})
	cs, _ := fb.ListContainers(false)
	h.run(containersUpdatedMsg{cs})
	return h
}

func (h *goneHarness) run(msg tea.Msg) {
	var cmd tea.Cmd
	h.tm, cmd = h.tm.Update(msg)
	for i := 0; cmd != nil && i < 10; i++ {
		next := cmd()
		if next == nil {
			break
		}
		h.tm, cmd = h.tm.Update(next)
	}
}

func (h *goneHarness) model() Model { return h.tm.(Model) }

// dispatch runs a `:` command through dispatchCommand and feeds its result back.
func (h *goneHarness) dispatch(name string, args ...string) {
	h.t.Helper()
	m := h.model()
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: name, Args: args})
	if err != nil {
		h.t.Fatalf("dispatch %s: %v", name, err)
	}
	h.run(cmd())
}

func hasContainer(cs []docker.Container, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}

func TestGoneNotice(t *testing.T) {
	tests := []struct {
		name     string
		resource ResourceView
		total    int
		gone     int
		failErr  error
		want     string
	}{
		{"single container", ViewContainers, 1, 1, nil,
			"container already removed — possibly by another user; table refreshed"},
		{"single image", ViewImages, 1, 1, nil,
			"image already removed — possibly by another user; table refreshed"},
		{"single network", ViewNetworks, 1, 1, nil,
			"network already removed — possibly by another user; table refreshed"},
		{"single volume", ViewVolumes, 1, 1, nil,
			"volume already removed — possibly by another user; table refreshed"},
		{"single compose", ViewCompose, 1, 1, nil,
			"compose project already removed — possibly by another user; table refreshed"},
		{"bulk", ViewContainers, 5, 2, nil,
			"3 done, 2 already gone (possibly removed by another user); table refreshed"},
		{"bulk all gone", ViewContainers, 2, 2, nil,
			"0 done, 2 already gone (possibly removed by another user); table refreshed"},
		{"bulk with failure", ViewContainers, 4, 1, errors.New("1 of 4 failed: boom"),
			"1 of 4 failed: boom; 1 already gone (possibly removed by another user); table refreshed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := goneNotice(tt.resource, tt.total, tt.gone, tt.failErr); got != tt.want {
				t.Errorf("goneNotice() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBulkAction_Gone(t *testing.T) {
	notFound := errString("Error response from daemon: No such container: b")

	t.Run("gone targets are not failures", func(t *testing.T) {
		cmd := bulkAction([]string{"a", "b", "c"}, func(id string) error {
			if id == "b" {
				return notFound
			}
			return nil
		})
		msg := cmd().(actionResultMsg)
		if msg.err != nil {
			t.Errorf("err = %v, want nil", msg.err)
		}
		if len(msg.gone) != 1 || msg.gone[0] != "b" || msg.total != 3 {
			t.Errorf("gone = %v total = %d, want [b] of 3", msg.gone, msg.total)
		}
	})

	t.Run("mixed gone and failure", func(t *testing.T) {
		cmd := bulkAction([]string{"a", "b", "c"}, func(id string) error {
			switch id {
			case "a":
				return errString("boom")
			case "b":
				return notFound
			}
			return nil
		})
		msg := cmd().(actionResultMsg)
		if msg.err == nil || msg.err.Error() != "1 of 3 failed: boom" {
			t.Errorf("err = %v, want '1 of 3 failed: boom'", msg.err)
		}
		if len(msg.gone) != 1 {
			t.Errorf("gone = %v, want one id", msg.gone)
		}
	})

	t.Run("single target error is unwrapped", func(t *testing.T) {
		cmd := bulkAction([]string{"a"}, func(string) error { return errString("boom") })
		msg := cmd().(actionResultMsg)
		if msg.err == nil || msg.err.Error() != "boom" {
			t.Errorf("err = %v, want plain 'boom'", msg.err)
		}
	})
}

// TestStaleContainerAction: another client removed the container under the
// cursor; :stop reports it readably and refreshes the table immediately.
func TestStaleContainerAction(t *testing.T) {
	h := newGoneHarness(t)
	id := h.model().selectedID()
	if err := h.fb.RemoveContainer(id, true); err != nil { // "another client"
		t.Fatal(err)
	}

	h.dispatch("stop")

	m := h.model()
	want := "container already removed — possibly by another user; table refreshed"
	if m.err != want {
		t.Errorf("footer = %q, want %q", m.err, want)
	}
	if hasContainer(m.containers, id) {
		t.Errorf("stale container %s still listed — table was not refreshed", id)
	}
}

// TestStaleBulkAction: one of two selected containers vanished — the other is
// still stopped, the footer summarizes, the selection is consumed.
func TestStaleBulkAction(t *testing.T) {
	h := newGoneHarness(t)
	h.run(tea.KeyMsg{Type: tea.KeySpace})
	first := h.model().selectedID()
	h.run(tea.KeyMsg{Type: tea.KeyDown})
	h.run(tea.KeyMsg{Type: tea.KeySpace})
	second := h.model().selectedID()
	if err := h.fb.RemoveContainer(first, true); err != nil {
		t.Fatal(err)
	}

	h.dispatch("stop")

	m := h.model()
	if !strings.HasPrefix(m.err, "1 done, 1 already gone") {
		t.Errorf("footer = %q, want '1 done, 1 already gone…'", m.err)
	}
	if len(m.selected) != 0 {
		t.Errorf("selection = %v, want cleared", m.selected)
	}
	cs, _ := h.fb.ListContainers(true)
	for _, c := range cs {
		if c.ID == second && c.State != "exited" {
			t.Errorf("surviving target state = %q, want exited", c.State)
		}
	}
}

// TestStaleBulkFailureKeepsOthersSelected: with a real failure the selection
// survives for a retry, minus the vanished ids.
func TestStaleBulkFailureKeepsOthersSelected(t *testing.T) {
	fb := docker.NewFakeBackend()
	m := Model{backend: fb, resource: ViewContainers, selected: map[string]bool{"gone1": true, "keep": true}}
	tm, _ := m.Update(actionResultMsg{err: errString("1 of 2 failed: boom"), gone: []string{"gone1"}, total: 2})
	got := tm.(Model)
	if got.selected["gone1"] || !got.selected["keep"] {
		t.Errorf("selection = %v, want only keep", got.selected)
	}
	if !strings.HasPrefix(got.err, "1 of 2 failed: boom; 1 already gone") {
		t.Errorf("footer = %q", got.err)
	}
}

// TestStaleNetworkRemove covers a single-target section other than containers.
func TestStaleNetworkRemove(t *testing.T) {
	h := newGoneHarness(t)
	h.run(switchResourceMsg{ViewNetworks})
	nets, _ := h.fb.ListNetworks()
	h.run(networksUpdatedMsg{nets})
	id := h.model().selectedID()
	if id == "" {
		t.Fatal("no network under the cursor")
	}
	h.fb.Networks = nil // removed by another client

	h.dispatch("rm")

	if got := h.model().err; !strings.HasPrefix(got, "network already removed") {
		t.Errorf("footer = %q, want 'network already removed…'", got)
	}
}

// TestStaleInspectAndLogs: opening details or logs of a vanished container
// takes the same readable path instead of the raw daemon error.
func TestStaleInspectAndLogs(t *testing.T) {
	fb := docker.NewFakeBackend()
	for name, cmd := range map[string]tea.Cmd{
		"inspect": fetchInspect(fb, ViewContainers, "deadbeef"),
		"logs":    openLogs(fb, "deadbeef", docker.LogOptions{}),
	} {
		msg, ok := cmd().(actionResultMsg)
		if !ok || len(msg.gone) != 1 || msg.gone[0] != "deadbeef" || msg.err != nil {
			t.Errorf("%s: msg = %#v, want gone [deadbeef]", name, msg)
		}
	}
}
