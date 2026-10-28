package agent

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
)

// Per-target outcomes of a lifecycle command.
const (
	outcomeOK     = "ok"
	outcomeFailed = "failed"
)

// maxFailureDetails caps how many per-target failures an all-failed error
// message spells out.
const maxFailureDetails = 5

// ActionData is the output of a container lifecycle command (start, stop,
// restart, kill, rm): one outcome per resolved target.
type ActionData struct {
	Action  string         `json:"action"`
	Targets []ActionTarget `json:"targets"`
	Done    int            `json:"done"`
	Failed  int            `json:"failed"`
}

// ItemsKey implements Lister: the per-target outcomes are the items.
func (ActionData) ItemsKey() string { return "targets" }

// ActionTarget is the outcome for one container. Ref is the argument that named
// it (empty for a --filter match); ID and Name are empty when Ref resolved to
// no container.
type ActionTarget struct {
	Ref    string `json:"ref,omitempty"`
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Result string `json:"result"`
	Code   Code   `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`

	err *Error // classified failure, for the all-failed error
}

// containerAction runs one lifecycle action on a container ID.
type containerAction func(b docker.Backend, id string, args Args) error

// actionStep phrases one lifecycle action on a container for the plan.
type actionStep func(c docker.Container, args Args) string

// filterHelp documents the --filter syntax in schemas and usage hints.
const filterHelp = "Select containers by key=value (repeatable, all must match): " +
	"name=<glob>, image=<glob>, status=<state>, label=<key>[=<value>], network=<name>, " +
	"health=<healthy|unhealthy|starting|none>."

// lifecycleOp declares a multi-target container command. Targets come as
// IDs/ID prefixes/names (positional on the CLI) and/or --filter predicates;
// extra adds action-specific arguments; step phrases the action for the plan.
func lifecycleOp(name, summary string, tier Tier, extra map[string]*Schema, act containerAction, step actionStep) Op {
	props := map[string]*Schema{
		"targets": StringArray("Container IDs, unique ID prefixes or names.").WithPositional(),
		"filter":  StringArray(filterHelp),
	}
	maps.Copy(props, extra)
	return Op{
		Name:    name,
		Summary: summary,
		Args:    Object(props),
		Tier:    tier,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			targets, _, err := lifecycleTargets(env, name, args)
			if err != nil {
				return Result{}, err
			}
			data := ActionData{Action: name, Targets: targets}
			for i := range data.Targets {
				t := &data.Targets[i]
				if t.Result == outcomeFailed {
					continue // unresolved ref
				}
				if err := ctx.Err(); err != nil {
					t.fail(Errorf(CodeInternal, "not attempted: %v", err))
					continue
				}
				if err := act(env.Backend, t.ID, args); err != nil {
					t.fail(Classify(err))
					continue
				}
				t.Result = outcomeOK
			}
			return actionResult(data, len(args.Strings("filter")) > 0)
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			targets, byID, err := lifecycleTargets(env, name, args)
			if err != nil {
				return nil, err
			}
			steps := make([]string, 0, len(targets))
			for _, t := range targets {
				if t.Result == outcomeFailed {
					steps = append(steps, t.Error+" — will be reported as failed")
					continue
				}
				steps = append(steps, step(byID[t.ID], args))
			}
			return steps, nil
		},
	}
}

// lifecycleTargets resolves the targets of a lifecycle command (see
// resolveTargets) and returns them with the listed containers by ID. No refs
// and no filter is a usage error.
func lifecycleTargets(env *Env, name string, args Args) ([]ActionTarget, map[string]docker.Container, error) {
	if env.Backend == nil {
		return nil, nil, errNoBackend(name)
	}
	refs, filters := args.Strings("targets"), args.Strings("filter")
	if len(refs) == 0 && len(filters) == 0 {
		return nil, nil, Errorf(CodeUsage, "%s: no containers given", name).
			WithHint("pass container IDs or names, or --filter key=value; " + filterHelp)
	}
	match, err := parseContainerFilter(filters)
	if err != nil {
		return nil, nil, err
	}
	list, err := env.Backend.ListContainers(true)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]docker.Container, len(list))
	for _, c := range list {
		byID[c.ID] = c
	}
	return resolveTargets(list, refs, match), byID, nil
}

// containerLabel names a container in a plan step: "web (9ae942fd8fbc)".
func containerLabel(c docker.Container) string {
	return fmt.Sprintf("%s (%s)", c.Name, c.ID)
}

