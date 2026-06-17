package ui

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"d9c/internal/alerts"
	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/keymap"
	"d9c/internal/plugins"
	"d9c/internal/ui/cmdline"
	"d9c/internal/ui/composeedit"
	"d9c/internal/ui/detail"
	"d9c/internal/ui/events"
	"d9c/internal/ui/execform"
	"d9c/internal/ui/filter"
	"d9c/internal/ui/fsbrowser"
	"d9c/internal/ui/help"
	"d9c/internal/ui/hostform"
	"d9c/internal/ui/logs"
	"d9c/internal/ui/netform"
	"d9c/internal/ui/pushform"
	"d9c/internal/ui/runform"
	"d9c/internal/ui/shell"
	"d9c/internal/ui/table"
	"d9c/internal/ui/volform"

	tea "github.com/charmbracelet/bubbletea"
)

// ResourceView identifies which Docker resource type is currently displayed.
type ResourceView int

const (
	ViewContainers ResourceView = iota
	ViewImages
	ViewNetworks
	ViewVolumes
	ViewHosts
	ViewCompose
)

func (r ResourceView) String() string {
	switch r {
	case ViewImages:
		return "Images"
	case ViewNetworks:
		return "Networks"
	case ViewVolumes:
		return "Volumes"
	case ViewHosts:
		return "Hosts"
	case ViewCompose:
		return "Compose"
	default:
		return "Containers"
	}
}

type Mode int

const (
	ModeNormal       Mode = iota
	ModeDetail            // 'i'
	ModeFilter            // '/'
	ModeCommand           // ':'
	ModeLogs              // logs viewer
	ModeCopy              // right-click / 'y' copy menu
	ModeHostForm          // add/edit host modal form
	ModeComposeEdit       // compose file editor
	ModeBackupPicker      // backup catalog overlay (list/restore/delete)
	ModeHelp              // keyboard/command reference overlay ('?')
	ModeShell             // embedded interactive exec terminal ('x')
	ModeEvents            // live daemon events feed
	ModePushForm          // registry-credentials modal for image push
	ModeNetForm           // create-network modal form
	ModeVolForm           // create-volume modal form
	ModeRunForm           // run-container wizard modal form
	ModeExecForm          // one-off interactive run wizard (`run --rm -it`)
	ModeConfirm           // generic y/esc confirmation overlay
	ModeFSBrowser         // container filesystem browser ('f' / :files)
	ModeNotice            // informational modal (e.g. SSH known_hosts mismatch)
)

// copyItem is one selectable entry in the copy overlay.
type copyItem struct {
	Label string
	Value string
}

// Auto-refresh cadence bounds. The interval is user-controllable at runtime via
// `:interval <dur>` (and the -interval flag); clampInterval keeps it sane.
const (
	defaultRefreshInterval = 3 * time.Second
	minRefreshInterval     = 1 * time.Second
	maxRefreshInterval     = 1 * time.Hour
)

// clampInterval bounds d to [minRefreshInterval, maxRefreshInterval].
func clampInterval(d time.Duration) time.Duration {
	switch {
	case d < minRefreshInterval:
		return minRefreshInterval
	case d > maxRefreshInterval:
		return maxRefreshInterval
	default:
		return d
	}
}

// Saved-host summaries back the hosts view (STATUS + aggregate `docker info`
// counts): each opens a fresh connection to a host, so a batch is heavier than
// the refresh tick and runs less often. hostSummaryTimeout bounds one host's
// probe before it's reported down; hostSummaryInterval throttles new batches.
const (
	hostSummaryTimeout  = 5 * time.Second
	hostSummaryInterval = 10 * time.Second
)

// clearCopyNotifMsg clears the "copied!" footer notification.
type clearCopyNotifMsg struct{}

// ── messages ──────────────────────────────────────────────────────────────────

type containersUpdatedMsg struct{ containers []docker.Container }
type imagesUpdatedMsg struct{ images []docker.Image }
type networksUpdatedMsg struct{ networks []docker.Network }
type volumesUpdatedMsg struct{ volumes []docker.Volume }
type hostsUpdatedMsg struct{ hosts []hosts.Host }
type composeUpdatedMsg struct{ projects []docker.ComposeProject }

// statsUpdatedMsg carries fresh container resource samples (best-effort).
type statsUpdatedMsg struct {
	stats map[string]docker.ContainerStats
}

// Operation-console messages drive the live progress view for long-running
// compose-engine commands (up/pull/down) streamed over SSH.
type (
	// opStartedMsg opens the console once the streaming channel is ready. stop
	// releases the backend resources behind the stream (SSH session, daemon
	// request, local process) and is stored as the console's logStop so closing
	// the console tears the stream down; it may be nil for streams with no
	// lifecycle to release.
	opStartedMsg struct {
		ch    <-chan string
		stop  func()
		title string
	}
	// opLineMsg carries one streamed progress line.
	opLineMsg struct {
		title string
		line  string
	}
	// opDoneMsg signals the stream closed (operation finished).
	opDoneMsg struct{ title string }
)

