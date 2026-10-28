package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
)

// readTools are the tools of a read-only session.
var readTools = []string{"d9c_compose", "d9c_containers", "d9c_hosts", "d9c_logs", "d9c_resources", "d9c_system"}

// writePolicy admits every tier.
func writePolicy() agent.Policy {
	yes := true
	return agent.Policy{Global: &hosts.AgentPolicy{AllowWrite: &yes, MaxTier: "destructive"}, ConfigPath: "cfg.yaml"}
}

// session is an in-process client connected to a server built from opts
// (demo backend, label "demo" and the builtin registry by default).
type session struct {
	*mcp.ClientSession
	srv   *Server
	audit string
}

func startSession(t *testing.T, opts Options) *session {
	t.Helper()
	return startSessionWith(t, opts, nil)
}

// startSessionWith is startSession with client options (notification
// handlers).
func startSessionWith(t *testing.T, opts Options, clientOpts *mcp.ClientOptions) *session {
	t.Helper()
	if opts.Registry == nil {
		opts.Registry = agent.Builtin()
	}
	if opts.Dial == nil {
		opts.Dial = func() (docker.Backend, error) { return docker.NewFakeBackend(), nil }
	}
	if opts.Label == "" {
		opts.Label = "demo"
	}
	sess := &session{}
	if ceiling, _ := opts.Policy.Ceiling(); ceiling > agent.TierRead && opts.Audit == nil {
		sess.audit = filepath.Join(t.TempDir(), agent.AuditFileName)
		audit, err := agent.OpenAuditLog(sess.audit)
		if err != nil {
			t.Fatal(err)
		}
		opts.Audit = audit
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, clientOpts).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
		srv.Close()
	})
	sess.ClientSession, sess.srv = cs, srv
	return sess
}

// reply is a decoded tool result: the envelope or the error.
type reply struct {
	isError   bool
	Schema    int             `json:"schema"`
	Host      string          `json:"host"`
	Runtime   string          `json:"runtime"`
	Data      json.RawMessage `json:"data"`
	Truncated bool            `json:"truncated"`
	Partial   bool            `json:"partial"`
	Hint      string          `json:"hint"`
	Error     *agent.Error    `json:"error"`
}

func (s *session) call(t *testing.T, name string, args map[string]any) reply {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("content = %d items, want 1", len(res.Content))
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var r reply
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("text %q: %v", text, err)
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var again reply
	if err := json.Unmarshal(structured, &again); err != nil || again.Schema != r.Schema || (again.Error == nil) != (r.Error == nil) {
		t.Errorf("structuredContent %s does not match the text %s", structured, text)
	}
	r.isError = res.IsError
	if r.isError != (r.Error != nil) {
		t.Errorf("isError = %v but error = %v", r.isError, r.Error)
	}
	return r
}

func (s *session) toolNames(t *testing.T) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	return names
}

func TestToolsListFollowsPolicy(t *testing.T) {
	ro := startSession(t, Options{})
	if got := ro.toolNames(t); !slices.Equal(got, readTools) {
		t.Errorf("read-only tools = %v, want %v", got, readTools)
	}
	if ins := ro.InitializeResult().Instructions; strings.Contains(ins, destructiveRule) {
		t.Errorf("read-only instructions state the destructive rule: %q", ins)
	}
	if ins := ro.InitializeResult().Instructions; !strings.Contains(ins, "6 tools that change state are not offered") {
		t.Errorf("instructions do not mention the hidden tools: %q", ins)
	}

	rw := startSession(t, Options{Policy: writePolicy()})
	if got := rw.toolNames(t); len(got) != len(toolTable) {
		t.Errorf("write session lists %d tools, want all %d: %v", len(got), len(toolTable), got)
	}
	if ins := rw.InitializeResult().Instructions; !strings.Contains(ins, destructiveRule) {
		t.Errorf("instructions with destructive tools lack the confirmation rule: %q", ins)
	}
	if ins := rw.InitializeResult().Instructions; strings.Contains(ins, "not offered") {
		t.Errorf("instructions mention hidden tools with nothing hidden: %q", ins)
	}

	yes := true
	readOnly := startSession(t, Options{Policy: agent.Policy{ReadOnly: "-read-only flag",
		Global: &hosts.AgentPolicy{AllowWrite: &yes, MaxTier: "destructive"}}})
	if got := readOnly.toolNames(t); !slices.Equal(got, readTools) {
		t.Errorf("read-only mode lists %v, want only %v", got, readTools)
	}
}

