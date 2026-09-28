package pfform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m, _ = m.Update(key(string(r)))
	}
	return m
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		in         string
		allowEmpty bool
		want       int
		wantErr    bool
	}{
		{"80", false, 80, false},
		{" 8080 ", false, 8080, false},
		{"65535", false, 65535, false},
		{"", true, 0, false},
		{"", false, 0, true},
		{"0", false, 0, true},
		{"65536", true, 0, true},
		{"http", true, 0, true},
		{"-1", false, 0, true},
	}
	for _, tt := range tests {
		got, err := ParsePort(tt.in, tt.allowEmpty)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParsePort(%q, %v) = %d, %v; want %d, err=%v", tt.in, tt.allowEmpty, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestOpenSingleTargetPrefillsAndFocusesPort(t *testing.T) {
	m := New()
	m.Open([]Target{{ID: "c1", Name: "web", Ports: []int{80, 443}}})
	if m.focus != fieldPort {
		t.Errorf("focus = %d, want the port field (no target choice)", m.focus)
	}
	cp, lp, err := m.Values()
	if err != nil || cp != 80 || lp != 0 {
		t.Fatalf("Values = %d, %d, %v; want 80, 0", cp, lp, err)
	}
	// Tab skips the (single-choice) target selector: port → local → port.
	m.Next()
	if m.focus != fieldLocal {
		t.Errorf("after Next focus = %d, want local", m.focus)
	}
	m.Next()
	if m.focus != fieldPort {
		t.Errorf("Next should wrap past the target selector, focus = %d", m.focus)
	}
	m.Prev()
	if m.focus != fieldLocal {
		t.Errorf("Prev should wrap past the target selector, focus = %d", m.focus)
	}
	m = typeText(m, "9090")
	if _, lp, _ := m.Values(); lp != 9090 {
		t.Errorf("local = %d, want 9090", lp)
	}
}

func TestMultipleTargetsCycleAndPrefill(t *testing.T) {
	m := New()
	m.Open([]Target{
		{ID: "a", Name: "web", Ports: []int{80}},
		{ID: "b", Name: "db", Ports: []int{5432}},
		{ID: "c", Name: "worker"},
	})
	if m.focus != fieldTarget {
		t.Fatalf("focus = %d, want the target selector", m.focus)
	}
	m, _ = m.Update(key("right"))
	if tgt, _ := m.Target(); tgt.ID != "b" {
		t.Errorf("target = %q, want b", tgt.ID)
	}
	if cp, _, _ := m.Values(); cp != 5432 {
		t.Errorf("port pre-fill = %d, want 5432", cp)
	}
	m, _ = m.Update(key("right"))
	if _, _, err := m.Values(); err == nil {
		t.Error("a target without known ports leaves the port empty (required)")
	}
	m, _ = m.Update(key("right")) // wraps to a
	m, _ = m.Update(key("left"))  // back to c
	if tgt, _ := m.Target(); tgt.ID != "c" {
		t.Errorf("target = %q, want c", tgt.ID)
	}
	m.Next()
	if m.focus != fieldPort {
		t.Errorf("focus = %d, want port", m.focus)
	}
	m = typeText(m, "8000")
	if cp, _, _ := m.Values(); cp != 8000 {
		t.Errorf("typed port = %d, want 8000", cp)
	}
}

func TestValuesErrors(t *testing.T) {
	m := New()
	m.Open([]Target{{ID: "c1", Name: "web"}})
	if _, _, err := m.Values(); err == nil || !strings.Contains(err.Error(), "container port") {
		t.Errorf("empty port error = %v", err)
	}
	m = typeText(m, "80")
	m.Next()
	m = typeText(m, "99999")
	if _, _, err := m.Values(); err == nil || !strings.Contains(err.Error(), "local port") {
		t.Errorf("bad local port error = %v", err)
	}
}

func TestBusyIgnoresInputAndErrorClearsBusy(t *testing.T) {
	m := New()
	m.Open([]Target{{ID: "c1", Name: "web", Ports: []int{80}}})
	m.SetBusy(true)
	if !m.Busy() {
		t.Fatal("Busy should report true")
	}
	m = typeText(m, "1")
	if cp, _, _ := m.Values(); cp != 80 {
		t.Errorf("busy form must ignore typing, port = %d", cp)
	}
	if !strings.Contains(m.View(100, 30), "opening the tunnel") {
		t.Error("busy view should say the tunnel is opening")
	}
	m.SetError("boom")
	if m.Busy() {
		t.Error("SetError should clear busy")
	}
	if !strings.Contains(m.View(100, 30), "boom") {
		t.Error("view should render the error")
	}
}

func TestViewShowsTargetAndPorts(t *testing.T) {
	m := New()
	m.Open([]Target{{ID: "a", Name: "web", Ports: []int{80, 443}}, {ID: "b", Name: "db"}})
	v := m.View(120, 30)
	for _, want := range []string{"Port-forward", "web", "(1/2)", "ports: 80, 443", "←/→ container"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q", want)
		}
	}
	var empty Model
	if _, ok := empty.Target(); ok {
		t.Error("zero form has no target")
	}
}