// showComposeContainersMsg drills into a compose project's containers.
type showComposeContainersMsg struct{ project string }

// showDetailMsg opens the detail viewer with arbitrary content (e.g. compose config).
type showDetailMsg struct{ result *docker.InspectResult }

// openComposeEditMsg opens the compose file editor with loaded content.
type openComposeEditMsg struct {
	project string
	path    string
	content string
}

// openComposeCreateMsg opens the editor to author a new project in dir.
type openComposeCreateMsg struct{ dir string }

// composeSavedMsg carries the outcome of saving an edited compose file.
type composeSavedMsg struct{ err error }

// composeBackupMsg carries the outcome of backing up a compose project.
type composeBackupMsg struct {
	path string
	err  error
}

// backupsListedMsg carries the catalog of existing backups for a deployment.
// project is the identity (working_dir) used to restore; name is the display
// name the archives are filed under.
type backupsListedMsg struct {
	project string
	name    string
	items   []backupEntry
	err     error
}

// backupEntry is one archive in the backup catalog.
type backupEntry struct {
	name    string
	path    string
	size    int64
	modTime time.Time
}

// openHostFormMsg requests opening the add/edit modal form.
type openHostFormMsg struct {
	editing bool
	name    string
	host    string
}
type inspectResultMsg struct{ result *docker.InspectResult }
type switchResourceMsg struct{ resource ResourceView }
type actionResultMsg struct{ err error }

// connectResultMsg carries the outcome of a live :connect to another host.
type connectResultMsg struct {
	backend docker.Backend
	host    string
	err     error
}
type logsOpenedMsg struct {
	ch          <-chan string
	containerID string
	// stop tears down the backend stream; the UI must call it when the viewer
	// closes or the stream is replaced, or the producer goroutine leaks.
	stop func()
}

// reconnectResultMsg carries the outcome of one auto-reconnect attempt.
type reconnectResultMsg struct {
	backend docker.Backend
	err     error
	attempt int
}

// pingResultMsg carries the outcome of the periodic daemon health check that
// drives the server-status dot in the header. seq identifies the backend
// generation the ping was issued against, so a result that raced a host
// switch is dropped instead of painting the dot for the wrong server.
type pingResultMsg struct {
	seq int
	err error
}

// hostSummariesMsg carries per-host daemon snapshots for the hosts view (STATUS
// + aggregate counts), keyed by host URL.
type hostSummariesMsg struct{ summaries map[string]docker.HostSummary }

// execMsg carries a freshly-opened interactive exec session to drive the
// embedded terminal panel, along with a display title (container name).
type execMsg struct {
	session docker.ExecSession
	title   string
}

// execDoneMsg reports the outcome of a terminal hand-off (used by interactive
// plugins, which suspend the TUI via tea.Exec rather than embedding a panel).
type execDoneMsg struct{ err error }
type tickMsg time.Time
type errMsg struct{ err error }

// logsSavedMsg carries the outcome of saving the current log buffer to a file.
type logsSavedMsg struct {
	path string
	err  error
}

// openEventsMsg is emitted when the events stream is ready and the viewer
// should open. stop follows the same contract as logsOpenedMsg.stop.
type openEventsMsg struct {
	ch   <-chan string
	stop func()
}

// eventsLineMsg carries one line from the daemon event stream.
type eventsLineMsg struct{ line string }

// openPushFormMsg requests the registry-credentials modal for pushing ref.
type openPushFormMsg struct{ ref string }

// openNetFormMsg requests the create-network modal form.
type openNetFormMsg struct{}

// openVolFormMsg requests the create-volume modal form.
type openVolFormMsg struct{}

// openRunFormMsg requests the run-container wizard, optionally pre-filling the
// image reference (e.g. when invoked from the images view).
type openRunFormMsg struct{ image string }

// openExecFormMsg requests the one-off interactive run wizard, optionally
// pre-filling the image reference.
type openExecFormMsg struct{ image string }

// openConfirmMsg requests the generic confirmation overlay: prompt is shown
// centered, and action runs when the user confirms (y/enter).
type openConfirmMsg struct {
	prompt string
	action tea.Cmd
}

// openNoticeMsg requests the informational modal: a small centered panel with
// a title and a body, dismissed by any key. Used for explanatory dialogs that
// don't take a yes/no decision — e.g. an SSH host-key mismatch suggesting the
// user clean known_hosts.
type openNoticeMsg struct {
	title string
	body  string
}

// systemPruneMsg carries the outcome of a full system prune.
type systemPruneMsg struct {
	summary string
	err     error
}

// fsListedMsg carries a container directory listing: it both opens the browser
// (first listing for a container) and refreshes it on navigation. On error the
// browser stays at its current path (a failed descent), or — if not yet open —
// the error goes to the footer.
type fsListedMsg struct {
	containerID string
	name        string
	path        string
	entries     []docker.FileEntry
	err         error
}

// fsCopiedMsg carries the outcome of a download from the container (browser 'd').
type fsCopiedMsg struct {
	name string // base name written into the working directory
	err  error
}

// ── model ─────────────────────────────────────────────────────────────────────