func TestNewNeedsAuditForWrites(t *testing.T) {
	_, err := New(Options{Registry: agent.Builtin(), Policy: writePolicy()})
	if err == nil {
		t.Fatal("New with writes and no audit log succeeded")
	}
	bad := agent.Policy{Global: &hosts.AgentPolicy{MaxTier: "everything"}}
	var ae *agent.Error
	if _, err := New(Options{Registry: agent.Builtin(), Policy: bad}); !errors.As(err, &ae) || ae.Code != agent.CodeUsage {
		t.Errorf("bad maxTier: err = %v, want usage", err)
	}
}

func TestCallReadTools(t *testing.T) {
	s := startSession(t, Options{})

	r := s.call(t, "d9c_containers", map[string]any{"action": "ps", "all": true})
	if r.isError || r.Schema != agent.SchemaVersion || r.Host != "demo" || r.Runtime != "docker" {
		t.Fatalf("ps envelope = %+v", r)
	}
	var list []map[string]any
	if err := json.Unmarshal(r.Data, &list); err != nil || len(list) < 3 {
		t.Fatalf("ps data = %s (%v)", r.Data, err)
	}

	r = s.call(t, "d9c_containers", map[string]any{"action": "ps", "all": true, "fields": []string{"name"}, "limit": 1})
	list = nil
	if err := json.Unmarshal(r.Data, &list); err != nil || len(list) != 1 || len(list[0]) != 1 || list[0]["name"] == nil {
		t.Errorf("shaped ps data = %s", r.Data)
	}
	if !r.Truncated || r.Hint == "" {
		t.Errorf("limit 1 of 3 not reported as truncated: %+v", r)
	}

	r = s.call(t, "d9c_logs", map[string]any{"action": "container", "container": "web", "tail": 2})
	if r.isError || !strings.Contains(string(r.Data), `"lines"`) {
		t.Errorf("logs = %+v %s", r, r.Data)
	}

	r = s.call(t, "d9c_system", map[string]any{"action": "info"})
	if r.isError || !strings.Contains(string(r.Data), `"version"`) {
		t.Errorf("info = %+v %s", r, r.Data)
	}
}

func TestCallLocalTool(t *testing.T) {
	store := hosts.NewStore([]hosts.Host{{Name: "prod", Host: "ssh://ops@prod"}}, nil)
	dialed := false
	s := startSession(t, Options{Hosts: store, Dial: func() (docker.Backend, error) {
		dialed = true
		return nil, errors.New("must not dial")
	}})
	r := s.call(t, "d9c_hosts", nil)
	if r.isError || r.Runtime != "" || !strings.Contains(string(r.Data), `"name":"prod"`) {
		t.Errorf("hosts = %+v %s", r, r.Data)
	}
	if dialed {
		t.Error("a local tool dialed the host")
	}
}

func TestCallErrors(t *testing.T) {
	s := startSession(t, Options{})
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		wantCode agent.Code
		wantHint string
	}{
		{"missing action", "d9c_containers", map[string]any{}, agent.CodeUsage, "ps, inspect, wait"},
		{"unknown action", "d9c_containers", map[string]any{"action": "top"}, agent.CodeUsage, "ps, inspect, wait"},
		{"argument of another action", "d9c_containers", map[string]any{"action": "ps", "until": "running"}, agent.CodeUsage, "all"},
		{"missing required", "d9c_containers", map[string]any{"action": "inspect"}, agent.CodeUsage, "target"},
		{"bad type", "d9c_logs", map[string]any{"action": "container", "container": "web", "tail": "many"}, agent.CodeUsage, "tail"},
		{"dry_run on a read tool", "d9c_system", map[string]any{"action": "info", "dry_run": true}, agent.CodeUsage, "only reads"},
		{"bad limit", "d9c_containers", map[string]any{"action": "ps", "limit": 0}, agent.CodeUsage, ""},
		{"action on a single tool", "d9c_hosts", map[string]any{"action": "ls"}, agent.CodeUsage, ""},
		{"not found", "d9c_containers", map[string]any{"action": "inspect", "target": "ghost"}, agent.CodeNotFound, ""},
		{"unknown field", "d9c_containers", map[string]any{"action": "ps", "fields": []string{"nope"}}, agent.CodeUsage, "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := s.call(t, tt.tool, tt.args)
			if !r.isError || r.Error.Code != tt.wantCode {
				t.Fatalf("got %+v (error %+v), want code %s", r, r.Error, tt.wantCode)
			}
			if !strings.Contains(r.Error.Hint, tt.wantHint) {
				t.Errorf("hint %q does not contain %q", r.Error.Hint, tt.wantHint)
			}
		})
	}
}

