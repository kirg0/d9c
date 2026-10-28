package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kirg0/d9c/internal/docker"
)

// Defaults of `exec`.
const (
	defaultExecTimeout  = 60    // seconds
	defaultExecMaxBytes = 65536 // per stream
)

// ExecData is the output of `exec`: the command's exit status and output. A
// non-zero ExitCode is a result, not a d9c failure.
type ExecData struct {
	Container string   `json:"container"`
	ID        string   `json:"id"`
	Cmd       []string `json:"cmd"`
	ExitCode  int      `json:"exit_code"`
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr"`
	// StdoutBytes and StderrBytes count everything the command wrote; above
	// the length of Stdout/Stderr the output was cut at --max-bytes.
	StdoutBytes int64 `json:"stdout_bytes"`
	StderrBytes int64 `json:"stderr_bytes"`
}

func execOp() Op {
	return Op{
		Name: "exec",
		Summary: "Run a command in a running container without a terminal and return its exit code, " +
			"stdout and stderr (`d9c exec web -- ls -la /`). The command can change anything inside the container.",
		Args: Object(map[string]*Schema{
			"container": String("Container ID, unique ID prefix or name."),
			"cmd":       StringArray("The command and its arguments, run directly (no shell; wrap in sh -c for pipes). On the CLI put them after --."),
			"timeout": Integer("Seconds to wait for the command. On timeout d9c stops waiting; " +
				"with the Docker API the process may keep running in the container.").
				WithDefault(defaultExecTimeout).WithMin(1),
			"max_bytes": Integer("Bytes kept of stdout and of stderr each; the rest is counted and dropped.").
				WithDefault(defaultExecMaxBytes).WithMin(1),
		}, "container", "cmd"),
		Tier:      TierDestructive,
		Untrusted: true,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			c, cmd, err := execTarget(env, args)
			if err != nil {
				return Result{}, err
			}
			timeout := time.Duration(args.Int("timeout")) * time.Second
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			res, err := env.Backend.Exec(ctx, c.ID, cmd, docker.ExecOptions{MaxOutput: args.Int("max_bytes")})
			if errors.Is(err, context.DeadlineExceeded) {
				return Result{}, execTimeout(c, args.Int("timeout"), res)
			}
			if err != nil {
				return Result{}, err
			}
			return execResult(c, cmd, res, args.Int("max_bytes")), nil
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			c, cmd, err := execTarget(env, args)
			if err != nil {
				return nil, err
			}
			return []string{fmt.Sprintf("run %s in container %s as its default user; "+
				"the command can change anything inside the container", quoteArgv(cmd), containerLabel(c))}, nil
		},
	}
}

// execTarget resolves the container of an exec and checks it can run one.
func execTarget(env *Env, args Args) (docker.Container, []string, error) {
	if env.Backend == nil {
		return docker.Container{}, nil, errNoBackend("exec")
	}
	cmd := args.Strings("cmd")
	if len(cmd) == 0 || cmd[0] == "" {
		return docker.Container{}, nil, Errorf(CodeUsage, "exec: no command given").
			WithHint("usage: d9c exec <container> -- <command> [args...]")
	}
	list, err := env.Backend.ListContainers(true)
	if err != nil {
		return docker.Container{}, nil, err
	}
	c, aerr := resolveContainer(list, args.String("container"))
	if aerr != nil {
		return docker.Container{}, nil, aerr
	}
	if !isRunning(c.State) {
		return c, nil, Errorf(CodeUsage, "container %s is %s, not running", c.Name, c.State).
			WithHint(fmt.Sprintf("commands run only in a running container; start it with `d9c start %s`", c.Name))
	}
	return c, cmd, nil
}

// execResult wraps a finished exec; output cut at maxBytes is reported as
// truncated.
func execResult(c docker.Container, cmd []string, res docker.ExecResult, maxBytes int) Result {
	data := ExecData{
		Container:   c.Name,
		ID:          c.ID,
		Cmd:         cmd,
		ExitCode:    res.ExitCode,
		Stdout:      strings.ToValidUTF8(string(res.Stdout), "�"),
		Stderr:      strings.ToValidUTF8(string(res.Stderr), "�"),
		StdoutBytes: res.StdoutBytes,
		StderrBytes: res.StderrBytes,
	}
	r := Result{Data: data}
	if res.StdoutBytes > int64(len(res.Stdout)) || res.StderrBytes > int64(len(res.Stderr)) {
		r.Truncated = true
		r.Hint = fmt.Sprintf("output was cut at %d bytes per stream (see stdout_bytes/stderr_bytes); "+
			"raise --max-bytes or narrow the command (| head, | tail, grep)", maxBytes)
	}
	return r
}

// execTimeout is the error of an exec that outlived its --timeout, quoting the
// last lines the command printed.
func execTimeout(c docker.Container, seconds int, res docker.ExecResult) *Error {
	msg := fmt.Sprintf("exec in %s timed out after %ds", c.Name, seconds)
	out := strings.Split(strings.TrimRight(string(res.Stdout)+string(res.Stderr), "\n"), "\n")
	if tail := lastLines(out, failureContextLines); len(tail) > 0 && tail[0] != "" {
		msg += "; last output: " + strings.ToValidUTF8(strings.Join(tail, " | "), "�")
	}
	return Errorf(CodeTimeout, "%s", msg).
		WithHint("raise --timeout, or run something that finishes sooner; " +
			"with the Docker API the process may still be running in the container")
}

// quoteArgv renders a command for a plan step: arguments with spaces or quotes
// are quoted, the rest left bare.
func quoteArgv(cmd []string) string {
	parts := make([]string, len(cmd))
	for i, a := range cmd {
		if a == "" || strings.ContainsAny(a, " \t\n\"'\\") {
			a = strconv.Quote(a)
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}
