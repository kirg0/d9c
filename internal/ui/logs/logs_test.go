package logs

import (
	"fmt"
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

func TestLogsFollowToggle(t *testing.T) {
	m := New()
	m.SetSize(80, 5)
	m.Open("web")
	if !m.IsFollowing() {
		t.Fatal("logs should follow by default")
	}
	if v := m.View(); !strings.Contains(v, "FOLLOW") {
		t.Errorf("expected FOLLOW badge while following:\n%s", v)
	}

	// Scrolling stops follow; the badge disappears.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.IsFollowing() {
		t.Error("scrolling should disable follow")
	}
	if v := m.View(); strings.Contains(v, "FOLLOW") {
		t.Errorf("FOLLOW badge should be hidden once follow is off:\n%s", v)
	}

	// 'f' re-enables follow and snaps to the bottom.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if !m.IsFollowing() {
		t.Error("'f' should re-enable follow")
	}

	// 'f' again toggles it back off.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.IsFollowing() {
		t.Error("'f' should toggle follow off")
	}
}

// TestAddLinesCapTrimsOldest checks the buffer never exceeds maxBufferLines and
// drops the OLDEST lines when it would, keeping the newest.
func TestAddLinesCapTrimsOldest(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open("web")

	var batch []string
	for i := 0; i < maxBufferLines+500; i++ {
		batch = append(batch, fmt.Sprintf("line %d", i))
		if len(batch) == 500 {
			m.AddLines(batch)
			batch = batch[:0]
		}
	}
	if got := m.LineCount(); got != maxBufferLines {
		t.Fatalf("LineCount = %d, want cap %d", got, maxBufferLines)
	}
	if got, want := m.rawLines[0], "line 500"; got != want {
		t.Errorf("oldest kept line = %q, want %q", got, want)
	}
	if got, want := m.rawLines[len(m.rawLines)-1], fmt.Sprintf("line %d", maxBufferLines+499); got != want {
		t.Errorf("newest line = %q, want %q", got, want)
	}
}

// TestAddLinesBatchLargerThanCap: one batch bigger than the whole cap keeps
// only its tail.
func TestAddLinesBatchLargerThanCap(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open("web")

	batch := make([]string, maxBufferLines+100)
	for i := range batch {
		batch[i] = fmt.Sprintf("l%d", i)
	}
	m.AddLines(batch)
	if got := m.LineCount(); got != maxBufferLines {
		t.Fatalf("LineCount = %d, want cap %d", got, maxBufferLines)
	}
	if got, want := m.rawLines[0], "l100"; got != want {
		t.Errorf("oldest kept line = %q, want %q", got, want)
	}
}

// TestAddLinesSearchIncremental checks that lines streamed in while a search is
// active extend the match list without a full rescan invalidating positions.
func TestAddLinesSearchIncremental(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open("web")
	m.AddLine("ERROR first")
	m.AddLine("plain")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeRunes(m, "error")
	if len(m.matches) != 1 || m.matches[0] != 0 {
		t.Fatalf("matches = %v, want [0]", m.matches)
	}

	m.AddLines([]string{"noise", "ERROR second"})
	if len(m.matches) != 2 || m.matches[1] != 3 {
		t.Fatalf("matches after batch = %v, want [0 3]", m.matches)
	}
}

// TestAddLinesTrimRebasesMatches: when the cap trims lines off the front, match
// indices must shift down and matches that slid out must disappear.
func TestAddLinesTrimRebasesMatches(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.Open("web")

	batch := make([]string, maxBufferLines)
	for i := range batch {
		batch[i] = fmt.Sprintf("line %d", i)
	}
	batch[0] = "ERROR top"
	batch[7000] = "ERROR mid"
	m.AddLines(batch)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeRunes(m, "error")
	if len(m.matches) != 2 || m.matches[0] != 0 || m.matches[1] != 7000 {
		t.Fatalf("matches = %v, want [0 7000]", m.matches)
	}

	// Two more lines push the two oldest out: the match at 0 slides off, the one
	// at 7000 rebases to 6998, and the fresh match lands at the end.
	m.AddLines([]string{"tail", "ERROR fresh"})
	want := []int{6998, maxBufferLines - 1}
	if len(m.matches) != 2 || m.matches[0] != want[0] || m.matches[1] != want[1] {
		t.Fatalf("matches after trim = %v, want %v", m.matches, want)
	}
	if got, want := m.rawLines[6998], "ERROR mid"; got != want {
		t.Errorf("rebased match points at %q, want %q", got, want)
	}
}

// TestShiftMatches covers the index arithmetic in isolation, including the
// current-match cursor following its line.
func TestShiftMatches(t *testing.T) {
	tests := []struct {
		name     string
		matches  []int
		matchIdx int
		n        int
		want     []int
		wantIdx  int
	}{
		{"no trim", []int{2, 5}, 1, 0, []int{2, 5}, 1},
		{"drop none", []int{4, 9}, 1, 3, []int{1, 6}, 1},
		{"drop first, cursor follows", []int{2, 5, 9}, 1, 4, []int{1, 5}, 0},
		{"drop all", []int{1, 2}, 1, 5, []int{}, 0},
		{"cursor at dropped head", []int{0, 8}, 0, 4, []int{4}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.matches = append([]int(nil), tt.matches...)
			m.matchIdx = tt.matchIdx
			m.shiftMatches(tt.n)
			if len(m.matches) != len(tt.want) {
				t.Fatalf("matches = %v, want %v", m.matches, tt.want)
			}
			for i := range tt.want {
				if m.matches[i] != tt.want[i] {
					t.Fatalf("matches = %v, want %v", m.matches, tt.want)
				}
			}
			if m.matchIdx != tt.wantIdx {
				t.Errorf("matchIdx = %d, want %d", m.matchIdx, tt.wantIdx)
			}
		})
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
