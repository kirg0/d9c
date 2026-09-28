package ui

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/keymap"
	"github.com/kirg0/d9c/internal/portfwd"
	"github.com/kirg0/d9c/internal/ui/cmdline"
	"github.com/kirg0/d9c/internal/ui/pfform"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// runCmd executes cmd synchronously and returns its message (nil-safe).
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// pressKey feeds a key through Update and returns the resulting command.
func (h *modelHarness) pressKey(k string) tea.Cmd {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	var cmd tea.Cmd
	h.tm, cmd = h.tm.Update(msg)
	return cmd
}

// forwardHarness is a sized demo model listing the demo containers, with its
// tunnels closed at the end of the test.
func forwardHarness(t *testing.T) *modelHarness {
	t.Helper()
	h := newSizedModel(t)
	h.step(containersUpdatedMsg{h.fb.Containers})
	t.Cleanup(func() { h.cur().pf.CloseAll() })
	return h
}

// startDemoForward drives F → enter on the cursor container (web) and returns
// the opened tunnel.
func startDemoForward(t *testing.T, h *modelHarness) portfwd.Info {
	t.Helper()
	msg := runCmd(h.pressKey("F"))
	open, ok := msg.(openPortForwardFormMsg)
	if !ok {
		t.Fatalf("F produced %T, want openPortForwardFormMsg (err=%q)", msg, h.cur().err)
	}
	h.step(open)
	if h.cur().mode != ModePortForwardForm {
		t.Fatalf("mode = %v, want ModePortForwardForm", h.cur().mode)
	}
	started, ok := runCmd(h.pressKey("enter")).(portForwardStartedMsg)
	if !ok || started.err != nil {
		t.Fatalf("enter produced %+v", started)
	}
	h.step(started)
	return started.info
}

func TestPortForwardFlowFromContainers(t *testing.T) {
	h := forwardHarness(t)
	info := startDemoForward(t, h)

	m := h.cur()
	if m.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after the tunnel opened", m.mode)
	}
	if info.Spec.Name != "web" || info.Spec.ContainerPort != 80 || !strings.Contains(info.Spec.Target, "172.17.0.2:80") {
		t.Errorf("unexpected tunnel spec: %+v", info.Spec)
	}
	if !strings.Contains(m.copyNotif, info.LocalAddr()) || !strings.Contains(m.copyNotif, "web:80") {
		t.Errorf("notice = %q, want the local address and target", m.copyNotif)
	}
	if row := m.table.SelectedRow(); !strings.Contains(row[4], "⇄:") {
		t.Errorf("PORTS cell = %q, want a ⇄ marker", row[4])
	}
	if hdr := m.viewHeader(); !strings.Contains(hdr, "⇄ 1") {
		t.Errorf("header should count the tunnel: %q", hdr)
	}

	// The demo backend answers HTTP through the real local listener.
	conn, err := net.DialTimeout("tcp", info.LocalAddr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(conn, "GET / HTTP/1.0\r\n\r\n")
	body, _ := io.ReadAll(conn)
	if !strings.Contains(string(body), "hello from web:80") {
		t.Errorf("tunnel response = %q", body)
	}
}

func TestPortForwardRejectsStoppedContainer(t *testing.T) {
	h := forwardHarness(t)
	h.pressKey("down")
	h.pressKey("down") // db (exited)
	if cmd := h.pressKey("F"); cmd != nil {
		t.Errorf("stopped container should not open the form, got %T", runCmd(cmd))
	}
	if !strings.Contains(h.cur().err, "not running") {
		t.Errorf("err = %q, want not running", h.cur().err)
	}
}

// noForwardBackend hides the PortForwarder capability of the fake backend.
type noForwardBackend struct{ docker.Backend }

func TestPortForwardUnsupportedBackend(t *testing.T) {
	fb := docker.NewFakeBackend()
	var tm tea.Model = NewModel(&config.Config{}, noForwardBackend{fb}, nil, nil, false)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	tm, _ = tm.Update(containersUpdatedMsg{fb.Containers})
	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	if cmd != nil {
		t.Error("unsupported backend should not open the form")
	}
	if !strings.Contains(tm.(Model).err, "not available") {
		t.Errorf("err = %q", tm.(Model).err)
	}
	if forwardDialer(noForwardBackend{fb}) != nil {
		t.Error("forwardDialer must be nil without the capability")
	}
}

