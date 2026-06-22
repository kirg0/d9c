package pullform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenClearsState(t *testing.T) {
	m := New()
	m.image.SetValue("stale")
	m.SetError("boom")
	m.Open()
	if m.Image() != "" {
		t.Errorf("image after Open = %q, want empty", m.Image())
	}
	if m.errMsg != "" {
		t.Errorf("errMsg after Open = %q, want empty", m.errMsg)
	}
}

func TestTypingRoutesToImage(t *testing.T) {
	m := New()
	m.Open()
	for _, r := range "nginx:1.25" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Image(); got != "nginx:1.25" {
		t.Errorf("image = %q, want nginx:1.25", got)
	}
}

func TestImageTrimsWhitespace(t *testing.T) {
	m := New()
	m.Open()
	m.image.SetValue("  redis  ")
	if got := m.Image(); got != "redis" {
		t.Errorf("image = %q, want redis", got)
	}
}

func TestViewShowsError(t *testing.T) {
	m := New()
	m.Open()
	m.SetError("boom")
	if got := m.View(80, 24); !strings.Contains(got, "boom") {
		t.Error("view should render the error message")
	}
}

func TestPullingShowsBusyStatus(t *testing.T) {
	m := New()
	m.Open()
	m.image.SetValue("nginx:latest")
	if cmd := m.Pulling(); cmd == nil {
		t.Fatal("Pulling should return a spinner tick command")
	}
	if !m.Busy() {
		t.Fatal("form should report busy after Pulling")
	}
	got := m.View(80, 24)
	if !strings.Contains(got, "pulling") || !strings.Contains(got, "nginx:latest") {
		t.Errorf("busy view should mention pulling the image, got:\n%s", got)
	}
}

func TestOpenPullingPrefillsAndBusies(t *testing.T) {
	m := New()
	if cmd := m.OpenPulling("alpine:3.20"); cmd == nil {
		t.Fatal("OpenPulling should return a spinner tick command")
	}
	if !m.Busy() {
		t.Fatal("form should report busy after OpenPulling")
	}
	if got := m.Image(); got != "alpine:3.20" {
		t.Errorf("image = %q, want alpine:3.20", got)
	}
	got := m.View(80, 24)
	if !strings.Contains(got, "pulling") || !strings.Contains(got, "alpine:3.20") {
		t.Errorf("busy view should mention pulling the image, got:\n%s", got)
	}
}

func TestBusyIgnoresTyping(t *testing.T) {
	m := New()
	m.Open()
	m.image.SetValue("redis")
	m.Pulling()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if got := m.Image(); got != "redis" {
		t.Errorf("image changed while busy = %q, want redis", got)
	}
}

func TestSetErrorClearsBusy(t *testing.T) {
	m := New()
	m.Open()
	m.Pulling()
	m.SetError("denied")
	if m.Busy() {
		t.Error("SetError should clear the busy state so the user can retry")
	}
}
