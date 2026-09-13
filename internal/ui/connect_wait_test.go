package ui

import (
	"errors"
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"

	tea "github.com/charmbracelet/bubbletea"
)

// A plain TCP host opens the connection-progress window and dials immediately.
func TestBeginConnectTCPOpensProgressWindow(t *testing.T) {
	h := hosts.Host{Name: "lab", Host: "tcp://lab:2375"}
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)

	model, cmd := m.beginConnect(h)
	got := model.(Model)
	if got.mode != ModeConnecting {
		t.Fatalf("mode = %v, want ModeConnecting", got.mode)
	}
	if !got.connWait.Busy() {
		t.Error("expected the progress window to be busy while dialing")
	}
	if cmd == nil {
		t.Fatal("expected a connect cmd")
	}
	if got.connWait.HostURL() != "tcp://lab:2375" {
		t.Errorf("host url = %q, want tcp://lab:2375", got.connWait.HostURL())
	}
}

// A failed dial keeps the progress window open with the error shown inline so
// the user can retry.
func TestConnectingErrorStaysInWindow(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	model, _ := m.beginConnect(hosts.Host{Name: "lab", Host: "tcp://lab:2375"})
	m = model.(Model)

	model, cmd := m.Update(connectResultMsg{err: errors.New("connection refused"), host: "tcp://lab:2375"})
	got := model.(Model)
	if got.mode != ModeConnecting {
		t.Fatalf("mode = %v, want ModeConnecting (stay open on failure)", got.mode)
	}
	if got.connWait.Busy() {
		t.Error("busy state should clear after a failed connect")
	}
	if got.connWait.Err() == "" {
		t.Error("expected the error to be shown inside the window")
	}
	if cmd != nil {
		t.Errorf("expected no follow-up cmd after inline error")
	}
}

// Enter after a failure retries the same host from the progress window.
func TestConnectingEnterRetries(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	model, _ := m.beginConnect(hosts.Host{Name: "lab", Host: "tcp://lab:2375"})
	m = model.(Model)
	model, _ = m.Update(connectResultMsg{err: errors.New("connection refused"), host: "tcp://lab:2375"})
	m = model.(Model)

	model, cmd := m.handleConnecting(tea.KeyMsg{Type: tea.KeyEnter})
	got := model.(Model)
	if got.mode != ModeConnecting {
		t.Fatalf("mode = %v, want ModeConnecting", got.mode)
	}
	if !got.connWait.Busy() {
		t.Error("expected the window busy again after retry")
	}
	if cmd == nil {
		t.Fatal("expected a connect cmd on retry")
	}
}

// While the dial is in flight, keys (other than the global esc) are swallowed
// so a second Enter can't fire a duplicate dial.
func TestConnectingSwallowsKeysWhileBusy(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	model, _ := m.beginConnect(hosts.Host{Name: "lab", Host: "tcp://lab:2375"})
	m = model.(Model)

	model, cmd := m.handleConnecting(tea.KeyMsg{Type: tea.KeyEnter})
	got := model.(Model)
	if cmd != nil {
		t.Error("enter while busy must not fire a duplicate dial")
	}
	if got.mode != ModeConnecting {
		t.Errorf("mode = %v, want ModeConnecting", got.mode)
	}
}

// A successful dial closes the progress window and switches to Containers.
func TestConnectingSuccessClosesWindow(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	model, _ := m.beginConnect(hosts.Host{Name: "lab", Host: "tcp://lab:2375"})
	m = model.(Model)

	model, _ = m.Update(connectResultMsg{backend: docker.NewFakeBackend(), host: "tcp://lab:2375"})
	got := model.(Model)
	if got.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal (window closed on success)", got.mode)
	}
	if got.resource != ViewContainers {
		t.Errorf("resource = %v, want ViewContainers", got.resource)
	}
	if got.cfg.Host != "tcp://lab:2375" {
		t.Errorf("cfg.Host = %q, want tcp://lab:2375", got.cfg.Host)
	}
}

// A host-key mismatch bypasses the inline error: the dedicated instructional
// notice opens instead of the progress window's retry state.
func TestConnectingHostKeyErrorOpensNotice(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	model, _ := m.beginConnect(hosts.Host{Name: "lab", Host: "ssh://me@lab"})
	m = model.(Model)

	hostKeyErr := errors.New("SSH tunnel: ssh: handshake failed: knownhosts: key mismatch")
	model, cmd := m.Update(connectResultMsg{err: hostKeyErr, host: "ssh://me@lab"})
	_ = model
	if cmd == nil {
		t.Fatal("expected an openNoticeMsg cmd for a host-key error")
	}
	if _, ok := cmd().(openNoticeMsg); !ok {
		t.Fatalf("cmd() = %T, want openNoticeMsg", cmd())
	}
}
