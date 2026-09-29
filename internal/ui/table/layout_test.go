package table

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"

	"github.com/charmbracelet/bubbles/table"
)

func TestNormalizeColumn(t *testing.T) {
	tests := []struct{ in, want string }{
		{"NAME", "name"},
		{"cpu %", "cpupct"},
		{"CPU%", "cpupct"},
		{"MEM %", "mempct"},
		{"NET I/O", "netio"},
		{"net_io", "netio"},
		{"REPOSITORY:TAG", "repositorytag"},
		{"  ", ""},
		{"Имя", "имя"},
	}
	for _, tt := range tests {
		if got := normalizeColumn(tt.in); got != tt.want {
			t.Errorf("normalizeColumn(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFindColumn(t *testing.T) {
	stats := ContainerStatsColumns(0)
	images := ImageColumns(0)
	tests := []struct {
		name string
		cols []table.Column
		in   string
		want int
	}{
		{"exact", stats, "NAME", 0},
		{"case-insensitive", stats, "name", 0},
		{"percent spelled out", stats, "cpu %", 1},
		{"percent sign optional", stats, "cpu", 1},
		{"plain MEM beats MEM %", stats, "mem", 2},
		{"MEM %", stats, "mem%", 3},
		{"slash dropped", stats, "net io", 4},
		{"alias net", stats, "net", 4},
		{"alias block", stats, "block", 5},
		{"alias repository", images, "repository", 0},
		{"alias tag", images, "Tag", 0},
		{"full repository:tag", images, "repository:tag", 0},
		{"unknown", images, "PORTS", -1},
		{"empty", images, " ", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findColumn(tt.cols, tt.in); got != tt.want {
				t.Errorf("findColumn(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveLayouts(t *testing.T) {
	tests := []struct {
		name      string
		cfg       map[string][]string
		want      Layouts
		wantWarns []string // substrings, one per expected warning, in order
	}{
		{name: "nil config", cfg: nil, want: nil},
		{name: "empty list keeps default", cfg: map[string][]string{"containers": {}}, want: nil},
		{
			name: "reorder and hide",
			cfg:  map[string][]string{"containers": {"name", "status", "cpu", "mem", "id"}},
			want: Layouts{SectionContainers: {0, 2, 5, 6, 7}},
		},
		{
			name: "section key is case-insensitive",
			cfg:  map[string][]string{"Volumes": {"name", "driver"}},
			want: Layouts{SectionVolumes: {0, 1}},
		},
		{
			name:      "unknown section",
			cfg:       map[string][]string{"pods": {"name"}},
			want:      nil,
			wantWarns: []string{`unknown section "pods"`},
		},
		{
			name:      "unknown column skipped",
			cfg:       map[string][]string{"networks": {"name", "bogus", "id"}},
			want:      Layouts{SectionNetworks: {0, 4}},
			wantWarns: []string{`unknown column "bogus"`},
		},
		{
			name:      "duplicate column skipped",
			cfg:       map[string][]string{"networks": {"name", "NAME", "id"}},
			want:      Layouts{SectionNetworks: {0, 4}},
			wantWarns: []string{`duplicate column "NAME"`},
		},
		{
			name:      "all invalid keeps default",
			cfg:       map[string][]string{"images": {"x", "y"}},
			want:      nil,
			wantWarns: []string{`unknown column "x"`, `unknown column "y"`},
		},
		{
			name:      "missing trailing ID appended",
			cfg:       map[string][]string{"images": {"size", "repository"}},
			want:      Layouts{SectionImages: {1, 0, 3}},
			wantWarns: []string{"column ID is required"},
		},
		{
			name:      "missing leading NAME prepended",
			cfg:       map[string][]string{"hosts": {"status", "host"}},
			want:      Layouts{SectionHosts: {0, 2, 1}},
			wantWarns: []string{"column NAME is required"},
		},
		{
			name:      "compose PATH restored at its default slot",
			cfg:       map[string][]string{"compose": {"project", "name", "status"}},
			want:      Layouts{SectionCompose: {0, 1, 2, 3}},
			wantWarns: []string{"column PATH is required"},
		},
		{
			name: "full default order collapses to default",
			cfg:  map[string][]string{"volumes": {"name", "driver", "mountpoint", "created"}},
			want: nil,
		},
		{
			name: "stats section",
			cfg:  map[string][]string{"stats": {"name", "cpu %", "mem %", "id"}},
			want: Layouts{SectionStats: {0, 1, 3, 6}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warns := ResolveLayouts(tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("layouts = %v, want %v", got, tt.want)
			}
			if len(warns) != len(tt.wantWarns) {
				t.Fatalf("warnings = %q, want %d matching %q", warns, len(tt.wantWarns), tt.wantWarns)
			}
			for i, sub := range tt.wantWarns {
				if !strings.Contains(warns[i], sub) {
					t.Errorf("warning %d = %q, want it to contain %q", i, warns[i], sub)
				}
			}
		})
	}
}

// TestResolveLayoutsWarningOrder checks warnings come out in a stable order
// (sections sorted), independent of map iteration.
func TestResolveLayoutsWarningOrder(t *testing.T) {
	cfg := map[string][]string{"zzz": {"a"}, "aaa": {"b"}, "images": {"nope", "id"}}
	for range 5 {
		_, warns := ResolveLayouts(cfg)
		if len(warns) != 3 || !strings.Contains(warns[0], "aaa") || !strings.Contains(warns[1], "nope") || !strings.Contains(warns[2], "zzz") {
			t.Fatalf("unexpected warning order: %q", warns)
		}
	}
}

func TestLayoutsForNilSafe(t *testing.T) {
	var l Layouts
	if got := l.For(SectionContainers); got != nil {
		t.Errorf("nil Layouts.For = %v, want nil", got)
	}
}

func TestProjectColumns(t *testing.T) {
	cols := []table.Column{{Title: "A", Width: 10}, {Title: "B", Width: 20}, {Title: "C", Width: 30}, {Title: "D", Width: 40}}
	if got := projectColumns(cols, nil); !reflect.DeepEqual(got, cols) {
		t.Errorf("nil projection changed columns: %v", got)
	}
	got := projectColumns(cols, []int{3, 0})
	if len(got) != 2 || got[0].Title != "D" || got[1].Title != "A" {
		t.Fatalf("projection = %v, want D, A", got)
	}
	// 40 and 10 of 100 are scaled up by 100/50 so the pair fills the width.
	if got[0].Width != 80 || got[1].Width != 20 {
		t.Errorf("widths = %d,%d, want 80,20", got[0].Width, got[1].Width)
	}
	if cols[3].Width != 40 {
		t.Error("projectColumns mutated its input")
	}
	// Zero widths (seeding before the first resize) stay zero.
	zero := projectColumns(ImageColumns(0), []int{0, 3})
	if zero[0].Width != 0 || zero[1].Width != 0 {
		t.Errorf("zero-width projection = %v", zero)
	}
	// An out-of-range index is dropped rather than panicking.
	if got := projectColumns(cols, []int{1, 9}); len(got) != 1 || got[0].Title != "B" {
		t.Errorf("out-of-range projection = %v", got)
	}
}

func TestProjectRow(t *testing.T) {
	row := table.Row{"a", "b", "c"}
	if got := projectRow(row, nil); !reflect.DeepEqual(got, row) {
		t.Errorf("nil projection = %v", got)
	}
	if got := projectRow(row, []int{2, 0, 5}); !reflect.DeepEqual(got, table.Row{"c", "a", ""}) {
		t.Errorf("projection = %v", got)
	}
}

func TestProjectColorizers(t *testing.T) {
	cs := ContainerColorizers()
	if got := projectColorizers(nil, []int{0}); got != nil {
		t.Errorf("nil colorizers projected to %v", got)
	}
	if got := projectColorizers(cs, nil); len(got) != len(cs) {
		t.Errorf("nil projection changed length: %d", len(got))
	}
	// STATUS (2) is colored, IMAGE (1) is not; order follows the projection.
	got := projectColorizers(cs, []int{2, 1, 99})
	if len(got) != 3 || got[0] == nil || got[1] != nil || got[2] != nil {
		t.Errorf("projected colorizers wrong: nil-ness %v %v %v", got[0] == nil, got[1] == nil, got[2] == nil)
	}
}

// TestModelLayoutProjection checks the table shows only the configured columns
// (one cell per visible column — the bubbles invariant) while SelectedRow and
// SelectedID still read the full default row.
func TestModelLayoutProjection(t *testing.T) {
	m := New()
	m.SetSize(120, 10)
	m.SetLayout([]int{2, 0, 7}) // STATUS, NAME, ID
	m.SetColumns(ContainerColumns(120))
	m.SetColorizers(ContainerColorizers())
	m.SetContainers([]docker.Container{{ID: "abc123", Name: "web", Image: "nginx", Status: "Up 1 hour"}}, "", nil, false, nil, nil)

	cols := m.Table().Columns()
	if len(cols) != 3 || cols[0].Title != "STATUS" || cols[1].Title != "NAME" || cols[2].Title != "ID" {
		t.Fatalf("columns = %v", cols)
	}
	rows := m.Table().Rows()
	if len(rows) != 1 || len(rows[0]) != len(cols) {
		t.Fatalf("rows = %v, want 1 row of %d cells", rows, len(cols))
	}
	if rows[0][0] != "Up 1 hour" {
		t.Errorf("first visible cell = %q, want STATUS", rows[0][0])
	}
	if full := m.SelectedRow(); len(full) != len(ContainerColumns(0)) {
		t.Errorf("SelectedRow has %d cells, want the full default row", len(full))
	}
	if id := m.SelectedID(); id != "abc123" {
		t.Errorf("SelectedID = %q, want abc123", id)
	}
	if len(m.colorize) != 3 || m.colorize[0] == nil {
		t.Error("colorizers not projected onto the visible columns")
	}
	view := stripANSI(m.View())
	if strings.Contains(view, "IMAGE") || !strings.Contains(view, "STATUS") || !strings.Contains(view, "Up 1 hour") {
		t.Errorf("view does not reflect the projection:\n%s", view)
	}

	// Back to the default layout: every column returns.
	m.SetLayout(nil)
	m.SetColumns(ContainerColumns(120))
	if n := len(m.Table().Columns()); n != len(ContainerColumns(0)) {
		t.Errorf("default layout has %d columns", n)
	}
	if n := len(m.Table().Rows()); n != 0 {
		t.Errorf("rows not cleared on column-count change: %d", n)
	}
	if m.SelectedRow() != nil {
		t.Error("SelectedRow survived the column-count change")
	}
}

// TestSelectComposeRowWithLayout checks the compose drill-down restore still
// finds the deployment by its identity when PATH is moved.
func TestSelectComposeRowWithLayout(t *testing.T) {
	m := New()
	m.SetSize(120, 10)
	m.SetLayout([]int{2, 0}) // PATH first, then PROJECT
	m.SetColumns(ComposeColumns(120))
	m.SetCompose([]docker.ComposeProject{
		{Project: "a", Name: "a", WorkingDir: "/srv/a"},
		{Project: "b", Name: "b", WorkingDir: "/srv/b"},
	}, "")
	m.SelectComposeRow("/srv/b")
	if got := m.SelectedRow(); len(got) <= ComposeIDColumn || got[ComposeIDColumn] != "/srv/b" {
		t.Errorf("selected row = %v, want /srv/b", got)
	}
}

// TestSetRowsRecoversCursorAfterEmptyList checks that loading rows after an
// empty list (cursor parked at -1 by bubbles' clamp) selects the first row.
func TestSetRowsRecoversCursorAfterEmptyList(t *testing.T) {
	m := New()
	m.SetSize(120, 10)
	m.SetColumns(NetworkColumns(120))
	m.SetNetworks(nil, "")
	if c := m.Table().Cursor(); c != -1 {
		t.Fatalf("precondition: cursor on empty list = %d, want -1", c)
	}
	m.SetNetworks([]docker.Network{{ID: "n1", Name: "bridge"}, {ID: "n2", Name: "host"}}, "")
	if c := m.Table().Cursor(); c != 0 {
		t.Errorf("cursor = %d, want 0", c)
	}
	if id := m.SelectedID(); id != "n1" {
		t.Errorf("SelectedID = %q, want n1", id)
	}
}
