package docker

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/config"

	"github.com/docker/go-connections/nat"
)

func TestPickForwardAddr(t *testing.T) {
	published := []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "8080"}}
	tests := []struct {
		name    string
		info    portInfo
		viaSSH  bool
		want    string
		wantErr string
	}{
		{"not running", portInfo{IPs: []string{"172.17.0.2"}}, true, "", "not running"},
		{"ssh published wildcard → loopback", portInfo{Running: true, Bindings: published, IPs: []string{"172.17.0.2"}}, true, "127.0.0.1:8080", ""},
		{"ssh published specific IP kept", portInfo{Running: true, Bindings: []nat.PortBinding{{HostIP: "10.0.0.7", HostPort: "81"}}}, true, "10.0.0.7:81", ""},
		{"ssh ipv6 wildcard", portInfo{Running: true, Bindings: []nat.PortBinding{{HostIP: "::", HostPort: "9000"}}}, true, "127.0.0.1:9000", ""},
		{"ssh unpublished → container IP", portInfo{Running: true, IPs: []string{"172.18.0.3", "172.17.0.2"}}, true, "172.18.0.3:80", ""},
		{"ssh host network", portInfo{Running: true, HostNetwork: true}, true, "127.0.0.1:80", ""},
		{"ssh no IP", portInfo{Running: true}, true, "", "no network IP"},
		{"skip empty host port", portInfo{Running: true, Bindings: []nat.PortBinding{{HostIP: "0.0.0.0"}}, IPs: []string{"172.17.0.2"}}, true, "172.17.0.2:80", ""},
		{"tcp published wildcard → daemon host", portInfo{Running: true, Bindings: published}, false, "10.1.1.1:8080", ""},
		{"tcp published loopback → daemon host", portInfo{Running: true, Bindings: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "8080"}}}, false, "10.1.1.1:8080", ""},
		{"tcp published specific IP", portInfo{Running: true, Bindings: []nat.PortBinding{{HostIP: "192.168.1.9", HostPort: "8080"}}}, false, "192.168.1.9:8080", ""},
		{"tcp unpublished", portInfo{Running: true, IPs: []string{"172.17.0.2"}}, false, "", "ssh://"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pickForwardAddr(tt.info, 80, tt.viaSSH, "10.1.1.1")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("addr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDaemonHostname(t *testing.T) {
	tests := map[string]string{
		"tcp://10.0.0.5:2375":         "10.0.0.5",
		"tcp://docker.example.com":    "docker.example.com",
		"unix:///var/run/docker.sock": "127.0.0.1",
		"npipe:////./pipe/docker":     "127.0.0.1",
		"::bad":                       "127.0.0.1",
		"":                            "127.0.0.1",
	}
	for in, want := range tests {
		if got := daemonHostname(in); got != want {
			t.Errorf("daemonHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContainerPorts(t *testing.T) {
	tests := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"8080->80/tcp", []int{80}},
		{"0.0.0.0:8080->80/tcp", []int{80}},
		{"8080->80/tcp, 5432/tcp, 53/udp", []int{80, 5432}},
		{"80/tcp, 8080->80/tcp", []int{80}},
		{"8000-8002/tcp", []int{8000}},
		{"garbage, 70000/tcp, 443", []int{443}},
	}
	for _, tt := range tests {
		if got := ContainerPorts(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ContainerPorts(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestFriendlyDialErr(t *testing.T) {
	refused := friendlyDialErr(&net.OpError{Op: "dial", Err: errString("connection refused")})
	if !strings.Contains(refused.Error(), "nothing in the container listens") {
		t.Errorf("refused dial should carry a hint, got %v", refused)
	}
	other := errString("boom")
	if got := friendlyDialErr(other); got != other {
		t.Errorf("unrelated error should pass through, got %v", got)
	}
}

func TestFakeBackendPortForward(t *testing.T) {
	f := NewFakeBackend()
	var _ PortForwarder = f

	target, err := f.PortTarget("9ae942fd8fbc", 80)
	if err != nil || !strings.Contains(target, "172.17.0.2:80") {
		t.Fatalf("PortTarget = %q, %v", target, err)
	}
	if _, err := f.PortTarget("3f1ab77c9012", 5432); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("stopped container should be rejected, got %v", err)
	}
	if _, err := f.DialPort("nope", 80); err == nil {
		t.Error("unknown container should fail to dial")
	}

	conn, err := f.DialPort("9ae942fd8fbc", 80)
	if err != nil {
		t.Fatalf("DialPort: %v", err)
	}
	defer func() { _ = conn.Close() }()
	go func() { _, _ = io.WriteString(conn, "GET / HTTP/1.0\r\nHost: x\r\n\r\n") }()
	body, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "200 OK") || !strings.Contains(string(body), "hello from web:80") {
		t.Errorf("unexpected demo response: %q", body)
	}
}

// echoServer listens on 127.0.0.1 and echoes every connection back; it returns
// the bound port.
func echoServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// roundTrip writes msg to conn and reads the same number of bytes back.
func roundTrip(t *testing.T, conn net.Conn, msg string) string {
	t.Helper()
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf)
}

// forwardInspect is a minimal running-container inspect payload publishing
// published (host port, empty = none) for containerPort and attached to one
// network with ip.
func forwardInspect(containerPort int, published, ip string) string {
	ports := fmt.Sprintf(`{"%d/tcp":[]}`, containerPort)
	if published != "" {
		ports = fmt.Sprintf(`{"%d/tcp":[{"HostIp":"0.0.0.0","HostPort":"%s"}]}`, containerPort, published)
	}
	return fmt.Sprintf(`{"Id":"fwd","Name":"/fwd","State":{"Status":"running","Running":true},
  "HostConfig":{"NetworkMode":"bridge"},
  "NetworkSettings":{"Ports":%s,"Networks":{"bridge":{"IPAddress":"%s"}}}}`, ports, ip)
}

func TestDockerBackendForwardTCPPublished(t *testing.T) {
	echo := echoServer(t)
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && strings.HasSuffix(p, "/fwd/json"):
			jsonOK(w, forwardInspect(80, strconv.Itoa(echo), "172.17.0.2"))
		default:
			jsonErr(w, http.StatusNotFound, "no such container")
		}
	})
	var _ PortForwarder = b

	target, err := b.PortTarget("fwd", 80)
	if want := "127.0.0.1:" + strconv.Itoa(echo); err != nil || target != want {
		t.Fatalf("PortTarget = %q, %v; want %q", target, err, want)
	}
	conn, err := b.DialPort("fwd", 80)
	if err != nil {
		t.Fatalf("DialPort: %v", err)
	}
	if got := roundTrip(t, conn, "ping"); got != "ping" {
		t.Errorf("echo = %q", got)
	}
	// Port 81 is not published: over tcp:// it can't be reached.
	if _, err := b.DialPort("fwd", 81); err == nil || !strings.Contains(err.Error(), "ssh://") {
		t.Errorf("unpublished port over tcp should hint at ssh://, got %v", err)
	}
	if _, err := b.PortTarget("missing", 80); err == nil {
		t.Error("inspect failure should surface")
	}
}

