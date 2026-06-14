package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"d9c/internal/config"
	"d9c/internal/docker"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReconnectBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second}, // clamped to attempt 1
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second}, // 32s → capped
		{100, 30 * time.Second},
	}
	for _, tt := range tests {
		if got := reconnectBackoff(tt.attempt); got != tt.want {
			t.Errorf("reconnectBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

// TestReconnectStartsOnConnectionError checks a dropped connection enters the
// reconnecting state (with a banner) instead of surfacing a footer error.
func TestReconnectStartsOnConnectionError(t *testing.T) {
	fb := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://dead:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, cmd := tm.Update(errMsg{errors.New("dial tcp: connection refused")})
	m := tm.(Model)
	if !m.reconnecting || m.reconnectAttempt != 1 {
		t.Fatalf("reconnecting=%v attempt=%d, want true/1", m.reconnecting, m.reconnectAttempt)
	}
	if cmd == nil {
		t.Error("expected a reconnect command")
	}
	if m.err != "" {
		t.Errorf("err = %q, want empty (status is in the banner)", m.err)
	}
	if !strings.Contains(m.View(), "reconnecting") {
		t.Error("header should show the reconnecting banner")
	}
}

// TestNonConnectionErrorNoReconnect keeps operational errors as plain footer
// errors.
func TestNonConnectionErrorNoReconnect(t *testing.T) {
	fb := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://h:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, _ = tm.Update(errMsg{errors.New("Error: No such container: abc")})
	m := tm.(Model)
	if m.reconnecting {
		t.Error("operational error must not start a reconnect")
	}
	if m.err == "" {
		t.Error("operational error should be shown in the footer")
	}
}

// TestReconnectRetryThenRecover walks a failed attempt (backoff escalates) and
// then a successful one (backend swapped, state cleared).
func TestReconnectRetryThenRecover(t *testing.T) {
	fb1 := docker.NewFakeBackend()
	fb2 := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://dead:2375"}
	var tm tea.Model = NewModel(cfg, fb1, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, _ = tm.Update(errMsg{errors.New("connection reset by peer")})

	tm, cmd := tm.Update(reconnectResultMsg{err: errors.New("connection refused"), attempt: 1})
	m := tm.(Model)
	if !m.reconnecting || m.reconnectAttempt != 2 {
		t.Fatalf("after failed attempt: reconnecting=%v attempt=%d, want true/2", m.reconnecting, m.reconnectAttempt)
	}
	if cmd == nil {
		t.Error("expected another retry command after a failed attempt")
	}

	tm, _ = tm.Update(reconnectResultMsg{backend: fb2, attempt: 2})
	m = tm.(Model)
	if m.reconnecting || m.reconnectAttempt != 0 {
		t.Errorf("after recovery: reconnecting=%v attempt=%d, want false/0", m.reconnecting, m.reconnectAttempt)
	}
	if m.backend != fb2 {
		t.Error("backend should be swapped to the recovered connection")
	}
}

// TestReconnectStaleResultIgnored ensures a result arriving after recovery (or a
// manual connect) doesn't clobber the live backend.
func TestReconnectStaleResultIgnored(t *testing.T) {
	fb := docker.NewFakeBackend()
	stale := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://h:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, _ = tm.Update(reconnectResultMsg{backend: stale, attempt: 1}) // not reconnecting
	m := tm.(Model)
	if m.backend != fb {
		t.Error("stale reconnect result must not replace the live backend")
	}
	if m.reconnecting {
		t.Error("should not be reconnecting")
	}
}

// TestPingResultDrivesStatusDot walks the header dot through down and back up:
// a failed operational ping paints it red without starting a reconnect or
// polluting the footer, a successful one paints it green again.
func TestPingResultDrivesStatusDot(t *testing.T) {
	fb := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://h:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	if !tm.(Model).serverUp {
		t.Fatal("a model built from a successful connect should start with serverUp")
	}
	if !strings.Contains(tm.(Model).View(), "●") {
		t.Error("header should render the status dot")
	}

	tm, cmd := tm.Update(pingResultMsg{err: errors.New("Error: something operational")})
	m := tm.(Model)
	if m.serverUp {
		t.Error("failed ping should mark the server down")
	}
	if m.reconnecting || cmd != nil {
		t.Error("non-connection ping error must not start a reconnect")
	}
	if m.err != "" {
		t.Errorf("ping errors must not reach the footer, got %q", m.err)
	}

	tm, _ = tm.Update(pingResultMsg{})
	if !tm.(Model).serverUp {
		t.Error("successful ping should mark the server up again")
	}
}

// TestPingConnectionErrorStartsReconnect checks the heartbeat noticing a dead
// connection enters the same auto-reconnect flow as a failed fetch.
func TestPingConnectionErrorStartsReconnect(t *testing.T) {
	fb := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://dead:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, cmd := tm.Update(pingResultMsg{err: errors.New("dial tcp: connection refused")})
	m := tm.(Model)
	if !m.reconnecting || m.reconnectAttempt != 1 {
		t.Fatalf("reconnecting=%v attempt=%d, want true/1", m.reconnecting, m.reconnectAttempt)
	}
	if cmd == nil {
		t.Error("expected a reconnect command")
	}
	if m.serverUp {
		t.Error("server must be marked down when its connection dropped")
	}
}

// TestStalePingResultIgnored ensures a ping that raced a host switch (stale
// backend generation) can't paint the dot for the wrong server.
func TestStalePingResultIgnored(t *testing.T) {
	fb := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://h:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Tick issues a ping for generation 0…
	tm, _ = tm.Update(tickMsg(time.Now()))
	if !tm.(Model).pingInFlight {
		t.Fatal("tick should put a ping in flight")
	}
	// …then a manual connect swaps the backend (bumps the generation).
	tm, _ = tm.Update(connectResultMsg{backend: docker.NewFakeBackend(), host: "tcp://new:2375"})
	m := tm.(Model)
	if !m.serverUp || m.pingInFlight {
		t.Fatalf("after connect: serverUp=%v pingInFlight=%v, want true/false", m.serverUp, m.pingInFlight)
	}

	tm, _ = tm.Update(pingResultMsg{seq: 0, err: errors.New("i/o timeout")})
	m = tm.(Model)
	if !m.serverUp {
		t.Error("stale ping result must not mark the new server down")
	}
	if m.reconnecting {
		t.Error("stale ping result must not start a reconnect")
	}
}

// TestTickKeepsSinglePingInFlight checks refresh ticks don't stack overlapping
// health checks while one is still running.
func TestTickKeepsSinglePingInFlight(t *testing.T) {
	fb := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://h:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, _ = tm.Update(tickMsg(time.Now()))
	tm, _ = tm.Update(tickMsg(time.Now())) // ping from the first tick still in flight
	m := tm.(Model)
	if !m.pingInFlight {
		t.Fatal("ping should still be in flight")
	}

	tm, _ = tm.Update(pingResultMsg{seq: m.pingSeq})
	if tm.(Model).pingInFlight {
		t.Error("ping result should release the in-flight guard")
	}
}

// TestTickSkipsFetchWhileReconnecting checks the heartbeat keeps ticking but
// doesn't fetch against the dead connection.
func TestTickSkipsFetchWhileReconnecting(t *testing.T) {
	fb := docker.NewFakeBackend()
	cfg := &config.Config{Host: "tcp://h:2375"}
	var tm tea.Model = NewModel(cfg, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	tm, _ = tm.Update(errMsg{errors.New("connection refused")})

	tm, cmd := tm.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Error("tick should still reschedule itself")
	}
	if !tm.(Model).reconnecting {
		t.Error("tick must not clear the reconnecting state")
	}
}