type Model struct {
	cfg     *config.Config
	backend docker.Backend

	mode     Mode
	resource ResourceView

	// Data per resource type
	containers []docker.Container
	images     []docker.Image
	networks   []docker.Network
	volumes    []docker.Volume
	hosts      []hosts.Host
	composes   []docker.ComposeProject

	// Live container resource samples (CPU/MEM), keyed by container ID.
	stats map[string]docker.ContainerStats

	// statsInFlight is set while a stats batch is being collected, so refresh
	// ticks don't pile up overlapping batches on hosts with many containers.
	statsInFlight bool

	// statsView toggles the containers table between the default columns and the
	// `docker stats`-style layout (CPU/MEM/MEM%/NET I/O/BLOCK I/O).
	statsView bool

	// alerts holds the resource-usage thresholds that flag containers (⚠ row
	// marker + header count). Disabled (zero) by default; set via the config file
	// or the :alert command.
	alerts alerts.Thresholds

	// sortField/sortDesc order the containers view (NAME/STATUS/CPU/MEM). The
	// sort spec is also pushed into the table model so refreshes keep the order.
	sortField table.SortField
	sortDesc  bool

	// selected holds the container IDs marked for a bulk operation (Space to
	// toggle). Only used in the containers view; empty means "act on the cursor".
	selected map[string]bool

	// Saved-hosts store (persisted next to the binary)
	hostStore *hosts.Store

	// When set, the containers view is scoped to this compose project.
	composeFilter string

	// When set, the next compose-list refresh restores the cursor to this
	// deployment (its working_dir) — so leaving a drill-down returns to the row
	// it was opened from instead of jumping to the top. Cleared once applied.
	composeReselect string

	showAll bool

	// refreshInterval is the auto-refresh cadence (configurable at runtime via
	// `:interval`). paused freezes auto-refresh while the heartbeat (status dot)
	// keeps ticking; manual refresh (`r`) still works while paused.
	refreshInterval time.Duration
	paused          bool

	// Components
	table       table.Model
	detail      detail.Model
	filter      filter.Model
	cmdline     cmdline.Model
	logs        logs.Model
	hostForm    hostform.Model
	composeEdit composeedit.Model
	help        help.Model
	shell       shell.Model
	eventsModel events.Model
	pushForm    pushform.Model
	netForm     netform.Model
	volForm     volform.Model
	runForm     runform.Model
	execForm    execform.Model
	fsBrowser   fsbrowser.Model

	// pushAuth remembers registry credentials for the session (keyed by registry
	// host), so the push form pre-fills after the first push. Never persisted.
	pushAuth map[string]docker.RegistryAuth

	// Active log/event streams; nil when not streaming. The stop functions
	// release the backend resources behind a stream and MUST be called when the
	// channel is abandoned (view closed, stream replaced, host switched).
	logCh     <-chan string
	logStop   func()
	eventCh   <-chan string
	eventStop func()

	// opTitle is non-empty while the logs viewer is showing a streamed
	// operation console (compose up/pull/down) rather than container logs.
	opTitle string

	// UI state
	width  int
	height int
	err    string

	// Auto-reconnect state: set while retrying a dropped daemon connection.
	reconnecting     bool
	reconnectAttempt int

	// serverUp mirrors the last known daemon health (periodic Ping on the
	// refresh tick) and drives the status dot in the header.
	serverUp bool

	// pingInFlight keeps a single health check in flight, so slow pings (e.g.
	// over SSH) don't pile up across refresh ticks. pingSeq is the current
	// backend generation; it is bumped on every backend swap to invalidate
	// pings still in flight against the old connection.
	pingInFlight bool
	pingSeq      int

	// Saved-host daemon snapshots back the hosts view (STATUS + aggregate
	// `docker info` counts), keyed by host URL (absent = not summarized yet).
	// summarizeHost is the per-host probe, injected so demo mode and tests never
	// touch the network. One batch at a time, started no more often than
	// hostSummaryInterval.
	summaries       map[string]docker.HostSummary
	summarizeHost   func(host string) docker.HostSummary
	summaryInFlight bool
	lastHostSummary time.Time

	// User-defined plugins (custom :commands / key actions). May be nil.
	plugins *plugins.Set

	// keys binds normal-mode action keys (configurable via d9c-config.yaml).
	keys keymap.Map

	// Copy overlay state
	copyItems  []copyItem
	copyCursor int
	copyNotif  string // brief "✔ copied" message shown in footer

	// Backup catalog overlay state
	backupItems         []backupEntry
	backupCursor        int
	backupProject       string // identity (working_dir) of the catalog's deployment
	backupName          string // its display name (archive prefix / restore title)
	backupConfirmDelete string // name awaiting a second 'd' to confirm deletion

	// Generic confirmation overlay (ModeConfirm) state.
	confirmPrompt string
	confirmAction tea.Cmd

	// Informational notice overlay (ModeNotice) state.
	noticeTitle string
	noticeBody  string

	// startupNotice, when non-nil, is dispatched from Init so a startup
	// connection failure that warrants an explanatory dialog (currently:
	// SSH known_hosts mismatch) opens the modal instead of just sitting
	// in the footer.
	startupNotice *openNoticeMsg
}

