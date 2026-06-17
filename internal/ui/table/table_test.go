package table

import (
	"strconv"
	"strings"
	"testing"

	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello w…"},
		{"", 5, ""},
		// Rune-based: multi-byte text must not be cut mid-character.
		{"привет-мир", 20, "привет-мир"},
		{"привет-мир", 7, "привет…"},
		{"данные-проекта", 8, "данные-…"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

// Identity columns (the ones selectedID reads) must survive intact no matter
// how long the name is — a clipped value would break rm/inspect/connect.
// Display clipping is bubbles' job (it truncates cells to the column width).
func TestIdentityColumnsNotTruncated(t *testing.T) {
	longName := strings.Repeat("a", 64) // e.g. an anonymous volume's hex name

	volRows := buildVolumeRows([]docker.Volume{{Name: longName, Driver: "local"}}, "")
	if volRows[0][0] != longName {
		t.Errorf("volume NAME = %q, want full %d-char name", volRows[0][0], len(longName))
	}

	hostRows := buildHostRows([]hosts.Host{{Name: longName, Host: "ssh://u@h"}}, "", nil)
	if hostRows[0][0] != longName {
		t.Errorf("host NAME = %q, want full name", hostRows[0][0])
	}

	// For compose the identity is the working_dir (PATH column), not NAME.
	longDir := "/srv/" + strings.Repeat("a", 64)
	composeRows := buildComposeRows([]docker.ComposeProject{{Project: "p", Name: longName, WorkingDir: longDir, Status: "running"}}, "")
	if composeRows[0][ComposeIDColumn] != longDir {
		t.Errorf("compose identity = %q, want full working_dir", composeRows[0][ComposeIDColumn])
	}
}

// The hosts STATUS cell reflects per-URL reachability: up/down once probed,
// a pending placeholder before; the cell count must match HostColumns.
// TestBuildHostRows covers the unified hosts/dashboard rows: STATUS token plus
// the aggregate daemon counts sourced from the per-host `docker info` summary.
func TestBuildHostRows(t *testing.T) {
	list := []hosts.Host{
		{Name: "prod", Host: "ssh://u@prod"},
		{Name: "lab", Host: "tcp://lab:2375"},
		{Name: "new", Host: "tcp://new:2375"}, // not summarized yet
	}
	summaries := map[string]docker.HostSummary{
		"ssh://u@prod":   {Host: "ssh://u@prod", Reachable: true, Containers: 7, Running: 5, Images: 12, Version: "27.4.0"},
		"tcp://lab:2375": {Host: "tcp://lab:2375", Err: "refused"}, // unreachable
	}
	rows := buildHostRows(list, "", summaries)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if got := len(rows[0]); got != len(HostColumns(120)) {
		t.Fatalf("cell count = %d, want %d (must match columns)", got, len(HostColumns(120)))
	}
	// prod: up, with real counts (STATUS col 2, then CONTAINERS/RUNNING/IMAGES/VERSION).
	if !strings.Contains(rows[0][2], "up") {
		t.Errorf("prod STATUS = %q, want up", rows[0][2])
	}
	if rows[0][3] != "7" || rows[0][4] != "5" || rows[0][5] != "12" || rows[0][6] != "27.4.0" {
		t.Errorf("prod counts = %v, want [7 5 12 27.4.0]", rows[0][3:7])
	}
	// lab: down, counts must stay "-".
	if !strings.Contains(rows[1][2], "down") {
		t.Errorf("lab STATUS = %q, want down", rows[1][2])
	}
	if rows[1][3] != "-" || rows[1][6] != "-" {
		t.Errorf("lab unreachable counts = %v, want placeholders", rows[1][3:7])
	}
	// new: pending.
	if !strings.Contains(rows[2][2], "…") {
		t.Errorf("new STATUS = %q, want pending …", rows[2][2])
	}
}

func TestSortContainers(t *testing.T) {
	containers := []docker.Container{
		{ID: "a", Name: "web", State: "running"},
		{ID: "b", Name: "db", State: "exited"},
		{ID: "c", Name: "cache", State: "running"},
	}
	stats := map[string]docker.ContainerStats{
		"a": {CPUPerc: 10, MemUsage: 300},
		"b": {CPUPerc: 50, MemUsage: 100},
		"c": {CPUPerc: 5, MemUsage: 200},
	}
	tests := []struct {
		name  string
		field SortField
		desc  bool
		want  []string // expected ID order
	}{
		{"none keeps order", SortNone, false, []string{"a", "b", "c"}},
		{"name asc", SortName, false, []string{"c", "b", "a"}},     // cache, db, web
		{"name desc", SortName, true, []string{"a", "b", "c"}},     // web, db, cache
		{"status asc", SortStatus, false, []string{"b", "c", "a"}}, // exited first; running tie→name (cache<web)
		{"cpu desc", SortCPU, true, []string{"b", "a", "c"}},       // 50,10,5
		{"cpu asc", SortCPU, false, []string{"c", "a", "b"}},       // 5,10,50
		{"mem desc", SortMem, true, []string{"a", "c", "b"}},       // 300,200,100
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SortContainers(containers, stats, tt.field, tt.desc)
			var ids []string
			for _, c := range got {
				ids = append(ids, c.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tt.want, ",") {
				t.Errorf("order = %v, want %v", ids, tt.want)
			}
		})
	}
	// The input slice must not be mutated.
	_ = SortContainers(containers, stats, SortName, false)
	if containers[0].ID != "a" {
		t.Errorf("input slice was mutated: first ID = %q, want a", containers[0].ID)
	}
}

func TestBuildRows_NoFilter(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx", Status: "Up 2h", State: "running"},
		{ID: "def456", Name: "db", Image: "postgres", Status: "Up 1h", State: "running"},
	}
	rows := buildRows(containers, "", nil, nil, nil)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// NAME carries a fixed-width selection marker prefix.
	if strings.TrimSpace(rows[0][0]) != "web" {
		t.Errorf("row[0][0] = %q, want %q", rows[0][0], "web")
	}
}

