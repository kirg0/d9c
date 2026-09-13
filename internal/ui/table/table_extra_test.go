package table

import (
	"strings"
	"testing"
	"time"

	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSortFieldString(t *testing.T) {
	tests := map[SortField]string{
		SortName: "NAME", SortStatus: "STATUS", SortCPU: "CPU", SortMem: "MEM", SortNone: "",
	}
	for f, want := range tests {
		if got := f.String(); got != want {
			t.Errorf("String(%d) = %q, want %q", f, got, want)
		}
	}
}

func demoContainers() []docker.Container {
	return []docker.Container{
		{ID: "b", Name: "beta", State: "running", Status: "Up", Created: time.Now()},
		{ID: "a", Name: "alpha", State: "exited", Status: "Exited (0)", Created: time.Now()},
	}
}

func TestSetSortOrdersContainers(t *testing.T) {
	m := New()
	m.SetSize(120, 20)
	m.SetColumns(ContainerColumns(120))
	m.SetSort(SortName, false)
	m.SetContainers(demoContainers(), "", nil, false, nil, nil)
	if got := m.SelectedRow(); len(got) == 0 || !strings.Contains(got[0], "alpha") {
		t.Errorf("first row = %v, want alpha first under NAME asc", got)
	}
	m.SetSort(SortName, true)
	m.SetContainers(demoContainers(), "", nil, false, nil, nil)
	if got := m.SelectedRow(); len(got) == 0 || !strings.Contains(got[0], "beta") {
		t.Errorf("first row = %v, want beta first under NAME desc", got)
	}
}

func TestCompareContainersCPUAndMem(t *testing.T) {
	a := docker.Container{ID: "a", Name: "a"}
	b := docker.Container{ID: "b", Name: "b"}
	stats := map[string]docker.ContainerStats{
		"a": {CPUPerc: 1, MemUsage: 100},
		"b": {CPUPerc: 2, MemUsage: 50},
	}
	if compareContainers(a, b, stats, SortCPU) >= 0 {
		t.Error("a should sort before b by CPU")
	}
	if compareContainers(a, b, stats, SortMem) <= 0 {
		t.Error("b should sort before a by MEM")
	}
}

func TestResourceRowSettersAndSelection(t *testing.T) {
	m := New()
	m.SetSize(120, 20)

	m.SetColumns(ImageColumns(120))
	m.SetImages([]docker.Image{{ID: "img1", Tags: "nginx:1.25", Size: "1 MB", Created: time.Now()}}, "", nil)
	if got := m.SelectedID(); got != "img1" {
		t.Errorf("image SelectedID = %q", got)
	}

	m.SetColumns(NetworkColumns(120))
	m.SetNetworks([]docker.Network{{ID: "net1", Name: "bridge", Driver: "bridge", Scope: "local"}}, "")
	if got := m.SelectedID(); got != "net1" {
		t.Errorf("network SelectedID = %q", got)
	}

	m.SetColumns(VolumeColumns(120))
	m.SetVolumes([]docker.Volume{{Name: "pgdata", Driver: "local", Mountpoint: "/x", Created: "2026-01-01"}}, "")
	if row := m.SelectedRow(); len(row) == 0 || row[0] != "pgdata" {
		t.Errorf("volume row = %v", row)
	}

	// Empty table: no selection.
	m.SetVolumes(nil, "")
	if got := m.SelectedID(); got != "" {
		t.Errorf("empty table SelectedID = %q", got)
	}
	if row := m.SelectedRow(); row != nil {
		t.Errorf("empty table SelectedRow = %v", row)
	}
}

func TestSelectComposeRow(t *testing.T) {
	m := New()
	m.SetSize(120, 20)
	m.SetColumns(ComposeColumns(120))
	m.SetCompose([]docker.ComposeProject{
		{Project: "a", Name: "a", WorkingDir: "/srv/a", Status: "running"},
		{Project: "b", Name: "b", WorkingDir: "/srv/b", Status: "stopped"},
	}, "")
	m.SelectComposeRow("/srv/b")
	if got := m.SelectedRow(); len(got) <= ComposeIDColumn || got[ComposeIDColumn] != "/srv/b" {
		t.Errorf("selected row = %v, want /srv/b", got)
	}
	m.SelectComposeRow("/srv/ghost") // no-op
	if got := m.SelectedRow(); got[ComposeIDColumn] != "/srv/b" {
		t.Errorf("selection moved on unknown id: %v", got)
	}
}

func TestUpdateMovesCursor(t *testing.T) {
	m := New()
	m.SetSize(120, 20)
	m.SetColumns(ImageColumns(120))
	m.SetImages([]docker.Image{
		{ID: "one", Tags: "a:1"}, {ID: "two", Tags: "b:2"},
	}, "", nil)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedID(); got != "two" {
		t.Errorf("SelectedID after down = %q", got)
	}
	if m.Table().Cursor() != 1 {
		t.Errorf("cursor = %d", m.Table().Cursor())
	}
}

func TestViewColorizedAndPlain(t *testing.T) {
	// Plain view (no colorizers).
	m := New()
	m.SetSize(120, 20)
	m.SetColumns(ImageColumns(120))
	m.SetImages([]docker.Image{{ID: "one", Tags: "nginx:1.25"}}, "", nil)
	if v := m.View(); !strings.Contains(v, "nginx:1.25") {
		t.Error("plain view should render the row")
	}

	// Colorized containers view.
	m2 := New()
	m2.SetSize(120, 20)
	m2.SetColumns(ContainerColumns(120))
	m2.SetColorizers(ContainerColorizers())
	m2.SetContainers(demoContainers(), "", nil, false, nil, map[string]bool{"b": true})
	if v := m2.View(); !strings.Contains(v, "beta") {
		t.Error("colorized view should render the rows")
	}

	// Stats layout with its colorizers.
	m3 := New()
	m3.SetSize(120, 20)
	m3.SetColumns(ContainerStatsColumns(120))
	m3.SetColorizers(ContainerStatsColorizers())
	stats := map[string]docker.ContainerStats{"b": {CPUPerc: 5, MemUsage: 1024, MemLimit: 2048, MemPerc: 50}}
	m3.SetContainers(demoContainers(), "", stats, true, nil, map[string]bool{"b": true})
	if v := m3.View(); !strings.Contains(v, "beta") {
		t.Error("stats view should render the rows")
	}

	// Hosts layout.
	m4 := New()
	m4.SetSize(120, 20)
	m4.SetColumns(HostColumns(120))
	m4.SetColorizers(HostColorizers())
	m4.SetHosts([]hosts.Host{{Name: "lab", Host: "ssh://me@lab"}}, "",
		map[string]docker.HostSummary{"ssh://me@lab": {Reachable: true, Version: "27.0"}})
	if v := m4.View(); !strings.Contains(v, "lab") {
		t.Error("hosts view should render the rows")
	}

	// Zero width falls back to the raw bubbles output.
	m5 := New()
	if v := m5.View(); v == "" {
		t.Error("zero-width view should still render something")
	}
}

func TestColumnBuildersHaveIDLast(t *testing.T) {
	if cols := ContainerStatsColumns(100); cols[len(cols)-1].Title != "ID" {
		t.Error("stats columns should end with ID")
	}
	if cols := ImageColumns(100); cols[len(cols)-1].Title != "ID" {
		t.Error("image columns should end with ID")
	}
	if cols := NetworkColumns(100); cols[len(cols)-1].Title != "ID" {
		t.Error("network columns should end with ID")
	}
	if cols := VolumeColumns(100); cols[0].Title != "NAME" {
		t.Error("volume columns should start with NAME")
	}
}

func TestTimeAgo(t *testing.T) {
	now := time.Now()
	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-48 * time.Hour), "2d ago"},
	}
	for _, tt := range tests {
		if got := timeAgo(tt.t); got != tt.want {
			t.Errorf("timeAgo = %q, want %q", got, tt.want)
		}
	}
	old := now.Add(-100 * 24 * time.Hour)
	if got := timeAgo(old); got != old.Format("2006-01-02") {
		t.Errorf("old timeAgo = %q", got)
	}
}
