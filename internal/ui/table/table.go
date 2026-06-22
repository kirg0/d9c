package table

import (
	"cmp"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/ui/filter"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// SortField identifies the column the container list is ordered by. SortNone
// keeps the daemon's own order (the default).
type SortField int

const (
	SortNone SortField = iota
	SortName
	SortStatus
	SortCPU
	SortMem
)

// String returns a short label for the active sort, shown in the header.
func (f SortField) String() string {
	switch f {
	case SortName:
		return "NAME"
	case SortStatus:
		return "STATUS"
	case SortCPU:
		return "CPU"
	case SortMem:
		return "MEM"
	default:
		return ""
	}
}

// selectedPrefix is the ANSI opening sequence bubbles puts at the start of the
// selected (cursor) row — the Selected style's escape. Computed fresh per View
// (not at init): it depends on the active color profile, which a test may
// switch after package load, and an empty prefix (no-color profile) would make
// HasPrefix match every line. The sentinel "\x00" can't appear in cell text.
func selectedPrefix() string {
	if before, _, ok := strings.Cut(styles.TableSelected.Render("\x00"), "\x00"); ok {
		return before
	}
	return ""
}

// Colorizer maps a column's plain cell text to the style it should be drawn
// with. The bool reports whether to apply that style: returning false leaves the
// cell in the base style (so a colorizer can opt out per row, e.g. the alert
// marker only colors breaching rows).
type Colorizer func(plainCell string) (lipgloss.Style, bool)

type Model struct {
	table table.Model
	width int

	// colorize[i], when non-nil, colors column i after bubbles has laid the row
	// out in plain text. Colored cells MUST be stored as plain text in the row:
	// bubbles truncates a cell by counting bytes (ANSI escapes included), so a
	// pre-colored value loses its visible text in a narrow column on truecolor
	// terminals. See View / renderColoredLine.
	colorize []Colorizer

	// sortField/sortDesc order the containers view (other views ignore them).
	sortField SortField
	sortDesc  bool
}

func New() Model {
	t := table.New(
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
	)
	return Model{table: t}
}

// SetSize sets width and height. Caller must call SetColumns separately.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.table.SetWidth(width)
	m.table.SetHeight(height)
}

// SetColorizers installs the per-column colorizers used to color cells after
// layout. Pass nil for views with no colored columns. The slice is indexed by
// column; a nil entry leaves that column in the base style.
func (m *Model) SetColorizers(cs []Colorizer) { m.colorize = cs }

// SetColumns updates column definitions (called on resize or resource switch).
// Rows are cleared ONLY when the column count changes to avoid
// index-out-of-range in renderRow while preserving existing rows on resize.
func (m *Model) SetColumns(cols []table.Column) {
	if len(cols) != len(m.table.Columns()) {
		m.table.SetRows(nil)
	}
	m.table.SetColumns(cols)
}

// ── resource-specific row setters ─────────────────────────────────────────────

func (m *Model) SetContainers(containers []docker.Container, filter string, stats map[string]docker.ContainerStats, statsView bool, selected, alerted map[string]bool) {
	containers = SortContainers(containers, stats, m.sortField, m.sortDesc)
	if statsView {
		m.setRows(buildStatsRows(containers, filter, stats, selected, alerted))
		return
	}
	m.setRows(buildRows(containers, filter, stats, selected, alerted))
}

// setRows installs rows and keeps the cursor in range. bubbles' SetRows leaves
// the cursor untouched, so loading a much shorter list (e.g. drilling from a
// long compose list into one container) would leave it pointing past the end —
// UpdateViewport then renders an empty window and the list looks blank until an
// arrow key clamps the cursor. Re-clamping here keeps the rows visible. A cursor
// already within range is preserved, so a periodic refresh doesn't jump the
// selection.
func (m *Model) setRows(rows []table.Row) {
	m.table.SetRows(rows)
	if m.table.Cursor() >= len(rows) {
		m.table.SetCursor(len(rows) - 1) // SetCursor clamps to ≥0 and re-runs UpdateViewport
	}
}