func TestBuildRows_FilterByName(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx", Status: "Up 2h", State: "running"},
		{ID: "def456", Name: "db", Image: "postgres", Status: "Up 1h", State: "running"},
	}
	rows := buildRows(containers, "web", nil, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if strings.TrimSpace(rows[0][0]) != "web" {
		t.Errorf("row[0][0] = %q, want %q", rows[0][0], "web")
	}
}

func TestBuildRows_FilterByImage(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx:latest", Status: "Up", State: "running"},
		{ID: "def456", Name: "db", Image: "postgres:15", Status: "Up", State: "running"},
	}
	rows := buildRows(containers, "postgres", nil, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestBuildRows_FilterCaseInsensitive(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "MyApp", Image: "alpine", Status: "Up", State: "running"},
	}
	rows := buildRows(containers, "myapp", nil, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestBuildRows_FilterNoMatch(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx", Status: "Up", State: "running"},
	}
	rows := buildRows(containers, "redis", nil, nil, nil)
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

// TestBuildRows_AdvancedFilters checks the regex/status/label/network query
// terms are wired through to the container row builder.
func TestBuildRows_AdvancedFilters(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web-1", Image: "nginx", Status: "Up 2h", State: "running",
			Labels: map[string]string{"env": "prod"}, Networks: []string{"frontend"}},
		{ID: "def456", Name: "db", Image: "postgres", Status: "Exited (0)", State: "exited",
			Labels: map[string]string{"env": "staging"}, Networks: []string{"backend"}},
	}
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"regex", `re:^web-\d+`, 1},
		{"status", "status:exited", 1},
		{"label key", "label:env", 2},
		{"label kv", "label:env=prod", 1},
		{"network", "network:backend", 1},
		{"combined", "status:running label:env=prod", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(buildRows(containers, tt.query, nil, nil, nil)); got != tt.want {
				t.Errorf("buildRows(%q) = %d rows, want %d", tt.query, got, tt.want)
			}
		})
	}
}

func TestBuildRows_Stats(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx", Status: "Up", State: "running"},
		{ID: "def456", Name: "db", Image: "postgres", Status: "Exited", State: "exited"},
	}
	stats := map[string]docker.ContainerStats{
		"abc123": {ID: "abc123", CPUPerc: 2.5, MemUsage: 48 * 1024 * 1024},
	}
	rows := buildRows(containers, "", stats, nil, nil)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Row layout: NAME, IMAGE, STATUS, HEALTH, PORTS, CPU%, MEM, ID.
	if rows[0][5] != "2.5%" {
		t.Errorf("web CPU cell = %q, want %q", rows[0][5], "2.5%")
	}
	if rows[0][6] != "48.0 MB" {
		t.Errorf("web MEM cell = %q, want %q", rows[0][6], "48.0 MB")
	}
	if rows[0][7] != "abc123" {
		t.Errorf("web ID cell = %q, want %q", rows[0][7], "abc123")
	}
	// Container without a stats sample shows placeholders.
	if rows[1][5] != "-" || rows[1][6] != "-" {
		t.Errorf("db CPU/MEM = %q/%q, want -/-", rows[1][5], rows[1][6])
	}
}

