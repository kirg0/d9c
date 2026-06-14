package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ExecSession is a live interactive exec session bridged to an in-app terminal.
// Read pulls the remote TTY output, Write pushes user input, Resize updates the
// remote window size and Close ends the session. With a TTY the daemon merges
// stdout/stderr into the single stream carried by the hijacked connection. The
// same path works over TCP and SSH because the SDK client carries the (possibly
// SSH-tunnelled) transport used for the hijack.
type ExecSession interface {
	io.ReadWriteCloser
	// Resize sets the remote TTY window size (rows × cols, in cells).
	Resize(rows, cols int) error
}

// ExecInteractive opens an interactive exec session against a container. cmd is
// the command to run; when empty it defaults to a shell. The returned session is
// driven by the UI's embedded terminal (it pumps Read/Write and Resize) rather
// than handing the local terminal over.
func (b *dockerBackend) ExecInteractive(containerID string, cmd []string) (ExecSession, error) {
	cmd = execArgv(cmd)
	ctx := context.Background()

	created, err := b.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("create exec: %w", err)
	}

	att, err := b.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return nil, fmt.Errorf("attach exec: %w", err)
	}

	return &execSession{cli: b.cli, execID: created.ID, att: att}, nil
}

// execArgv returns the command to run, defaulting to a shell when none is given.
func execArgv(cmd []string) []string {
	if len(cmd) == 0 {
		return defaultShell()
	}
	return cmd
}

// defaultShell is the command used when exec is given no arguments. sh exists in
// virtually every image; users can ask for bash explicitly.
func defaultShell() []string { return []string{"/bin/sh"} }

// ExecRunOptions describes a one-off interactive container run from an image —
// the `docker run --rm -it` analogue driven by the exec wizard. Volumes take
// bind specs ("/host:/ctr[:ro]", "vol:/data"); an empty Cmd opens a shell.
type ExecRunOptions struct {
	Image   string
	Volumes []string
	Cmd     []string
}

// RunInteractive creates and starts a disposable interactive container from
// opts and returns the attached session for the embedded terminal. The
// container is created with AutoRemove, and closing the session force-removes
// it, so nothing is left behind whichever way the panel is closed.
func (b *dockerBackend) RunInteractive(opts ExecRunOptions) (ExecSession, error) {
	if strings.TrimSpace(opts.Image) == "" {
		return nil, fmt.Errorf("image is required")
	}
	cmd := execArgv(opts.Cmd)
	ctx := context.Background()

	created, err := b.cli.ContainerCreate(ctx, &container.Config{
		Image:        opts.Image,
		Cmd:          cmd,
		Tty:          true,
		OpenStdin:    true,
		StdinOnce:    true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}, &container.HostConfig{
		Binds:      opts.Volumes,
		AutoRemove: true,
	}, nil, nil, "")
	if err != nil {
		return nil, friendlyRunErr(err)
	}

	// Attach before starting so the first output bytes are not lost.
	att, err := b.cli.ContainerAttach(ctx, created.ID, container.AttachOptions{
		Stream: true, Stdin: true, Stdout: true, Stderr: true,
	})
	if err != nil {
		removeOneOff(b.cli, created.ID)
		return nil, fmt.Errorf("attach container: %w", err)
	}
	if err := b.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		att.Close()
		removeOneOff(b.cli, created.ID)
		return nil, fmt.Errorf("start container: %w", err)
	}
	return &oneOffSession{cli: b.cli, id: created.ID, att: att}, nil
}

// removeOneOff best-effort force-removes a disposable container (AutoRemove
// may already have cleaned it up, so the error is ignored).
func removeOneOff(cli *client.Client, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

// oneOffSession is the live attach to a disposable `run --rm -it` container.
type oneOffSession struct {
	cli *client.Client
	id  string
	att types.HijackedResponse
}

func (s *oneOffSession) Read(p []byte) (int, error)  { return s.att.Reader.Read(p) }
func (s *oneOffSession) Write(p []byte) (int, error) { return s.att.Conn.Write(p) }

// Close detaches and force-removes the container: the wizard's container is
// disposable, so closing the panel must not leave it running.
func (s *oneOffSession) Close() error {
	s.att.Close()
	removeOneOff(s.cli, s.id)
	return nil
}

// Resize pushes a new window size to the container's TTY. Non-positive
// dimensions are ignored (the terminal is not laid out yet).
func (s *oneOffSession) Resize(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return nil
	}
	return s.cli.ContainerResize(context.Background(), s.id, container.ResizeOptions{
		Height: uint(rows),
		Width:  uint(cols),
	})
}

type execSession struct {
	cli    *client.Client
	execID string
	att    types.HijackedResponse
}

func (s *execSession) Read(p []byte) (int, error)  { return s.att.Reader.Read(p) }
func (s *execSession) Write(p []byte) (int, error) { return s.att.Conn.Write(p) }

func (s *execSession) Close() error {
	s.att.Close()
	return nil
}

// Resize pushes a new window size to the remote TTY. Non-positive dimensions are
// ignored (the terminal is not laid out yet).
func (s *execSession) Resize(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return nil
	}
	return s.cli.ContainerExecResize(context.Background(), s.execID, container.ResizeOptions{
		Height: uint(rows),
		Width:  uint(cols),
	})
}