// SetSort selects the column the containers view is ordered by. SortNone keeps
// the daemon's order. Only the containers view honours it.
func (m *Model) SetSort(field SortField, desc bool) {
	m.sortField = field
	m.sortDesc = desc
}

// SortContainers returns containers reordered by field (stable; the input slice
// is not mutated). CPU/MEM figures come from stats — a container without a
// sample sorts as zero. SortNone returns the slice unchanged. Equal rows fall
// back to NAME ascending regardless of direction, so the order stays stable.
func SortContainers(containers []docker.Container, stats map[string]docker.ContainerStats, field SortField, desc bool) []docker.Container {
	if field == SortNone || len(containers) < 2 {
		return containers
	}
	out := make([]docker.Container, len(containers))
	copy(out, containers)
	sort.SliceStable(out, func(i, j int) bool {
		c := compareContainers(out[i], out[j], stats, field)
		if c == 0 {
			return out[i].Name < out[j].Name
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
	return out
}

// compareContainers orders a before b for field, ascending: <0 a first, >0 b
// first, 0 equal.
func compareContainers(a, b docker.Container, stats map[string]docker.ContainerStats, field SortField) int {
	switch field {
	case SortName:
		return cmp.Compare(a.Name, b.Name)
	case SortStatus:
		return cmp.Compare(a.State, b.State)
	case SortCPU:
		return cmp.Compare(stats[a.ID].CPUPerc, stats[b.ID].CPUPerc)
	case SortMem:
		return cmp.Compare(stats[a.ID].MemUsage, stats[b.ID].MemUsage)
	default:
		return 0
	}
}

// selMarker is the fixed-width prefix prepended to a container NAME cell to show
// bulk-selection state, keeping unselected and selected names aligned.
func selMarker(id string, selected map[string]bool) string {
	if selected[id] {
		return "● "
	}
	return "  "
}

// alertGlyph flags a container row breaching a resource-usage threshold. It is
// stored as plain text in the NAME cell and colored after layout (nameAlertStyle
// / ContainerColorizers); a pre-colored value would be truncated away by bubbles
// in narrow columns on truecolor terminals.
const alertGlyph = '⚠'

// alertMarker is the fixed-width NAME prefix flagging a breaching container. When
// alerted is nil the alert feature is off and no slot is added, so layout is
// unchanged; when active, every row carries a 2-wide slot so names stay aligned
// whether or not the row is breaching.
func alertMarker(id string, alerted map[string]bool) string {
	if alerted == nil {
		return ""
	}
	if alerted[id] {
		return string(alertGlyph) + " "
	}
	return "  "
}

func (m *Model) SetImages(images []docker.Image, filter string, selected map[string]bool) {
	m.setRows(buildImageRows(images, filter, selected))
}

func (m *Model) SetNetworks(networks []docker.Network, filter string) {
	m.setRows(buildNetworkRows(networks, filter))
}

func (m *Model) SetVolumes(volumes []docker.Volume, filter string) {
	m.setRows(buildVolumeRows(volumes, filter))
}

// SetHosts fills the unified hosts/dashboard view; summaries holds per-host
// daemon snapshots keyed by host URL (absent = probe pending).
func (m *Model) SetHosts(hostList []hosts.Host, filter string, summaries map[string]docker.HostSummary) {
	m.setRows(buildHostRows(hostList, filter, summaries))
}

func (m *Model) SetCompose(projects []docker.ComposeProject, filter string) {
	m.setRows(buildComposeRows(projects, filter))
}

// SelectComposeRow moves the cursor to the compose row whose identity column
// (working_dir) equals id; it's a no-op if no row matches. Used to restore the
// selection when returning from a drill-down to the deployment list.
func (m *Model) SelectComposeRow(id string) {
	for i, r := range m.table.Rows() {
		if len(r) > ComposeIDColumn && r[ComposeIDColumn] == id {
			m.table.SetCursor(i)
			return
		}
	}
}

// SelectedID returns the last column of the selected row (the resource ID for
// containers/images/networks, whose last column is a real ID column).
func (m Model) SelectedID() string {
	row := m.SelectedRow()
	if len(row) == 0 {
		return ""
	}
	return row[len(row)-1]
}

// SelectedRow returns the cells of the currently selected row, or nil.
func (m Model) SelectedRow() []string {
	rows := m.table.Rows()
	cursor := m.table.Cursor()
	if cursor < 0 || cursor >= len(rows) {
		return nil
	}
	return rows[cursor]
}

func (m *Model) Update(msg any) {
	m.table, _ = m.table.Update(msg)
}

// View post-processes bubbles output. bubbles truncates each cell with
// runewidth, which counts ANSI-escape bytes as visible width — so a pre-colored
// cell loses its text in a narrow column on truecolor terminals. We therefore
// keep colored content out of the row values and apply color here.
//
// Two paths:
//   - colorized views (containers/compose/hosts): every data row is rebuilt
//     from its plain text so colored columns can be styled after layout, with
//     the selection background preserved under the cursor row.
//   - plain views: only the selected line needs fixing — its inner cell resets
//     break the Selected background, so we strip and re-render it uniformly.
func (m Model) View() string {
	raw := m.table.View()
	if m.width == 0 {
		return raw
	}
	lines := strings.Split(raw, "\n")
	selPrefix := selectedPrefix()
	if len(m.colorize) > 0 {
		for i, line := range lines {
			if i == 0 || strings.ContainsRune(line, '─') {
				continue // header text / header bottom border
			}
			plain := stripANSI(line)
			if strings.TrimSpace(plain) == "" {
				continue // viewport padding
			}
			selected := selPrefix != "" && strings.HasPrefix(line, selPrefix)
			lines[i] = m.renderColoredLine(plain, selected)
		}
		return strings.Join(lines, "\n")
	}
	if selPrefix == "" {
		return raw
	}
	for i, line := range lines {
		if strings.HasPrefix(line, selPrefix) {
			plain := stripANSI(line)
			lines[i] = styles.TableSelected.Width(m.width).Render(plain)
			break
		}
	}
	return strings.Join(lines, "\n")
}

// renderColoredLine rebuilds one data row from its plain text, applying each
// column's colorizer (if any) and leaving the rest in the base style. On the
// selected (cursor) row the base is the selection highlight and a column's
// color is composited onto the selection background, so colored cells stay
// readable under the cursor. Cells are split by display width so they stay
// aligned exactly as bubbles laid them out.
func (m Model) renderColoredLine(plain string, selected bool) string {
	base := styles.TableCell
	if selected {
		base = styles.TableSelected
	}
	cols := m.table.Columns()
	runes := []rune(plain)
	pos := 0
	var b strings.Builder
	for i, c := range cols {
		if c.Width <= 0 {
			continue
		}
		start, acc := pos, 0
		for pos < len(runes) && acc < c.Width {
			acc += runewidth.RuneWidth(runes[pos])
			pos++
		}
		seg := string(runes[start:pos])
		style := base
		if i < len(m.colorize) && m.colorize[i] != nil {
			if s, ok := m.colorize[i](strings.TrimSpace(seg)); ok {
				style = s
				if selected {
					style = style.Background(styles.SelectedBg)
				}
			}
		}
		b.WriteString(style.Render(seg))
	}
	// Anything past the last column (viewport row padding) plus any remaining
	// gap to full width keeps the base style, so the highlight spans the row.
	if pos < len(runes) {
		b.WriteString(base.Render(string(runes[pos:])))
	}
	if gap := m.width - lipgloss.Width(plain); gap > 0 {
		b.WriteString(base.Render(strings.Repeat(" ", gap)))
	}
	return b.String()
}

func (m Model) Table() table.Model        { return m.table }
func (m *Model) InnerTable() *table.Model { return &m.table }

// ── column builders ───────────────────────────────────────────────────────────

func ContainerColumns(w int) []table.Column {
	return columns(w)
}

// ContainerStatsColumns is the alternate container layout (toggled with 's')
// that mirrors `docker stats`: live CPU/MEM/MEM%/NET I/O/BLOCK I/O. ID stays
// last so Model.selectedID() keeps working.
func ContainerStatsColumns(w int) []table.Column {
	f := func(pct float64) int { return int(float64(w) * pct) }
	return []table.Column{
		{Title: "NAME", Width: f(0.18)},
		{Title: "CPU %", Width: f(0.10)},
		{Title: "MEM", Width: f(0.14)},
		{Title: "MEM %", Width: f(0.10)},
		{Title: "NET I/O", Width: f(0.19)},
		{Title: "BLOCK I/O", Width: f(0.19)},
		{Title: "ID", Width: f(0.10)},
	}
}

func ImageColumns(w int) []table.Column {
	f := func(pct float64) int { return int(float64(w) * pct) }
	return []table.Column{
		{Title: "REPOSITORY:TAG", Width: f(0.45)},
		{Title: "SIZE", Width: f(0.12)},
		{Title: "CREATED", Width: f(0.25)},
		{Title: "ID", Width: f(0.18)},
	}
}

func NetworkColumns(w int) []table.Column {
	f := func(pct float64) int { return int(float64(w) * pct) }
	return []table.Column{
		{Title: "NAME", Width: f(0.28)},
		{Title: "DRIVER", Width: f(0.14)},
		{Title: "SCOPE", Width: f(0.12)},
		{Title: "SUBNET", Width: f(0.28)},
		{Title: "ID", Width: f(0.18)},
	}
}

func VolumeColumns(w int) []table.Column {
	f := func(pct float64) int { return int(float64(w) * pct) }
	return []table.Column{
		{Title: "NAME", Width: f(0.28)},
		{Title: "DRIVER", Width: f(0.12)},
		{Title: "MOUNTPOINT", Width: f(0.42)},
		{Title: "CREATED", Width: f(0.18)},
	}
}

// HostColumns is the unified hosts/dashboard layout: saved-host identity plus
// the aggregate daemon summary (`docker info`). NAME stays first (identity for
// selectedID) and STATUS (col 2) is colorized after layout.
func HostColumns(w int) []table.Column {
	f := func(pct float64) int { return int(float64(w) * pct) }
	return []table.Column{
		{Title: "NAME", Width: f(0.15)},
		{Title: "HOST", Width: f(0.27)},
		{Title: "STATUS", Width: f(0.11)},
		{Title: "CONTAINERS", Width: f(0.13)},
		{Title: "RUNNING", Width: f(0.11)},
		{Title: "IMAGES", Width: f(0.10)},
		{Title: "VERSION", Width: f(0.13)},
	}
}

// ComposeIDColumn is the index of the column holding a deployment's identity
// (its full working_dir). Model.selectedID() reads it for the compose view — the
// PROJECT column is first but not unique, so it can't serve as the identity.
const ComposeIDColumn = 2

func ComposeColumns(w int) []table.Column {
	f := func(pct float64) int { return int(float64(w) * pct) }
	return []table.Column{
		{Title: "PROJECT", Width: f(0.16)},
		{Title: "NAME", Width: f(0.18)},
		{Title: "PATH", Width: f(0.30)},
		{Title: "STATUS", Width: f(0.15)},
		{Title: "COMMAND", Width: f(0.19)},
	}
}

// ── colorizers (per-column, applied after layout in View) ───────────────────

// ContainerColorizers colors the NAME (alert ⚠ marker), STATUS and HEALTH
// columns of the default container layout; the column order matches columns().
func ContainerColorizers() []Colorizer {
	return []Colorizer{nameAlertStyle, nil, containerStatusStyle, healthStyle, nil, nil, nil, nil}
}

// ContainerStatsColorizers colors the NAME column's alert ⚠ marker in the
// `docker stats`-style layout (7 columns); the other columns stay base.
func ContainerStatsColorizers() []Colorizer {
	return []Colorizer{nameAlertStyle, nil, nil, nil, nil, nil, nil}
}

// ComposeColorizers colors the STATUS column of the compose layout.
func ComposeColorizers() []Colorizer {
	return []Colorizer{nil, nil, nil, composeStatusStyle, nil}
}

// HostColorizers colors the STATUS column of the hosts layout (col 2).
func HostColorizers() []Colorizer {
	return []Colorizer{nil, nil, hostStatusStyle, nil, nil, nil, nil}
}

// nameAlertStyle colors a container NAME cell red when it carries the alert
// marker glyph (⚠), leaving every other row in the base style.
func nameAlertStyle(name string) (lipgloss.Style, bool) {
	if strings.ContainsRune(name, alertGlyph) {
		return styles.Alert, true
	}
	return lipgloss.Style{}, false
}

// containerStatusStyle picks a color from a container's status text (e.g.
// "Up 2 hours", "Exited (0) …"), mirroring styles.StateColor without needing
// the State field, since the post-layout step only sees the cell text.
func containerStatusStyle(status string) (lipgloss.Style, bool) {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "paused"):
		return styles.StatusOther, true
	case strings.HasPrefix(s, "up"):
		return styles.StatusRunning, true
	case strings.HasPrefix(s, "exited"), strings.HasPrefix(s, "dead"):
		return styles.StatusExited, true
	case strings.HasPrefix(s, "created"):
		return styles.StatusOther, true
	default: // restarting, removing, unknown
		return styles.StatusStopped, true
	}
}

// healthStyle colors a healthcheck verdict cell ("healthy"/"unhealthy"/…).
func healthStyle(health string) (lipgloss.Style, bool) {
	return styles.HealthColor(health), true
}

// composeStatusStyle colors a compose STATUS cell ("running 2/3" → "running").
func composeStatusStyle(status string) (lipgloss.Style, bool) {
	word, _, _ := strings.Cut(status, " ")
	return styles.ComposeStatusColor(word), true
}

// ── row builders ──────────────────────────────────────────────────────────────

// containerTarget builds the filter haystack for a container: the free-text
// search spans name+image+status, while status/labels/networks back the
// status:/label:/network: predicates.
func containerTarget(c docker.Container) filter.Target {
	return filter.Target{
		Text:     c.Name + c.Image + c.Status,
		Status:   c.Status + " " + c.State,
		Labels:   c.Labels,
		Networks: c.Networks,
	}
}

// buildRows is used by unit tests (package-internal). The stats map is keyed by
// container ID; CPU%/MEM show "-" for containers without a sample (stopped, or
// stats not yet fetched). HEALTH shows the healthcheck verdict or "-" when the
// container defines none.
func buildRows(containers []docker.Container, filterStr string, stats map[string]docker.ContainerStats, selected, alerted map[string]bool) []table.Row {
	rows := make([]table.Row, 0, len(containers))
	matcher := filter.Compile(filterStr)
	for _, c := range containers {
		if !matcher.Match(containerTarget(c)) {
			continue
		}
		// STATUS/HEALTH (and the alert ⚠ marker) are stored as PLAIN text and
		// colored after layout (see ContainerColorizers / View); a pre-colored
		// value would be truncated away by bubbles in narrow columns on
		// truecolor terminals.
		health := "-"
		if c.Health != "" {
			health = c.Health
		}
		cpu, mem := "-", "-"
		if s, ok := stats[c.ID]; ok {
			cpu = s.CPUString()
			mem = s.MemString()
		}
		rows = append(rows, table.Row{
			selMarker(c.ID, selected) + alertMarker(c.ID, alerted) + c.Name,
			truncate(c.Image, 35),
			c.Status,
			health,
			truncate(c.Ports, 25),
			cpu,
			mem,
			c.ID,
		})
	}
	return rows
}

// buildStatsRows builds rows for the `docker stats`-style container layout.
// Cell count must match ContainerStatsColumns (7); "-" for missing samples.
func buildStatsRows(containers []docker.Container, filterStr string, stats map[string]docker.ContainerStats, selected, alerted map[string]bool) []table.Row {
	rows := make([]table.Row, 0, len(containers))
	matcher := filter.Compile(filterStr)
	for _, c := range containers {
		if !matcher.Match(containerTarget(c)) {
			continue
		}
		cpu, mem, memp, net, blk := "-", "-", "-", "-", "-"
		if s, ok := stats[c.ID]; ok {
			cpu = s.CPUString()
			mem = s.MemString()
			memp = s.MemPercString()
			net = s.NetString()
			blk = s.BlockString()
		}
		rows = append(rows, table.Row{selMarker(c.ID, selected) + alertMarker(c.ID, alerted) + c.Name, cpu, mem, memp, net, blk, c.ID})
	}
	return rows
}

// sortByName returns items reordered by the key extracted from each element,
// ascending and case-insensitively (stable; the input slice is not mutated).
// Network/Volume/Image/Compose lists come back from the daemon in an unstable
// order, so without this a periodic refresh reshuffles the rows under the
// cursor. Equal keys keep their relative order, so the result stays steady.
func sortByName[T any](items []T, key func(T) string) []T {
	if len(items) < 2 {
		return items
	}
	out := make([]T, len(items))
	copy(out, items)
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(key(out[i])) < strings.ToLower(key(out[j]))
	})
	return out
}

