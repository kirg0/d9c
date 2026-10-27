package main

import (
	"context"
	"io"
	"os"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/dockerctx"
	"github.com/kirg0/d9c/internal/hosts"
)

// passwordEnv is the only source of an SSH password in CLI mode: the CLI never
// prompts, so a password-auth host without it fails with auth_required.
const passwordEnv = "D9C_SSH_PASSWORD"

// cliCeiling is the tier ceiling of the CLI. Phase 6.1 ships read operations
// only; the allowWrite/maxTier policy arrives with the write commands.
const cliCeiling = agent.TierRead

// runCLI executes one CLI command (args starts at the command word) and
// returns the process exit status. The result goes to stdout as a JSON
// envelope, a failure to stderr as a JSON error.
func runCLI(cfg *config.Config, reg *agent.Registry, args []string, stdout, stderr io.Writer) int {
	inv, err := cli.Parse(reg, cfg, args)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	if inv.Help {
		if err := cli.WriteHelp(stdout, reg, inv, cliCeiling); err != nil {
			return cli.WriteError(stderr, err)
		}
		return 0
	}

	// Migration notices would break the stderr contract; the TUI prints them.
	set, err := loadSettings(cfg, io.Discard)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	store := set.Hosts()

	// An agent must never trust an SSH host key nobody has verified.
	docker.SetStrictHostKeys(true)

	env := &agent.Env{Hosts: store}
	label, rt := "", docker.RuntimeUnknown
	if !inv.Op.Local {
		b, l, err := connectCLI(cfg, store, os.Getenv)
		if err != nil {
			return cli.WriteError(stderr, err)
		}
		defer b.Close()
		env.Backend, label = b, l
	}

	res, err := reg.Invoke(context.Background(), env, inv.Op.Name, inv.Args, cliCeiling)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	if env.Backend != nil {
		rt = env.Backend.Runtime()
	}
	out := agent.NewEnvelope(label, rt, res)
	if inv.Op.Local {
		out.Runtime = "" // no engine involved
	}
	if err := cli.WriteResult(stdout, stderr, out, inv.Output); err != nil {
		return cli.WriteError(stderr, err)
	}
	return 0
}

// connectCLI builds the backend for a CLI command and returns it with the
// host label for the output envelope (saved-host or context name when known,
// else the URL; "demo" for -demo).
func connectCLI(cfg *config.Config, store *hosts.Store, getenv func(string) string) (docker.Backend, string, error) {
	if cfg.Demo {
		return docker.NewFakeBackend(), "demo", nil
	}
	label, err := resolveCLITarget(cfg, store, getenv, dockerctx.Dir())
	if err != nil {
		return nil, "", err
	}
	b, err := docker.New(cfg)
	if err != nil {
		e := agent.Classify(err)
		if !hostConfigured(cfg.Host, cfg.HostFlagSet || cfg.SavedHost != "" || cfg.Context != "") {
			e = e.WithHint("no host was given, so the default socket was tried; " +
				"pass --host <saved name> (see `d9c hosts ls`), -H <url> or -context <name>")
		}
		return nil, "", e
	}
	return b, label, nil
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
