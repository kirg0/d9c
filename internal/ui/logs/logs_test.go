package logs

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func typeRunes(m Model, s string) Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestLogsSearch(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open("web")
	m.AddLine("2024 INFO started")
	m.AddLine("2024 ERROR boom")
	m.AddLine("2024 INFO ok")

	// Open search and type a query matching two lines.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.IsSearching() {
		t.Fatal("expected IsSearching after '/'")
	}
	m = typeRunes(m, "info")
	if !m.HasSearch() {
		t.Fatal("expected HasSearch after typing query")
	}
	if v := m.View(); !strings.Contains(v, "1/2") {
		t.Errorf("expected match counter 1/2 in view:\n%s", v)
	}

	// Confirm keeps the query but leaves the input.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.IsSearching() {
		t.Error("should not be searching after enter")
	}
	if !m.HasSearch() {
		t.Error("query should persist after enter")
	}

	// Esc clears the active search.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.HasSearch() {
		t.Error("esc should clear the search")
	}
}

// colorizeLogLine maps keyword offsets found in the ToUpper'd copy back onto
// the original string; when the case mapping changes a rune's byte width that
// mapping is invalid and must fall back to whole-line colouring, not corrupt
// the output or panic.
func TestColorizeLogLineCaseWidthChange(t *testing.T) {
	// 'ɱ' (U+0271, 2 bytes) uppercases to 'Ɱ' (U+2C6E, 3 bytes), so the
	// ToUpper'd copy is longer than the original.
	line := "ɱɱɱ error after width-changing runes"
	got := colorizeLogLine(line) // must not panic
	if !strings.Contains(got, "width-changing runes") {
		t.Errorf("colorized line lost content: %q", got)
	}

	// Plain ASCII lines keep the in-line keyword highlight path.
	if got := colorizeLogLine("plain ERROR here"); !strings.Contains(got, "ERROR") {
		t.Errorf("ASCII line lost keyword: %q", got)
	}
}

func TestLogsSearchNoMatch(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open("web")
	m.AddLine("hello")
	m.AddLine("world")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeRunes(m, "zzz")
	if v := m.View(); !strings.Contains(v, "no match") {
		t.Errorf("expected 'no match' in view:\n%s", v)
	}
}
