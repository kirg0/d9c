// Package portfwd manages local port-forward tunnels to container ports: each
// tunnel listens on 127.0.0.1:<local> and pipes every accepted connection to a
// connection opened by a Dialer (the Docker backend — over the existing SSH
// connection, or straight to a published port).
//
// Tunnels outlive view switches: they belong to the Manager, not to any
// screen. The dialer is swappable (SetDialer), so after an auto-reconnect the
// same listeners keep serving through the new connection; while the connection
// is down each failed dial is recorded on the tunnel (State Failing) instead of
// tearing it down. A host switch or exit closes everything (CloseAll).
package portfwd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/kirg0/d9c/internal/i18n"
)

// Dialer opens a connection to a container's TCP port. docker.PortForwarder
// satisfies it.
type Dialer interface {
	DialPort(containerID string, containerPort int) (net.Conn, error)
}

// Spec describes what a tunnel forwards.
type Spec struct {
	ContainerID   string
	Name          string // container display name
	ContainerPort int
	// LocalPort is the port to listen on at 127.0.0.1; 0 picks a free one.
	LocalPort int
	// Target is the resolved remote address, for display only.
	Target string
}

// State is a tunnel's lifecycle state.
type State int

// Tunnel states. Failing means the listener is up but the last dial failed
// (e.g. the connection is reconnecting or the container stopped); the next
// successful dial returns it to Active.
const (
	Active State = iota
	Failing
	Stopped
)

// String returns the lowercase state label shown in the tunnel list.
func (s State) String() string {
	switch s {
	case Failing:
		return "failing"
	case Stopped:
		return "stopped"
	default:
		return "active"
	}
}

// Info is a point-in-time snapshot of one tunnel, safe to render.
type Info struct {
	ID    int
	Spec  Spec
	State State
	// LocalPort is the bound port (resolved when Spec.LocalPort was 0).
	LocalPort int
	// Conns is the number of currently open client connections; Total counts
	// every connection accepted so far.
	Conns   int
	Total   int
	LastErr string
}

// LocalAddr returns the listening address as "127.0.0.1:<port>".
func (i Info) LocalAddr() string {
	return net.JoinHostPort(loopback, strconv.Itoa(i.LocalPort))
}

// loopback is the only interface tunnels bind to: forwarding a remote port onto
// every interface of the workstation would expose it to the local network.
const loopback = "127.0.0.1"

// tunnel is the live state behind one Info; guarded by Manager.mu.
type tunnel struct {
	info  Info
	ln    net.Listener
	conns map[net.Conn]struct{}
}

// Manager owns every tunnel. It is safe for concurrent use; the zero value is
// not usable — build one with New.
type Manager struct {
	mu      sync.Mutex
	dialer  Dialer
	tunnels map[int]*tunnel
	nextID  int
	// listen is net.Listen, injectable for tests.
	listen func(network, addr string) (net.Listener, error)
}

// New returns an empty manager with no dialer (dials fail until SetDialer).
func New() *Manager {
	return &Manager{tunnels: map[int]*tunnel{}, nextID: 1, listen: net.Listen}
}

// SetDialer swaps the dialer used for new connections (nil = not connected).
// Existing listeners keep running, so tunnels survive an auto-reconnect.
func (m *Manager) SetDialer(d Dialer) {
	m.mu.Lock()
	m.dialer = d
	m.mu.Unlock()
}

// ErrNotConnected is returned by dials while no dialer is set.
var ErrNotConnected = errors.New("not connected")