func buildImageRows(images []docker.Image, filterStr string, selected map[string]bool) []table.Row {
	images = sortByName(images, func(i docker.Image) string { return i.Tags })
	rows := make([]table.Row, 0, len(images))
	matcher := filter.Compile(filterStr)
	for _, img := range images {
		if !matcher.Match(filter.Target{Text: img.Tags + img.ID}) {
			continue
		}
		rows = append(rows, table.Row{
			// Identity is the ID column (last), so prefixing the selection marker
			// to REPOSITORY:TAG keeps selectedID()/the cell-count invariant intact.
			selMarker(img.ID, selected) + truncate(img.Tags, 55),
			img.Size,
			timeAgo(img.Created),
			img.ID,
		})
	}
	return rows
}

func buildNetworkRows(networks []docker.Network, filterStr string) []table.Row {
	networks = sortByName(networks, func(n docker.Network) string { return n.Name })
	rows := make([]table.Row, 0, len(networks))
	matcher := filter.Compile(filterStr)
	for _, n := range networks {
		if !matcher.Match(filter.Target{Text: n.Name + n.Driver + n.Subnet}) {
			continue
		}
		rows = append(rows, table.Row{
			truncate(n.Name, 35),
			n.Driver,
			n.Scope,
			n.Subnet,
			n.ID,
		})
	}
	return rows
}

