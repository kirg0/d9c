package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
)

// mcpEnv isolates runMCP from the user's Docker contexts and password.
func mcpEnv(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { docker.SetStrictHostKeys(false) })
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv(passwordEnv, "")
}

// TestRunMCPStdio drives `d9c mcp -demo` over pipes with the SDK client, as an
// MCP host would over the process's stdin/stdout.
func TestRunMCPStdio(t *testing.T) {
	mcpEnv(t)
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n")
	cfg := &config.Config{ConfigFile: cfgPath}

	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runMCP(cfg, agent.Builtin(), []string{"-demo", "--allow-write"}, serverIn, serverOut, &stderr)
		_ = serverOut.Close()
	}()

	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info := cs.InitializeResult().ServerInfo; info.Name != "d9c" || !strings.HasPrefix(info.Version, "v") {
		t.Errorf("server info = %+v", info)
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// --allow-write admits safe tools; destructive ones need maxTier.
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	if !names["d9c_lifecycle"] || names["d9c_remove"] || !names["d9c_containers"] {
		t.Errorf("tools under --allow-write = %v", names)
	}
	// The embedded skills are the prompts.
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) == 0 || prompts.Prompts[0].Name != "d9c" {
		t.Errorf("prompts = %+v, %v; want the embedded skills, d9c first", prompts, err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "d9c_containers", Arguments: map[string]any{"action": "ps"}})
	if err != nil || res.IsError {
		t.Fatalf("ps: %v %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"host":"demo"`) || !strings.Contains(text, `"web"`) {
		t.Errorf("ps text = %s", text)
	}
	// A write is audited next to the config.
	if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "d9c_lifecycle",
		Arguments: map[string]any{"action": "restart", "targets": []string{"web"}}}); err != nil || res.IsError {
		t.Fatalf("restart: %v %+v", err, res)
	}
	log, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), agent.AuditFileName))
	if err != nil || !strings.Contains(string(log), `"restart"`) {
		t.Errorf("audit log = %q (%v)", log, err)
	}

	_ = cs.Close()
	_ = clientOut.Close()
	if code := <-done; code != 0 {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestRunMCPStartupErrors(t *testing.T) {
	mcpEnv(t)
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n  - name: pw\n    host: ssh://u@pw.invalid\n    ssh_auth: password\n")
	tests := []struct {
		name     string
		args     []string
		wantCode agent.Code
	}{
		{"positional", []string{"prod"}, agent.CodeUsage},
		{"several hosts", []string{"--host", "prod,pw"}, agent.CodeUsage},
		{"all hosts", []string{"--all-hosts"}, agent.CodeUsage},
		{"unknown host", []string{"--host", "nope"}, agent.CodeNotFound},
		{"password host", []string{"--host", "pw"}, agent.CodeAuthRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runMCP(&config.Config{ConfigFile: cfgPath}, agent.Builtin(), tt.args, strings.NewReader(""), &stdout, &stderr)
			if code != tt.wantCode.ExitCode() || !strings.Contains(stderr.String(), `"`+string(tt.wantCode)+`"`) {
				t.Errorf("exit %d, stderr %q; want %s", code, stderr.String(), tt.wantCode)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q; the protocol stream must stay clean", stdout.String())
			}
		})
	}
}

func TestRunMCPHelp(t *testing.T) {
	mcpEnv(t)
	var stdout, stderr bytes.Buffer
	if code := runMCP(&config.Config{}, agent.Builtin(), []string{"-h"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "mcpServers") || !strings.Contains(out, "--host <saved name>") {
		t.Errorf("help = %q", out)
	}
}

// A host that is down at startup does not stop the server: the failure is
// reported by the tool call, and a later call may succeed.
func TestRunMCPLazyConnect(t *testing.T) {
	mcpEnv(t)
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n")
	srv, err := newMCPServer(&config.Config{ConfigFile: cfgPath, SavedHost: "prod"}, agent.Builtin(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close(); _ = ss.Wait(); srv.Close() }()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "d9c_system", Arguments: map[string]any{"action": "info"}})
	if err != nil {
		t.Fatal(err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, `"connection"`) {
		t.Errorf("info on a closed port = %s", text)
	}
}