// The HEALTH cell shows the healthcheck verdict (colorized) or "-" when the
// container defines none; the cell count must match the 8 default columns.
func TestBuildRows_Health(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", State: "running", Health: "healthy"},
		{ID: "def456", Name: "api", State: "running", Health: "starting"},
		{ID: "ghi789", Name: "db", State: "exited"}, // no healthcheck
	}
	rows := buildRows(containers, "", nil, nil, nil)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if got := len(rows[0]); got != len(ContainerColumns(120)) {
		t.Fatalf("cell count = %d, want %d (must match columns)", got, len(ContainerColumns(120)))
	}
	if !strings.Contains(rows[0][3], "healthy") {
		t.Errorf("web HEALTH = %q, want healthy", rows[0][3])
	}
	if !strings.Contains(rows[1][3], "starting") {
		t.Errorf("api HEALTH = %q, want starting", rows[1][3])
	}
	if rows[2][3] != "-" {
		t.Errorf("db HEALTH = %q, want -", rows[2][3])
	}
}

func TestBuildStatsRows(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx", Status: "Up", State: "running"},
		{ID: "def456", Name: "db", Image: "postgres", Status: "Exited", State: "exited"},
	}
	stats := map[string]docker.ContainerStats{
		"abc123": {
			ID: "abc123", CPUPerc: 2.5, MemUsage: 48 * 1024 * 1024, MemPerc: 9.4,
			NetRx: 1024 * 1024, NetTx: 512 * 1024,
			BlockRead: 8 * 1024 * 1024, BlockWrite: 2 * 1024 * 1024,
		},
	}
	rows := buildStatsRows(containers, "", stats, nil, nil)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Layout: NAME, CPU%, MEM, MEM%, NET I/O, BLOCK I/O, ID (7 cells).
	want := []string{"web", "2.5%", "48.0 MB", "9.4%", "1.0 MB / 512.0 KB", "8.0 MB / 2.0 MB", "abc123"}
	if len(rows[0]) != len(want) {
		t.Fatalf("row has %d cells, want %d", len(rows[0]), len(want))
	}
	for i, w := range want {
		got := rows[0][i]
		if i == 0 {
			got = strings.TrimSpace(got) // strip selection marker
		}
		if got != w {
			t.Errorf("cell[%d] = %q, want %q", i, rows[0][i], w)
		}
	}
	// Container without a sample shows placeholders in every metric column.
	for i := 1; i <= 5; i++ {
		if rows[1][i] != "-" {
			t.Errorf("db cell[%d] = %q, want -", i, rows[1][i])
		}
	}
}

func TestBuildRows_SelectionMarker(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx", Status: "Up", State: "running"},
		{ID: "def456", Name: "db", Image: "postgres", Status: "Up", State: "running"},
	}
	selected := map[string]bool{"abc123": true}
	rows := buildRows(containers, "", nil, selected, nil)
	if !strings.HasPrefix(rows[0][0], "●") {
		t.Errorf("selected row NAME = %q, want a ● marker", rows[0][0])
	}
	if strings.Contains(rows[1][0], "●") {
		t.Errorf("unselected row NAME = %q, should have no marker", rows[1][0])
	}
}

// TestBuildRows_AlertMarker checks the ⚠ NAME marker is added only for breaching
// containers and only when the alerted set is non-nil (feature active), keeping
// names aligned with a fixed-width slot otherwise.
func TestBuildRows_AlertMarker(t *testing.T) {
	containers := []docker.Container{
		{ID: "abc123", Name: "web", State: "running"},
		{ID: "def456", Name: "db", State: "running"},
	}

	// Feature off (nil): no ⚠ anywhere.
	for _, row := range buildRows(containers, "", nil, nil, nil) {
		if strings.ContainsRune(row[0], '⚠') {
			t.Errorf("alerts off: NAME %q should carry no ⚠", row[0])
		}
	}

	// Feature on: only the breaching container is flagged, both names resolve.
	alerted := map[string]bool{"abc123": true}
	rows := buildRows(containers, "", nil, nil, alerted)
	if !strings.ContainsRune(rows[0][0], '⚠') {
		t.Errorf("breaching NAME = %q, want a ⚠ marker", rows[0][0])
	}
	if strings.ContainsRune(rows[1][0], '⚠') {
		t.Errorf("non-breaching NAME = %q, should have no ⚠", rows[1][0])
	}
	if got := strings.TrimSpace(strings.ReplaceAll(rows[0][0], "⚠", "")); got != "web" {
		t.Errorf("breaching NAME trims to %q, want web", got)
	}
	if got := strings.TrimSpace(rows[1][0]); got != "db" {
		t.Errorf("non-breaching NAME trims to %q, want db", got)
	}
}

