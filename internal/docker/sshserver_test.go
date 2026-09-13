package docker

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshExecHandler plays the remote side of one exec request: it receives the
// command line plus the session's stdin/stdout/stderr and returns the exit
// status reported to the client.
type sshExecHandler func(cmd string, stdin io.Reader, stdout, stderr io.Writer) int

// sshTestServer is an in-process SSH server that answers exec requests through
// a handler, so the SSH transport code (compose over SSH, sshRunner, dial-stdio)
// runs against a real SSH protocol stack without a remote host.
type sshTestServer struct {
	addr    string
	handler sshExecHandler

	mu            sync.Mutex
	cmds          []string
	ptyRequests   int
	windowChanges int
}

func newSSHTestServer(t *testing.T, handler sshExecHandler) *sshTestServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	s := &sshTestServer{addr: ln.Addr().String(), handler: handler}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serveConn(conn, cfg)
		}
	}()
	return s
}

func (s *sshTestServer) serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only sessions are supported")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.serveSession(ch, chReqs)
	}
}

func (s *sshTestServer) serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "exec":
			var payload struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			s.mu.Lock()
			s.cmds = append(s.cmds, payload.Command)
			s.mu.Unlock()
			_ = req.Reply(true, nil)
			go func(cmd string) {
				code := s.handler(cmd, ch, ch, ch.Stderr())
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
				_ = ch.Close()
			}(payload.Command)
		case "pty-req":
			s.mu.Lock()
			s.ptyRequests++
			s.mu.Unlock()
			_ = req.Reply(true, nil)
		case "window-change":
			s.mu.Lock()
			s.windowChanges++
			s.mu.Unlock()
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// commands returns the exec command lines received so far, in arrival order.
func (s *sshTestServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

// requestCounts reports how many pty-req and window-change requests arrived.
func (s *sshTestServer) requestCounts() (pty, window int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ptyRequests, s.windowChanges
}

// dial opens a client to the server; the host key is not verified.
func (s *sshTestServer) dial(t *testing.T) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", s.addr, &ssh.ClientConfig{
		User:            "tester",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// sshReply answers with fixed output and exit status, ignoring stdin.
func sshReply(stdout, stderr string, code int) sshExecHandler {
	return func(_ string, _ io.Reader, out, errw io.Writer) int {
		_, _ = io.WriteString(out, stdout)
		_, _ = io.WriteString(errw, stderr)
		return code
	}
}

// sshScript dispatches on the exact command line; anything unscripted fails
// with exit 127, like a shell that cannot find the command.
func sshScript(rules map[string]sshExecHandler) sshExecHandler {
	return func(cmd string, in io.Reader, out, errw io.Writer) int {
		if h, ok := rules[cmd]; ok {
			return h(cmd, in, out, errw)
		}
		_, _ = io.WriteString(errw, "unexpected command: "+cmd)
		return 127
	}
}

// stdinCapture records everything a remote command read from stdin.
type stdinCapture struct {
	mu   sync.Mutex
	data string
}

func (c *stdinCapture) handler(code int) sshExecHandler {
	return func(_ string, in io.Reader, _, _ io.Writer) int {
		b, _ := io.ReadAll(in)
		c.mu.Lock()
		c.data = string(b)
		c.mu.Unlock()
		return code
	}
}

func (c *stdinCapture) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data
}

// drainStream collects a streamed operation's lines until the channel closes.
func drainStream(t *testing.T, ch <-chan string, stop func(), err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("start stream: %v", err)
	}
	defer stop()
	var lines []string
	timeout := time.After(10 * time.Second)
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				return strings.Join(lines, "\n")
			}
			lines = append(lines, l)
		case <-timeout:
			t.Fatalf("stream did not finish; got %q", lines)
		}
	}
}