func buildVolumeRows(volumes []docker.Volume, filterStr string) []table.Row {
	volumes = sortByName(volumes, func(v docker.Volume) string { return v.Name })
	rows := make([]table.Row, 0, len(volumes))
	matcher := filter.Compile(filterStr)
	for _, v := range volumes {
		if !matcher.Match(filter.Target{Text: v.Name + v.Driver + v.Mountpoint}) {
			continue
		}
		rows = append(rows, table.Row{
			// NAME is the row's identity (selectedID) — never truncate it here;
			// bubbles clips the cell to the column width for display anyway.
			// Anonymous volumes have 64-char hex names that must survive intact.
			v.Name,
			v.Driver,
			truncate(v.Mountpoint, 50),
			v.Created,
		})
	}
	return rows
}

// Host STATUS cell tokens. Stored as PLAIN text in the row: bubbles truncates
// a cell by counting bytes (ANSI escapes included), so a pre-colored value
// would have its visible text eaten in truecolor terminals. The color is
// applied later, after layout, in View → renderColoredLine (HostColorizers).
const (
	hostStatusUp      = "● up"
	hostStatusDown    = "● down"
	hostStatusPending = "…"
)

// buildHostRows builds one row per saved host for the unified hosts/dashboard
// view: NAME (identity) + HOST + colored STATUS token + aggregate daemon counts
// from `docker info`. A host with no summary yet shows "…" and "-" placeholders;
// an unreachable host shows "● down" with "-" counts. STATUS is stored as plain
// text and colored after layout (HostColorizers / View).
func buildHostRows(hostList []hosts.Host, filterStr string, summaries map[string]docker.HostSummary) []table.Row {
	rows := make([]table.Row, 0, len(hostList))
	matcher := filter.Compile(filterStr)
	for _, h := range hostList {
		if !matcher.Match(filter.Target{Text: h.Name + h.Host}) {
			continue
		}
		st := hostStatusPending
		containers, running, images, version := "-", "-", "-", "-"
		if s, ok := summaries[h.Host]; ok {
			if s.Reachable {
				st = hostStatusUp
				containers = strconv.Itoa(s.Containers)
				running = strconv.Itoa(s.Running)
				images = strconv.Itoa(s.Images)
				version = s.Version
			} else {
				st = hostStatusDown
			}
		}
		rows = append(rows, table.Row{
			h.Name, // identity column (selectedID) — keep intact
			h.Host,
			st,
			containers,
			running,
			images,
			version,
		})
	}
	return rows
}