func TestPortForwardFormValidationAndErrors(t *testing.T) {
	h := forwardHarness(t)
	h.step(openPortForwardFormMsg{targets: []pfform.Target{{ID: "9ae942fd8fbc", Name: "web"}}})
	if cmd := h.pressKey("enter"); cmd != nil {
		t.Error("empty container port must not start a tunnel")
	}
	if v := h.cur().pfForm.View(120, 30); !strings.Contains(v, "container port") {
		t.Errorf("form should show the validation error:\n%s", v)
	}

	// A backend-side failure lands in the open form…
	h.step(portForwardStartedMsg{err: errString("port 81 is not published")})
	if h.cur().mode != ModePortForwardForm || !strings.Contains(h.cur().pfForm.View(120, 30), "not published") {
		t.Error("start error should stay inside the form")
	}
	// …or in the footer once the form was dismissed.
	h.pressKey("esc")
	h.step(portForwardStartedMsg{err: errString("late failure")})
	if !strings.Contains(h.cur().err, "late failure") {
		t.Errorf("err = %q", h.cur().err)
	}
}

func TestPortForwardFromCompose(t *testing.T) {
	h := forwardHarness(t)
	h.step(switchResourceMsg{ViewCompose})
	h.step(composeUpdatedMsg{h.fb.Composes})
	msg := runCmd(h.pressKey("F"))
	open, ok := msg.(openPortForwardFormMsg)
	if !ok {
		t.Fatalf("F in compose produced %T", msg)
	}
	// Running containers only, those with ports first: web (80), then api.
	if len(open.targets) != 2 || open.targets[0].Name != "web" || open.targets[1].Name != "api" {
		t.Errorf("targets = %+v", open.targets)
	}

	// A project whose containers are all stopped reports an error.
	stopped := docker.NewFakeBackend()
	for i := range stopped.Containers {
		stopped.Containers[i].State = "exited"
	}
	if em, ok := runCmd(composeForwardTargetsCmd(stopped, "/srv/webapp", "webapp")).(errMsg); !ok || !strings.Contains(em.err.Error(), "no running containers") {
		t.Errorf("all-stopped project should error, got %+v", em)
	}
	if em, ok := runCmd(composeForwardTargetsCmd(stopped, "missing", "missing")).(errMsg); !ok || em.err == nil {
		t.Error("list failure should surface as errMsg")
	}
}

func TestPortForwardListOverlay(t *testing.T) {
	h := forwardHarness(t)
	info := startDemoForward(t, h)

	m := h.cur()
	cmd, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "pf"})
	if err != nil {
		t.Fatal(err)
	}
	h.step(runCmd(cmd))
	if h.cur().mode != ModePortForwards {
		t.Fatalf("mode = %v, want ModePortForwards", h.cur().mode)
	}
	view := h.cur().View()
	for _, want := range []string{"Port-forwards", info.LocalAddr(), "web:80", "active", "Stop/Start"} {
		if !strings.Contains(view, want) {
			t.Errorf("overlay missing %q", want)
		}
	}

	// s stops the tunnel (off the event loop), s again resumes it.
	h.step(runCmd(h.pressKey("s")))
	if got, _ := h.cur().pf.Get(info.ID); got.State != portfwd.Stopped {
		t.Errorf("state after s = %v, want stopped", got.State)
	}
	if !strings.Contains(h.cur().View(), "stopped") {
		t.Error("overlay should show the stopped state")
	}
	h.step(runCmd(h.pressKey("s")))
	if got, _ := h.cur().pf.Get(info.ID); got.State != portfwd.Active {
		t.Errorf("state after second s = %v, want active", got.State)
	}
	h.step(portForwardChangedMsg{err: errString("resume failed")})
	if !strings.Contains(h.cur().View(), "resume failed") {
		t.Error("toggle error should show in the overlay")
	}

	// d deletes; the list shows the empty hint; q closes.
	h.pressKey("d")
	if h.cur().pf.Len() != 0 {
		t.Fatalf("d should remove the tunnel, %d left", h.cur().pf.Len())
	}
	if !strings.Contains(h.cur().View(), "no tunnels") {
		t.Error("empty overlay should show the hint")
	}
	h.pressKey("q")
	if h.cur().mode != ModeNormal {
		t.Errorf("q should close the overlay, mode = %v", h.cur().mode)
	}
	if row := h.cur().table.SelectedRow(); strings.Contains(row[4], "⇄") {
		t.Errorf("marker should be gone after delete: %q", row[4])
	}
}