// stateStep returns a plan step for verb on c, noting a state the action will
// not change ("start container web (…) — already running").
func stateStep(verb string, c docker.Container, noop func(state string) bool, note string) string {
	s := verb + " container " + containerLabel(c)
	if noop(c.State) {
		s += " — " + note
	}
	return s
}

func (t *ActionTarget) fail(e *Error) {
	t.Result, t.Code, t.Error, t.err = outcomeFailed, e.Code, e.Message, e
}

// resolveTargets maps refs onto containers of list (in the order given), then
// appends the containers match selects, each container once. A ref matches a
// container by exact name, exact ID, unique ID prefix, or a full ID that
// starts with the listed short ID; an unknown or ambiguous ref becomes a
// failed target.
func resolveTargets(list []docker.Container, refs []string, match func(docker.Container) bool) []ActionTarget {
	var out []ActionTarget
	seen := map[string]bool{}
	add := func(t ActionTarget) {
		if t.ID != "" {
			if seen[t.ID] {
				return
			}
			seen[t.ID] = true
		}
		out = append(out, t)
	}
	for _, ref := range refs {
		c, err := resolveContainer(list, ref)
		if err != nil {
			t := ActionTarget{Ref: ref}
			t.fail(err)
			out = append(out, t)
			continue
		}
		add(ActionTarget{Ref: ref, ID: c.ID, Name: c.Name})
	}
	if match != nil {
		for _, c := range list {
			if match(c) {
				add(ActionTarget{ID: c.ID, Name: c.Name})
			}
		}
	}
	return out
}

// resolveContainer finds the container ref names, preferring an exact name or
// ID over an ID prefix.
func resolveContainer(list []docker.Container, ref string) (docker.Container, *Error) {
	if ref == "" {
		return docker.Container{}, Errorf(CodeUsage, "empty container reference")
	}
	name := strings.TrimPrefix(ref, "/")
	for _, c := range list {
		if c.Name == name || c.ID == ref {
			return c, nil
		}
	}
	var hits []docker.Container
	for _, c := range list {
		if strings.HasPrefix(c.ID, ref) || (len(ref) > len(c.ID) && strings.HasPrefix(ref, c.ID)) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return docker.Container{}, Errorf(CodeNotFound, "no such container: %s", ref).
			WithHint("list the containers with `d9c ps -a`")
	default:
		names := make([]string, len(hits))
		for i, c := range hits {
			names[i] = c.Name
		}
		return docker.Container{}, Errorf(CodeUsage, "container prefix %q is ambiguous (%s)", ref, strings.Join(names, ", ")).
			WithHint("use a longer ID prefix or the container name")
	}
}