// NewModel builds the root model. The app starts in the hosts view when
// startInHosts is set (e.g. no host configured, or the initial connection
// failed); connectErr, when non-nil, is shown as the footer error.
func NewModel(cfg *config.Config, backend docker.Backend, store *hosts.Store, connectErr error, startInHosts bool) Model {
	if store == nil {
		store = &hosts.Store{}
	}
	resource := ViewContainers
	if startInHosts || connectErr != nil {
		resource = ViewHosts
	}
	// A zero/unset interval (e.g. tests building Config directly) falls back to
	// the default; anything explicit is clamped into the sane range.
	interval := cfg.RefreshInterval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	errStr := ""
	if connectErr != nil && !docker.IsHostKeyError(connectErr) && !docker.IsHostNotFoundError(connectErr) {
		errStr = connectErr.Error()
	}
	cmd := cmdline.New()
	cmd.SetResource(strings.ToLower(resource.String()))
	m := Model{
		cfg:         cfg,
		backend:     backend,
		hostStore:   store,
		resource:    resource,
		err:         errStr,
		showAll:     cfg.ShowAll,
		table:       table.New(),
		detail:      detail.New(),
		filter:      filter.New(),
		cmdline:     cmd,
		logs:        logs.New(),
		hostForm:    hostform.New(),
		composeEdit: composeedit.New(),
		help:        help.New(),
		shell:       shell.New(),
		eventsModel: events.New(),
		pushForm:    pushform.New(),
		netForm:     netform.New(),
		volForm:     volform.New(),
		runForm:     runform.New(),
		execForm:    execform.New(),
		fsBrowser:   fsbrowser.New(),
		pushAuth:    map[string]docker.RegistryAuth{},
		keys:        keymap.Default(),
		// The first periodic ping lands within one refresh tick and corrects
		// this initial guess either way.
		serverUp:        connectErr == nil && !startInHosts,
		summaries:       map[string]docker.HostSummary{},
		refreshInterval: clampInterval(interval),
	}
	// Saved-host summaries dial real TCP/SSH connections; demo mode (which also
	// backs the headless TUI tests) stubs them out so it stays network-free.
	if cfg.Demo {
		m.summarizeHost = func(h string) docker.HostSummary {
			s, _ := docker.NewFakeBackend().Info()
			s.Host = h
			return s
		}
	} else {
		m.summarizeHost = func(h string) docker.HostSummary {
			return docker.ProbeHostSummary(cfg, h, hostSummaryTimeout)
		}
	}
	// Seed columns for the starting resource so a fast first data update can't
	// render rows against an empty column set (panic in bubbles renderRow).
	m.applyColumns(0)
	if connectErr != nil && docker.IsHostKeyError(connectErr) {
		title, body := hostKeyNoticeText(cfg.Host)
		m.startupNotice = &openNoticeMsg{title: title, body: body}
	} else if connectErr != nil && docker.IsHostNotFoundError(connectErr) {
		title, body := hostNotFoundNoticeText(cfg.Host)
		m.startupNotice = &openNoticeMsg{title: title, body: body}
	}
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.fetchCurrentResource(), tickCmd(m.refreshInterval), enableQuickEditCmd()}
	// If the initial connect failed because the SSH host key changed, surface it
	// as the notice modal instead of a footer error line — same dialog the user
	// would get from a live :connect.
	if m.startupNotice != nil {
		notice := *m.startupNotice
		cmds = append(cmds, func() tea.Msg { return notice })
	}
	return tea.Batch(cmds...)
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// pingCmd checks daemon health off the event loop; the result updates the
// server-status dot in the header. seq tags the result with the backend
// generation it was issued against.
func pingCmd(b docker.Backend, seq int) tea.Cmd {
	return func() tea.Msg {
		return pingResultMsg{seq: seq, err: b.Ping()}
	}
}

// ── fetch helpers ─────────────────────────────────────────────────────────────

func fetchContainers(b docker.Backend, showAll bool) tea.Cmd {
	return func() tea.Msg {
		cs, err := b.ListContainers(showAll)
		if err != nil {
			return errMsg{err}
		}
		return containersUpdatedMsg{cs}
	}
}

// fetchStats samples CPU/MEM for the given container IDs off the event loop.
// Stats are best-effort: an error yields an empty update (no error banner) —
// the message must still arrive so the in-flight guard is released.
func fetchStats(b docker.Backend, ids []string) tea.Cmd {
	if len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		st, err := b.ContainerStats(ids)
		if err != nil {
			return statsUpdatedMsg{}
		}
		return statsUpdatedMsg{st}
	}
}