func TestPortForwardFailingRowShowsError(t *testing.T) {
	h := forwardHarness(t)
	info := startDemoForward(t, h)
	h.cur().pf.SetDialer(nil) // connection dropped
	conn, err := net.DialTimeout("tcp", info.LocalAddr(), 2*time.Second)
	if err == nil {
		_, _ = conn.Read(make([]byte, 1))
		_ = conn.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := h.cur().pf.Get(info.ID); got.State == portfwd.Failing {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.step(openPortForwardsMsg{})
	if v := h.cur().View(); !strings.Contains(v, "failing") || !strings.Contains(v, "not connected") {
		t.Errorf("overlay should show the failing tunnel and its error:\n%s", v)
	}
}

func TestPortForwardHostSwitchClosesReconnectKeeps(t *testing.T) {
	h := forwardHarness(t)
	startDemoForward(t, h)

	// Auto-reconnect to the same host keeps the tunnels.
	m := h.cur()
	m.reconnecting = true
	h.set(m)
	h.step(reconnectResultMsg{backend: docker.NewFakeBackend()})
	if h.cur().pf.Len() != 1 {
		t.Fatalf("reconnect should keep tunnels, have %d", h.cur().pf.Len())
	}

	// Switching hosts closes them.
	h.step(connectResultMsg{backend: docker.NewFakeBackend(), host: "tcp://other:2375"})
	if h.cur().pf.Len() != 0 {
		t.Errorf("host switch should close tunnels, have %d", h.cur().pf.Len())
	}
}

func TestPortForwardHelpAndKeymap(t *testing.T) {
	if got := keymap.Default().KeyFor(keymap.PortForward); got != "F" {
		t.Errorf("default port-forward key = %q, want F", got)
	}
	h := forwardHarness(t)
	if help := h.cur().buildHelpContent(); !strings.Contains(help, "Port-forward") || !strings.Contains(help, ":portforward :pf") {
		t.Error("help should document the port-forward key and :pf")
	}
}

func TestComposeForwardTargets(t *testing.T) {
	cs := []docker.Container{
		{ID: "1", Name: "worker", State: "running"},
		{ID: "2", Name: "db", State: "exited", Ports: "5432/tcp"},
		{ID: "3", Name: "web", State: "running", Ports: "8080->80/tcp"},
	}
	got := composeForwardTargets(cs)
	if len(got) != 2 || got[0].Name != "web" || got[1].Name != "worker" {
		t.Errorf("targets = %+v, want web then worker", got)
	}
	if len(got[0].Ports) != 1 || got[0].Ports[0] != 80 {
		t.Errorf("web ports = %v, want [80]", got[0].Ports)
	}
}

func TestForwardMarkers(t *testing.T) {
	if forwardMarkers(nil) != nil {
		t.Error("no tunnels → nil markers")
	}
	got := forwardMarkers(map[string][]int{"a": {8080}, "b": {3000, 3001}})
	if got["a"] != "⇄:8080" || got["b"] != "⇄:3000,:3001" {
		t.Errorf("markers = %v", got)
	}
}

// TestDemo_PortForwardTeatest drives the real program: F opens the form over
// the containers view and Enter opens the tunnel, announced in the footer.
func TestDemo_PortForwardTeatest(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "web", "nginx:1.25")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	waitFor(t, tm, "Port-forward", "Container port", "Local port")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "⇄ 127.0.0.1:", "web:80")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if !ok {
		t.Fatal("final model is not a Model")
	}
	if final.pf.Len() != 1 {
		t.Errorf("tunnels = %d, want 1", final.pf.Len())
	}
	final.pf.CloseAll()
}