// parseContainerFilter compiles --filter key=value predicates into one
// matcher (all must hold); nil when there are none.
func parseContainerFilter(filters []string) (func(docker.Container) bool, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	var preds []func(docker.Container) bool
	for _, f := range filters {
		key, val, ok := strings.Cut(f, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !ok || val == "" {
			return nil, Errorf(CodeUsage, "bad filter %q: want key=value", f).WithHint(filterHelp)
		}
		p, err := filterPredicate(key, val)
		if err != nil {
			return nil, Errorf(CodeUsage, "bad filter %q: %v", f, err).WithHint(filterHelp)
		}
		preds = append(preds, p)
	}
	return func(c docker.Container) bool {
		for _, p := range preds {
			if !p(c) {
				return false
			}
		}
		return true
	}, nil
}

// filterPredicate builds the matcher for one filter key. Matching is exact
// (globs for name and image), never substring: a filter decides what a
// destructive command touches, so "web" must not also hit "webhook".
func filterPredicate(key, val string) (func(docker.Container) bool, error) {
	switch key {
	case "name", "image":
		if _, err := path.Match(val, ""); err != nil {
			return nil, fmt.Errorf("invalid glob %q", val)
		}
		return func(c docker.Container) bool {
			s := c.Name
			if key == "image" {
				s = c.Image
			}
			ok, _ := path.Match(val, s)
			return ok
		}, nil
	case "status":
		return func(c docker.Container) bool { return strings.EqualFold(c.State, val) }, nil
	case "health":
		want := strings.ToLower(val)
		if want == "none" {
			want = ""
		}
		return func(c docker.Container) bool { return strings.EqualFold(c.Health, want) }, nil
	case "label":
		k, v, hasValue := strings.Cut(val, "=")
		return func(c docker.Container) bool {
			got, ok := c.Labels[k]
			return ok && (!hasValue || got == v)
		}, nil
	case "network":
		return func(c docker.Container) bool { return slices.Contains(c.Networks, val) }, nil
	default:
		return nil, fmt.Errorf("unknown key %q", key)
	}
}

// actionResult turns the per-target outcomes into the operation result: all
// succeeded → the data; all failed → one error carrying the first failure's
// code; some failed → the data marked Partial. A filter that selected nothing
// is a success with a hint.
func actionResult(data ActionData, filtered bool) (Result, error) {
	var failures []ActionTarget
	for _, t := range data.Targets {
		if t.Result == outcomeOK {
			data.Done++
		} else {
			failures = append(failures, t)
		}
	}
	data.Failed = len(failures)
	switch {
	case len(data.Targets) == 0:
		hint := ""
		if filtered {
			hint = "no containers matched the filter; check it with `d9c ps -a`"
		}
		return Result{Data: data, Hint: hint}, nil
	case data.Failed == 0:
		return Result{Data: data}, nil
	case data.Done == 0:
		if len(failures) == 1 {
			return Result{}, failures[0].err
		}
		details := make([]string, 0, maxFailureDetails)
		for _, t := range failures[:min(len(failures), maxFailureDetails)] {
			details = append(details, targetLabel(t)+": "+t.Error)
		}
		if len(failures) > maxFailureDetails {
			details = append(details, fmt.Sprintf("and %d more", len(failures)-maxFailureDetails))
		}
		first := failures[0].err
		return Result{}, &Error{
			Code:    first.Code,
			Message: fmt.Sprintf("%s failed on all %d containers: %s", data.Action, len(failures), strings.Join(details, "; ")),
			Hint:    first.Hint,
			Err:     first,
		}
	default:
		return Result{
			Data:    data,
			Partial: true,
			Hint:    fmt.Sprintf("%d of %d containers failed; see the failed entries in data.targets", data.Failed, len(data.Targets)),
		}, nil
	}
}

// targetLabel names a target in messages: its container name, else the ref.
func targetLabel(t ActionTarget) string {
	if t.Name != "" {
		return t.Name
	}
	return t.Ref
}

func isRunning(state string) bool { return state == "running" }

func startOp() Op {
	return lifecycleOp("start", "Start stopped containers.", TierSafe, nil,
		func(b docker.Backend, id string, _ Args) error { return b.StartContainer(id) },
		func(c docker.Container, _ Args) string {
			return stateStep("start", c, isRunning, "already running")
		})
}

func stopOp() Op {
	return lifecycleOp("stop", "Stop running containers gracefully.", TierSafe, nil,
		func(b docker.Backend, id string, _ Args) error { return b.StopContainer(id) },
		func(c docker.Container, _ Args) string {
			return stateStep("stop", c, func(s string) bool { return !isRunning(s) && s != "paused" }, "not running")
		})
}

func restartOp() Op {
	return lifecycleOp("restart", "Restart containers.", TierSafe, nil,
		func(b docker.Backend, id string, _ Args) error { return b.RestartContainer(id) },
		func(c docker.Container, _ Args) string {
			return "restart container " + containerLabel(c)
		})
}

func killOp() Op {
	return lifecycleOp("kill", "Send a signal to running containers (KILL by default).", TierDestructive,
		map[string]*Schema{
			"signal": String("Signal to send, e.g. KILL, TERM, HUP.").WithDefault("KILL").WithShort("s"),
		},
		func(b docker.Backend, id string, args Args) error { return b.KillContainer(id, args.String("signal")) },
		func(c docker.Container, args Args) string {
			return stateStep("send "+args.String("signal")+" to", c,
				func(s string) bool { return !isRunning(s) }, "not running, will fail")
		})
}

func rmOp() Op {
	return lifecycleOp("rm", "Remove containers (running ones only with --force).", TierDestructive,
		map[string]*Schema{
			"force": Boolean("Kill and remove running containers.").WithShort("f"),
		},
		func(b docker.Backend, id string, args Args) error { return b.RemoveContainer(id, args.Bool("force")) },
		func(c docker.Container, args Args) string {
			note := "running, will fail without --force"
			if args.Bool("force") {
				note = "running, killed first"
			}
			return stateStep("remove", c, isRunning, note)
		})
}
