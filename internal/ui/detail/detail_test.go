package detail

import (
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"

	tea "github.com/charmbracelet/bubbletea"
)

func sample() *docker.InspectResult {
	return &docker.InspectResult{
		Name: "web",
		RawYAML: strings.Join([]string{
			"name: web",
			"running: true",
			"exit_code: 0",
			"labels:",
			"  - app=web",
			"parent: null",
			"plain text line",
		}, "\n"),
	}
}

func keyRunes(m Model, s string) Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestContainerName(t *testing.T) {
	m := New()
	if got := m.ContainerName(); got != "…" {
		t.Errorf("name without resource = %q, want …", got)
	}
	m.SetSize(80, 24)
	m.SetContent(sample())
	if got := m.ContainerName(); got != "web" {
		t.Errorf("name = %q, want web", got)
	}
}

func TestSetContentResetsSearch(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.SetContent(sample())
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = keyRunes(m, "web")
	if !m.IsSearching() || !m.HasSearch() {
		t.Fatal("expected active search with query")
	}
	m.SetContent(sample())
	if m.IsSearching() || m.HasSearch() || len(m.matches) != 0 {
		t.Error("SetContent should reset search state")
	}
}

func TestVpHeight(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	if got := m.vpHeight(); got != 23 {
		t.Errorf("vpHeight = %d, want 23 (height-scrollbar)", got)
	}
	m.searching = true
	if got := m.vpHeight(); got != 22 {
		t.Errorf("vpHeight while searching = %d, want 22", got)
	}
	m.height = 1
	if got := m.vpHeight(); got != 1 {
		t.Errorf("vpHeight floor = %d, want 1", got)
	}
}

func TestSearchFlow(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.SetContent(sample())

	// Open search, type query with two matching lines ("web").
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.IsSearching() {
		t.Fatal("/ should open the search bar")
	}
	m = keyRunes(m, "web")
	if len(m.matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(m.matches))
	}

	// Enter closes the bar but keeps the query.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.IsSearching() || !m.HasSearch() {
		t.Fatal("enter should close bar and keep query")
	}

	// n / N cycle through matches with wrap-around.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.matchIdx != 1 {
		t.Errorf("matchIdx after n = %d, want 1", m.matchIdx)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.matchIdx != 0 {
		t.Errorf("matchIdx after wrap = %d, want 0", m.matchIdx)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if m.matchIdx != 1 {
		t.Errorf("matchIdx after N = %d, want 1", m.matchIdx)
	}

	// esc with an active query clears it (stays inside detail).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.HasSearch() || len(m.matches) != 0 {
		t.Error("esc should clear the finished search")
	}

	// esc without a query falls through to the viewport (no state change).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.IsSearching() || m.HasSearch() {
		t.Error("esc without query should be a no-op for search state")
	}
}

func TestSearchEscWhileTyping(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.SetContent(sample())
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = keyRunes(m, "web")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.IsSearching() || m.HasSearch() || len(m.matches) != 0 {
		t.Error("esc while typing should cancel the whole search")
	}
}

func TestStepMatchNoMatches(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.SetContent(sample())
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.matchIdx != 0 {
		t.Error("n without matches should be a no-op")
	}
}

func TestRecomputeMatchesEmptyQuery(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.SetContent(sample())
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = keyRunes(m, "w")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if len(m.matches) != 0 {
		t.Error("empty query should clear matches")
	}
}

func TestNonKeyMsgGoesToViewport(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.SetContent(sample())
	if m, _ = m.Update(tea.MouseMsg{}); m.resource == nil {
		t.Error("non-key msg should pass through without losing state")
	}
}

func TestViewStates(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	if got := m.View(); !strings.Contains(got, "Loading…") {
		t.Error("view without resource should show Loading…")
	}

	m.SetContent(sample())
	if got := m.View(); !strings.Contains(got, "END") && !strings.Contains(got, "%") {
		t.Error("view should render a scroll indicator")
	}

	// Search bar with match counter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = keyRunes(m, "web")
	if got := m.View(); !strings.Contains(got, "1/2") {
		t.Error("view should show the match counter")
	}
	m = keyRunes(m, "zzz")
	if got := m.View(); !strings.Contains(got, "no match") {
		t.Error("view should show 'no match' for a fruitless query")
	}

	// Narrow window: width guards must not panic.
	m.SetSize(3, 2)
	_ = m.View()
}

func TestRenderContentNilResource(t *testing.T) {
	m := New()
	if got := m.renderContent(); got != "" {
		t.Errorf("renderContent without resource = %q, want empty", got)
	}
}

func TestColorizeLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string // substring that must survive styling
	}{
		{"key-value", "name: web", "web"},
		{"list item", "  - app=web", "app=web"},
		{"plain", "plain text", "plain text"},
		{"bool", "running: true", "true"},
		{"null", "parent: null", "null"},
		{"number", "exit_code: 42", "42"},
		{"negative float", "score: -1.5", "-1.5"},
		{"empty value", "labels:", "labels"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := colorizeLine(tt.line); !strings.Contains(got, tt.want) {
				t.Errorf("colorizeLine(%q) = %q, want it to contain %q", tt.line, got, tt.want)
			}
		})
	}
}
