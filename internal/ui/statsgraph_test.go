package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kirg0/d9c/internal/docker"
)

func TestRecordStatsHistory(t *testing.T) {
	containers := []docker.Container{{ID: "a", State: "running"}, {ID: "b", State: "running"}}

	hist := recordStatsHistory(nil, map[string]docker.ContainerStats{
		"a": {CPUPerc: 1, MemUsage: 10},
		"b": {CPUPerc: 2, MemUsage: 20},
	}, containers, 3)
	// "b" misses the next batch: no stale point is repeated for it.
	hist = recordStatsHistory(hist, map[string]docker.ContainerStats{
		"a": {CPUPerc: 3, MemUsage: 30},
	}, containers, 3)

	if got := hist["a"].cpu.Values(); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("a cpu = %v, want [1 3]", got)
	}
	if got := hist["a"].mem.Values(); len(got) != 2 || got[1] != 30 {
		t.Errorf("a mem = %v, want [10 30]", got)
	}
	if got := hist["b"].cpu.Values(); len(got) != 1 || got[0] != 2 {
		t.Errorf("b cpu = %v, want [2]", got)
	}

	// Capacity bounds the history.
	for range 5 {
		hist = recordStatsHistory(hist, map[string]docker.ContainerStats{"a": {CPUPerc: 9}}, containers, 3)
	}
	if n := hist["a"].cpu.Len(); n != 3 {
		t.Errorf("a history len = %d, want capacity 3", n)
	}

	// A container that disappears from the list loses its history; one with no
	// sample ever gets no entry.
	hist = recordStatsHistory(hist, nil, []docker.Container{{ID: "a"}, {ID: "c"}}, 3)
	if _, ok := hist["b"]; ok {
		t.Error("history of removed container b kept")
	}
	if _, ok := hist["c"]; ok {
		t.Error("history created for c without a sample")
	}
	if _, ok := hist["a"]; !ok {
		t.Error("history of listed container a dropped")
	}
}

func TestStatsGraphRows(t *testing.T) {
	tests := []struct {
		h, want int
	}{
		{10, 0}, {15, 0}, {16, 2}, {29, 2}, {30, 3}, {60, 3},
	}
	for _, tt := range tests {
		if got := statsGraphRows(tt.h); got != tt.want {
			t.Errorf("statsGraphRows(%d) = %d, want %d", tt.h, got, tt.want)
		}
	}
}

func TestStatsPanelHeight(t *testing.T) {
	tests := []struct {
		name      string
		resource  ResourceView
		statsView bool
		width     int
		height    int
		want      int
	}{
		{"default layout", ViewContainers, false, 120, 40, 0},
		{"other resource", ViewImages, true, 120, 40, 0},
		{"tall window", ViewContainers, true, 120, 40, 7},
		{"medium window", ViewContainers, true, 120, 20, 5},
		{"short window", ViewContainers, true, 120, 12, 0},
		{"narrow window", ViewContainers, true, 30, 40, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{resource: tt.resource, statsView: tt.statsView, width: tt.width, height: tt.height}
			if got := m.statsPanelHeight(); got != tt.want {
				t.Errorf("statsPanelHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRenderStatsPanel(t *testing.T) {
	h := recordStatsHistory(nil, map[string]docker.ContainerStats{"a": {CPUPerc: 4, MemUsage: 48 << 20}},
		[]docker.Container{{ID: "a"}}, statsHistoryLen)
	h = recordStatsHistory(h, map[string]docker.ContainerStats{"a": {CPUPerc: 2, MemUsage: 32 << 20}},
		[]docker.Container{{ID: "a"}}, statsHistoryLen)

	tests := []struct {
		name   string
		cname  string
		hist   *statsHistory
		width  int
		rows   int
		lines  int
		expect []string
	}{
		{name: "single row", cname: "web", hist: h["a"], width: 80, rows: 1, lines: 3,
			expect: []string{"web · CPU/MEM · 2/120 samples", "CPU", "2.0%", "max 4.0%", "MEM", "32.0 MB", "max 48.0 MB"}},
		{name: "two rows show min", cname: "web", hist: h["a"], width: 80, rows: 2, lines: 5,
			expect: []string{"max 48.0 MB", "min 32.0 MB"}},
		{name: "tall chart", cname: "web", hist: h["a"], width: 80, rows: 3, lines: 7,
			expect: []string{"CPU/MEM", "max 4.0%", "max 48.0 MB", "min 32.0 MB"}},
		{name: "no samples yet", cname: "db", hist: nil, width: 60, rows: 1, lines: 3,
			expect: []string{"db · CPU/MEM · 0/120 samples", "max -"}},
		{name: "no selection", cname: "", hist: nil, width: 60, rows: 1, lines: 3,
			expect: []string{"no container selected"}},
		{name: "long name truncated to width", cname: strings.Repeat("x", 100), hist: nil, width: 50, rows: 1, lines: 3,
			expect: []string{"…"}},
		{name: "too narrow", cname: "web", hist: h["a"], width: 20, rows: 1, lines: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderStatsPanel(tt.cname, tt.hist, tt.width, tt.rows)
			if tt.lines == 0 {
				if out != "" {
					t.Fatalf("want empty panel, got %q", out)
				}
				return
			}
			lines := strings.Split(out, "\n")
			if len(lines) != tt.lines {
				t.Fatalf("got %d lines, want %d:\n%s", len(lines), tt.lines, out)
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w != tt.width {
					t.Errorf("line %d width = %d, want %d: %q", i, w, tt.width, l)
				}
			}
			for _, s := range tt.expect {
				if !strings.Contains(out, s) {
					t.Errorf("panel lacks %q:\n%s", s, out)
				}
			}
		})
	}
}

// TestDemo_ContainerStatsGraph toggles the stats view with 's' and checks the
// CPU/MEM graph panel renders under the table for the selected container.
func TestDemo_ContainerStatsGraph(t *testing.T) {
	tm := newTestModel(t)
	waitFor(t, tm, "2.5%")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	waitFor(t, tm, "CPU/MEM", "samples", "max 2.5%", "max 48.0 MB")
	tm.Quit()
}
