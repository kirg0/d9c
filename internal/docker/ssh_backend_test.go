package docker

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kirg0/d9c/internal/config"
)

// isolateSSHHome points the home directory at a temp dir so known_hosts writes
// and default key lookups never touch the real ~/.ssh, and disables the agent.
func isolateSSHHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	return home
}

// writeTestKey stores a fresh ed25519 private key in OpenSSH format.
func writeTestKey(t *testing.T, dir string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	path := filepath.Join(dir, "id_test")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

// The ssh:// backend end to end: the SSH server answers the daemon-access probe
// and proxies `docker system dial-stdio` into a mock daemon, so API calls
// really travel through the tunnel.
func TestNewSSHBackendOverDialStdio(t *testing.T) {
	home := isolateSSHHome(t)
	keyFile := writeTestKey(t, home)

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", "1.43")
			w.WriteHeader(http.StatusOK)
			return
		}
		if _, p := route(r); p == "/containers/json" {
			jsonOK(w, mockContainerList)
			return
		}
		jsonErr(w, http.StatusNotFound, "unexpected")
	}))
	t.Cleanup(daemon.Close)
	daemonAddr := strings.TrimPrefix(daemon.URL, "http://")

	srv := newSSHTestServer(t, func(cmd string, in io.Reader, out, errw io.Writer) int {
		switch cmd {
		case "docker version --format '{{.Server.Version}}' < /dev/null":
			_, _ = io.WriteString(out, "27.4.0\n")
			return 0
		case "docker system dial-stdio":
			conn, err := net.Dial("tcp", daemonAddr)
			if err != nil {
				_, _ = io.WriteString(errw, err.Error())
				return 1
			}
			defer func() { _ = conn.Close() }()
			go func() {
				_, _ = io.Copy(conn, in)
				if tc, ok := conn.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
			}()
			_, _ = io.Copy(out, conn)
			return 0
		}
		_, _ = io.WriteString(errw, "unexpected command: "+cmd)
		return 127
	})

	b, err := New(&config.Config{Host: "ssh://tester@" + srv.addr, SSHKeyFile: keyFile, SSHPassword: "unused"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(b.Close)

	if err := b.Ping(); err != nil {
		t.Fatalf("Ping through the tunnel: %v", err)
	}
	ctrs, err := b.ListContainers(true)
	if err != nil || len(ctrs) != 2 {
		t.Errorf("containers through the tunnel = %d, %v; want 2", len(ctrs), err)
	}
	if !b.SupportsHostCompose() {
		t.Error("ssh backend should support host compose operations")
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "known_hosts")); err != nil {
		t.Errorf("host key was not trusted on first use: %v", err)
	}
}

// A user without daemon access (and without sudo) must fail at connect with a
// clear error after trying both the plain and the sudo probe.
func TestNewSSHBackendNoDaemonAccess(t *testing.T) {
	isolateSSHHome(t)
	srv := newSSHTestServer(t, sshReply("", "permission denied while trying to connect to the Docker daemon socket", 1))

	_, err := New(&config.Config{Host: "ssh://tester@" + srv.addr})
	if err == nil || !strings.Contains(err.Error(), "docker daemon unreachable") {
		t.Fatalf("New error = %v, want daemon unreachable", err)
	}
	cmds := srv.commands()
	if len(cmds) != 2 || !strings.HasPrefix(cmds[1], "sudo ") {
		t.Errorf("probes = %q, want plain then sudo", cmds)
	}
}

func TestSSHClientSessionConn(t *testing.T) {
	isolateSSHHome(t)
	srv := newSSHTestServer(t, func(_ string, in io.Reader, out, _ io.Writer) int {
		_, _ = io.Copy(out, in)
		return 0
	})
	client, err := SSHClient("ssh://tester@"+srv.addr, "", "secret")
	if err != nil {
		t.Fatalf("SSHClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	conn, err := tryDialStdio(client, "docker system dial-stdio")
	if err != nil {
		t.Fatalf("tryDialStdio: %v", err)
	}
	if conn.LocalAddr() == nil || conn.RemoteAddr() == nil {
		t.Error("addresses must be non-nil for net/http")
	}
	if conn.SetDeadline(time.Time{}) != nil || conn.SetReadDeadline(time.Time{}) != nil || conn.SetWriteDeadline(time.Time{}) != nil {
		t.Error("deadlines are no-ops and must not fail")
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Errorf("echo = %q, %v", buf, err)
	}
	_ = conn.Close()
	if err := conn.Close(); err != nil {
		t.Errorf("second close = %v, want nil", err)
	}
}

func TestNewCRIBackendOverSSH(t *testing.T) {
	isolateSSHHome(t)
	const versionCmd = `PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin" crictl '-r' 'unix:///run/crio/crio.sock' 'version'`
	srv := newSSHTestServer(t, sshScript(map[string]sshExecHandler{
		versionCmd: sshReply("RuntimeName:  cri-o\nRuntimeVersion:  1.33.0\n", "", 0),
	}))

	b, err := New(&config.Config{Host: "crio+ssh://tester@" + srv.addr + "/run/crio/crio.sock"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Ping(); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if got := b.Runtime(); got != RuntimeCRIO {
		t.Errorf("runtime = %v, want cri-o", got)
	}
	b.Close()

	if _, err := New(&config.Config{Host: "crio+ssh://tester@127.0.0.1:1"}); err == nil ||
		!strings.Contains(err.Error(), "crictl ssh") {
		t.Errorf("unreachable host error = %v", err)
	}
}
