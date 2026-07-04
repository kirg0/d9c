package connwait

import (
	"strings"
	"testing"
)

func TestOpenSetsBusyAndClearsError(t *testing.T) {
	m := New()
	m.SetError("boom")
	cmd := m.Open("prod", "ssh://user@prod")
	if cmd == nil {
		t.Fatal("Open must return the spinner tick command")
	}
	if !m.Busy() {
		t.Error("Busy() = false after Open, want true")
	}
	if m.Err() != "" {
		t.Errorf("Err() = %q after Open, want empty", m.Err())
	}
	if m.HostName() != "prod" || m.HostURL() != "ssh://user@prod" {
		t.Errorf("host = %q/%q, want prod/ssh://user@prod", m.HostName(), m.HostURL())
	}
}

func TestSetErrorClearsBusy(t *testing.T) {
	m := New()
	m.Open("prod", "ssh://user@prod")
	m.SetError("connection refused")
	if m.Busy() {
		t.Error("Busy() = true after SetError, want false")
	}
	if m.Err() != "connection refused" {
		t.Errorf("Err() = %q, want %q", m.Err(), "connection refused")
	}
}

func TestRetrySetsBusyAgain(t *testing.T) {
	m := New()
	m.Open("prod", "ssh://user@prod")
	m.SetError("connection refused")
	cmd := m.Retry()
	if cmd == nil {
		t.Fatal("Retry must return the spinner tick command")
	}
	if !m.Busy() {
		t.Error("Busy() = false after Retry, want true")
	}
	if m.Err() != "" {
		t.Errorf("Err() = %q after Retry, want empty", m.Err())
	}
}

func TestViewStates(t *testing.T) {
	m := New()
	m.Open("prod", "ssh://user@prod")

	busy := m.View(80, 24)
	for _, want := range []string{"Connect to prod", "connecting to prod", "esc cancel"} {
		if !strings.Contains(busy, want) {
			t.Errorf("busy view missing %q", want)
		}
	}

	m.SetError("connection refused")
	failed := m.View(80, 24)
	for _, want := range []string{"connection refused", "enter retry", "esc close"} {
		if !strings.Contains(failed, want) {
			t.Errorf("error view missing %q", want)
		}
	}
}

func TestViewFallsBackToURLWithoutName(t *testing.T) {
	m := New()
	m.Open("", "tcp://10.0.0.5:2375")
	if got := m.View(80, 24); !strings.Contains(got, "Connect to tcp://10.0.0.5:2375") {
		t.Error("view must fall back to the URL when the host has no name")
	}
}
