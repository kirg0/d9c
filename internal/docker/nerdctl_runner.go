package docker

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// nerdctlRunner abstracts *how* a nerdctl command is executed — locally via
// os/exec or on a remote host over SSH — so the nerdctl backend can build the
// same argv regardless of transport. output captures stdout for one-shot
// queries, stream fans a command's combined output into a channel (logs/events/
// build/push/compose), and interactive opens a PTY session for `exec -it` /
// `run --rm -it`.
type nerdctlRunner interface {
	output(args []string) (string, error)
	stream(args []string) (<-chan string, func(), error)
	interactive(args []string) (ExecSession, error)
	close()
}

// ── local runner (os/exec) ──────────────────────────────────────────────────

// localRunner runs the CLI on the machine d9c itself runs on.
type localRunner struct{ bin string }

func (r localRunner) output(args []string) (string, error) {
	out, err := exec.Command(r.bin, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

func (r localRunner) stream(args []string) (<-chan string, func(), error) {
	return streamLocalCmd(r.bin, args)
}

// interactive is unsupported locally: bridging a local PTY into the embedded
// terminal would need an extra dependency. The ssh transport (nerdctl+ssh://)
// covers interactive exec; everything else works locally.
func (r localRunner) interactive([]string) (ExecSession, error) {
	return nil, fmt.Errorf("interactive exec is only available over the ssh transport (use nerdctl+ssh://…)")
}

func (r localRunner) close() {}

// streamLocalCmd starts a local command and streams its combined stdout/stderr
// line-by-line into the returned channel, which closes when the process exits (a
// non-zero exit appends a trailing "error: …" line). The returned stop kills the
// process and unblocks producers stuck on a send nobody reads; the caller MUST
// call it when it abandons the channel, otherwise the process and goroutines
// leak. Mirrors ui.streamLocalProcess (kept separate to avoid an import cycle).
func streamLocalCmd(name string, args []string) (<-chan string, func(), error) {
	c := exec.Command(name, args...)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := c.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", name, err)
	}

	out := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			if c.Process != nil {
				_ = c.Process.Kill()
			}
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
	scan := func(rd io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(rd)
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
		if err := c.Wait(); err != nil {
			send("error: " + err.Error())
		}
		stop()
		close(out)
	}()
	return out, stop, nil
}

// ── ssh runner ──────────────────────────────────────────────────────────────

// sshRunner runs the CLI on a remote host over an SSH connection, reusing the
// transport helpers in ssh_exec.go.
type sshRunner struct {
	client  *ssh.Client
	bin     string
	closeFn func()
}

// sbinPath lists the directories where container networking tools (iptables)
// live. A non-interactive SSH session's PATH typically omits /usr/sbin and
// /sbin, so `nerdctl run -p` fails to find iptables ("failed to load networking
// flags: exec: \"iptables\": executable file not found in $PATH") and port
// publishing breaks; we append these to PATH for every nerdctl invocation over
// SSH. Harmless when they don't exist.
const sbinPath = "/usr/local/sbin:/usr/sbin:/sbin"

func (r sshRunner) cmd(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, r.bin)
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	// Prepend a PATH augmentation so iptables (in /usr/sbin) is found even in the
	// stripped-down PATH of a non-interactive SSH exec.
	return `PATH="$PATH:` + sbinPath + `" ` + strings.Join(parts, " ")
}

func (r sshRunner) output(args []string) (string, error) {
	return sshOutput(r.client, r.cmd(args))
}

func (r sshRunner) stream(args []string) (<-chan string, func(), error) {
	return sshStream(r.client, r.cmd(args))
}

func (r sshRunner) interactive(args []string) (ExecSession, error) {
	return sshInteractive(r.client, r.cmd(args))
}

func (r sshRunner) close() {
	if r.closeFn != nil {
		r.closeFn()
	}
}
