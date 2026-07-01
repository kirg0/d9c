package docker

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// This file holds the transport-level SSH exec helpers as *client-parameterized
// free functions*, so they can be shared by dockerBackend (which runs
// `docker compose` on the host) and the nerdctl backend's sshRunner (which runs
// `nerdctl` on the host). The dockerBackend methods in compose.go delegate here;
// behavior is unchanged.

// sshOutput runs cmd over SSH, returning combined stdout+stderr. On a non-zero
// exit the trimmed remote output becomes the error message.
func sshOutput(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	out, err := session.CombinedOutput(cmd)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

// sshStream runs cmd over SSH and streams its combined stdout/stderr line-by-line
// into the returned channel. The channel is closed when the command exits; on a
// non-zero exit a trailing line carries the error. The returned stop aborts the
// command early: closing the session kills the remote process and unblocks
// producers stuck on a send nobody reads. The caller MUST call stop when it
// abandons the channel, otherwise the SSH session and goroutines leak.
func sshStream(client *ssh.Client, cmd string) (<-chan string, func(), error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("ssh session: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	if err := session.Start(cmd); err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("start %q: %w", cmd, err)
	}

	out := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			_ = session.Close()
		})
	}
	send := func(line string) bool {
		select {
		case out <- line:
			return true
		case <-done:
			return false
		}
	}
	var wg sync.WaitGroup
	scan := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if !send(sc.Text()) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			send("error: read output: " + err.Error())
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	go func() {
		wg.Wait()
		if err := session.Wait(); err != nil {
			send("error: " + err.Error())
		}
		stop() // release the session when the command ends naturally
		close(out)
	}()
	return out, stop, nil
}

// sshPipe streams r into a remote command's stdin (e.g. `tee <path>` or
// `tar xzf - -C <dir>`). On a non-zero exit the trimmed remote stderr becomes the
// error message.
func sshPipe(client *ssh.Client, cmd string, r io.Reader) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	session.Stderr = &stderr
	if err := session.Start(cmd); err != nil {
		return err
	}
	if _, err := io.Copy(stdin, r); err != nil {
		_ = stdin.Close()
		return err
	}
	_ = stdin.Close()
	if err := session.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// sshInteractive opens an interactive PTY session running cmd on the host and
// returns it as an ExecSession bridged to the app's embedded terminal. Used by
// the nerdctl backend for `nerdctl exec -it` and `nerdctl run --rm -it`, which —
// unlike the Docker SDK's hijack — are just remote processes with a TTY.
func sshInteractive(client *ssh.Client, cmd string) (ExecSession, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := session.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.Start(cmd); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("start %q: %w", cmd, err)
	}
	return &sshExecSession{session: session, stdin: stdin, stdout: stdout}, nil
}

// sshExecSession bridges an SSH PTY session to the ExecSession interface.
type sshExecSession struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	once    sync.Once
}

func (s *sshExecSession) Read(p []byte) (int, error)  { return s.stdout.Read(p) }
func (s *sshExecSession) Write(p []byte) (int, error) { return s.stdin.Write(p) }

func (s *sshExecSession) Close() error {
	var err error
	s.once.Do(func() {
		_ = s.stdin.Close()
		err = s.session.Close()
	})
	return err
}

// Resize pushes a new window size to the remote PTY. Non-positive dimensions are
// ignored (the terminal is not laid out yet).
func (s *sshExecSession) Resize(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return nil
	}
	return s.session.WindowChange(rows, cols)
}
