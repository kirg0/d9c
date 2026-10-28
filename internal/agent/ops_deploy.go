package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
)

// maxProgressLines caps the engine output a write command returns. The
// operation always runs to completion; only the oldest lines are dropped.
const maxProgressLines = 200

// failureContextLines is how many output lines before a failure its error
// message quotes — compose prints the reason there, not in the exit status.
const failureContextLines = 5

// progressErrorPrefix marks the line a backend progress stream sends when the
// operation fails (non-zero exit, broken stream).
const progressErrorPrefix = "error: "

// PullData is the `pull` output.
type PullData struct {
	Image string `json:"image"`
}

func pullOp() Op {
	return Op{
		Name:    "pull",
		Summary: "Pull an image from its registry.",
		Args: Object(map[string]*Schema{
			"image": String("Image reference, e.g. nginx:1.27 or ghcr.io/org/app@sha256:<digest>."),
		}, "image"),
		Tier: TierSafe,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("pull")
			}
			ref := strings.TrimSpace(args.String("image"))
			if ref == "" {
				return Result{}, Errorf(CodeUsage, "pull: empty image reference")
			}
			if err := env.Backend.PullImage(ref); err != nil {
				return Result{}, err
			}
			return Result{Data: PullData{Image: ref}}, nil
		},
		Plan: func(_ context.Context, _ *Env, args Args) ([]string, error) {
			ref := strings.TrimSpace(args.String("image"))
			if ref == "" {
				return nil, Errorf(CodeUsage, "pull: empty image reference")
			}
			return []string{"pull image " + ref + " from its registry"}, nil
		},
	}
}

// ComposeActionData is the output of a compose write command: the engine's
// progress lines (the newest maxProgressLines of them).
type ComposeActionData struct {
	Action  string   `json:"action"`
	Project string   `json:"project"`
	Output  []string `json:"output"`
}

// ItemsKey implements Lister: the progress lines are the items.
func (ComposeActionData) ItemsKey() string { return "output" }

// progress is a drained progress stream.
type progress struct {
	lines   []string // the newest maxProgressLines lines, failure lines included
	dropped int      // older lines cut to stay within the cap
	failure string   // the first failure line without its prefix; "" on success
	context []string // the output lines just before the failure
}

// collectProgress reads a progress stream to its end. Unlike drain it never
// stops early on the cap: stopping would abort the operation half-way. A
// cancelled ctx does abort it, through stop.
func collectProgress(ctx context.Context, ch <-chan string, stop func()) (progress, error) {
	defer stop()
	p := progress{lines: []string{}}
	for {
		select {
		case <-ctx.Done():
			return progress{}, ctx.Err()
		case l, ok := <-ch:
			if !ok {
				return p, nil
			}
			l = strings.TrimRight(l, " \r\n")
			if l == "" {
				continue
			}
			if msg, failed := strings.CutPrefix(l, progressErrorPrefix); failed && p.failure == "" {
				p.failure = msg
				p.context = lastLines(p.lines, failureContextLines)
			}
			if len(p.lines) == maxProgressLines {
				p.lines = append(p.lines[:0], p.lines[1:]...)
				p.dropped++
			}
			p.lines = append(p.lines, l)
		}
	}
}

// lastLines copies the last n lines.
func lastLines(lines []string, n int) []string {
	return append([]string(nil), lines[max(0, len(lines)-n):]...)
}

// result turns a drained stream into the operation result: a failure becomes
// an error quoting the output that led to it.
func (p progress) result(action, project string) (Result, error) {
	if p.failure != "" {
		e := Classify(errors.New(p.failure))
		msg := fmt.Sprintf("%s %s failed: %s", action, project, e.Message)
		if len(p.context) > 0 {
			msg += "; last output: " + strings.Join(p.context, " | ")
		}
		return Result{}, &Error{Code: e.Code, Message: msg, Hint: e.Hint, Err: e}
	}
	r := Result{Data: ComposeActionData{Action: action, Project: project, Output: p.lines}}
	if p.dropped > 0 {
		r.Truncated = true
		r.Hint = fmt.Sprintf("the first %d output lines were dropped; the operation completed", p.dropped)
	}
	return r, nil
}

