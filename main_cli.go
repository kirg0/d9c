package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/dockerctx"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/settings"
)

// passwordEnv is the only source of an SSH password in CLI mode: the CLI never
// prompts, so a password-auth host without it fails with auth_required.
const passwordEnv = "D9C_SSH_PASSWORD"

// runCLI executes one CLI command (args starts at the command word) and
// returns the process exit status. The result goes to stdout as a JSON
// envelope, a failure to stderr as a JSON error.
func runCLI(cfg *config.Config, reg *agent.Registry, args []string, stdout, stderr io.Writer) int {
	inv, err := cli.Parse(reg, cfg, args)
	if err != nil {
		return cli.WriteError(stderr, err)
	}

	// Migration notices would break the stderr contract; the TUI prints them.
	set, err := loadSettings(cfg, io.Discard)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	store := set.Hosts()
	pol := cliPolicy(cfg, set)

	if inv.Help {
		// The host is not resolved for help: a saved host or -H URL is enough
		// to show the commands its policy admits.
		pol.Host = policyHost(cfg, store)
		ceiling, err := pol.Ceiling()
		if err == nil {
			err = cli.WriteHelp(stdout, reg, inv, ceiling)
		}
		if err != nil {
			return cli.WriteError(stderr, err)
		}
		return 0
	}

	// Every write command leaves a line in the audit log, refused ones
	// included; without a log it does not run at all.
	if inv.Op.Tier > agent.TierRead {
		audit, err := agent.OpenAuditLog(auditPath(set))
		if err != nil {
			return cli.WriteError(stderr, err)
		}
		defer func() { _ = audit.Close() }()
		call := agent.Call{DryRun: inv.DryRun, Confirmed: inv.Yes}
		label, rt, res, err := execCLI(cfg, reg, inv, store, pol)
		if label == "" {
			label = cliTargetGuess(cfg)
		}
		// The command has run: a failed log write cannot undo it, and stderr
		// carries only the command's own error.
		_ = audit.Record(agent.NewAuditEntry(time.Now(), label, inv.Op, inv.Args, call, res, err))
		return writeCLIOutcome(stdout, stderr, inv, label, rt, res, err)
	}
	label, rt, res, err := execCLI(cfg, reg, inv, store, pol)
	return writeCLIOutcome(stdout, stderr, inv, label, rt, res, err)
}

// execCLI resolves the target, applies the write policy, connects and runs
// inv. It returns the host label and runtime for the envelope (the label is
// empty when the target could not be resolved).
func execCLI(cfg *config.Config, reg *agent.Registry, inv cli.Invocation, store *hosts.Store, pol agent.Policy) (string, docker.Runtime, agent.Result, error) {
	// An agent must never trust an SSH host key nobody has verified.
	docker.SetStrictHostKeys(true)

	env := &agent.Env{Hosts: store, Policy: pol}
	label, rt := "", docker.RuntimeUnknown
	switch {
	case inv.Op.Local:
	case cfg.Demo:
		label = "demo"
	default:
		var err error
		if label, err = resolveCLITarget(cfg, store, os.Getenv, dockerctx.Dir()); err != nil {
			return "", rt, agent.Result{}, err
		}
		pol.Host = policyHost(cfg, store)
	}
	// Refuse before dialing: a forbidden command never touches the host. A
	// dry run only reads, so the write policy does not hold it back.
	ceiling, err := pol.Ceiling()
	if err == nil && !inv.DryRun {
		err = pol.Check(inv.Op)
	}
	if err != nil {
		return label, rt, agent.Result{}, err
	}
	if !inv.Op.Local {
		b, err := connectCLI(cfg)
		if err != nil {
			return label, rt, agent.Result{}, err
		}
		defer b.Close()
		env.Backend = b
	}

	res, err := reg.Invoke(context.Background(), env, inv.Op.Name, inv.Args,
		agent.Call{Ceiling: ceiling, DryRun: inv.DryRun, Confirmed: inv.Yes})
	if env.Backend != nil {
		rt = env.Backend.Runtime()
	}
	return label, rt, res, err
}

