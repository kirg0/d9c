package ui

import (
	"testing"
	"time"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/ui/cmdline"

	tea "github.com/charmbracelet/bubbletea"
)

// TestClampInterval bounds the auto-refresh cadence into the sane range.
func TestClampInterval(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{0, minRefreshInterval},
		{100 * time.Millisecond, minRefreshInterval},
		{minRefreshInterval, minRefreshInterval},
		{5 * time.Second, 5 * time.Second},
		{maxRefreshInterval, maxRefreshInterval},
		{2 * maxRefreshInterval, maxRefreshInterval},
	}
	for _, c := range cases {
		if got := clampInterval(c.in); got != c.want {
			t.Errorf("clampInterval(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestNewModelInterval resolves the configured interval: a zero falls back to
// the default, an explicit value is clamped.
func TestNewModelInterval(t *testing.T) {
	fb := docker.NewFakeBackend()

	def := NewModel(&config.Config{}, fb, nil, nil, false)
	if def.refreshInterval != defaultRefreshInterval {
		t.Errorf("zero config interval = %v, want default %v", def.refreshInterval, defaultRefreshInterval)
	}

	tiny := NewModel(&config.Config{RefreshInterval: 50 * time.Millisecond}, fb, nil, nil, false)
	if tiny.refreshInterval != minRefreshInterval {
		t.Errorf("tiny config interval = %v, want clamped %v", tiny.refreshInterval, minRefreshInterval)
	}

	set := NewModel(&config.Config{RefreshInterval: 7 * time.Second}, fb, nil, nil, false)
	if set.refreshInterval != 7*time.Second {
		t.Errorf("config interval = %v, want 7s", set.refreshInterval)
	}
}

// TestPauseKeyToggles confirms the 'p' key flips the paused flag and that a tick
// skips the data fetch while paused but still reschedules itself.
func TestPauseKeyToggles(t *testing.T) {
	m := newKeysTestModel(t)
	if m.paused {
		t.Fatal("model should start unpaused")
	}

	m = pressRune(t, m, 'p')
	if !m.paused {
		t.Error("'p' should pause auto-refresh")
	}

	// A tick while paused must not enqueue a fetch, but must keep ticking.
	var tm tea.Model = m
	tm, cmd := tm.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Error("tick should still reschedule while paused")
	}
	if got := tm.(Model); !got.paused {
		t.Error("tick must not clear the paused state")
	}

	m = pressRune(t, tm.(Model), 'p')
	if m.paused {
		t.Error("second 'p' should resume auto-refresh")
	}
}

// TestIntervalCommand exercises :interval — no arg reports state, a duration
// sets and clamps it (and resumes), bad input errors, pause/resume toggle.
func TestIntervalCommand(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, fb, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m := tm.(Model)

	// No arg reports the current interval rather than changing it.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "interval"}); err == nil {
		t.Error("interval without args should report current state as an error")
	}

	// A valid duration sets and unpauses.
	m.paused = true
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "interval", Args: []string{"5s"}})
	if err != nil {
		t.Fatalf("interval 5s: %v", err)
	}
	if m.refreshInterval != 5*time.Second {
		t.Errorf("interval = %v, want 5s", m.refreshInterval)
	}
	if m.paused {
		t.Error("setting an interval should resume auto-refresh")
	}
	if cmd == nil {
		t.Error("interval set should return a command (refresh + clear-notif)")
	}

	// Out-of-range durations are clamped.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "interval", Args: []string{"10ms"}}); err != nil {
		t.Fatalf("interval 10ms: %v", err)
	}
	if m.refreshInterval != minRefreshInterval {
		t.Errorf("interval = %v, want clamped %v", m.refreshInterval, minRefreshInterval)
	}

	// Garbage errors and leaves the interval untouched.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "interval", Args: []string{"soon"}}); err == nil {
		t.Error("bad interval should error")
	}
	if m.refreshInterval != minRefreshInterval {
		t.Error("failed parse must not change the interval")
	}

	// pause / resume sub-commands toggle the freeze.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "interval", Args: []string{"pause"}}); err != nil {
		t.Fatalf("interval pause: %v", err)
	}
	if !m.paused {
		t.Error("interval pause should freeze auto-refresh")
	}
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "interval", Args: []string{"resume"}}); err != nil {
		t.Fatalf("interval resume: %v", err)
	}
	if m.paused {
		t.Error("interval resume should unfreeze auto-refresh")
	}
}
