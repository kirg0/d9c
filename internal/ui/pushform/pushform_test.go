package pushform

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenPrefills(t *testing.T) {
	m := New()
	m.Open("reg.local/app:v1", "reg.local", "bob", "s3cret")
	if got := m.Ref(); got != "reg.local/app:v1" {
		t.Errorf("ref = %q, want reg.local/app:v1", got)
	}
	if got := m.Registry(); got != "reg.local" {
		t.Errorf("registry = %q, want reg.local", got)
	}
	if got := m.Username(); got != "bob" {
		t.Errorf("username = %q, want bob", got)
	}
	if got := m.Password(); got != "s3cret" {
		t.Errorf("password = %q, want s3cret", got)
	}
	if m.focus != 0 {
		t.Errorf("focus after Open = %d, want 0", m.focus)
	}
}

func TestOpenResetsError(t *testing.T) {
	m := New()
	m.SetError("boom")
	m.Open("app:v1", "", "", "")
	if m.errMsg != "" {
		t.Errorf("errMsg after Open = %q, want empty", m.errMsg)
	}
}

func TestNextPrevWraps(t *testing.T) {
	m := New()
	m.Open("app:v1", "", "", "")
	m.Prev() // wraps to last field
	if m.focus != fieldCount-1 {
		t.Errorf("focus after Prev from 0 = %d, want %d", m.focus, fieldCount-1)
	}
	m.Next() // back to 0
	if m.focus != 0 {
		t.Errorf("focus after Next = %d, want 0", m.focus)
	}
}

func TestTypingRoutesToFocusedField(t *testing.T) {
	m := New()
	m.Open("app:v1", "", "", "")
	for _, r := range "ghcr.io" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Registry(); got != "ghcr.io" {
		t.Errorf("registry = %q, want ghcr.io", got)
	}
	m.Next()
	for _, r := range "alice" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Username(); got != "alice" {
		t.Errorf("username = %q, want alice", got)
	}
	m.Next()
	for _, r := range "tok" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Password(); got != "tok" {
		t.Errorf("password = %q, want tok", got)
	}
}

func TestGettersTrimExceptPassword(t *testing.T) {
	m := New()
	m.Open("app:v1", "  reg  ", "  bob  ", "  tok  ")
	if got := m.Registry(); got != "reg" {
		t.Errorf("Registry should trim, got %q", got)
	}
	if got := m.Username(); got != "bob" {
		t.Errorf("Username should trim, got %q", got)
	}
	if got := m.Password(); got != "  tok  " {
		t.Errorf("Password should stay verbatim, got %q", got)
	}
}

func TestViewShowsFieldsAndError(t *testing.T) {
	m := New()
	m.Open("app:v1", "", "", "s3cret")
	m.SetError("boom")
	got := m.View(80, 24)
	if !strings.Contains(got, "Push: app:v1") ||
		!strings.Contains(got, "Registry") ||
		!strings.Contains(got, "Username") ||
		!strings.Contains(got, "Password") ||
		!strings.Contains(got, "boom") {
		t.Error("view should render title, labels and the error message")
	}
	if strings.Contains(got, "s3cret") {
		t.Error("view must not leak the password in clear text")
	}
	// Marker moves with focus through all fields.
	m.Next()
	m.Next()
	if !strings.Contains(m.View(80, 24), "▸") {
		t.Error("view should render the active-field marker")
	}
}
