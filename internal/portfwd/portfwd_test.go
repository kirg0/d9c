package portfwd

import (
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// echoDialer answers every DialPort with an in-memory echo connection, or
// fails with err when set.
type echoDialer struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (d *echoDialer) DialPort(_ string, _ int) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	client, server := net.Pipe()
	go func() { _, _ = io.Copy(server, server); _ = server.Close() }()
	return client, nil
}

// roundTrip connects to addr, writes msg and returns what came back.
func roundTrip(t *testing.T, addr, msg string) (string, error) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(conn, msg); err != nil {
		return "", err
	}
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(conn, buf)
	return string(buf), err
}

// waitFor polls cond for up to 2s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newManager(t *testing.T, d Dialer) *Manager {
	t.Helper()
	m := New()
	m.SetDialer(d)
	t.Cleanup(m.CloseAll)
	return m
}

func TestStartForwardsTraffic(t *testing.T) {
	m := newManager(t, &echoDialer{})
	info, err := m.Start(Spec{ContainerID: "c1", Name: "web", ContainerPort: 80})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.LocalPort == 0 || info.State != Active || !strings.HasPrefix(info.LocalAddr(), "127.0.0.1:") {
		t.Fatalf("unexpected info: %+v", info)
	}
	got, err := roundTrip(t, info.LocalAddr(), "hello")
	if err != nil || got != "hello" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	waitFor(t, "connection counters", func() bool {
		i, _ := m.Get(info.ID)
		return i.Total == 1 && i.Conns == 0
	})
}

func TestStartRejectsDuplicateAndBadPorts(t *testing.T) {
	m := newManager(t, &echoDialer{})
	if _, err := m.Start(Spec{ContainerID: "c1", ContainerPort: 80}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(Spec{ContainerID: "c1", ContainerPort: 80}); err == nil || !strings.Contains(err.Error(), "already forwarded") {
		t.Errorf("duplicate should be rejected, got %v", err)
	}
	// Another port of the same container is fine.
	if _, err := m.Start(Spec{ContainerID: "c1", ContainerPort: 443}); err != nil {
		t.Errorf("second port: %v", err)
	}
	for _, spec := range []Spec{{ContainerPort: 0}, {ContainerPort: 70000}, {ContainerPort: 80, LocalPort: -1}, {ContainerPort: 80, LocalPort: 65536}} {
		if _, err := m.Start(spec); err == nil {
			t.Errorf("Start(%+v) should fail", spec)
		}
	}
}

func TestStartBusyLocalPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	m := newManager(t, &echoDialer{})
	_, err = m.Start(Spec{ContainerID: "c1", ContainerPort: 80, LocalPort: port})
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy port should give a friendly error, got %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("a failed start must not leave a tunnel, have %d", m.Len())
	}
}

func TestFriendlyListenErr(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errors.New("listen tcp 127.0.0.1:80: bind: address already in use"), "busy"},
		{errors.New("bind: Only one usage of each socket address (protocol/network address/port) is normally permitted."), "busy"},
		{errors.New("bind: An attempt was made to access a socket in a way forbidden by its access permissions."), "unavailable"},
		{errors.New("bind: permission denied"), "unavailable"},
		{errors.New("weird"), "listen on 127.0.0.1:80: weird"},
	}
	for _, tt := range tests {
		if got := friendlyListenErr(80, tt.err).Error(); !strings.Contains(got, tt.want) {
			t.Errorf("friendlyListenErr(%q) = %q, want containing %q", tt.err, got, tt.want)
		}
	}
}