// Start opens a new tunnel for spec. It rejects a duplicate (same container and
// port already forwarded and not stopped) and translates a busy local port into
// a readable error.
func (m *Manager) Start(spec Spec) (Info, error) {
	if spec.ContainerPort <= 0 || spec.ContainerPort > 65535 {
		return Info{}, fmt.Errorf(i18n.T("неверный порт контейнера: %d", "invalid container port: %d"), spec.ContainerPort)
	}
	if spec.LocalPort < 0 || spec.LocalPort > 65535 {
		return Info{}, fmt.Errorf(i18n.T("неверный локальный порт: %d", "invalid local port: %d"), spec.LocalPort)
	}
	m.mu.Lock()
	for _, t := range m.tunnels {
		if t.info.Spec.ContainerID == spec.ContainerID && t.info.Spec.ContainerPort == spec.ContainerPort && t.info.State != Stopped {
			addr := t.info.LocalAddr()
			m.mu.Unlock()
			return Info{}, fmt.Errorf(i18n.T("порт %d уже проброшен на %s", "port %d is already forwarded to %s"), spec.ContainerPort, addr)
		}
	}
	m.mu.Unlock()

	ln, err := m.listenLocal(spec.LocalPort)
	if err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &tunnel{
		info:  Info{ID: m.nextID, Spec: spec, State: Active, LocalPort: boundPort(ln)},
		ln:    ln,
		conns: map[net.Conn]struct{}{},
	}
	m.nextID++
	m.tunnels[t.info.ID] = t
	go m.serve(t, ln)
	return t.info, nil
}

// listenLocal binds 127.0.0.1:port, mapping "address in use" to a hint.
func (m *Manager) listenLocal(port int) (net.Listener, error) {
	ln, err := m.listen("tcp", net.JoinHostPort(loopback, strconv.Itoa(port)))
	if err != nil {
		return nil, friendlyListenErr(port, err)
	}
	return ln, nil
}

// friendlyListenErr explains the usual reasons a local port cannot be bound.
func friendlyListenErr(port int, err error) error {
	if isAddrInUse(err) {
		return fmt.Errorf(i18n.T(
			"локальный порт %d занят — укажите другой или оставьте поле пустым (свободный порт)",
			"local port %d is busy — choose another or leave the field empty for a free port"), port)
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "forbidden by its access permissions") || strings.Contains(msg, "permission denied") {
		return fmt.Errorf(i18n.T(
			"локальный порт %d недоступен (зарезервирован системой или нужны права) — укажите другой",
			"local port %d is unavailable (reserved by the system or needs privileges) — choose another"), port)
	}
	return fmt.Errorf("listen on %s:%d: %w", loopback, port, err)
}

// isAddrInUse reports whether err is an "address already in use" bind failure
// (EADDRINUSE on Unix, WSAEADDRINUSE on Windows).
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

// boundPort returns the TCP port a listener is bound to.
func boundPort(ln net.Listener) int {
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// serve accepts connections until ln is closed.
func (m *Manager) serve(t *tunnel, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		if t.ln != ln { // stopped/removed meanwhile
			m.mu.Unlock()
			_ = conn.Close()
			return
		}
		t.conns[conn] = struct{}{}
		t.info.Conns++
		t.info.Total++
		d := m.dialer
		spec := t.info.Spec
		m.mu.Unlock()
		go m.handle(t, conn, d, spec)
	}
}

// handle dials the target for one client connection and pipes both ways.
func (m *Manager) handle(t *tunnel, client net.Conn, d Dialer, spec Spec) {
	defer m.release(t, client)
	if d == nil {
		m.markDial(t, ErrNotConnected)
		return
	}
	remote, err := d.DialPort(spec.ContainerID, spec.ContainerPort)
	m.markDial(t, err)
	if err != nil {
		return
	}
	m.mu.Lock()
	t.conns[remote] = struct{}{}
	m.mu.Unlock()
	pipe(client, remote)
	m.mu.Lock()
	delete(t.conns, remote)
	m.mu.Unlock()
}

// markDial records the outcome of a dial on the tunnel's state.
func (m *Manager) markDial(t *tunnel, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.info.State == Stopped {
		return
	}
	if err != nil {
		t.info.State = Failing
		t.info.LastErr = err.Error()
		return
	}
	t.info.State = Active
	t.info.LastErr = ""
}