func TestDockerBackendForwardOverSSH(t *testing.T) {
	echo := echoServer(t)
	srv := newSSHTestServer(t, sshReply("", "", 0))
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "GET" && strings.HasSuffix(p, "/fwd/json"):
			// Unpublished port: reached through the container IP from the host.
			jsonOK(w, forwardInspect(echo, "", "127.0.0.1"))
		default:
			jsonErr(w, http.StatusNotFound, "no such container")
		}
	})
	b.sshClient = srv.dial(t)

	target, err := b.PortTarget("fwd", echo)
	if err != nil || !strings.HasSuffix(target, "via SSH") {
		t.Fatalf("PortTarget = %q, %v", target, err)
	}
	conn, err := b.DialPort("fwd", echo)
	if err != nil {
		t.Fatalf("DialPort: %v", err)
	}
	if got := roundTrip(t, conn, "over ssh"); got != "over ssh" {
		t.Errorf("echo = %q", got)
	}
	want := "127.0.0.1:" + strconv.Itoa(echo)
	if got := srv.forwardedTargets(); len(got) != 1 || got[0] != want {
		t.Errorf("sshd forwarded to %v, want [%s]", got, want)
	}

	// Nothing listens on the closed port: the dial fails with a hint.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	b2 := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, forwardInspect(closed, "", "127.0.0.1"))
	})
	b2.sshClient = b.sshClient
	if _, err := b2.DialPort("fwd", closed); err == nil || !strings.Contains(err.Error(), "listens") {
		t.Errorf("refused SSH dial should carry the hint, got %v", err)
	}
}

// TestForwardConnectionLost checks an unreachable daemon is reported with the
// readable "connection lost" message (the raw cause kept for errors.Is/As),
// while daemon-side errors pass through untouched.
func TestForwardConnectionLost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	_ = ln.Close()
	b, err := New(&config.Config{Host: "tcp://" + dead})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	fw := b.(PortForwarder)

	_, err = fw.DialPort("web", 80)
	if err == nil || err.Error() != "connection to the host lost — the tunnel resumes after reconnect" {
		t.Fatalf("DialPort err = %v, want the connection-lost message", err)
	}
	var lost *connLostError
	if !errors.As(err, &lost) || errors.Unwrap(err) == nil {
		t.Error("the raw transport error should stay wrapped")
	}
	if _, err := fw.PortTarget("web", 80); !errors.As(err, &lost) {
		t.Errorf("PortTarget err = %v, want connLostError", err)
	}

	// A daemon answering "no such container" is not a lost connection.
	mb := newMockBackend(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonErr(w, http.StatusNotFound, "No such container: web")
	})
	if _, err := mb.DialPort("web", 80); err == nil || errors.As(err, &lost) {
		t.Errorf("daemon-side error should pass through, got %v", err)
	}
}