// writeCLIOutcome writes the result envelope or the error and returns the
// exit status.
func writeCLIOutcome(stdout, stderr io.Writer, inv cli.Invocation, label string, rt docker.Runtime, res agent.Result, err error) int {
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	out := agent.NewEnvelope(label, rt, res)
	if inv.Op.Local {
		out.Runtime = "" // no engine involved
	}
	if err := cli.WriteResult(stdout, stderr, out, inv.Output); err != nil {
		return cli.WriteError(stderr, err)
	}
	if res.Partial {
		return agent.ExitPartial
	}
	return 0
}

// auditPath is where the CLI keeps the audit log: next to the config file,
// so ~/.d9c/agent-audit.log by default.
func auditPath(set *settings.Store) string {
	return filepath.Join(filepath.Dir(set.Path()), agent.AuditFileName)
}

// cliTargetGuess names the host a command meant to address when resolving it
// failed: the saved host or context asked for, else the -H URL.
func cliTargetGuess(cfg *config.Config) string {
	switch {
	case cfg.Demo:
		return "demo"
	case cfg.SavedHost != "":
		return cfg.SavedHost
	case cfg.Context != "":
		return cfg.Context
	}
	return cfg.Host
}

// cliPolicy assembles the agent write policy of a CLI command from the flags
// and the config; the caller sets its Host once the target is known.
func cliPolicy(cfg *config.Config, set *settings.Store) agent.Policy {
	p := agent.Policy{Global: set.File.Agent, AllowWriteFlag: cfg.AllowWrite, ConfigPath: set.Path()}
	switch {
	case cfg.ReadOnly:
		p.ReadOnly = "-read-only flag"
	case set.ReadOnly():
		p.ReadOnly = "readOnly: true in " + set.Path()
	}
	return p
}

// policyHost returns the saved host whose policy applies to the command: the
// one named by --host, else the one with the URL cfg points at (as the TUI
// matches per-host read_only), else nil.
func policyHost(cfg *config.Config, store *hosts.Store) *hosts.Host {
	h, ok := store.Find(cfg.SavedHost)
	if cfg.SavedHost == "" {
		h, ok = store.FindByURL(cfg.Host)
	}
	if !ok {
		return nil
	}
	return &h
}

// connectCLI builds the backend for a CLI command whose target
// resolveCLITarget has already applied to cfg (the demo backend for -demo).
func connectCLI(cfg *config.Config) (docker.Backend, error) {
	if cfg.Demo {
		return docker.NewFakeBackend(), nil
	}
	b, err := docker.New(cfg)
	if err != nil {
		e := agent.Classify(err)
		if !hostConfigured(cfg.Host, cfg.HostFlagSet || cfg.SavedHost != "" || cfg.Context != "") {
			e = e.WithHint("no host was given, so the default socket was tried; " +
				"pass --host <saved name> (see `d9c hosts ls`), -H <url> or -context <name>")
		}
		return nil, e
	}
	return b, nil
}

// resolveCLITarget points cfg at the host a CLI command addresses — the saved
// host (--host), else the Docker context (-context/DOCKER_CONTEXT), else -H /
// DOCKER_HOST with the TLS of a matching saved host — exactly as the TUI picks
// its startup host. The SSH password comes only from D9C_SSH_PASSWORD.
func resolveCLITarget(cfg *config.Config, store *hosts.Store, getenv func(string) string, ctxDir string) (string, error) {
	pw := getenv(passwordEnv)
	if cfg.SavedHost != "" {
		h, err := applySavedHost(cfg, store)
		if err != nil {
			return "", err
		}
		if isPasswordHost(h) {
			if pw == "" {
				return "", agent.Errorf(agent.CodeAuthRequired, "saved host %q uses SSH password authentication", h.Name).
					WithHint("set " + passwordEnv + " for this command; the CLI never prompts for a password")
			}
			cfg.SSHPassword = pw
		}
		return h.Name, nil
	}

	if pw != "" {
		cfg.SSHPassword = pw
	}
	ctxHost, err := applyDockerContext(cfg, ctxDir, getenv, io.Discard)
	if err != nil {
		return "", &agent.Error{Code: agent.CodeUsage, Message: err.Error(), Err: err,
			Hint: "check the context name with `docker context ls`"}
	}
	if ctxHost != nil {
		return ctxHost.Name, nil
	}
	applySavedHostTLS(cfg, store)
	if h, ok := store.FindByURL(cfg.Host); ok {
		return h.Name, nil
	}
	return cfg.Host, nil
}
