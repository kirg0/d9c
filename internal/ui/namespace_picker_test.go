package ui

import (
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/ui/cmdline"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeNamespacedBackend is a FakeBackend that also implements
// docker.NamespacedBackend, so the :namespace command and picker can be tested
// without a real containerd host.
type fakeNamespacedBackend struct {
	*docker.FakeBackend
	current string
	names   []string
}

func newFakeNamespaced() *fakeNamespacedBackend {
	return &fakeNamespacedBackend{
		FakeBackend: docker.NewFakeBackend(),
		current:     "default",
		names:       []string{"default", "k8s.io"},
	}
}

func (f *fakeNamespacedBackend) Namespaces() ([]string, error) { return f.names, nil }
func (f *fakeNamespacedBackend) CurrentNamespace() string      { return f.current }
func (f *fakeNamespacedBackend) SetNamespace(n string)         { f.current = n }

func TestNamespaceCommandSwitches(t *testing.T) {
	be := newFakeNamespaced()
	m := NewModel(&config.Config{}, be, nil, nil, false)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "namespace", Args: []string{"k8s.io"}}); err != nil {
		t.Fatal(err)
	}
	if be.CurrentNamespace() != "k8s.io" {
		t.Errorf("namespace = %q, want k8s.io", be.CurrentNamespace())
	}
}

func TestNamespaceCommandRejectedWithoutBackend(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "namespace", Args: []string{"x"}}); err == nil {
		t.Error("expected error on non-namespaced backend")
	}
}

func TestNamespacePickerEnterSwitches(t *testing.T) {
	be := newFakeNamespaced()
	m := NewModel(&config.Config{}, be, nil, nil, false)
	m.openNamespacePicker([]string{"default", "k8s.io"}, "default")
	if m.mode != ModeNamespacePicker {
		t.Fatalf("mode = %v, want ModeNamespacePicker", m.mode)
	}
	// Move down to "k8s.io" and commit.
	next, _ := m.handleNamespacePicker(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, _ = m.handleNamespacePicker(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if be.CurrentNamespace() != "k8s.io" {
		t.Errorf("namespace = %q, want k8s.io", be.CurrentNamespace())
	}
	if m.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal", m.mode)
	}
}