// mergeStats overlays freshly sampled figures onto the previous ones: a running
// container missing from this batch (its sample timed out or errored) keeps its
// last known values instead of blinking off, while entries for containers that
// no longer exist or have stopped are dropped.
func mergeStats(old, fresh map[string]docker.ContainerStats, containers []docker.Container) map[string]docker.ContainerStats {
	out := make(map[string]docker.ContainerStats, len(fresh))
	for _, c := range containers {
		if s, ok := fresh[c.ID]; ok {
			out[c.ID] = s
			continue
		}
		if c.State != "running" {
			continue
		}
		if s, ok := old[c.ID]; ok {
			out[c.ID] = s
		}
	}
	return out
}

// containerAlerts evaluates the live stats against the configured thresholds.
// Returns nil when alerts are disabled.
func (m Model) containerAlerts() []alerts.Breach {
	return alerts.Evaluate(m.containers, m.stats, m.alerts)
}

// containerAlertSet returns the set of breaching container IDs for the table row
// markers, or nil when alerts are disabled (which keeps the NAME column free of
// the alignment slot).
func (m Model) containerAlertSet() map[string]bool {
	if !m.alerts.Active() {
		return nil
	}
	return alerts.BreachSet(m.containerAlerts())
}

// runningIDs returns the IDs of the running containers (the only ones with stats).
func runningIDs(cs []docker.Container) []string {
	ids := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.State == "running" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func fetchImages(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		imgs, err := b.ListImages()
		if err != nil {
			return errMsg{err}
		}
		return imagesUpdatedMsg{imgs}
	}
}

func fetchNetworks(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		nets, err := b.ListNetworks()
		if err != nil {
			return errMsg{err}
		}
		return networksUpdatedMsg{nets}
	}
}

func fetchVolumes(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		vols, err := b.ListVolumes()
		if err != nil {
			return errMsg{err}
		}
		return volumesUpdatedMsg{vols}
	}
}

// fetchHosts reads the saved-hosts store (in-memory, synchronous).
func fetchHosts(store *hosts.Store) tea.Cmd {
	return func() tea.Msg {
		return hostsUpdatedMsg{store.List()}
	}
}

// summarizeHostsCmd fetches a daemon summary from every saved host concurrently
// (each bounded by hostSummaryTimeout inside summarize) and delivers a
// URL→summary map for the hosts view (STATUS + aggregate counts).
func summarizeHostsCmd(summarize func(host string) docker.HostSummary, hostURLs []string) tea.Cmd {
	if summarize == nil || len(hostURLs) == 0 {
		return nil
	}
	return func() tea.Msg {
		out := make(map[string]docker.HostSummary, len(hostURLs))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, h := range hostURLs {
			wg.Add(1)
			go func(h string) {
				defer wg.Done()
				s := summarize(h)
				mu.Lock()
				out[h] = s
				mu.Unlock()
			}(h)
		}
		wg.Wait()
		return hostSummariesMsg{out}
	}
}

// hostURLs extracts the host URLs from the saved-host list (probe batch input).
func hostURLs(list []hosts.Host) []string {
	urls := make([]string, 0, len(list))
	for _, h := range list {
		urls = append(urls, h.Host)
	}
	return urls
}

func fetchCompose(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		projects, err := b.ListComposeProjects()
		if err != nil {
			return errMsg{err}
		}
		return composeUpdatedMsg{projects}
	}
}

// streamOpCmd starts a streaming operation (compose up/pull/down, image
// build/push) and, once the channel is ready, opens the progress console under
// the given title.
func streamOpCmd(start func() (<-chan string, func(), error), title string) tea.Cmd {
	return func() tea.Msg {
		ch, stop, err := start()
		if err != nil {
			return errMsg{err}
		}
		return opStartedMsg{ch: ch, stop: stop, title: title}
	}
}

// streamOp reads the next progress line, re-subscribing until the channel
// closes, at which point it reports completion.
func streamOp(ch <-chan string, title string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return opDoneMsg{title: title}
		}
		return opLineMsg{title: title, line: line}
	}
}

// imageHistoryCmd fetches an image's layer history and opens it in the detail viewer.
func imageHistoryCmd(b docker.Backend, ref string) tea.Cmd {
	return func() tea.Msg {
		res, err := b.ImageHistory(ref)
		if err != nil {
			return errMsg{err}
		}
		return showDetailMsg{result: res}
	}
}

// composeConfigCmd fetches `docker compose config` and opens it in the detail viewer.
func composeConfigCmd(b docker.Backend, project string) tea.Cmd {
	return func() tea.Msg {
		out, err := b.ComposeConfig(project)
		if err != nil {
			return errMsg{err}
		}
		return showDetailMsg{result: &docker.InspectResult{Name: project + " · config", RawYAML: out}}
	}
}

// createComposeCmd writes a new docker-compose.yaml in dir and brings it up,
// opening the streaming operation console with the `up` output.
func createComposeCmd(b docker.Backend, dir, content string) tea.Cmd {
	return func() tea.Msg {
		ch, stop, err := b.CreateComposeFile(dir, content)
		if err != nil {
			return errMsg{err}
		}
		return opStartedMsg{ch: ch, stop: stop, title: "compose create: " + dir}
	}
}

