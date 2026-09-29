package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
	uitbl "github.com/kirg0/d9c/internal/ui/table"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func columnTitles(m Model) []string {
	var out []string
	for _, c := range m.table.Table().Columns() {
		out = append(out, c.Title)
	}
	return out
}

func TestColumnSection(t *testing.T) {
	tests := []struct {
		resource ResourceView
		stats    bool
		want     uitbl.Section
	}{
		{ViewContainers, false, uitbl.SectionContainers},
		{ViewContainers, true, uitbl.SectionStats},
		{ViewImages, false, uitbl.SectionImages},
		{ViewNetworks, false, uitbl.SectionNetworks},
		{ViewVolumes, false, uitbl.SectionVolumes},
		{ViewCompose, false, uitbl.SectionCompose},
		{ViewHosts, false, uitbl.SectionHosts},
	}
	for _, tt := range tests {
		m := Model{resource: tt.resource, statsView: tt.stats}
		if got := m.columnSection(); got != tt.want {
			t.Errorf("columnSection(%v, stats=%v) = %q, want %q", tt.resource, tt.stats, got, tt.want)
		}
	}
}

// TestColumnLayoutsApplied checks configured layouts drive the visible columns
// per section, and that actions still address the right container (identity is
// read from the full row).
func TestColumnLayoutsApplied(t *testing.T) {
	layouts, warns := uitbl.ResolveLayouts(map[string][]string{
		"containers": {"status", "name", "id"},
		"volumes":    {"driver", "name"},
	})
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %q", warns)
	}
	fb := docker.NewFakeBackend()
	m := NewModel(&config.Config{}, fb, nil, nil, false)
	m.SetColumnLayouts(layouts, warns)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	cs, _ := fb.ListContainers(false)
	tm, _ = tm.Update(containersUpdatedMsg{cs})
	m = tm.(Model)

	if got := strings.Join(columnTitles(m), ","); got != "STATUS,NAME,ID" {
		t.Errorf("containers columns = %s", got)
	}
	for _, r := range m.table.Table().Rows() {
		if len(r) != 3 {
			t.Fatalf("row has %d cells, want 3: %v", len(r), r)
		}
	}
	if id := m.selectedID(); id != cs[0].ID {
		t.Errorf("selectedID = %q, want first container %q", id, cs[0].ID)
	}

	// The stats layout has no entry → default columns.
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if n := len(columnTitles(tm.(Model))); n != len(uitbl.ContainerStatsColumns(0)) {
		t.Errorf("stats view has %d columns, want the default set", n)
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

	// Volumes: NAME (identity) stays addressable though it is no longer first.
	tm, _ = tm.Update(switchResourceMsg{ViewVolumes})
	tm, _ = tm.Update(volumesUpdatedMsg{fb.Volumes})
	m = tm.(Model)
	if got := strings.Join(columnTitles(m), ","); got != "DRIVER,NAME" {
		t.Errorf("volumes columns = %s", got)
	}
	if id := m.selectedID(); id == "" || id == m.table.Table().Rows()[0][0] {
		t.Errorf("volume selectedID = %q, want the NAME, not the DRIVER cell", id)
	}

	// Images have no layout → default columns.
	tm, _ = tm.Update(switchResourceMsg{ViewImages})
	if n := len(columnTitles(tm.(Model))); n != len(uitbl.ImageColumns(0)) {
		t.Errorf("images have %d columns, want the default set", n)
	}
}

func TestSetColumnLayoutsWarningsNotice(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.SetColumnLayouts(nil, []string{"columns: unknown section \"pods\"", "second"})
	if m.startupNotice == nil {
		t.Fatal("warnings should queue a startup notice")
	}
	if m.startupNotice.title != "Column settings" || !strings.Contains(m.startupNotice.body, "pods") || !strings.Contains(m.startupNotice.body, "\nsecond") {
		t.Errorf("notice = %+v", *m.startupNotice)
	}

	// No warnings → no notice.
	clean := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	clean.SetColumnLayouts(nil, nil)
	if clean.startupNotice != nil {
		t.Error("no warnings should not queue a notice")
	}

	// An existing startup notice (connection problem) is not replaced.
	busy := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	busy.startupNotice = &openNoticeMsg{title: "host key", body: "x"}
	busy.SetColumnLayouts(nil, []string{"w"})
	if busy.startupNotice.title != "host key" {
		t.Errorf("startup notice overwritten: %+v", *busy.startupNotice)
	}
}

// TestColumnLayoutRendered checks the configured layout end to end in a real
// frame: the hidden IMAGE column is gone, the reordered ones are shown.
func TestColumnLayoutRendered(t *testing.T) {
	layouts, _ := uitbl.ResolveLayouts(map[string][]string{"containers": {"name", "status", "id"}})
	m := NewModel(&config.Config{Demo: true}, docker.NewFakeBackend(), &hosts.Store{}, nil, false)
	m.SetColumnLayouts(layouts, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		s := string(b)
		return strings.Contains(s, "STATUS") && strings.Contains(s, "web") && strings.Contains(s, "Up 2 hours") &&
			!strings.Contains(s, "nginx:1.25") && !strings.Contains(s, "0.0.0.0:8080")
	}, teatest.WithDuration(5*time.Second))
	tm.Quit()
}