func TestDialFailureMarksFailingAndRecovers(t *testing.T) {
	d := &echoDialer{err: errors.New("ssh: tunnel down")}
	m := newManager(t, d)
	info, err := m.Start(Spec{ContainerID: "c1", ContainerPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roundTrip(t, info.LocalAddr(), "x"); err == nil {
		t.Error("round trip should fail while the dial fails")
	}
	waitFor(t, "failing state", func() bool {
		i, _ := m.Get(info.ID)
		return i.State == Failing && strings.Contains(i.LastErr, "tunnel down")
	})

	// Reconnect: a new dialer makes the same listener work again.
	m.SetDialer(&echoDialer{})
	if got, err := roundTrip(t, info.LocalAddr(), "back"); err != nil || got != "back" {
		t.Fatalf("after SetDialer round trip = %q, %v", got, err)
	}
	i, _ := m.Get(info.ID)
	if i.State != Active || i.LastErr != "" {
		t.Errorf("tunnel should recover to active, got %+v", i)
	}
}

func TestNilDialerNotConnected(t *testing.T) {
	m := newManager(t, nil)
	info, err := m.Start(Spec{ContainerID: "c1", ContainerPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = roundTrip(t, info.LocalAddr(), "x")
	waitFor(t, "not connected error", func() bool {
		i, _ := m.Get(info.ID)
		return i.State == Failing && i.LastErr == ErrNotConnected.Error()
	})
}

func TestStopResumeToggle(t *testing.T) {
	m := newManager(t, &echoDialer{})
	info, err := m.Start(Spec{ContainerID: "c1", ContainerPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(info.ID); err != nil {
		t.Fatal(err)
	}
	if i, _ := m.Get(info.ID); i.State != Stopped {
		t.Fatalf("state = %v, want stopped", i.State)
	}
	if _, err := net.DialTimeout("tcp", info.LocalAddr(), time.Second); err == nil {
		t.Error("stopped tunnel should not accept connections")
	}
	// A stopped tunnel does not block a new one for the same port.
	if len(m.ByContainer()) != 0 {
		t.Error("stopped tunnels must not be marked in the table")
	}

	resumed, err := m.Toggle(info.ID)
	if err != nil {
		t.Fatalf("Toggle(resume): %v", err)
	}
	if resumed.State != Active || resumed.LocalPort != info.LocalPort {
		t.Fatalf("resumed = %+v, want active on port %d", resumed, info.LocalPort)
	}
	if got, err := roundTrip(t, info.LocalAddr(), "again"); err != nil || got != "again" {
		t.Fatalf("round trip after resume = %q, %v", got, err)
	}
	// Resume of an already open tunnel is a no-op.
	if again, err := m.Resume(info.ID); err != nil || again.State != Active {
		t.Errorf("Resume(active) = %+v, %v", again, err)
	}
	stopped, err := m.Toggle(info.ID)
	if err != nil || stopped.State != Stopped {
		t.Errorf("Toggle(stop) = %+v, %v", stopped, err)
	}

	for _, fn := range []func() error{
		func() error { return m.Stop(99) },
		func() error { _, err := m.Resume(99); return err },
		func() error { _, err := m.Toggle(99); return err },
		func() error { _, err := m.Get(99); return err },
	} {
		if err := fn(); err == nil {
			t.Error("unknown tunnel id should fail")
		}
	}
}

func TestStopClosesOpenConnections(t *testing.T) {
	m := newManager(t, &echoDialer{})
	info, err := m.Start(Spec{ContainerID: "c1", ContainerPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", info.LocalAddr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	waitFor(t, "open connection", func() bool {
		i, _ := m.Get(info.ID)
		return i.Conns == 1
	})
	if err := m.Stop(info.ID); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("stopping the tunnel should close its client connections")
	}
}

func TestRemoveListCloseAll(t *testing.T) {
	m := newManager(t, &echoDialer{})
	a, _ := m.Start(Spec{ContainerID: "c1", ContainerPort: 80})
	b, _ := m.Start(Spec{ContainerID: "c2", ContainerPort: 5432})
	c, _ := m.Start(Spec{ContainerID: "c1", ContainerPort: 443})

	list := m.List()
	if len(list) != 3 || list[0].ID != a.ID || list[1].ID != b.ID || list[2].ID != c.ID {
		t.Fatalf("List order = %+v", list)
	}
	want := map[string][]int{"c1": sortedPair(a.LocalPort, c.LocalPort), "c2": {b.LocalPort}}
	if got := m.ByContainer(); !reflect.DeepEqual(got, want) {
		t.Errorf("ByContainer = %v, want %v", got, want)
	}

	m.Remove(b.ID)
	m.Remove(12345) // unknown: no-op
	if m.Len() != 2 {
		t.Fatalf("Len after Remove = %d, want 2", m.Len())
	}
	if _, err := net.DialTimeout("tcp", b.LocalAddr(), time.Second); err == nil {
		t.Error("removed tunnel should stop listening")
	}

	m.CloseAll()
	if m.Len() != 0 || len(m.List()) != 0 {
		t.Error("CloseAll should forget every tunnel")
	}
	if _, err := net.DialTimeout("tcp", a.LocalAddr(), time.Second); err == nil {
		t.Error("CloseAll should close the listeners")
	}
}

func sortedPair(x, y int) []int {
	if x > y {
		return []int{y, x}
	}
	return []int{x, y}
}

func TestStateString(t *testing.T) {
	for s, want := range map[State]string{Active: "active", Failing: "failing", Stopped: "stopped"} {
		if s.String() != want {
			t.Errorf("%d.String() = %q, want %q", s, s.String(), want)
		}
	}
}