// backupComposeCmd downloads a tar.gz of the project's working directory.
func backupComposeCmd(b docker.Backend, project string) tea.Cmd {
	return func() tea.Msg {
		path, err := b.BackupComposeProject(project)
		return composeBackupMsg{path: path, err: err}
	}
}

// listBackupsCmd scans the working directory for a deployment's backup archives
// and opens the catalog. identity scopes the eventual restore; name (the
// deployment's display name) is how the archives are named and matched.
func listBackupsCmd(identity, name string) tea.Cmd {
	return func() tea.Msg {
		items, err := listBackups(".", name)
		return backupsListedMsg{project: identity, name: name, items: items, err: err}
	}
}

// backupTimestampRe matches the "<8 digits>-<6 digits>" stamp that
// backupFileName appends, so a project's archives are recognised unambiguously
// (e.g. "web-…" won't capture "web-api-…").
var backupTimestampRe = regexp.MustCompile(`^\d{8}-\d{6}$`)

// isBackupFile reports whether name is a backup archive for the given prefix
// ("<sanitized-project>-"): it must carry that prefix, the ".tar.gz" suffix and
// a valid timestamp in between.
func isBackupFile(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
		return false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".tar.gz")
	return backupTimestampRe.MatchString(mid)
}

// listBackups returns the project's backup archives found in dir, newest first.
func listBackups(dir, project string) ([]backupEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := docker.BackupFilePrefix(project) + "-"
	var out []backupEntry
	for _, e := range entries {
		if e.IsDir() || !isBackupFile(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, backupEntry{
			name:    e.Name(),
			path:    filepath.Join(dir, e.Name()),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

// humanSize renders a byte count as a compact human-readable string.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// restoreComposeCmd uploads a backup archive, extracts it into the project's
// working directory and brings it back up, streaming progress to the console.
func restoreComposeCmd(b docker.Backend, project, label, backupPath string) tea.Cmd {
	return func() tea.Msg {
		ch, stop, err := b.RestoreComposeProject(project, backupPath)
		if err != nil {
			return errMsg{err}
		}
		return opStartedMsg{ch: ch, stop: stop, title: "compose restore: " + label}
	}
}

// composeEditCmd reads the project's compose file and opens it in the editor.
func composeEditCmd(b docker.Backend, project string) tea.Cmd {
	return func() tea.Msg {
		path, content, err := b.ReadComposeFile(project)
		if err != nil {
			return errMsg{err}
		}
		return openComposeEditMsg{project: project, path: path, content: content}
	}
}

// composeSaveCmd writes edited content back to the project's compose file.
func composeSaveCmd(b docker.Backend, project, content string) tea.Cmd {
	return func() tea.Msg {
		return composeSavedMsg{err: b.WriteComposeFile(project, content)}
	}
}

// fetchComposeContainers lists the containers of a single compose project.
func fetchComposeContainers(b docker.Backend, project string) tea.Cmd {
	return func() tea.Msg {
		cs, err := b.ListComposeContainers(project)
		if err != nil {
			return errMsg{err}
		}
		return containersUpdatedMsg{cs}
	}
}

// connectCmd builds a new backend for the given host URL, reusing the auth
// settings of the current config. It runs off the event loop because SSH
// backends dial during construction.
func connectCmd(base *config.Config, hostURL string) tea.Cmd {
	return func() tea.Msg {
		cfg := *base // copy auth fields (SSH key/password, TLS) from current config
		cfg.Host = hostURL
		b, err := docker.New(&cfg)
		if err != nil {
			return connectResultMsg{err: err, host: hostURL}
		}
		return connectResultMsg{backend: b, host: hostURL}
	}
}

// maybeStartReconnect enters the auto-reconnect state when err signals a lost
// daemon connection (and a host is configured to reconnect to), returning the
// first retry command. It returns nil when err is an ordinary operational
// error or a retry is already running.
func (m *Model) maybeStartReconnect(err error) tea.Cmd {
	if m.reconnecting || m.cfg == nil || m.cfg.Host == "" || !docker.IsConnectionError(err) {
		return nil
	}
	m.reconnecting = true
	m.reconnectAttempt = 1
	m.serverUp = false
	m.err = ""
	return reconnectCmd(m.cfg, m.cfg.Host, 1)
}

// reconnectCmd waits out the backoff for this attempt, then tries to rebuild and
// ping a backend for the given host. It runs off the event loop. The config is
// copied (and the host pinned) so a concurrent :connect can't race it.
func reconnectCmd(base *config.Config, hostURL string, attempt int) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(reconnectBackoff(attempt))
		cfg := *base // copy auth fields (SSH key/password, TLS)
		cfg.Host = hostURL
		b, err := docker.New(&cfg)
		if err == nil {
			if err = b.Ping(); err != nil {
				b.Close() // built but unhealthy — don't leak it
			}
		}
		if err != nil {
			return reconnectResultMsg{err: err, attempt: attempt}
		}
		return reconnectResultMsg{backend: b, attempt: attempt}
	}
}

// reconnectBackoff returns the delay before the given attempt: exponential
// (1s, 2s, 4s, …) capped at 30s.
func reconnectBackoff(attempt int) time.Duration {
	const maxDelay = 30 * time.Second
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 30 { // guard the shift against overflow
		return maxDelay
	}
	d := time.Second << (attempt - 1)
	if d <= 0 || d > maxDelay {
		return maxDelay
	}
	return d
}

// closeBackendCmd closes a (usually stale) backend off the event loop so a slow
// Close can't block Update.
func closeBackendCmd(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		if b != nil {
			b.Close()
		}
		return nil
	}
}

func fetchInspect(b docker.Backend, resource ResourceView, id string) tea.Cmd {
	return func() tea.Msg {
		var result *docker.InspectResult
		var err error
		switch resource {
		case ViewImages:
			result, err = b.InspectImage(id)
		case ViewNetworks:
			result, err = b.InspectNetwork(id)
		case ViewVolumes:
			result, err = b.InspectVolume(id)
		case ViewCompose:
			result, err = b.InspectComposeProject(id)
		default:
			result, err = b.InspectContainer(id)
		}
		if err != nil {
			return errMsg{err}
		}
		return inspectResultMsg{result}
	}
}

func openLogs(b docker.Backend, id string, opts docker.LogOptions) tea.Cmd {
	return func() tea.Msg {
		ch, stop, err := b.ContainerLogs(id, opts)
		if err != nil {
			return errMsg{err}
		}
		return logsOpenedMsg{ch: ch, containerID: id, stop: stop}
	}
}

// execCmd opens an interactive exec session for the container and, once the
// session is ready, hands it to the embedded terminal panel.
func execCmd(b docker.Backend, id, title string, args []string) tea.Cmd {
	return func() tea.Msg {
		session, err := b.ExecInteractive(id, args)
		if err != nil {
			return errMsg{err}
		}
		return execMsg{session: session, title: title}
	}
}

// systemDFCmd fetches the daemon's disk usage and opens it in the detail viewer.
func systemDFCmd(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		res, err := b.SystemDF()
		if err != nil {
			return errMsg{err}
		}
		return showDetailMsg{result: res}
	}
}