// requireHostCompose rejects an operation the backend can only run with shell
// access to the host (compose up/down/pull/config/backup/restore).
func requireHostCompose(b docker.Backend, name string) error {
	if b.SupportsHostCompose() {
		return nil
	}
	return Errorf(CodeUsage, "%s needs shell access to the host", name).
		WithHint("connect over SSH: --host <saved ssh host> or -H ssh://user@host")
}

// composeStream starts a progress-streaming compose operation.
type composeStream func(b docker.Backend, identity string, args Args) (<-chan string, func(), error)

// composePlan phrases what a compose command would do to project p; it may
// read the project's containers and fails like the command would on bad
// arguments.
type composePlan func(b docker.Backend, p docker.ComposeProject, args Args) ([]string, error)

// composeStreamOp declares a compose command that runs on the host over SSH
// and returns the engine output; extra adds arguments after the project.
func composeStreamOp(name, summary string, tier Tier, extra map[string]*Schema, required []string, start composeStream, plan composePlan) Op {
	props := projectArg()
	maps.Copy(props, extra)
	return Op{
		Name:    name,
		Summary: summary,
		Args:    Object(props, append([]string{"project"}, required...)...),
		Tier:    tier,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			p, err := hostComposeProject(env, name, args)
			if err != nil {
				return Result{}, err
			}
			ch, stop, err := start(env.Backend, p.Identity(), args)
			if err != nil {
				return Result{}, err
			}
			prog, err := collectProgress(ctx, ch, stop)
			if err != nil {
				return Result{}, err
			}
			return prog.result(name, p.Project)
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			p, err := hostComposeProject(env, name, args)
			if err != nil {
				return nil, err
			}
			return plan(env.Backend, p, args)
		},
	}
}

// hostComposeProject resolves the project of a compose command that needs
// shell access to the host.
func hostComposeProject(env *Env, name string, args Args) (docker.ComposeProject, error) {
	if env.Backend == nil {
		return docker.ComposeProject{}, errNoBackend(name)
	}
	p, err := resolveCompose(env.Backend, args.String("project"))
	if err != nil {
		return p, err
	}
	return p, requireHostCompose(env.Backend, name)
}

// projectLabel names a compose deployment in a plan step.
func projectLabel(p docker.ComposeProject) string {
	if p.WorkingDir == "" {
		return "project " + p.Project
	}
	return fmt.Sprintf("project %s (%s)", p.Project, p.WorkingDir)
}

// projectContainers phrases project p's containers for a plan step:
// "the 2 containers of project app (/srv/app): web, db".
func projectContainers(b docker.Backend, p docker.ComposeProject) (string, error) {
	list, err := b.ListComposeContainers(p.Identity())
	if err != nil {
		return "", err
	}
	names := make([]string, len(list))
	for i, c := range list {
		names[i] = c.Name
	}
	return fmt.Sprintf("the %d containers of %s: %s", len(list), projectLabel(p), strings.Join(names, ", ")), nil
}

// checkBackupArchive fails unless file is an existing regular file.
func checkBackupArchive(file string) error {
	if st, err := os.Stat(file); err != nil || st.IsDir() {
		return Errorf(CodeNotFound, "backup archive %q not found", file).
			WithHint("make one with `d9c compose backup <project>`; the path is local to this machine")
	}
	return nil
}

func composeUpOp() Op {
	return composeStreamOp("compose up", "Create and start a compose project's containers (`up -d`; needs an SSH connection).",
		TierSafe, nil, nil,
		func(b docker.Backend, id string, _ Args) (<-chan string, func(), error) { return b.ComposeUp(id) },
		func(_ docker.Backend, p docker.ComposeProject, _ Args) ([]string, error) {
			return []string{fmt.Sprintf("run compose up -d for %s (%d of %d containers running now)",
				projectLabel(p), p.Running, p.Total)}, nil
		})
}