func TestCallWriteTools(t *testing.T) {
	s := startSession(t, Options{Policy: writePolicy()})

	r := s.call(t, "d9c_lifecycle", map[string]any{"action": "stop", "targets": []string{"web"}, "dry_run": true})
	if r.isError || !strings.Contains(string(r.Data), `"steps"`) || !strings.Contains(string(r.Data), "web") {
		t.Fatalf("dry-run stop = %+v %s", r, r.Data)
	}

	// The client confirms destructive calls itself, so the server runs them.
	r = s.call(t, "d9c_remove", map[string]any{"action": "rm", "targets": []string{"db"}})
	if r.isError || !strings.Contains(string(r.Data), `"done":1`) {
		t.Fatalf("rm = %+v (error %+v) %s", r, r.Error, r.Data)
	}

	log, err := os.ReadFile(s.audit)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"planned"`) || !strings.Contains(lines[1], `"rm"`) {
		t.Errorf("audit log = %s", log)
	}
}

func TestCallForbiddenByHostPolicy(t *testing.T) {
	pol := writePolicy()
	pol.Host = &hosts.Host{Name: "prod", Host: "ssh://prod", ReadOnly: true}
	s := startSession(t, Options{Policy: pol})
	if got := s.toolNames(t); !slices.Equal(got, readTools) {
		t.Errorf("read-only host lists %v", got)
	}
}

// flaky wraps a backend whose ListContainers fails with a dropped connection
// while broken is set.
type flaky struct {
	docker.Backend
	broken *atomic.Bool
	closed atomic.Bool
}

func (f *flaky) ListContainers(all bool) ([]docker.Container, error) {
	if f.broken.Load() {
		return nil, errors.New("read tcp 10.0.0.1:2375: connection reset by peer")
	}
	return f.Backend.ListContainers(all)
}

func (f *flaky) Close() { f.closed.Store(true) }

func TestReconnect(t *testing.T) {
	reconnectDelay = 0
	t.Cleanup(func() { reconnectDelay = 500e6 })

	var broken atomic.Bool
	var dials atomic.Int32
	var conns []*flaky
	dial := func() (docker.Backend, error) {
		dials.Add(1)
		f := &flaky{Backend: docker.NewFakeBackend(), broken: &broken}
		conns = append(conns, f)
		return f, nil
	}
	s := startSession(t, Options{Policy: writePolicy(), Dial: dial})

	ps := map[string]any{"action": "ps"}
	if r := s.call(t, "d9c_containers", ps); r.isError {
		t.Fatalf("first ps: %+v", r.Error)
	}
	if r := s.call(t, "d9c_containers", ps); r.isError || dials.Load() != 1 {
		t.Fatalf("second ps: %+v, dials %d (the connection must be reused)", r.Error, dials.Load())
	}

	// The connection drops; a read redials and retries, the new one works.
	broken.Store(true)
	dialHealed := func() (docker.Backend, error) { broken.Store(false); return dial() }
	s.srv.conn.dial = dialHealed
	if r := s.call(t, "d9c_containers", ps); r.isError {
		t.Fatalf("ps after a drop: %+v", r.Error)
	}
	if dials.Load() != 2 || !conns[0].closed.Load() {
		t.Errorf("dials = %d, first closed = %v; want a redial and the dropped one closed", dials.Load(), conns[0].closed.Load())
	}

	// A write is not retried: it may have taken effect.
	broken.Store(true)
	r := s.call(t, "d9c_lifecycle", map[string]any{"action": "stop", "targets": []string{"web"}})
	if !r.isError || r.Error.Code != agent.CodeConnection || !strings.Contains(r.Error.Hint, "may or may not") {
		t.Fatalf("write on a dropped connection = %+v", r.Error)
	}
	if dials.Load() != 2 {
		t.Errorf("a write redialed at once (dials %d)", dials.Load())
	}
	// The next call reconnects.
	if r := s.call(t, "d9c_containers", ps); r.isError || dials.Load() != 3 {
		t.Errorf("ps after the failed write: %+v, dials %d", r.Error, dials.Load())
	}
}

func TestReconnectFails(t *testing.T) {
	reconnectDelay = 0
	t.Cleanup(func() { reconnectDelay = 500e6 })

	var broken atomic.Bool
	first := true
	s := startSession(t, Options{Dial: func() (docker.Backend, error) {
		if !first {
			return nil, errors.New("dial tcp 10.0.0.1:2375: connect: connection refused")
		}
		first = false
		return &flaky{Backend: docker.NewFakeBackend(), broken: &broken}, nil
	}})
	ps := map[string]any{"action": "ps"}
	s.call(t, "d9c_containers", ps)
	broken.Store(true)
	r := s.call(t, "d9c_containers", ps)
	if !r.isError || r.Error.Code != agent.CodeConnection {
		t.Fatalf("ps with the host gone = %+v", r.Error)
	}
}
