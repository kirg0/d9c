package ui

import (
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
)

func TestConnectCmd(t *testing.T) {
	// unix:// with a missing socket fails fast without dialing anything.
	msg := connectCmd(&config.Config{}, "unix://C:/definitely/missing.sock")()
	res, ok := msg.(connectResultMsg)
	if !ok {
		t.Fatalf("msg = %#v", msg)
	}
	if res.err == nil || res.backend != nil || res.host == "" {
		t.Errorf("result = %+v, want error for missing socket", res)
	}

	// A tcp:// backend builds lazily, so the connect itself succeeds.
	msg = connectCmd(&config.Config{}, "tcp://127.0.0.1:1")()
	res = msg.(connectResultMsg)
	if res.err != nil || res.backend == nil {
		t.Errorf("tcp result = %+v, want lazily-built backend", res)
	}
	res.backend.Close()
}

func TestReconnectCmd(t *testing.T) {
	// Attempt 0 clamps the backoff to ~1s; the unreachable port fails the ping.
	msg := reconnectCmd(&config.Config{}, "tcp://127.0.0.1:1", 0)()
	res, ok := msg.(reconnectResultMsg)
	if !ok {
		t.Fatalf("msg = %#v", msg)
	}
	if res.err == nil || res.backend != nil {
		t.Errorf("result = %+v, want ping failure", res)
	}

	// A backend that can't even be built (bad socket) also reports the error.
	msg = reconnectCmd(&config.Config{}, "unix://C:/definitely/missing.sock", 1)()
	if res := msg.(reconnectResultMsg); res.err == nil {
		t.Errorf("result = %+v, want build failure", res)
	}
}

func TestCloseBackendCmd(t *testing.T) {
	if msg := closeBackendCmd(nil)(); msg != nil {
		t.Errorf("nil backend close = %#v, want nil msg", msg)
	}
	if msg := closeBackendCmd(docker.NewFakeBackend())(); msg != nil {
		t.Errorf("fake backend close = %#v, want nil msg", msg)
	}
}

func TestFetchCmds(t *testing.T) {
	fb := docker.NewFakeBackend()
	if msg := fetchNetworks(fb)(); len(msg.(networksUpdatedMsg).networks) == 0 {
		t.Error("fetchNetworks should return demo networks")
	}
	bad := docker.NewDisconnected(nil)
	if _, ok := fetchNetworks(bad)().(errMsg); !ok {
		t.Error("fetchNetworks on disconnected backend should yield errMsg")
	}
	if _, ok := fetchVolumes(bad)().(errMsg); !ok {
		t.Error("fetchVolumes on disconnected backend should yield errMsg")
	}
}

func TestFetchInspectAllResources(t *testing.T) {
	fb := docker.NewFakeBackend()
	cases := []struct {
		res ResourceView
		id  string
	}{
		{ViewContainers, "9ae942fd8fbc"},
		{ViewImages, "a08c488a9779"},
		{ViewNetworks, "f0a1b2c3d4e5"},
		{ViewVolumes, "pgdata"},
		{ViewCompose, "/srv/webapp"},
	}
	for _, c := range cases {
		msg := fetchInspect(fb, c.res, c.id)()
		if _, ok := msg.(inspectResultMsg); !ok {
			t.Errorf("%v: msg = %#v, want inspectResultMsg", c.res, msg)
		}
	}
	if _, ok := fetchInspect(fb, ViewContainers, "ghost")().(errMsg); !ok {
		t.Error("inspect of unknown id should yield errMsg")
	}
}