func composePullOp() Op {
	return composeStreamOp("compose pull", "Pull the images of a compose project's services (needs an SSH connection).",
		TierSafe, nil, nil,
		func(b docker.Backend, id string, _ Args) (<-chan string, func(), error) { return b.ComposePull(id) },
		func(_ docker.Backend, p docker.ComposeProject, _ Args) ([]string, error) {
			return []string{"pull the images of the services of " + projectLabel(p) + "; running containers are not recreated"}, nil
		})
}

func composeDownOp() Op {
	return composeStreamOp("compose down",
		"Stop and remove a compose project's containers and networks; volumes are kept (needs an SSH connection). "+
			"The project then no longer shows in `compose ls`.",
		TierDestructive, nil, nil,
		func(b docker.Backend, id string, _ Args) (<-chan string, func(), error) { return b.ComposeDown(id) },
		func(b docker.Backend, p docker.ComposeProject, _ Args) ([]string, error) {
			ctrs, err := projectContainers(b, p)
			if err != nil {
				return nil, err
			}
			return []string{
				"stop and remove " + ctrs,
				"remove the project's networks; its volumes are kept",
			}, nil
		})
}

func composeRestoreOp() Op {
	return composeStreamOp("compose restore",
		"Upload a backup archive into a compose project's working directory, overwriting its files, "+
			"and bring the project up again (needs an SSH connection).",
		TierDestructive,
		map[string]*Schema{"file": String("Local path of the .tar.gz archive made by `compose backup`.")},
		[]string{"file"},
		func(b docker.Backend, id string, args Args) (<-chan string, func(), error) {
			file := args.String("file")
			if err := checkBackupArchive(file); err != nil {
				return nil, nil, err
			}
			return b.RestoreComposeProject(id, file)
		},
		func(_ docker.Backend, p docker.ComposeProject, args Args) ([]string, error) {
			file := args.String("file")
			if err := checkBackupArchive(file); err != nil {
				return nil, err
			}
			if abs, err := filepath.Abs(file); err == nil {
				file = abs
			}
			return []string{
				"upload " + file + " and overwrite the files of " + projectLabel(p) + " with it",
				"bring the project up again",
			}, nil
		})
}

func composeRestartOp() Op {
	return Op{
		Name:    "compose restart",
		Summary: "Restart every container of a compose project.",
		Args:    Object(projectArg(), "project"),
		Tier:    TierSafe,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("compose restart")
			}
			p, err := resolveCompose(env.Backend, args.String("project"))
			if err != nil {
				return Result{}, err
			}
			if err := env.Backend.ComposeRestart(p.Identity()); err != nil {
				return Result{}, err
			}
			return Result{Data: ComposeActionData{Action: "compose restart", Project: p.Project, Output: []string{}}}, nil
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			if env.Backend == nil {
				return nil, errNoBackend("compose restart")
			}
			p, err := resolveCompose(env.Backend, args.String("project"))
			if err != nil {
				return nil, err
			}
			ctrs, err := projectContainers(env.Backend, p)
			if err != nil {
				return nil, err
			}
			return []string{"restart " + ctrs}, nil
		},
	}
}

// BackupData is the `compose backup` output: where the archive was saved.
type BackupData struct {
	Project string `json:"project"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
}

func composeBackupOp() Op {
	return Op{
		Name: "compose backup",
		Summary: "Download a compose project's working directory as a .tar.gz into the current directory " +
			"(needs an SSH connection).",
		Args: Object(projectArg(), "project"),
		Tier: TierSafe,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			p, err := hostComposeProject(env, "compose backup", args)
			if err != nil {
				return Result{}, err
			}
			path, err := env.Backend.BackupComposeProject(p.Identity())
			if err != nil {
				return Result{}, err
			}
			data := BackupData{Project: p.Project, Path: path}
			if abs, err := filepath.Abs(path); err == nil {
				data.Path = abs
			}
			if st, err := os.Stat(path); err == nil {
				data.Size = st.Size()
			}
			return Result{Data: data}, nil
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			p, err := hostComposeProject(env, "compose backup", args)
			if err != nil {
				return nil, err
			}
			return []string{"download the working directory of " + projectLabel(p) +
				" as a .tar.gz into the current directory"}, nil
		},
	}
}