// systemPruneCmd runs a full system prune and reports the summary.
func systemPruneCmd(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		summary, err := b.SystemPrune()
		return systemPruneMsg{summary: summary, err: err}
	}
}

// runInteractiveCmd starts a disposable interactive container from an image
// (the exec wizard) and hands the session to the embedded terminal panel.
func runInteractiveCmd(b docker.Backend, opts docker.ExecRunOptions) tea.Cmd {
	return func() tea.Msg {
		session, err := b.RunInteractive(opts)
		if err != nil {
			return errMsg{err}
		}
		return execMsg{session: session, title: opts.Image + " · one-off"}
	}
}

// fsListCmd lists dir inside the container off the event loop; the result both
// opens and refreshes the filesystem browser. containerID/name are echoed back
// so the handler can title the panel and keep targeting the same container.
func fsListCmd(b docker.Backend, containerID, name, dir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := b.ListPath(containerID, dir)
		return fsListedMsg{containerID: containerID, name: name, path: dir, entries: entries, err: err}
	}
}

// fsDownloadCmd downloads srcPath from the container into the working directory.
func fsDownloadCmd(b docker.Backend, containerID, srcPath, baseName string) tea.Cmd {
	return func() tea.Msg {
		err := b.CopyFromContainer(containerID, srcPath, ".")
		return fsCopiedMsg{name: baseName, err: err}
	}
}

// stopLogStream releases the active log stream (if any): the backend closes the
// Follow connection and the pending streamLogs cmd unblocks on channel close.
func (m *Model) stopLogStream() {
	if m.logStop != nil {
		m.logStop()
		m.logStop = nil
	}
	m.logCh = nil
}

// stopEventStream releases the active daemon-events subscription (if any).
func (m *Model) stopEventStream() {
	if m.eventStop != nil {
		m.eventStop()
		m.eventStop = nil
	}
	m.eventCh = nil
}

// containerName returns the display name for a container ID, falling back to the
// ID when the container isn't in the current list.
func (m Model) containerName(id string) string {
	for _, c := range m.containers {
		if c.ID == id {
			return c.Name
		}
	}
	return id
}

// openComposeLogs streams the aggregated logs of a whole compose project.
func openComposeLogs(b docker.Backend, project string, opts docker.LogOptions) tea.Cmd {
	return func() tea.Msg {
		ch, stop, err := b.ComposeLogs(project, opts)
		if err != nil {
			return errMsg{err}
		}
		return logsOpenedMsg{ch: ch, containerID: project, stop: stop}
	}
}

// saveLogsCmd writes the current log buffer to a timestamped file in the working
// directory and reports the path (or error) back to the UI.
func saveLogsCmd(name, content string) tea.Cmd {
	return func() tea.Msg {
		path := logFileName(name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return logsSavedMsg{err: err}
		}
		return logsSavedMsg{path: path}
	}
}

// logFileName builds a safe "<name>-<timestamp>.log" file name.
func logFileName(name string) string {
	safe := sanitizeFileName(name)
	if safe == "" {
		safe = "logs"
	}
	return safe + "-" + time.Now().Format("20060102-150405") + ".log"
}

