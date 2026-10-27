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

// composeStreamOp declares a compose command that runs on the host over SSH
// and returns the engine output; extra adds arguments after the project.
func composeStreamOp(name, summary string, tier Tier, extra map[string]*Schema, required []string, start composeStream) Op {
	props := projectArg()
	maps.Copy(props, extra)
	return Op{
		Name:    name,
		Summary: summary,
		Args:    Object(props, append([]string{"project"}, required...)...),
		Tier:    tier,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend(name)
			}
			p, err := resolveCompose(env.Backend, args.String("project"))
			if err != nil {
				return Result{}, err
			}
			if err := requireHostCompose(env.Backend, name); err != nil {
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
	}
}

func composeUpOp() Op {
	return composeStreamOp("compose up", "Create and start a compose project's containers (`up -d`; needs an SSH connection).",
		TierSafe, nil, nil,
		func(b docker.Backend, id string, _ Args) (<-chan string, func(), error) { return b.ComposeUp(id) })
}

func composePullOp() Op {
	return composeStreamOp("compose pull", "Pull the images of a compose project's services (needs an SSH connection).",
		TierSafe, nil, nil,
		func(b docker.Backend, id string, _ Args) (<-chan string, func(), error) { return b.ComposePull(id) })
}

func composeDownOp() Op {
	return composeStreamOp("compose down",
		"Stop and remove a compose project's containers and networks; volumes are kept (needs an SSH connection). "+
			"The project then no longer shows in `compose ls`.",
		TierDestructive, nil, nil,
		func(b docker.Backend, id string, _ Args) (<-chan string, func(), error) { return b.ComposeDown(id) })
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
			if st, err := os.Stat(file); err != nil || st.IsDir() {
				return nil, nil, Errorf(CodeNotFound, "backup archive %q not found", file).
					WithHint("make one with `d9c compose backup <project>`; the path is local to this machine")
			}
			return b.RestoreComposeProject(id, file)
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
			if env.Backend == nil {
				return Result{}, errNoBackend("compose backup")
			}
			p, err := resolveCompose(env.Backend, args.String("project"))
			if err != nil {
				return Result{}, err
			}
			if err := requireHostCompose(env.Backend, "compose backup"); err != nil {
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
	}
}