// hostStatusStyle maps a plain STATUS token to its color.
func hostStatusStyle(token string) (lipgloss.Style, bool) {
	switch token {
	case hostStatusUp:
		return styles.StatusRunning, true
	case hostStatusDown:
		return styles.StatusExited, true
	default:
		return styles.StatusStopped, true
	}
}

func buildComposeRows(projects []docker.ComposeProject, filterStr string) []table.Row {
	// PROJECT is the first column but not unique; tie-break by working_dir (the
	// row identity) so deployments sharing a project keep a steady order.
	projects = sortByName(projects, func(p docker.ComposeProject) string { return p.Project + "\x00" + p.WorkingDir })
	rows := make([]table.Row, 0, len(projects))
	matcher := filter.Compile(filterStr)
	for _, p := range projects {
		if !matcher.Match(filter.Target{Text: p.Project + p.Name + p.WorkingDir + p.Status, Status: p.Status}) {
			continue
		}
		// STATUS is plain text, colored after layout (ComposeColorizers / View).
		status := fmt.Sprintf("%s %d/%d", p.Status, p.Running, p.Total)
		rows = append(rows, table.Row{
			truncate(p.Project, 22),
			truncate(p.Name, 26),
			p.WorkingDir, // identity column (selectedID/ComposeIDColumn) — keep intact
			status,
			truncate(p.Command, 28),
		})
	}
	return rows
}