// TestNameAlertStyle checks the NAME colorizer only overrides rows carrying the
// alert glyph.
func TestNameAlertStyle(t *testing.T) {
	if _, ok := nameAlertStyle("⚠ web"); !ok {
		t.Error("nameAlertStyle should override a ⚠ row")
	}
	if _, ok := nameAlertStyle("web"); ok {
		t.Error("nameAlertStyle should not override a plain row")
	}
}

// TestHostsViewStatusTrueColor reproduces the real-terminal bug: with a
// truecolor profile, bubbles' runewidth truncation counts a pre-colored cell's
// ANSI bytes as width and eats the visible text. The hosts view must keep the
// STATUS text visible (colored after layout). Forcing the profile here is what
// the Ascii-profile teatest harness can't catch.
func TestHostsViewStatusTrueColor(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	const w = 100
	summaries := map[string]docker.HostSummary{
		"tcp://prod:2375": {Reachable: true, Containers: 3, Running: 2, Images: 4, Version: "27.4.0"},
		"ssh://user@lab":  {Err: "down"},
	}
	list := []hosts.Host{
		{Name: "prod", Host: "tcp://prod:2375"},
		{Name: "lab", Host: "ssh://user@lab"},
		{Name: "new", Host: "tcp://new:2375"}, // pending
	}

	m := New()
	m.SetSize(w, 12)
	m.SetColumns(HostColumns(w))
	m.SetColorizers(HostColorizers())
	m.SetHosts(list, "", summaries)

	out := m.View()
	plain := stripANSI(out)
	for _, want := range []string{"up", "down", "…", "27.4.0"} {
		if !strings.Contains(plain, want) {
			t.Errorf("STATUS text %q missing from rendered hosts view:\n%s", want, plain)
		}
	}
	// The status color escapes must be present (text is actually colored).
	// Derive the expected escape from the style itself so termenv's color
	// rounding can't make the assertion brittle. A host may be the cursor row,
	// where the color is composited onto the selection background, so accept
	// either form.
	if !hasStatusColor(out, styles.StatusRunning) {
		t.Error("up status not colored (green escape absent)")
	}
	if !hasStatusColor(out, styles.StatusExited) {
		t.Error("down status not colored (red escape absent)")
	}
}

// fgEscape returns the opening SGR sequence a style emits (everything before
// the content), so tests can match it without hardcoding RGB values.
func fgEscape(s lipgloss.Style) string {
	return strings.SplitN(s.Render("\x00"), "\x00", 2)[0]
}

// hasStatusColor reports whether out carries st's color in either its plain
// form or composited onto the selection background (the cursor row).
func hasStatusColor(out string, st lipgloss.Style) bool {
	return strings.Contains(out, fgEscape(st)) ||
		strings.Contains(out, fgEscape(st.Background(styles.SelectedBg)))
}

// TestRenderHostDataLineSelected checks the selected row keeps the STATUS color
// composited on the selection background (the user's request), while NAME/HOST
// use the selection style.
func TestRenderHostDataLineSelected(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	const w = 100
	m := New()
	m.SetSize(w, 12)
	m.SetColumns(HostColumns(w))
	m.SetColorizers(HostColorizers())
	m.SetHosts([]hosts.Host{{Name: "prod", Host: "tcp://prod:2375"}}, "",
		map[string]docker.HostSummary{"tcp://prod:2375": {Reachable: true}})

	// The single row is the cursor row; its STATUS must be the green status
	// color composited onto the selection background.
	out := m.View()
	if !strings.Contains(stripANSI(out), "up") {
		t.Fatal("selected row lost its STATUS text")
	}
	want := fgEscape(styles.StatusRunning.Background(styles.SelectedBg))
	if !strings.Contains(out, want) {
		t.Error("selected row STATUS not rendered as green-on-selection")
	}
}

