package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
)

func TestWrapSegments(t *testing.T) {
	segs := []string{"aaaa", "bbb", "cc", "dddd"} // widths 4,3,2,4

	tests := []struct {
		name     string
		w, max   int
		want     []string
		wantCuts bool
	}{
		{"all on one line", 20, 3, []string{"aaaabbbccdddd"}, false},
		{"exact fit", 13, 3, []string{"aaaabbbccdddd"}, false},
		{"wraps between segments", 9, 3, []string{"aaaabbbcc", "dddd"}, false},
		{"one per line", 4, 4, []string{"aaaa", "bbb", "cc", "dddd"}, false},
		{"line cap adds ellipsis", 9, 1, []string{"aaaabbbcc"}, true},
		{"unknown width keeps one line", 0, 3, []string{"aaaabbbccdddd"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapSegments(segs, tt.w, tt.max)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i, l := range got {
				plain := ansi.Strip(l)
				if tt.wantCuts && i == len(got)-1 {
					if !strings.HasSuffix(plain, footerEllipsis) {
						t.Errorf("last line %q lacks the ellipsis", plain)
					}
				} else if plain != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, plain, tt.want[i])
				}
				if tt.w > 0 && lipgloss.Width(l) > tt.w {
					t.Errorf("line %d is %d cells, wider than %d", i, lipgloss.Width(l), tt.w)
				}
			}
		})
	}
}

func TestWrapSegmentsTruncatesOversized(t *testing.T) {
	got := wrapSegments([]string{"x", strings.Repeat("e", 30), "y"}, 10, 3)
	want := []string{"x", "eeeeeeeee…", "y"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if p := ansi.Strip(got[i]); p != want[i] {
			t.Errorf("line %d = %q, want %q", i, p, want[i])
		}
	}
}

func TestCmdHints(t *testing.T) {
	got := cmdHints("run", "rm [-f]", "prune")
	want := []string{"  run ·", " rm [-f] ·", " prune  "}
	if len(got) != len(want) {
		t.Fatalf("got %d segments, want %d", len(got), len(want))
	}
	for i := range want {
		if p := ansi.Strip(got[i]); p != want[i] {
			t.Errorf("segment %d = %q, want %q", i, p, want[i])
		}
	}
	if one := ansi.Strip(strings.Join(cmdHints("create"), "")); one != "  create  " {
		t.Errorf("single command = %q", one)
	}
}

func TestMaxFooterLines(t *testing.T) {
	for _, tt := range []struct{ h, want int }{{40, 3}, {20, 3}, {19, 2}, {12, 2}, {11, 1}, {5, 1}} {
		if got := (Model{height: tt.h}).maxFooterLines(); got != tt.want {
			t.Errorf("height %d: maxFooterLines = %d, want %d", tt.h, got, tt.want)
		}
	}
}

// On a narrow window the footer wraps instead of losing its trailing hints,
// and the body shrinks so the frame still fits the window exactly.
func TestFooterWrapsOnNarrowWindow(t *testing.T) {
	m := NewModel(&config.Config{Demo: true}, docker.NewFakeBackend(), &hosts.Store{}, nil, false)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 400, Height: 30})
	m = nm.(Model)
	if m.footerH != 1 {
		t.Fatalf("wide window: footerH = %d, want 1", m.footerH)
	}
	wideBody := m.bodyHeight()

	nm, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	m = nm.(Model)
	if m.footerH < 2 || m.footerH > m.maxFooterLines() {
		t.Fatalf("narrow window: footerH = %d, want 2..%d", m.footerH, m.maxFooterLines())
	}
	if m.bodyHeight() != wideBody-(m.footerH-1) {
		t.Errorf("bodyHeight = %d, want %d", m.bodyHeight(), wideBody-(m.footerH-1))
	}
	footer := ansi.Strip(m.viewFooter())
	for _, want := range []string{"Navigate", "Inspect", "Logs"} {
		if !strings.Contains(footer, want) {
			t.Errorf("wrapped footer lost %q: %q", want, footer)
		}
	}
	for i, l := range strings.Split(m.viewFooter(), "\n") {
		if lipgloss.Width(l) != 70 {
			t.Errorf("footer line %d is %d cells, want 70", i, lipgloss.Width(l))
		}
	}
	if got := strings.Count(m.View(), "\n") + 1; got != m.height {
		t.Errorf("View() has %d lines, want %d", got, m.height)
	}

	// Entering command mode changes the footer without a resize: Update must
	// re-lay the body out to the new footer height.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = nm.(Model)
	if m.footerH != len(m.footerLines()) {
		t.Errorf("footerH = %d after mode change, footer has %d lines", m.footerH, len(m.footerLines()))
	}
	if !strings.Contains(ansi.Strip(m.viewFooter()), "cp <local> <ctr-dir>") {
		t.Errorf("command footer lost the tail of the list: %q", ansi.Strip(m.viewFooter()))
	}
	if got := strings.Count(m.View(), "\n") + 1; got != m.height {
		t.Errorf("command mode: View() has %d lines, want %d", got, m.height)
	}
}