// ── helpers ───────────────────────────────────────────────────────────────────

func columns(totalWidth int) []table.Column {
	w := func(pct float64) int { return int(float64(totalWidth) * pct) }
	return []table.Column{
		{Title: "NAME", Width: w(0.19)},
		{Title: "IMAGE", Width: w(0.20)},
		{Title: "STATUS", Width: w(0.15)},
		{Title: "HEALTH", Width: w(0.09)},
		{Title: "PORTS", Width: w(0.15)},
		{Title: "CPU %", Width: w(0.07)},
		{Title: "MEM", Width: w(0.08)},
		{Title: "ID", Width: w(0.07)},
	}
}

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = styles.TableHeader
	s.Selected = styles.TableSelected
	s.Cell = styles.TableCell
	return s
}

// truncate shortens display-only cells to at most max runes. It must never be
// applied to identity columns (the ones selectedID reads) — a clipped value
// would no longer match the real resource name. Rune-based so multi-byte text
// (e.g. Cyrillic image names or paths) is never cut mid-character.
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// stripANSI removes ANSI SGR escape sequences (\x1b[...m) from s.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Summary returns a human-readable count string.
func Summary(total, shown int) string {
	if total == shown {
		return fmt.Sprintf(" %d container(s) ", total)
	}
	return lipgloss.NewStyle().Render(fmt.Sprintf(" %d/%d containers (filtered) ", shown, total))
}