// TestContainersViewStatusTrueColor verifies the same truncation fix for the
// container STATUS and HEALTH columns: in a truecolor profile the text must
// survive and be colored (this is the bug the user hit on hosts, present here
// too before the fix).
func TestContainersViewStatusTrueColor(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	const w = 120
	containers := []docker.Container{
		{ID: "abc123", Name: "web", Image: "nginx", Status: "Up 2 hours", State: "running", Health: "healthy"},
		{ID: "def456", Name: "db", Image: "postgres", Status: "Exited (0) 3 minutes ago", State: "exited"},
	}
	m := New()
	m.SetSize(w, 12)
	m.SetColumns(ContainerColumns(w))
	m.SetColorizers(ContainerColorizers())
	m.SetContainers(containers, "", nil, false, nil, nil)

	out := m.View()
	plain := stripANSI(out)
	for _, want := range []string{"Up 2 hours", "Exited", "healthy"} {
		if !strings.Contains(plain, want) {
			t.Errorf("text %q missing from rendered containers view:\n%s", want, plain)
		}
	}
	if !hasStatusColor(out, styles.StatusRunning) { // "Up …" green
		t.Error("running STATUS not colored")
	}
	if !hasStatusColor(out, styles.StatusExited) { // "Exited …" red
		t.Error("exited STATUS not colored")
	}
}

// TestComposeViewStatusTrueColor verifies the compose STATUS column survives
// and is colored under a truecolor profile.
func TestComposeViewStatusTrueColor(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	const w = 120
	projects := []docker.ComposeProject{
		{Name: "webapp", Status: "running", Running: 3, Total: 3},
		{Name: "legacy", Status: "stopped", Running: 0, Total: 2},
	}
	m := New()
	m.SetSize(w, 12)
	m.SetColumns(ComposeColumns(w))
	m.SetColorizers(ComposeColorizers())
	m.SetCompose(projects, "")

	out := m.View()
	plain := stripANSI(out)
	for _, want := range []string{"running 3/3", "stopped 0/2"} {
		if !strings.Contains(plain, want) {
			t.Errorf("text %q missing from rendered compose view:\n%s", want, plain)
		}
	}
	if !hasStatusColor(out, styles.StatusRunning) {
		t.Error("running compose STATUS not colored")
	}
}

// TestContainerStatusStyle locks the status-text → color mapping (used by the
// container STATUS colorizer) against styles.StateColor's intent.
func TestContainerStatusStyle(t *testing.T) {
	tests := []struct {
		status string
		want   lipgloss.Style
	}{
		{"Up 2 hours", styles.StatusRunning},
		{"Up 5 minutes (healthy)", styles.StatusRunning},
		{"Up 3 days (Paused)", styles.StatusOther},
		{"Exited (0) 1 hour ago", styles.StatusExited},
		{"Dead", styles.StatusExited},
		{"Created", styles.StatusOther},
		{"Restarting (1) 2 seconds ago", styles.StatusStopped},
	}
	for _, tt := range tests {
		if got, _ := containerStatusStyle(tt.status); got.GetForeground() != tt.want.GetForeground() {
			t.Errorf("containerStatusStyle(%q) fg = %v, want %v", tt.status, got.GetForeground(), tt.want.GetForeground())
		}
	}
}

func TestSummary(t *testing.T) {
	if got := Summary(5, 5); got != " 5 container(s) " {
		t.Errorf("Summary(5,5) = %q", got)
	}
}

// TestShrinkRowsKeepsCursorInView reproduces the compose drill-down bug: after
// scrolling far down a long list and then loading a much shorter one (e.g.
// drilling from a 130-row compose list into a single container), bubbles'
// SetRows leaves the cursor out of range, so UpdateViewport renders an empty
// window and the list looks blank until an arrow key clamps the cursor back.
// Setting the rows must keep the cursor within range so the rows render.
func TestShrinkRowsKeepsCursorInView(t *testing.T) {
	const w = 120
	m := New()
	m.SetSize(w, 12)
	m.SetColumns(ContainerColumns(w))
	m.SetColorizers(ContainerColorizers())

	many := make([]docker.Container, 130)
	for i := range many {
		many[i] = docker.Container{ID: "id" + strconv.Itoa(i), Name: "c" + strconv.Itoa(i), State: "running"}
	}
	m.SetContainers(many, "", nil, false, nil, nil)
	m.InnerTable().SetCursor(129) // scroll to the bottom of the long list

	// Drill-down loads a single container.
	m.SetContainers([]docker.Container{{ID: "solo", Name: "only-one", State: "running"}}, "", nil, false, nil, nil)

	if c := m.InnerTable().Cursor(); c >= 1 {
		t.Errorf("cursor = %d after shrinking to 1 row, want in [0,0]", c)
	}
	if plain := stripANSI(m.View()); !strings.Contains(plain, "only-one") {
		t.Errorf("rendered view is blank after shrink, want the single row:\n%s", plain)
	}
}
