package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kirg0/d9c/internal/docker"
)

// seqBackend answers ListContainers from a script, one snapshot per call,
// repeating the last one.
type seqBackend struct {
	docker.Backend
	mu    sync.Mutex
	snaps [][]docker.Container
	calls int
}

func (b *seqBackend) ListContainers(bool) ([]docker.Container, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := min(b.calls, len(b.snaps)-1)
	b.calls++
	return b.snaps[i], nil
}

func ctr(state, health string) []docker.Container {
	return []docker.Container{{ID: "9ae942fd8fbc", Name: "web", State: state, Health: health}}
}

func TestWait(t *testing.T) {
	waitPoll = time.Millisecond
	t.Cleanup(func() { waitPoll = 500 * time.Millisecond })
	tests := []struct {
		name      string
		snaps     [][]docker.Container
		args      Args
		wantCode  Code // "" = success
		wantCalls int
		waited    bool
	}{
		{"already running", [][]docker.Container{ctr("running", "")}, Args{"container": "web"}, "", 1, false},
		{"becomes healthy", [][]docker.Container{ctr("created", ""), ctr("running", "starting"), ctr("running", "healthy")},
			Args{"container": "web", "until": "healthy"}, "", 3, true},
		{"exits", [][]docker.Container{ctr("running", ""), ctr("exited", "")}, Args{"container": "web", "until": "exited"}, "", 2, true},
		{"dead counts as exited", [][]docker.Container{ctr("dead", "")}, Args{"container": "web", "until": "exited"}, "", 1, false},
		{"no healthcheck", [][]docker.Container{ctr("running", "")}, Args{"container": "web", "until": "healthy"}, CodeUsage, 1, false},
		{"unhealthy until timeout", [][]docker.Container{ctr("running", "unhealthy")},
			Args{"container": "web", "until": "healthy", "timeout": 0}, CodeTimeout, 1, false},
		{"removed while waiting", [][]docker.Container{ctr("created", ""), nil},
			Args{"container": "web"}, CodeNotFound, 2, false},
		{"unknown container", [][]docker.Container{ctr("running", "")}, Args{"container": "ghost"}, CodeNotFound, 1, false},
		{"bad state", [][]docker.Container{ctr("running", "")}, Args{"container": "web", "until": "paused"}, CodeUsage, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &seqBackend{snaps: tt.snaps}
			res, err := Builtin().Invoke(context.Background(), &Env{Backend: b}, "wait", tt.args, Call{})
			if b.calls != tt.wantCalls {
				t.Errorf("polled %d times, want %d", b.calls, tt.wantCalls)
			}
			if tt.wantCode != "" {
				var ae *Error
				if !errors.As(err, &ae) || ae.Code != tt.wantCode {
					t.Fatalf("err = %v, want %s", err, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			d := res.Data.(WaitData)
			last := tt.snaps[len(tt.snaps)-1][0]
			if d.Container != "web" || d.ID != last.ID || d.State != last.State || d.Health != last.Health {
				t.Errorf("data = %+v", d)
			}
			if tt.waited && d.WaitedMS < 0 || !tt.waited && d.WaitedMS != 0 {
				t.Errorf("waited_ms = %d", d.WaitedMS)
			}
		})
	}
}

// The timeout is real time: a container that never gets there fails after
// roughly --timeout seconds, naming the state it was left in.
func TestWaitTimeoutMessage(t *testing.T) {
	e := waitTimeout(docker.Container{Name: "api", State: "running", Health: "starting"}, "healthy", 30)
	if e.Code != CodeTimeout || e.Message != "container api did not become healthy within 30s (now running, health starting)" {
		t.Errorf("err = %+v", e)
	}
	e = waitTimeout(docker.Container{Name: "db", State: "exited"}, "running", 5)
	if e.Message != "container db did not become running within 5s (now exited)" {
		t.Errorf("message = %q", e.Message)
	}
}

func TestWaitCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &seqBackend{snaps: [][]docker.Container{ctr("created", "")}}
	_, err := Builtin().Invoke(ctx, &Env{Backend: b}, "wait", Args{"container": "web"}, Call{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
