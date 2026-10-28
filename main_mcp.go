package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/dockerctx"
	"github.com/kirg0/d9c/internal/mcpserver"
	"github.com/kirg0/d9c/internal/version"
)

// mcpCommand is the subcommand that serves MCP over stdio.
const mcpCommand = "mcp"

// mcpHelp is the usage text of `d9c mcp -h`.
const mcpHelp = `usage: d9c [global flags] mcp [global flags]

Serve the Model Context Protocol over stdin/stdout for one session with one
host (the same target resolution as the CLI: --host, -H, -context, -demo).
The tools group the CLI commands; write tools are listed only when the write
policy admits them (agent: {allowWrite, maxTier} in the config, --allow-write).

Client configuration, e.g.:
  {"mcpServers": {"d9c": {"command": "d9c", "args": ["mcp", "--host", "prod"]}}}

Global flags: `

// runMCP serves MCP on stdin/stdout until the client disconnects. Startup
// problems (bad flags, unknown host, missing password) go to stderr as a JSON
// error with the CLI's exit status; connection problems are reported per tool
// call, so a host that is down at startup can come back during the session.
func runMCP(cfg *config.Config, reg *agent.Registry, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	help, err := cli.ParseGlobals(cfg, args)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	if help {
		_, _ = fmt.Fprintln(stdout, mcpHelp+cli.GlobalUsage())
		return 0
	}
	srv, err := newMCPServer(cfg, reg, stderr)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	t := &mcp.IOTransport{Reader: io.NopCloser(stdin), Writer: nopWriteCloser{stdout}}
	if err := srv.Run(ctx, t); err != nil && ctx.Err() == nil {
		return cli.WriteError(stderr, err)
	}
	return 0
}

// newMCPServer resolves the session's target and policy as the CLI does and
// builds the server. The audit log stays open for the whole session.
func newMCPServer(cfg *config.Config, reg *agent.Registry, stderr io.Writer) (*mcpserver.Server, error) {
	if cfg.AllHosts || strings.Contains(cfg.SavedHost, ",") {
		return nil, agent.Errorf(agent.CodeUsage, "an MCP session serves one host").
			WithHint("start one d9c mcp server per host: --host <name>")
	}
	set, err := loadSettings(cfg, io.Discard)
	if err != nil {
		return nil, err
	}
	store := set.Hosts()
	pol := cliPolicy(cfg, set)

	// An agent must never trust an SSH host key nobody has verified.
	docker.SetStrictHostKeys(true)
	label := "demo"
	if !cfg.Demo {
		if label, err = resolveCLITarget(cfg, store, os.Getenv, dockerctx.Dir()); err != nil {
			return nil, err
		}
		pol.Host = policyHost(cfg, store)
	}
	ceiling, err := pol.Ceiling()
	if err != nil {
		return nil, err
	}
	var audit *agent.AuditLog
	if ceiling > agent.TierRead {
		if audit, err = agent.OpenAuditLog(auditPath(set)); err != nil {
			return nil, err
		}
	}
	srv, err := mcpserver.New(mcpserver.Options{
		Registry:  reg,
		Hosts:     store,
		Policy:    pol,
		Label:     label,
		Dial:      func() (docker.Backend, error) { return connectCLI(cfg) },
		Audit:     audit,
		Version:   version.String(),
		Log:       stderr,
		Scenarios: skills(),
	})
	if err != nil && audit != nil {
		_ = audit.Close()
	}
	return srv, err
}

// nopWriteCloser keeps stdout open when the transport closes.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
