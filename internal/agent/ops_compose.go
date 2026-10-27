package agent

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/kirg0/d9c/internal/docker"
)

// ComposeEntry is one compose deployment in the `compose ls` output. Either
// project or working_dir names it in the other compose commands.
type ComposeEntry struct {
	Project     string `json:"project"`
	Name        string `json:"name"`
	WorkingDir  string `json:"working_dir,omitempty"`
	ConfigFiles string `json:"config_files,omitempty"`
	Status      string `json:"status"`
	Running     int    `json:"running"`
	Total       int    `json:"total"`
}

// projectArg is the argument naming a compose deployment.
func projectArg() map[string]*Schema {
	return map[string]*Schema{
		"project": String("Compose project name, or its working directory when several deployments share the name."),
	}
}

// resolveCompose finds the deployment ref names: an exact identity (working
// directory) first, else a unique project or display name.
func resolveCompose(b docker.Backend, ref string) (docker.ComposeProject, error) {
	list, err := b.ListComposeProjects()
	if err != nil {
		return docker.ComposeProject{}, err
	}
	var byName []docker.ComposeProject
	for _, p := range list {
		if p.Identity() == ref {
			return p, nil
		}
		if p.Project == ref || p.Name == ref {
			byName = append(byName, p)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return docker.ComposeProject{}, Errorf(CodeNotFound, "no compose project %q", ref).
			WithHint("run `d9c compose ls` to list the compose projects")
	}
	dirs := make([]string, len(byName))
	for i, p := range byName {
		dirs[i] = p.Identity()
	}
	sort.Strings(dirs)
	return docker.ComposeProject{}, Errorf(CodeUsage, "compose project %q is ambiguous: %d deployments share it", ref, len(byName)).
		WithHint("pass the working directory instead: " + strings.Join(dirs, ", "))
}

func composeLsOp() Op {
	return Op{
		Name:    "compose ls",
		Summary: "List the compose projects found on the host (by container labels).",
		Args:    NoArgs(),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, _ Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("compose ls")
			}
			list, err := env.Backend.ListComposeProjects()
			if err != nil {
				return Result{}, err
			}
			out := make([]ComposeEntry, 0, len(list))
			for _, p := range list {
				out = append(out, ComposeEntry{
					Project: p.Project, Name: p.Name, WorkingDir: p.WorkingDir, ConfigFiles: p.ConfigFiles,
					Status: p.Status, Running: p.Running, Total: p.Total,
				})
			}
			return Result{Data: out}, nil
		},
	}
}

func composePsOp() Op {
	return Op{
		Name:    "compose ps",
		Summary: "List the containers of a compose project.",
		Args:    Object(projectArg(), "project"),
		Tier:    TierRead,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("compose ps")
			}
			p, err := resolveCompose(env.Backend, args.String("project"))
			if err != nil {
				return Result{}, err
			}
			list, err := env.Backend.ListComposeContainers(p.Identity())
			if err != nil {
				return Result{}, err
			}
			return Result{Data: containerEntries(list)}, nil
		},
	}
}

// ComposeConfigData is the `compose config` output: the resolved compose
// file as YAML text.
type ComposeConfigData struct {
	Project string `json:"project"`
	Config  string `json:"config"`
}

func composeConfigOp() Op {
	return Op{
		Name:    "compose config",
		Summary: "Show the resolved compose file of a project (needs an SSH connection to the host).",
		Args: Object(map[string]*Schema{
			"project":   projectArg()["project"],
			"no_redact": noRedactArg(),
		}, "project"),
		Tier: TierRead,
		// Compose files carry environment values and labels set by their authors.
		Untrusted: true,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("compose config")
			}
			p, err := resolveCompose(env.Backend, args.String("project"))
			if err != nil {
				return Result{}, err
			}
			if err := requireHostCompose(env.Backend, "compose config"); err != nil {
				return Result{}, err
			}
			cfg, err := env.Backend.ComposeConfig(p.Identity())
			if err != nil {
				return Result{}, err
			}
			var r Result
			if !args.Bool("no_redact") {
				var n int
				if cfg, n, err = redactComposeConfig(cfg); err != nil {
					return Result{}, err
				}
				if n > 0 {
					r.Hint = redactHint(n)
				}
			}
			r.Data = ComposeConfigData{Project: p.Project, Config: cfg}
			return r, nil
		},
	}
}

func composeLogsOp() Op {
	return Op{
		Name: "compose logs",
		Summary: "Read the recent log lines of every container of a compose project, merged by time " +
			"(--tail applies per container).",
		Args:      Object(logArgs(projectArg()), "project"),
		Tier:      TierRead,
		Untrusted: true,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("compose logs")
			}
			grep, err := compileGrep(args)
			if err != nil {
				return Result{}, err
			}
			p, err := resolveCompose(env.Backend, args.String("project"))
			if err != nil {
				return Result{}, err
			}
			ch, stop, err := env.Backend.ComposeLogs(p.Identity(), logOptions(args))
			if err != nil {
				return Result{}, err
			}
			// --tail is per container, so a full tail from one service already
			// means older lines may exist: pass 0 and judge by the cap only.
			res, err := collectLogs(ctx, p.Project, ch, stop, 0, grep)
			if err != nil {
				return Result{}, err
			}
			data := res.Data.(LogsData)
			sortByTimestamp(data.Lines)
			res.Data = data
			return res, nil
		},
	}
}

// sortByTimestamp orders "<service> | <RFC3339 time> <text>" lines by time:
// the per-container streams arrive interleaved arbitrarily. A line without a
// parsable time (a stream error) sorts with the line before it.
func sortByTimestamp(lines []string) {
	type stamped struct {
		t    time.Time
		line string
	}
	all := make([]stamped, len(lines))
	var last time.Time
	for i, l := range lines {
		_, rest, ok := strings.Cut(l, " | ")
		if !ok {
			rest = l
		}
		field, _, _ := strings.Cut(rest, " ")
		if t, err := time.Parse(time.RFC3339Nano, field); err == nil {
			last = t
		}
		all[i] = stamped{last, l}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
	for i, s := range all {
		lines[i] = s.line
	}
}
