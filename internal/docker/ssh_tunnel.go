package docker

import (
	"context"
	"errors"
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

	dialer := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return newDialStdioConn(sshClient)
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

// dialStdioCommands are tried in order until one succeeds.
var dialStdioCommands = []string{
	"docker system dial-stdio",
	"sudo docker system dial-stdio",
}

func newDialStdioConn(client *ssh.Client) (net.Conn, error) {
	var lastErr error
	for _, cmd := range dialStdioCommands {
		conn, err := tryDialStdio(client, cmd)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
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
