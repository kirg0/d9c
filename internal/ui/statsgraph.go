package ui

import (
	"fmt"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/i18n"
	"github.com/kirg0/d9c/internal/ui/statgraph"
	"github.com/kirg0/d9c/internal/ui/styles"

	"github.com/charmbracelet/lipgloss"
)

const (
	// statsHistoryLen is how many samples per container the CPU/MEM graphs keep
	// (at the default 3s refresh — the last 6 minutes).
	statsHistoryLen = 120
	// statsGraphLeftW/RightW are the widths of the graph gutters: the metric
	// label with its current value ("MEM 1023.9 MB") and the peak ("max …").
	statsGraphLeftW  = 14
	statsGraphRightW = 14
	// statsGraphMinW is the narrowest chart worth drawing; below it the panel
	// is hidden and the table keeps the whole body.
	statsGraphMinW = 10
	// statsCPUScaleFloor keeps near-idle CPU (fractions of a percent) from
	// being blown up to full-height bars by the auto-scaling.
	statsCPUScaleFloor = 1.0
)

// statsHistory is the rolling CPU/MEM sample history of one container.
type statsHistory struct {
	cpu *statgraph.Ring // CPU %
	mem *statgraph.Ring // memory usage, bytes
}

// recordStatsHistory appends the fresh samples to their containers' histories
// (creating one of the given capacity on first sight) and drops the histories
// of containers no longer listed. Only fresh samples are recorded — a container
// missing from this batch gets no point, rather than a repeated stale one.
func recordStatsHistory(hist map[string]*statsHistory, fresh map[string]docker.ContainerStats, containers []docker.Container, capacity int) map[string]*statsHistory {
	out := make(map[string]*statsHistory, len(containers))
	for _, c := range containers {
		h := hist[c.ID]
		s, ok := fresh[c.ID]
		if ok && h == nil {
			h = &statsHistory{cpu: statgraph.NewRing(capacity), mem: statgraph.NewRing(capacity)}
		}
		if ok {
			h.cpu.Push(s.CPUPerc)
			h.mem.Push(float64(s.MemUsage))
		}
		if h != nil {
			out[c.ID] = h
		}
	}
	return out
}

// statsGraphRows picks the height of each chart for a body of h lines: 3 rows
// on tall windows, 2 on medium ones (a Braille row holds only 4 dot levels, so
// a single row is too coarse), none (0) when the table would be left too
// little room.
func statsGraphRows(h int) int {
	switch {
	case h >= 30:
		return 3
	case h >= 16:
		return 2
	}
	return 0
}

// statsGraphWidth returns the chart width that fits a panel of width w.
func statsGraphWidth(w int) int {
	return w - statsGraphLeftW - statsGraphRightW - 2
}

// statsPanelHeight returns the number of lines the CPU/MEM graph panel takes
// under the containers table (a title plus two charts), or 0 when it is not
// shown: outside the stats view, or when the window is too small.
func (m Model) statsPanelHeight() int {
	if m.resource != ViewContainers || !m.statsView {
		return 0
	}
	if statsGraphWidth(m.width) < statsGraphMinW {
		return 0
	}
	rows := statsGraphRows(m.height - 2)
	if rows == 0 {
		return 0
	}
	return 1 + 2*rows
}

// viewStatsPanel renders the graph panel for the container under the cursor.
func (m Model) viewStatsPanel() string {
	rows := statsGraphRows(m.height - 2)
	id := m.selectedID()
	name := ""
	for _, c := range m.containers {
		if c.ID == id {
			name = c.Name
			break
		}
	}
	if name == "" {
		name = id
	}
	return renderStatsPanel(name, m.statsHist[id], m.width, rows)
}

// renderStatsPanel draws the CPU/MEM history panel of one container: a title
// rule, then a chart per metric, each rows lines tall and width cells wide in
// total (label + current value on the left, peak on the right). A nil history
// renders empty charts with "-" values, keeping the panel height stable.
func renderStatsPanel(name string, h *statsHistory, width, rows int) string {
	gw := statsGraphWidth(width)
	if gw < 1 || rows < 1 {
		return ""
	}

	n, capacity := 0, statsHistoryLen
	if h != nil {
		n, capacity = h.cpu.Len(), h.cpu.Cap()
	}
	var title string
	if name == "" {
		title = i18n.T("контейнер не выбран", "no container selected")
	} else {
		title = fmt.Sprintf("%s · CPU/MEM · %d/%d %s", truncateRunes(name, max(width/2, 1)),
			n, capacity, i18n.T("замеров", "samples"))
	}
	title = truncateRunes("── "+title+" ", width)
	title += strings.Repeat("─", max(width-lipgloss.Width(title), 0))

	cpu := chartSpec{label: "CPU", now: "-", top: "max -", bar: styles.GraphCPU}
	mem := chartSpec{label: "MEM", now: "-", top: "max -", bar: styles.GraphMem}
	if h != nil && n > 0 {
		// CPU bars stand on 0%, so their height reads as load; memory rarely
		// moves far from its floor, so it is scaled over its min..max range
		// (labelled on taller charts) to make growth and leaks visible.
		cpu.values, mem.values = h.cpu.Values(), h.mem.Values()
		c, _ := h.cpu.Last()
		mv, _ := h.mem.Last()
		cpuPeak, memPeak, memLow := h.cpu.Peak(), h.mem.Peak(), statgraph.Min(mem.values)
		cpu.now = fmt.Sprintf("%.1f%%", c)
		cpu.top = fmt.Sprintf("max %.1f%%", cpuPeak)
		cpu.hi = max(cpuPeak, statsCPUScaleFloor)
		mem.now = memString(mv)
		mem.top = "max " + memString(memPeak)
		mem.bottom = "min " + memString(memLow)
		mem.lo, mem.hi = memLow, memPeak
	}

	lines := []string{styles.GraphTitle.Render(title)}
	lines = append(lines, cpu.lines(gw, rows)...)
	lines = append(lines, mem.lines(gw, rows)...)
	return strings.Join(lines, "\n")
}

// memString formats a byte count like the MEM column ("48.0 MB").
func memString(b float64) string {
	return docker.ContainerStats{MemUsage: uint64(max(b, 0))}.MemString()
}

// chartSpec describes one metric's chart in the stats graph panel.
type chartSpec struct {
	label  string         // metric name, e.g. "CPU"
	now    string         // current value, shown after the label
	top    string         // right gutter of the top row ("max …")
	bottom string         // right gutter of the bottom row on taller charts ("min …")
	values []float64      // history, oldest first
	lo, hi float64        // value range mapped to the chart's baseline..top
	bar    lipgloss.Style // bar color
}

// lines lays out the chart rows (rows tall, gw cells wide) with their gutters:
// the label and current value on the top row's left, top/bottom on the right.
func (c chartSpec) lines(gw, rows int) []string {
	out := make([]string, 0, rows)
	for i, row := range statgraph.Render(c.values, gw, rows, c.lo, c.hi) {
		left := strings.Repeat(" ", statsGraphLeftW)
		right := ""
		switch i {
		case 0:
			left = styles.GraphLabel.Render(fmt.Sprintf(" %-4s", c.label)) +
				styles.GraphValue.Render(fmt.Sprintf("%-*s", statsGraphLeftW-5, c.now))
			right = c.top
		case rows - 1:
			right = c.bottom
		}
		out = append(out, left+" "+c.bar.Render(row)+" "+
			styles.GraphPeak.Render(fmt.Sprintf("%-*s", statsGraphRightW, right)))
	}
	return out
}
