package main

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
)

// fanoutWorkers caps how many hosts a fan-out queries at once.
const fanoutWorkers = 8

// fanoutHosts returns the saved hosts a command fans out to — every saved
// host for --all-hosts, the names of a comma-separated --host list — or nil
// for a single-host command. Fan-out is for read commands that address an
// engine; mixing it with -H or -context is a usage error.
func fanoutHosts(cfg *config.Config, op agent.Op, store *hosts.Store) ([]string, error) {
	var names []string
	if cfg.AllHosts {
		if cfg.SavedHost != "" {
			return nil, agent.Errorf(agent.CodeUsage, "--all-hosts cannot be combined with --host").
				WithHint("pass --all-hosts alone, or list the hosts: --host a,b")
		}
		for _, h := range store.List() {
			names = append(names, h.Name)
		}
		if len(names) == 0 {
			return nil, agent.Errorf(agent.CodeUsage, "--all-hosts: there are no saved hosts").
				WithHint("add hosts in the d9c TUI (hosts view) or the hosts: list of the config")
		}
	} else {
		for n := range strings.SplitSeq(cfg.SavedHost, ",") {
			if n = strings.TrimSpace(n); n != "" && !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
		if len(names) < 2 {
			if strings.Contains(cfg.SavedHost, ",") && len(names) == 1 {
				cfg.SavedHost = names[0] // "a," is just a
			}
			return nil, nil
		}
	}
	switch {
	case op.Tier > agent.TierRead:
		return nil, agent.Errorf(agent.CodeUsage, "%q changes state; only read commands run on several hosts at once", op.Name).
			WithHint("run it on one host at a time with --host <name>")
	case op.Local:
		return nil, agent.Errorf(agent.CodeUsage, "%q reads only the local config; it needs no host", op.Name).
			WithHint("drop --host / --all-hosts")
	case cfg.HostFlagSet || cfg.Context != "":
		return nil, agent.Errorf(agent.CodeUsage, "several hosts cannot be combined with -H or -context").
			WithHint("fan-out works with saved hosts only; see `d9c hosts ls`")
	}
	return names, nil
}

// runFanout runs the read command inv on every named saved host concurrently
// and writes one envelope whose data lists each host's result or error; a
// host that fails does not stop the others. With -demo every host is served
// by the demo backend (names must still be saved hosts).
func runFanout(cfg *config.Config, reg *agent.Registry, inv cli.Invocation, store *hosts.Store, names []string, stdout, stderr io.Writer) int {
	// An agent must never trust an SSH host key nobody has verified.
	docker.SetStrictHostKeys(true)

	results := make([]agent.HostResult, len(names))
	sem := make(chan struct{}, fanoutWorkers)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = runOnHost(cfg, reg, inv, store, name)
		}()
	}
	wg.Wait()

	key, cols, err := cli.ShapeHosts(results, inv.Output.Shape)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	res, err := agent.FanoutResult(inv.Op.Name, results)
	if err != nil {
		return cli.WriteError(stderr, err)
	}
	env := agent.NewEnvelope(strings.Join(names, ","), docker.RuntimeUnknown, res)
	env.Runtime = "" // per host
	if err := cli.WriteHosts(stdout, stderr, env, key, cols, inv.Output.Format); err != nil {
		return cli.WriteError(stderr, err)
	}
	if res.Partial {
		return agent.ExitPartial
	}
	return 0
}

// runOnHost runs inv on the saved host name with its own copy of cfg.
func runOnHost(cfg *config.Config, reg *agent.Registry, inv cli.Invocation, store *hosts.Store, name string) agent.HostResult {
	out := agent.HostResult{Host: name}
	fail := func(err error) agent.HostResult {
		out.Error = agent.Classify(err)
		return out
	}
	c := *cfg
	c.SavedHost = name
	var b docker.Backend
	if c.Demo {
		if _, err := applySavedHost(&c, store); err != nil {
			return fail(err)
		}
		fb, err := newDemoBackend(&c)
		if err != nil {
			return fail(err)
		}
		b = fb
	} else {
		if _, err := resolveCLITarget(&c, store, os.Getenv, ""); err != nil {
			return fail(err)
		}
		var err error
		if b, err = connectCLI(&c); err != nil {
			return fail(err)
		}
	}
	defer b.Close()

	res, err := reg.Invoke(context.Background(), &agent.Env{Backend: b, Hosts: store}, inv.Op.Name, inv.Args,
		agent.Call{Ceiling: agent.TierRead})
	if err != nil {
		return fail(err)
	}
	out.Runtime = b.Runtime().Label()
	out.Data, out.Truncated, out.Hint = res.Data, res.Truncated, res.Hint
	return out
}
