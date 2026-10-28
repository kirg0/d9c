package mcpserver

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kirg0/d9c/internal/docker"
)

func TestProgressNotifications(t *testing.T) {
	var mu sync.Mutex
	var got []*mcp.ProgressNotificationParams
	s := startSessionWith(t, Options{Policy: writePolicy()}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, req.Params)
		},
	})
	params := &mcp.CallToolParams{Name: "d9c_deploy", Arguments: map[string]any{"action": "compose_up", "project": "webapp"}}
	params.SetProgressToken("up-1")
	res, err := s.CallTool(context.Background(), params)
	if err != nil || res.IsError {
		t.Fatalf("compose up: %v %+v", err, res)
	}
	// Notifications are delivered asynchronously to the result.
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no progress notifications")
	}
	for i, p := range got {
		if p.ProgressToken != "up-1" || p.Progress != float64(i+1) || p.Message == "" {
			t.Errorf("notification %d = %+v", i, p)
		}
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, got[0].Message) {
		t.Errorf("first progress line %q is not in the result", got[0].Message)
	}
}

func TestNoProgressWithoutToken(t *testing.T) {
	var called atomic.Bool
	s := startSessionWith(t, Options{Policy: writePolicy()}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(context.Context, *mcp.ProgressNotificationClientRequest) { called.Store(true) },
	})
	s.call(t, "d9c_deploy", map[string]any{"action": "compose_up", "project": "webapp"})
	s.call(t, "d9c_containers", map[string]any{"action": "ps"}) // flushes the stream
	if called.Load() {
		t.Error("progress sent without a progress token")
	}
}

// hanging serves a compose up stream that never ends until stopped.
type hanging struct {
	docker.Backend
	started chan struct{}
	stopped chan struct{}
}

func (h *hanging) ComposeUp(string) (<-chan string, func(), error) {
	ch := make(chan string, 1)
	ch <- "Container webapp-web-1 Starting"
	close(h.started)
	var once sync.Once
	return ch, func() { once.Do(func() { close(h.stopped) }) }, nil
}

// notifications/cancelled cancels the call's context, and the operation
// stops its stream (the stop pattern).
func TestCancelStopsStream(t *testing.T) {
	h := &hanging{Backend: docker.NewFakeBackend(), started: make(chan struct{}), stopped: make(chan struct{})}
	s := startSession(t, Options{Policy: writePolicy(), Dial: func() (docker.Backend, error) { return h, nil }})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "d9c_deploy",
			Arguments: map[string]any{"action": "compose_up", "project": "webapp"}})
		done <- err
	}()
	select {
	case <-h.started:
	case <-time.After(5 * time.Second):
		t.Fatal("compose up never started")
	}
	cancel()
	select {
	case <-h.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was not stopped after the call was cancelled")
	}
	if err := <-done; err == nil {
		t.Error("a cancelled call returned no error")
	}
	// The session survives the cancellation.
	if r := s.call(t, "d9c_containers", map[string]any{"action": "ps"}); r.isError {
		t.Errorf("ps after a cancelled call: %+v", r.Error)
	}
}