// release closes a finished client connection and updates the counters.
func (m *Manager) release(t *tunnel, client net.Conn) {
	_ = client.Close()
	m.mu.Lock()
	if _, ok := t.conns[client]; ok {
		delete(t.conns, client)
		t.info.Conns--
	}
	m.mu.Unlock()
}

// halfCloser is implemented by connections that can close their write side
// (TCP, SSH channels) — used so a client's EOF reaches the server without
// cutting the response off.
type halfCloser interface{ CloseWrite() error }

// pipe copies a↔b until both directions finish, then closes both.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if hc, ok := dst.(halfCloser); ok {
			_ = hc.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// closeLocked shuts the listener and every open connection of t (caller holds
// m.mu). Connection closes run in the background: closing an SSH channel on a
// dead connection may block, and this is called from the UI event loop.
func (t *tunnel) closeLocked() {
	if t.ln != nil {
		_ = t.ln.Close()
		t.ln = nil
	}
	conns := make([]net.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.conns = map[net.Conn]struct{}{}
	t.info.Conns = 0
	if len(conns) > 0 {
		go func() {
			for _, c := range conns {
				_ = c.Close()
			}
		}()
	}
}

// Stop closes the tunnel's listener and connections but keeps it in the list,
// so it can be resumed on the same local port.
func (m *Manager) Stop(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tunnels[id]
	if !ok {
		return fmt.Errorf("no such tunnel: %d", id)
	}
	t.closeLocked()
	t.info.State = Stopped
	t.info.LastErr = ""
	return nil
}

// Resume re-opens a stopped tunnel on its previous local port.
func (m *Manager) Resume(id int) (Info, error) {
	m.mu.Lock()
	t, ok := m.tunnels[id]
	if !ok {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("no such tunnel: %d", id)
	}
	if t.info.State != Stopped {
		info := t.info
		m.mu.Unlock()
		return info, nil
	}
	port := t.info.LocalPort
	m.mu.Unlock()

	ln, err := m.listenLocal(port)
	if err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, still := m.tunnels[id]; !still || t.info.State != Stopped {
		_ = ln.Close()
		return t.info, nil
	}
	t.ln = ln
	t.info.State = Active
	t.info.LastErr = ""
	go m.serve(t, ln)
	return t.info, nil
}

// Toggle stops an open tunnel or resumes a stopped one.
func (m *Manager) Toggle(id int) (Info, error) {
	m.mu.Lock()
	t, ok := m.tunnels[id]
	stopped := ok && t.info.State == Stopped
	m.mu.Unlock()
	if !ok {
		return Info{}, fmt.Errorf("no such tunnel: %d", id)
	}
	if stopped {
		return m.Resume(id)
	}
	if err := m.Stop(id); err != nil {
		return Info{}, err
	}
	return m.Get(id)
}

// Get returns the snapshot of one tunnel.
func (m *Manager) Get(id int) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tunnels[id]
	if !ok {
		return Info{}, fmt.Errorf("no such tunnel: %d", id)
	}
	return t.info, nil
}

// Remove closes the tunnel and drops it from the list.
func (m *Manager) Remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tunnels[id]; ok {
		t.closeLocked()
		delete(m.tunnels, id)
	}
}

// CloseAll closes and forgets every tunnel (host switch, exit).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.tunnels {
		t.closeLocked()
		delete(m.tunnels, id)
	}
}

// List returns snapshots of every tunnel, oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		out = append(out, t.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len reports how many tunnels exist (any state).
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tunnels)
}

// ByContainer maps container ID → the local ports forwarded to it by open
// (non-stopped) tunnels, sorted ascending — the table's forward marker.
func (m *Manager) ByContainer() map[string][]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]int{}
	for _, t := range m.tunnels {
		if t.info.State == Stopped {
			continue
		}
		out[t.info.Spec.ContainerID] = append(out[t.info.Spec.ContainerID], t.info.LocalPort)
	}
	for _, ports := range out {
		sort.Ints(ports)
	}
	return out
}
