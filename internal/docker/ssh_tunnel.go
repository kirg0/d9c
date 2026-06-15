package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sshDialer returns a DialContext that tunnels each Docker API call through
// "docker system dial-stdio" over SSH — the same method the Docker CLI uses.
// This works even when direct Unix-socket forwarding is disabled on the server.
func sshDialer(host, keyFile, password string) (func(ctx context.Context, network, addr string) (net.Conn, error), *ssh.Client, func(), error) {
	sshClient, err := buildSSHClient(host, keyFile, password)
	if err != nil {
		return nil, nil, nil, err
	}

	// Pick the dial-stdio invocation that actually reaches the daemon (plain or
	// sudo) ONCE, up front. Probing here makes a "no docker access" host fail
	// loudly at connect instead of opening dead sessions that surface later as a
	// silent reconnect loop.
	cmd, err := probeDialStdioCmd(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, nil, nil, err
	}

	dialer := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return tryDialStdio(sshClient, cmd)
	}

	return dialer, sshClient, func() { _ = sshClient.Close() }, nil
}

// SSHClient opens a raw SSH client (used by the setup utility).
func SSHClient(host, keyFile, password string) (*ssh.Client, error) {
	return buildSSHClient(host, keyFile, password)
}

func buildSSHClient(host, keyFile, password string) (*ssh.Client, error) {
	user, hostname, port := parseSSHHost(host)
	authMethods := buildAuthMethods(keyFile, password)

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback(),
	}
	return ssh.Dial("tcp", hostname+":"+port, cfg)
}

// hostKeyCallback returns an SSH host-key verifier that behaves like OpenSSH's
// StrictHostKeyChecking=accept-new: a host already in known_hosts is verified
// (a changed key is rejected so the caller can surface the mismatch notice),
// while a previously unseen host is trusted on first use and its key appended to
// known_hosts. Without the trust-on-first-use step, cleaning a stale entry —
// which the mismatch notice tells the user to do — would leave the host
// "unknown" and every reconnect would keep failing.
func hostKeyCallback() ssh.HostKeyCallback {
	return hostKeyCallbackFor(KnownHostsPath())
}

// hostKeyCallbackFor is the path-injectable core of hostKeyCallback, split out
// so the accept-new / reject-mismatch logic can be unit-tested against a temp
// known_hosts file.
func hostKeyCallbackFor(path string) ssh.HostKeyCallback {
	if path == "" {
		return ssh.InsecureIgnoreHostKey()
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		verify, err := knownhosts.New(path)
		if err != nil {
			// No readable known_hosts file yet: trust on first use.
			return appendKnownHost(path, hostname, key)
		}
		err = verify(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		// A KeyError with an empty Want list means the host is simply unknown;
		// a non-empty Want means a known host presented a different key (the
		// dangerous case) — reject it so the mismatch notice fires.
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			return appendKnownHost(path, hostname, key)
		}
		return err
	}
}

// appendKnownHost records key for hostname in the known_hosts file at path,
// creating the file (and ~/.ssh) if needed, so subsequent connects validate
// against it instead of prompting again.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(line + "\n")
	return err
}

func buildAuthMethods(keyFile, password string) []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	// SSH agent
	if agentConn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(agentConn).Signers))
	}

	// Explicit key file
	if keyFile != "" {
		if signer, err := signerFromFile(keyFile); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	// Default key locations
	for _, path := range defaultKeyPaths() {
		if signer, err := signerFromFile(path); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
			break
		}
	}

	// Password fallback
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}

	return methods
}

// sessionConn wraps an SSH exec session running "docker system dial-stdio"
// as a net.Conn. Each Docker API request gets its own session.
type sessionConn struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	once    sync.Once
}

// dialStdioPrefixes are the command prefixes tried, in order, to reach the
// daemon: run docker directly (SSH user is in the docker group) or via sudo
// (passwordless sudo allowed). Each maps to "<prefix>docker system dial-stdio".
var dialStdioPrefixes = []string{"", "sudo "}

// probeDialStdioCmd returns the dial-stdio command that actually has daemon
// access on this host. It must probe with a command that WAITS for an exit code
// (docker version), because the dial-stdio session itself reports success the
// moment it starts — even when the remote docker immediately exits with
// "permission denied" — which is exactly why the old start-and-hope fallback
// never advanced to the sudo variant.
func probeDialStdioCmd(client *ssh.Client) (string, error) {
	return pickDialStdioCmd(dialStdioPrefixes, func(prefix string) error {
		return sshRun(client, prefix+"docker version --format '{{.Server.Version}}'")
	})
}

// pickDialStdioCmd is the pure selection core of probeDialStdioCmd: it returns
// the first prefix whose probe succeeds, mapped to its dial-stdio command, or a
// wrapped error when none reach the daemon. Split out so the fallback order can
// be unit-tested without an SSH server.
func pickDialStdioCmd(prefixes []string, probe func(prefix string) error) (string, error) {
	var lastErr error
	for _, prefix := range prefixes {
		err := probe(prefix)
		if err == nil {
			return prefix + "docker system dial-stdio", nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no dial-stdio command configured")
	}
	return "", fmt.Errorf("docker daemon unreachable over SSH (user not in docker group and sudo unavailable): %w", lastErr)
}

// sshRun runs cmd on the host and waits for it to finish, returning a non-nil
// error (with the remote output) when it exits non-zero. Stdin is closed so a
// sudo password prompt fails fast instead of hanging.
func sshRun(client *ssh.Client, cmd string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	out, err := session.CombinedOutput(cmd + " < /dev/null")
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func tryDialStdio(client *ssh.Client, cmd string) (net.Conn, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
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
		return nil, err
	}

	return &sessionConn{session: session, stdin: stdin, stdout: stdout}, nil
}

func (c *sessionConn) Read(b []byte) (int, error)  { return c.stdout.Read(b) }
func (c *sessionConn) Write(b []byte) (int, error) { return c.stdin.Write(b) }

func (c *sessionConn) Close() error {
	var err error
	c.once.Do(func() {
		_ = c.stdin.Close()
		err = c.session.Close()
	})
	return err
}

func (c *sessionConn) LocalAddr() net.Addr                { return &net.UnixAddr{Name: "local", Net: "unix"} }
func (c *sessionConn) RemoteAddr() net.Addr               { return &net.UnixAddr{Name: "docker.sock", Net: "unix"} }
func (c *sessionConn) SetDeadline(t time.Time) error      { return nil }
func (c *sessionConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *sessionConn) SetWriteDeadline(t time.Time) error { return nil }

func parseSSHHost(raw string) (user, host, port string) {
	raw = strings.TrimPrefix(raw, "ssh://")
	port = "22"
	if at := strings.LastIndex(raw, "@"); at >= 0 {
		user = raw[:at]
		raw = raw[at+1:]
	}
	if strings.Contains(raw, ":") {
		parts := strings.SplitN(raw, ":", 2)
		host, port = parts[0], parts[1]
	} else {
		host = raw
	}
	return
}

func signerFromFile(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(b)
}

func defaultKeyPaths() []string {
	home, _ := os.UserHomeDir()
	return []string{
		home + "/.ssh/id_ed25519",
		home + "/.ssh/id_rsa",
	}
}

// KnownHostsPath returns the OS-specific path to the user's SSH known_hosts
// file (e.g. C:\Users\you\.ssh\known_hosts on Windows). The file may or may not
// exist; the path is exposed so the UI can tell the user which file to clean
// when the host key changed.
func KnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}
