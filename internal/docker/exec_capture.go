package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecOptions bounds a non-interactive exec (Backend.Exec).
type ExecOptions struct {
	// MaxOutput caps the bytes kept of stdout and of stderr each; the rest
	// is read and dropped so the command is not blocked. 0 keeps everything.
	MaxOutput int
}

// ExecResult is the outcome of a command run to completion by Backend.Exec.
type ExecResult struct {
	// Stdout and Stderr hold the first ExecOptions.MaxOutput bytes of each
	// stream (all of it when there is no cap).
	Stdout, Stderr []byte
	// StdoutBytes and StderrBytes count everything the command wrote, so
	// a value above len(Stdout) means the output was cut.
	StdoutBytes, StderrBytes int64
	// ExitCode is the command's exit status.
	ExitCode int
}

// errEmptyExec rejects an exec without a command: Backend.Exec never falls back
// to a shell (that is ExecInteractive's job).
var errEmptyExec = errors.New("exec: no command given")

// capture is an io.Writer that keeps the first max bytes written to it (all
// when max <= 0) and counts the rest.
type capture struct {
	max   int
	buf   bytes.Buffer
	total int64
}

func newCapture(max int) *capture { return &capture{max: max} }

func (c *capture) Write(p []byte) (int, error) {
	c.total += int64(len(p))
	keep := p
	if c.max > 0 {
		keep = p[:min(len(p), max(0, c.max-c.buf.Len()))]
	}
	c.buf.Write(keep)
	return len(p), nil
}

// result assembles an ExecResult from the two stream captures.
func execResult(stdout, stderr *capture, code int) ExecResult {
	return ExecResult{
		Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes(),
		StdoutBytes: stdout.total, StderrBytes: stderr.total,
		ExitCode: code,
	}
}

// execInspectWait bounds how long Exec polls for the exit code once the output
// streams have closed (the daemon may report the process as running for a
// moment after that).
const execInspectWait = 2 * time.Second

// Exec runs cmd in containerID without a TTY and waits for it to finish,
// capturing stdout and stderr separately. Cancelling ctx stops waiting and
// returns the output so far with ctx's error; the Docker API cannot kill an
// exec'd process, so it may keep running inside the container.
func (b *dockerBackend) Exec(ctx context.Context, containerID string, cmd []string, opts ExecOptions) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, errEmptyExec
	}
	created, err := b.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("create exec: %w", err)
	}
	att, err := b.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("attach exec: %w", err)
	}
	defer att.Close()

	stdout, stderr := newCapture(opts.MaxOutput), newCapture(opts.MaxOutput)
	done := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(stdout, stderr, att.Reader)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return execResult(stdout, stderr, -1), fmt.Errorf("read exec output: %w", err)
		}
	case <-ctx.Done():
		att.Close()
		<-done //nolint:errcheck // the reader stops once the connection is closed; its error is moot
		return execResult(stdout, stderr, -1), ctx.Err()
	}

	deadline := time.Now().Add(execInspectWait)
	for {
		ins, err := b.cli.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return execResult(stdout, stderr, -1), fmt.Errorf("inspect exec: %w", err)
		}
		if !ins.Running || time.Now().After(deadline) {
			return execResult(stdout, stderr, ins.ExitCode), nil
		}
		select {
		case <-ctx.Done():
			return execResult(stdout, stderr, -1), ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
