package docker

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"d9c/internal/config"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
)

func TestIsConnectionError(t *testing.T) {
	conn := client.ErrorConnectionFailed("tcp://host:2375")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"conn failed", conn, true},
		{"wrapped conn failed", fmt.Errorf("list containers: %w", conn), true},
		{"eof", io.EOF, true},
		{"wrapped eof", fmt.Errorf("read: %w", io.EOF), true},
		{"ssh handshake", errors.New("ssh: handshake failed"), true},
		{"refused text", errors.New("dial tcp: connection refused"), true},
		{"reset text", errors.New("read: connection reset by peer"), true},
		{"not found", errors.New("Error: No such container: abc"), false},
		{"plain op error", errors.New("invalid reference format"), false},
	}
	for _, tt := range tests {
		if got := IsConnectionError(tt.err); got != tt.want {
			t.Errorf("%s: IsConnectionError = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsHostKeyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"knownhosts key mismatch", errors.New("ssh: handshake failed: knownhosts: key mismatch"), true},
		{"knownhosts key unknown", errors.New("ssh: handshake failed: knownhosts: key is unknown"), true},
		{"wrapped knownhosts", fmt.Errorf("SSH tunnel: %w", errors.New("knownhosts: key mismatch")), true},
		{"ssh host key", errors.New("ssh: host key verification failed"), true},
		{"plain handshake", errors.New("ssh: handshake failed"), false},
		{"unrelated", errors.New("connection refused"), false},
	}
	for _, tt := range tests {
		if got := IsHostKeyError(tt.err); got != tt.want {
			t.Errorf("%s: IsHostKeyError = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsHostNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dns lookup", errors.New("dial tcp: lookup badhost: no such host"), true},
		{"wrapped lookup", fmt.Errorf("SSH tunnel: %w", errors.New("lookup nope.invalid: no such host")), true},
		{"connection refused", errors.New("dial tcp 1.2.3.4:2375: connect: connection refused"), false},
		{"unrelated", errors.New("ssh: handshake failed"), false},
	}
	for _, tt := range tests {
		if got := IsHostNotFoundError(tt.err); got != tt.want {
			t.Errorf("%s: IsHostNotFoundError = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestShortID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"9ae942fd8fbc1a2b3c4d5e6f", "9ae942fd8fbc"},
		{"9ae942fd8fbc", "9ae942fd8fbc"},
		{"web", "web"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := shortID(tt.in); got != tt.want {
			t.Errorf("shortID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatEvent(t *testing.T) {
	tests := []struct {
		name string
		msg  events.Message
		want string
	}{
		{
			"name attribute wins over id",
			events.Message{
				Type: "container", Action: "start", Scope: "local",
				Actor: events.Actor{ID: "9ae942fd8fbc1a2b3c4d", Attributes: map[string]string{"name": "web"}},
			},
			"container start web (local)",
		},
		{
			"long id truncated",
			events.Message{
				Type: "container", Action: "die", Scope: "local",
				Actor: events.Actor{ID: "9ae942fd8fbc1a2b3c4d"},
			},
			"container die 9ae942fd8fbc (local)",
		},
		{
			"container attribute truncated and wins over name",
			events.Message{
				Type: "network", Action: "connect", Scope: "local",
				Actor: events.Actor{ID: "f0a1", Attributes: map[string]string{
					"name":      "app-net",
					"container": "d2c94e258dcb1a2b3c4d",
				}},
			},
			"network connect d2c94e258dcb (local)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatEvent(tt.msg); got != tt.want {
				t.Errorf("formatEvent = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestProbeHostSummary_Unreachable checks a refused connection yields a summary
// flagged unreachable (with the dial error), never an error return.
func TestProbeHostSummary_Unreachable(t *testing.T) {
	s := ProbeHostSummary(&config.Config{}, "tcp://127.0.0.1:1", 5*time.Second)
	if s.Reachable {
		t.Error("expected unreachable for a refused host")
	}
	if s.Err == "" {
		t.Error("expected Err to carry the dial failure")
	}
	if s.Host != "tcp://127.0.0.1:1" {
		t.Errorf("Host = %q, want the probed URL echoed back", s.Host)
	}
}

// TestProbeHostSummary_Timeout checks a silent host is reported unreachable once
// the budget runs out instead of hanging the caller.
func TestProbeHostSummary_Timeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	start := time.Now()
	s := ProbeHostSummary(&config.Config{}, "tcp://"+ln.Addr().String(), 300*time.Millisecond)
	if s.Reachable {
		t.Fatal("expected unreachable from a silent host")
	}
	if !strings.Contains(s.Err, "no response") {
		t.Errorf("Err = %q, want the probe-timeout message", s.Err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("probe took %v, must respect its own timeout", elapsed)
	}
}

// TestFakeInfo checks the demo backend summarizes its own in-memory data.
func TestFakeInfo(t *testing.T) {
	s, err := NewFakeBackend().Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !s.Reachable || s.Containers != 3 || s.Running != 2 || s.Images != 4 {
		t.Errorf("demo summary = %+v, want 3 containers / 2 running / 4 images", s)
	}
}

// TestFakeEventsStop verifies the stop handle closes the demo event stream so
// a pending reader unblocks (the UI relies on this when the view closes).
func TestFakeEventsStop(t *testing.T) {
	f := NewFakeBackend()
	ch, stop, err := f.Events()
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var lines int
	for range len(ch) {
		<-ch
		lines++
	}
	if lines == 0 {
		t.Fatal("expected buffered demo events")
	}
	stop()
	stop() // idempotent
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after stop")
	}
}
