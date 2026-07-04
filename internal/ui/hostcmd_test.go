package ui

import (
	"errors"
	"strings"
	"testing"

	"d9c/internal/hosts"
	"d9c/internal/ui/cmdline"
)

// hostsModel builds a sized model in the hosts view with a working store.
func hostsModel(t *testing.T, persistErr error) (*modelHarness, *hosts.Store) {
	t.Helper()
	store := hosts.NewStore(
		[]hosts.Host{{Name: "lab", Host: "ssh://me@lab"}, {Name: "prod", Host: "tcp://prod:2375"}},
		func([]hosts.Host) error { return persistErr },
	)
	h := newSizedModel(t)
	m := h.cur()
	m.hostStore = store
	m.resource = ViewHosts
	m.hosts = store.List()
	m.refreshTableRows()
	h.set(m)
	return h, store
}

func TestDispatchHostCommands(t *testing.T) {
	h, store := hostsModel(t, nil)
	m := h.cur()

	// connect on the selected host emits a connect request.
	cmd, err := m.dispatchHostCommand(&cmdline.CommandMsg{Name: "connect"})
	if err != nil || cmd == nil {
		t.Fatalf("connect: %v", err)
	}
	if req, ok := cmd().(connectRequestMsg); !ok || req.host.Name != "lab" {
		t.Errorf("connect msg = %#v", cmd())
	}

	// add opens an empty form.
	cmd, err = m.dispatchHostCommand(&cmdline.CommandMsg{Name: "add"})
	if err != nil || cmd == nil {
		t.Fatalf("add: %v", err)
	}
	if f, ok := cmd().(openHostFormMsg); !ok || f.editing {
		t.Errorf("add msg = %#v", cmd())
	}

	// edit opens the form pre-filled with the selection.
	cmd, err = m.dispatchHostCommand(&cmdline.CommandMsg{Name: "edit"})
	if err != nil || cmd == nil {
		t.Fatalf("edit: %v", err)
	}
	if f, ok := cmd().(openHostFormMsg); !ok || !f.editing || f.host.Name != "lab" {
		t.Errorf("edit msg = %#v", cmd())
	}

	// rm removes and persists.
	cmd, err = m.dispatchHostCommand(&cmdline.CommandMsg{Name: "rm"})
	if err != nil || cmd == nil {
		t.Fatalf("rm: %v", err)
	}
	if _, ok := store.Find("lab"); ok {
		t.Error("rm should remove the host from the store")
	}

	// Unknown command.
	if _, err := m.dispatchHostCommand(&cmdline.CommandMsg{Name: "zzz"}); err == nil {
		t.Error("unknown command should fail")
	}
}

func TestDispatchHostCommandsNoSelection(t *testing.T) {
	h := newSizedModel(t)
	m := h.cur()
	m.hostStore = hosts.NewStore(nil, func([]hosts.Host) error { return nil })
	m.resource = ViewHosts
	m.hosts = nil
	m.refreshTableRows()
	for _, name := range []string{"connect", "edit", "rm"} {
		if _, err := m.dispatchHostCommand(&cmdline.CommandMsg{Name: name}); err == nil {
			t.Errorf("%s without selection should fail", name)
		}
	}
}

func TestSaveHostsThenRefreshError(t *testing.T) {
	h, _ := hostsModel(t, errors.New("disk full"))
	m := h.cur()
	if _, err := m.dispatchHostCommand(&cmdline.CommandMsg{Name: "rm"}); err == nil ||
		!strings.Contains(err.Error(), "disk full") {
		t.Errorf("rm with failing persist = %v, want disk full", err)
	}
}

// fakeNSBase is a canned docker.NamespacedBackend implementation.
type fakeNSBase struct {
	names   []string
	current string
	err     error
}

func (f *fakeNSBase) Namespaces() ([]string, error) { return f.names, f.err }
func (f *fakeNSBase) CurrentNamespace() string      { return f.current }
func (f *fakeNSBase) SetNamespace(name string)      { f.current = name }

func TestLoadNamespacesCmd(t *testing.T) {
	nb := &fakeNSBase{names: []string{"default", "k8s.io"}, current: "default"}
	msg := loadNamespacesCmd(nb)()
	loaded, ok := msg.(namespacesLoadedMsg)
	if !ok {
		t.Fatalf("msg = %#v", msg)
	}
	if len(loaded.names) != 2 || loaded.current != "default" || loaded.err != nil {
		t.Errorf("loaded = %+v", loaded)
	}

	nb.err = errors.New("boom")
	if loaded := loadNamespacesCmd(nb)().(namespacesLoadedMsg); loaded.err == nil {
		t.Error("error should propagate")
	}
}