// truncateRunes shortens a display string to at most max runes, appending an
// ellipsis when it clips. It counts and cuts by rune (not byte) so multi-byte
// text (e.g. Cyrillic error messages / names) is never split mid-character —
// the same invariant table.truncate keeps for table cells.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// sanitizeFileName keeps only filename-safe characters, replacing the rest with
// '-' and trimming leading/trailing separators.
func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func streamLogs(ch <-chan string, containerID string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return logs.LineMsg{ContainerID: containerID, Line: line}
	}
}

// openEvents starts the daemon event stream and opens the events viewer.
func openEvents(b docker.Backend) tea.Cmd {
	return func() tea.Msg {
		ch, stop, err := b.Events()
		if err != nil {
			return errMsg{err}
		}
		return openEventsMsg{ch: ch, stop: stop}
	}
}

// streamEvents reads the next event line, re-subscribing until the channel
// closes, at which point it stops (no restart — the user can press :events
// again if they want to reconnect).
func streamEvents(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return eventsLineMsg{line: line}
	}
}

func containerAction(fn func() error) tea.Cmd {
	return func() tea.Msg {
		return actionResultMsg{fn()}
	}
}

func clearCopyNotifCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearCopyNotifMsg{} })
}

// selectedID returns the identity of the selected row: NAME (first column) for
// hosts and volumes, the working_dir column for compose deployments (PROJECT is
// first but not unique), and the trailing ID column for the other resources.
func (m Model) selectedID() string {
	row := m.table.SelectedRow()
	if len(row) == 0 {
		return ""
	}
	switch m.resource {
	case ViewHosts, ViewVolumes:
		return row[0]
	case ViewCompose:
		if len(row) > table.ComposeIDColumn {
			return row[table.ComposeIDColumn]
		}
		return ""
	default:
		return row[len(row)-1]
	}
}

// composeNameFor returns the display name of the deployment identified by id
// (its working_dir), falling back to the path's base — used to keep titles and
// breadcrumbs short instead of showing the full working_dir.
func (m Model) composeNameFor(id string) string {
	for _, p := range m.composes {
		if p.WorkingDir == id {
			return p.Name
		}
	}
	return path.Base(id)
}

// composeFilterLabel is the human label for the active compose drill-down.
func (m Model) composeFilterLabel() string { return m.composeNameFor(m.composeFilter) }

// buildCopyItems returns the copyable fields for the currently selected resource.
func (m Model) buildCopyItems() []copyItem {
	var items []copyItem
	id := m.selectedID()

	switch m.resource {
	case ViewContainers:
		for _, c := range m.containers {
			if c.ID == id {
				items = []copyItem{
					{"Name", c.Name},
					{"Image", c.Image},
					{"Status", c.Status},
					{"ID", c.ID},
				}
				if c.Health != "" {
					items = append(items, copyItem{"Health", c.Health})
				}
				if c.Ports != "" {
					items = append(items, copyItem{"Ports", c.Ports})
				}
				if s, ok := m.stats[c.ID]; ok {
					items = append(items, copyItem{"CPU", s.CPUString()})
					items = append(items, copyItem{"Memory", s.MemString()})
				}
				break
			}
		}
	case ViewImages:
		for _, img := range m.images {
			if img.ID == id {
				items = []copyItem{
					{"Tags", img.Tags},
					{"ID", img.ID},
					{"Size", img.Size},
				}
				break
			}
		}
	case ViewNetworks:
		for _, n := range m.networks {
			if n.ID == id {
				items = []copyItem{
					{"Name", n.Name},
					{"ID", n.ID},
					{"Driver", n.Driver},
				}
				if n.Subnet != "" {
					items = append(items, copyItem{"Subnet", n.Subnet})
				}
				break
			}
		}
	case ViewVolumes:
		for _, v := range m.volumes {
			if v.Name == id {
				items = []copyItem{
					{"Name", v.Name},
					{"Driver", v.Driver},
					{"Mountpoint", v.Mountpoint},
				}
				break
			}
		}
	case ViewHosts:
		for _, h := range m.hosts {
			if h.Name == id {
				items = []copyItem{
					{"Name", h.Name},
					{"Host", h.Host},
				}
				if s, ok := m.summaries[h.Host]; ok && s.Reachable {
					items = append(items,
						copyItem{"Version", s.Version},
						copyItem{"Containers", fmt.Sprintf("%d (running %d)", s.Containers, s.Running)},
						copyItem{"Images", fmt.Sprintf("%d", s.Images)},
					)
				}
				break
			}
		}
	case ViewCompose:
		for _, p := range m.composes {
			if p.WorkingDir == id {
				items = []copyItem{
					{"Project", p.Project},
					{"Name", p.Name},
					{"Path", p.WorkingDir},
					{"Status", p.Status},
					{"Command", p.Command},
				}
				break
			}
		}
	}

	// Allow copying the last error message too.
	if m.err != "" {
		items = append(items, copyItem{"Error", m.err})
	}

	return items
}
