package ui

import (
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"time"

	"d9c/internal/alerts"
	"d9c/internal/docker"
	"d9c/internal/keymap"
	"d9c/internal/theme"
	"d9c/internal/ui/cmdline"
	"d9c/internal/ui/composeedit"
	"d9c/internal/ui/fsbrowser"
	"d9c/internal/ui/logs"
	"d9c/internal/ui/shell"
	"d9c/internal/ui/styles"
	uitbl "d9c/internal/ui/table"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		// A live shell needs the remote TTY resized to the new grid.
		if m.mode == ModeShell {
			return m, m.shell.ResizeRemoteCmd()
		}
		return m, nil

	case tickMsg:
		// While reconnecting, keep the heartbeat but don't issue fetches that would
		// just fail against the dead connection.
		if m.reconnecting {
			return m, tickCmd(m.refreshInterval)
		}
		cmds := []tea.Cmd{tickCmd(m.refreshInterval)}
		// Auto-refresh, unless paused — the heartbeat (status dot) keeps ticking
		// either way, and manual refresh (`r`) still works while paused.
		if !m.paused {
			cmds = append(cmds, m.fetchCurrentResource())
		}
		// Health check for the header status dot; one in flight at a time so
		// slow pings don't stack up.
		if !m.pingInFlight {
			m.pingInFlight = true
			cmds = append(cmds, pingCmd(m.backend, m.pingSeq))
		}
		return m, tea.Batch(cmds...)

	case pingResultMsg:
		// A ping issued against a backend we've since swapped out says nothing
		// about the current connection.
		if msg.seq != m.pingSeq {
			return m, nil
		}
		m.pingInFlight = false
		// A reconnect kicked off while the ping was in flight owns the state now.
		if m.reconnecting {
			return m, nil
		}
		m.serverUp = msg.err == nil
		// The heartbeat noticing a dropped connection starts the same
		// auto-reconnect flow as a failed fetch.
		if msg.err != nil {
			if cmd := m.maybeStartReconnect(msg.err); cmd != nil {
				return m, cmd
			}
		}
		return m, nil

	case containersUpdatedMsg:
		m.containers = msg.containers
		if m.resource == ViewContainers {
			m.table.SetContainers(m.containers, m.filter.Value(), m.stats, m.statsView, m.selected, m.containerAlertSet())
			// Keep a single stats batch in flight: on hosts with many containers
			// a batch can outlive the refresh tick, and overlapping batches would
			// land out of order, making the figures flicker.
			if !m.statsInFlight {
				if cmd := fetchStats(m.backend, runningIDs(m.containers)); cmd != nil {
					m.statsInFlight = true
					return m, cmd
				}
			}
		}
		return m, nil

	case statsUpdatedMsg:
		m.statsInFlight = false
		// Merge instead of replace: a container whose sample missed this batch
		// keeps its last known figures instead of blinking off.
		m.stats = mergeStats(m.stats, msg.stats, m.containers)
		if m.resource == ViewContainers {
			m.refreshTableRows()
		}
		return m, nil

	case imagesUpdatedMsg:
		m.images = msg.images
		if m.resource == ViewImages {
			m.table.SetImages(m.images, m.filter.Value())
		}
		return m, nil

	case networksUpdatedMsg:
		m.networks = msg.networks
		if m.resource == ViewNetworks {
			m.table.SetNetworks(m.networks, m.filter.Value())
		}
		return m, nil

	case volumesUpdatedMsg:
		m.volumes = msg.volumes
		if m.resource == ViewVolumes {
			m.table.SetVolumes(m.volumes, m.filter.Value())
		}
		return m, nil

	case hostsUpdatedMsg:
		m.hosts = msg.hosts
		if m.resource == ViewHosts {
			m.table.SetHosts(m.hosts, m.filter.Value(), m.summaries)
			// Per-host daemon summaries (STATUS + aggregate counts): one batch at a
			// time, and not on every refresh tick — each summary dials a fresh
			// TCP/SSH connection per host.
			if !m.summaryInFlight && time.Since(m.lastHostSummary) >= hostSummaryInterval {
				if cmd := summarizeHostsCmd(m.summarizeHost, hostURLs(m.hosts)); cmd != nil {
					m.summaryInFlight = true
					m.lastHostSummary = time.Now()
					return m, cmd
				}
			}
		}
		return m, nil

	case hostSummariesMsg:
		m.summaryInFlight = false
		// Merge instead of replace: a host added mid-batch keeps its pending "…"
		// only until the next batch, and nothing already known blinks off.
		maps.Copy(m.summaries, msg.summaries)
		if m.resource == ViewHosts {
			m.table.SetHosts(m.hosts, m.filter.Value(), m.summaries)
		}
		return m, nil

	case composeUpdatedMsg:
		m.composes = msg.projects
		if m.resource == ViewCompose {
			m.table.SetCompose(m.composes, m.filter.Value())
		}
		return m, nil

	case showDetailMsg:
		m.detail.SetContent(msg.result)
		m.mode = ModeDetail
		m.relayout()
		return m, nil

	case openComposeEditMsg:
		m.mode = ModeComposeEdit
		m.relayout()
		cmd := m.composeEdit.SetContent(msg.project, msg.path, msg.content)
		return m, cmd

	case openComposeCreateMsg:
		m.mode = ModeComposeEdit
		m.relayout()
		return m, m.composeEdit.SetCreate(msg.dir)

	case composeSavedMsg:
		m.composeEdit.SetSaving(false)
		if msg.err != nil {
			m.composeEdit.SetError(msg.err.Error())
			return m, nil
		}
		m.mode = ModeNormal
		m.relayout()
		return m, m.fetchCurrentResource()

	case showComposeContainersMsg:
		m.composeFilter = msg.project
		m.resource = ViewContainers
		m.selected = nil
		m.cmdline.SetResource("containers")
		m.refreshPluginCmds()
		m.filter.Reset()
		m.filter.Blur()
		m.mode = ModeNormal
		m.err = ""
		m.relayout()
		return m, m.fetchCurrentResource()

	case openHostFormMsg:
		if msg.editing {
			m.hostForm.OpenEdit(msg.name, msg.host)
		} else {
			m.hostForm.OpenAdd()
		}
		m.mode = ModeHostForm
		m.relayout()
		return m, nil

	case connectResultMsg:
		if msg.err != nil {
			m.err = fmt.Sprintf("connect to %s failed: %v", msg.host, msg.err)
			return m, nil
		}
		// Swap the live backend over to the newly connected host. Streams of the
		// old backend die with it; release them so their producers unblock.
		m.stopLogStream()
		m.stopEventStream()
		if m.backend != nil {
			m.backend.Close()
		}
		m.backend = msg.backend
		m.cfg.Host = msg.host
		// Forget the old host's samples and let the new host fetch immediately.
		m.stats = nil
		m.statsInFlight = false
		// Invalidate pings still in flight against the old backend.
		m.pingSeq++
		m.pingInFlight = false
		m.serverUp = true
		m.err = ""
		// A manual connect supersedes any in-flight auto-reconnect.
		m.reconnecting = false
		m.reconnectAttempt = 0
		m.resource = ViewContainers
		m.cmdline.SetResource("containers")
		m.refreshPluginCmds()
		m.relayout()
		return m, m.fetchCurrentResource()

	case inspectResultMsg:
		m.detail.SetContent(msg.result)
		return m, nil

	case switchResourceMsg:
		m.resource = msg.resource
		m.composeFilter = "" // explicit view switch leaves any compose drill-down
		m.selected = nil
		m.cmdline.SetResource(strings.ToLower(m.resource.String()))
		m.refreshPluginCmds()
		m.filter.Reset()
		m.filter.Blur()
		m.mode = ModeNormal
		m.cmdline.Reset()
		m.cmdline.Blur()
		m.err = ""
		m.relayout()
		return m, m.fetchCurrentResource()

	case logsOpenedMsg:
		m.stopLogStream() // release any stream this one replaces
		m.logCh = msg.ch
		m.logStop = msg.stop
		m.opTitle = ""
		m.logs.Open(msg.containerID)
		m.mode = ModeLogs
		return m, streamLogs(m.logCh, msg.containerID)

	case execMsg:
		// Open the embedded terminal panel and start pumping the session. The
		// app's header/footer stay on screen for the lifetime of the shell.
		m.mode = ModeShell
		m.err = ""
		m.relayout() // sizes the shell panel before its terminal is created
		return m, m.shell.Open(msg.session, msg.title)

	case shell.OutputMsg, shell.ClosedMsg:
		updated, cmd := m.shell.Update(msg)
		m.shell = updated
		if e := m.shell.ExitErr(); e != nil {
			m.err = "shell: " + e.Error()
		}
		return m, cmd

	case execDoneMsg:
		if msg.err != nil {
			m.err = "exec: " + msg.err.Error()
			return m, nil
		}
		m.err = ""
		// State may have changed during the session; refresh on return.
		return m, m.fetchCurrentResource()

	case opStartedMsg:
		m.stopLogStream() // release any container-log stream the console replaces
		m.logCh = msg.ch
		m.opTitle = msg.title
		m.logs.Open(msg.title)
		m.mode = ModeLogs
		return m, streamOp(m.logCh, msg.title)

	case opLineMsg:
		if msg.title == m.opTitle {
			m.logs.AddLine(msg.line)
			return m, streamOp(m.logCh, msg.title)
		}
		return m, nil

	case opDoneMsg:
		if msg.title == m.opTitle {
			m.logs.AddLine("")
			m.logs.AddLine("— готово (q/esc чтобы закрыть) —")
			m.logCh = nil
			// Refresh data in the background so statuses are current when the
			// user closes the console; stay here so the output stays readable.
			return m, m.fetchCurrentResource()
		}
		return m, nil

	case logs.LineMsg:
		if msg.ContainerID == m.logs.ContainerID() {
			m.logs.AddLine(msg.Line)
			return m, streamLogs(m.logCh, msg.ContainerID)
		}
		return m, nil

	case openEventsMsg:
		m.stopEventStream() // release any subscription this one replaces
		m.eventCh = msg.ch
		m.eventStop = msg.stop
		m.mode = ModeEvents
		m.relayout() // size the events panel before the first line streams in
		m.eventsModel.Open()
		return m, streamEvents(m.eventCh)

	case eventsLineMsg:
		if m.mode == ModeEvents {
			m.eventsModel.AddLine(msg.line)
			return m, streamEvents(m.eventCh)
		}
		return m, nil

	case openPushFormMsg:
		reg := docker.RegistryFromRef(msg.ref)
		remembered := m.pushAuth[reg]
		m.pushForm.Open(msg.ref, reg, remembered.Username, remembered.Password)
		m.mode = ModePushForm
		m.relayout()
		return m, nil

	case openNetFormMsg:
		m.netForm.Open()
		m.mode = ModeNetForm
		m.relayout()
		return m, nil

	case openVolFormMsg:
		m.volForm.Open()
		m.mode = ModeVolForm
		m.relayout()
		return m, nil

	case openRunFormMsg:
		m.runForm.Open(msg.image)
		m.mode = ModeRunForm
		m.relayout()
		return m, nil

	case openExecFormMsg:
		m.execForm.Open(msg.image)
		m.mode = ModeExecForm
		m.relayout()
		return m, nil

	case openConfirmMsg:
		m.confirmPrompt = msg.prompt
		m.confirmAction = msg.action
		m.mode = ModeConfirm
		return m, nil

	case systemPruneMsg:
		if msg.err != nil {
			// Partial progress is still worth showing alongside the error.
			m.err = msg.err.Error()
			if msg.summary != "" {
				m.copyNotif = msg.summary
				return m, tea.Batch(clearCopyNotifCmd(), m.fetchCurrentResource())
			}
			return m, nil
		}
		m.err = ""
		m.copyNotif = msg.summary
		return m, tea.Batch(clearCopyNotifCmd(), m.fetchCurrentResource())

	case fsListedMsg:
		if msg.err != nil {
			// A failed descent keeps the browser open at its current directory;
			// a failed initial open (browser not up yet) surfaces in the footer.
			if m.mode == ModeFSBrowser {
				m.fsBrowser.SetError(msg.err.Error())
				return m, nil
			}
			m.err = "files: " + msg.err.Error()
			return m, nil
		}
		m.fsBrowser.Show(msg.containerID, msg.name, msg.path, msg.entries)
		m.mode = ModeFSBrowser
		m.relayout()
		return m, nil

	case fsCopiedMsg:
		if msg.err != nil {
			m.fsBrowser.SetError("копирование: " + msg.err.Error())
			return m, nil
		}
		m.copyNotif = "скопировано: ./" + msg.name
		return m, clearCopyNotifCmd()

	case actionResultMsg:
		if msg.err != nil {
			// A failed create lands inside the still-open modal form, so the user
			// can correct the input; other errors go to the footer.
			switch m.mode {
			case ModeNetForm:
				m.netForm.SetError(msg.err.Error())
				return m, nil
			case ModeVolForm:
				m.volForm.SetError(msg.err.Error())
				return m, nil
			case ModeRunForm:
				m.runForm.SetError(msg.err.Error())
				return m, nil
			}
			m.err = msg.err.Error()
			// Don't refresh immediately on error — keeps error visible in footer.
			return m, nil
		}
		m.err = ""
		m.mode = ModeNormal
		m.selected = nil // bulk targets are consumed; clear the marks
		return m, m.fetchCurrentResource()

	case logsSavedMsg:
		if msg.err != nil {
			m.err = "save logs: " + msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.copyNotif = "logs saved: " + msg.path
		return m, clearCopyNotifCmd()

	case composeBackupMsg:
		if msg.err != nil {
			m.err = "backup: " + msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.copyNotif = "backup saved: " + msg.path
		return m, clearCopyNotifCmd()

	case backupsListedMsg:
		if msg.err != nil {
			m.err = "backups: " + msg.err.Error()
			m.mode = ModeNormal
			return m, nil
		}
		if len(msg.items) == 0 {
			m.err = "no backups found for " + msg.project + " (use :backup first)"
			m.mode = ModeNormal
			return m, nil
		}
		m.err = ""
		m.backupItems = msg.items
		m.backupProject = msg.project
		m.backupConfirmDelete = ""
		if m.backupCursor >= len(msg.items) {
			m.backupCursor = 0
		}
		m.mode = ModeBackupPicker
		return m, nil

	case errMsg:
		// Already retrying a dropped connection: swallow further error noise so the
		// reconnecting banner stays clean.
		if m.reconnecting {
			return m, nil
		}
		// A lost daemon connection kicks off auto-reconnect with backoff rather
		// than just surfacing the error.
		if cmd := m.maybeStartReconnect(msg.err); cmd != nil {
			return m, cmd
		}
		// A failed one-off run lands inside the still-open exec wizard so the
		// user can correct the image/command.
		if m.mode == ModeExecForm {
			m.execForm.SetError(msg.err.Error())
			return m, nil
		}
		m.err = msg.err.Error()
		return m, nil

	case reconnectResultMsg:
		// Drop results that arrived after we already recovered or switched hosts.
		if !m.reconnecting {
			return m, closeBackendCmd(msg.backend)
		}
		if msg.err != nil {
			m.reconnectAttempt = msg.attempt + 1
			return m, reconnectCmd(m.cfg, m.cfg.Host, m.reconnectAttempt)
		}
		old := m.backend
		m.backend = msg.backend
		m.reconnecting = false
		m.reconnectAttempt = 0
		// Invalidate pings still in flight against the dropped connection.
		m.pingSeq++
		m.pingInFlight = false
		m.serverUp = true
		m.err = ""
		// Streams of the dropped connection are dead; unblock their producers.
		m.stopLogStream()
		m.stopEventStream()
		// A stats batch stuck on the dead connection shouldn't gate the new one.
		m.statsInFlight = false
		return m, tea.Batch(closeBackendCmd(old), m.fetchCurrentResource())

	case clearCopyNotifMsg:
		m.copyNotif = ""
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// fetchCurrentResource returns a Cmd that refreshes data for the active view.
func (m Model) fetchCurrentResource() tea.Cmd {
	switch m.resource {
	case ViewImages:
		return fetchImages(m.backend)
	case ViewNetworks:
		return fetchNetworks(m.backend)
	case ViewVolumes:
		return fetchVolumes(m.backend)
	case ViewHosts:
		// Per-host summaries are kicked off from the hostsUpdatedMsg handler.
		return fetchHosts(m.hostStore)
	case ViewCompose:
		return fetchCompose(m.backend)
	default:
		if m.composeFilter != "" {
			return fetchComposeContainers(m.backend, m.composeFilter)
		}
		return fetchContainers(m.backend, m.showAll)
	}
}

// refreshTableRows re-applies the current filter to the table rows.
func (m *Model) refreshTableRows() {
	switch m.resource {
	case ViewImages:
		m.table.SetImages(m.images, m.filter.Value())
	case ViewNetworks:
		m.table.SetNetworks(m.networks, m.filter.Value())
	case ViewVolumes:
		m.table.SetVolumes(m.volumes, m.filter.Value())
	case ViewHosts:
		m.table.SetHosts(m.hosts, m.filter.Value(), m.summaries)
	case ViewCompose:
		m.table.SetCompose(m.composes, m.filter.Value())
	default:
		m.table.SetContainers(m.containers, m.filter.Value(), m.stats, m.statsView, m.selected, m.containerAlertSet())
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// The embedded shell captures the keyboard wholesale (including ctrl+c, which
	// must reach the remote process), so it runs before the global exit keys.
	if m.mode == ModeShell {
		return m.handleShell(msg)
	}

	// Global exit
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		if m.mode == ModeNormal {
			return m, tea.Quit
		}
	case "esc":
		if m.mode == ModeCopy {
			m.mode = ModeNormal
			return m, nil
		}
		// Esc first clears a pending bulk selection (before popping any view).
		if m.mode == ModeNormal && len(m.selected) > 0 {
			m.selected = nil
			m.refreshTableRows()
			return m, nil
		}
		// Esc in a compose drill-down (normal mode) pops back to the project list.
		if m.mode == ModeNormal && m.composeFilter != "" {
			return m, func() tea.Msg { return switchResourceMsg{ViewCompose} }
		}
		// Detail handles its own esc (search). Logs likewise, but only while a
		// search is active — otherwise esc should still close the logs view.
		logsSearch := m.mode == ModeLogs && (m.logs.IsSearching() || m.logs.HasSearch())
		if m.mode != ModeDetail && !logsSearch {
			// Closing a streaming view abandons its channel; release the stream.
			if m.mode == ModeLogs {
				m.stopLogStream()
				m.opTitle = ""
			}
			if m.mode == ModeEvents {
				m.stopEventStream()
			}
			if m.mode == ModeConfirm { // cancelled: drop the pending action
				m.confirmAction = nil
				m.confirmPrompt = ""
			}
			m.mode = ModeNormal
			m.filter.Blur()
			m.cmdline.Blur()
			m.refreshTableRows()
			m.relayout()
			return m, nil
		}
	}

	switch m.mode {
	case ModeNormal:
		return m.handleNormal(msg)
	case ModeDetail:
		return m.handleDetail(msg)
	case ModeFilter:
		return m.handleFilter(msg)
	case ModeCommand:
		return m.handleCommand(msg)
	case ModeLogs:
		return m.handleLogs(msg)
	case ModeCopy:
		return m.handleCopyMode(msg)
	case ModeBackupPicker:
		return m.handleBackupPicker(msg)
	case ModeHelp:
		return m.handleHelp(msg)
	case ModeHostForm:
		return m.handleHostForm(msg)
	case ModeComposeEdit:
		return m.handleComposeEdit(msg)
	case ModeEvents:
		return m.handleEvents(msg)
	case ModePushForm:
		return m.handlePushForm(msg)
	case ModeNetForm:
		return m.handleNetForm(msg)
	case ModeVolForm:
		return m.handleVolForm(msg)
	case ModeRunForm:
		return m.handleRunForm(msg)
	case ModeExecForm:
		return m.handleExecForm(msg)
	case ModeConfirm:
		return m.handleConfirm(msg)
	case ModeFSBrowser:
		return m.handleFSBrowser(msg)
	}
	return m, nil
}

// handleFSBrowser drives the container filesystem browser: Enter/l descends into
// a directory, Backspace/h/- ascends, `d` downloads the selected entry to the
// working directory, q closes (esc is handled globally), and everything else
// moves the cursor.
func (m Model) handleFSBrowser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	id := m.fsBrowser.ContainerID()
	name := m.fsBrowser.Name()
	switch msg.String() {
	case "q":
		m.mode = ModeNormal
		m.relayout()
		return m, nil
	case "enter", "l", "right":
		e := m.fsBrowser.Selected()
		if e.IsDir {
			dir := fsbrowser.PathJoin(m.fsBrowser.CurrentPath(), e.Name)
			return m, fsListCmd(m.backend, id, name, dir)
		}
		return m, nil
	case "backspace", "h", "left", "-":
		cur := m.fsBrowser.CurrentPath()
		if cur != "/" {
			return m, fsListCmd(m.backend, id, name, fsbrowser.PathParent(cur))
		}
		return m, nil
	case "d":
		e := m.fsBrowser.Selected()
		if e.Name == "" {
			return m, nil
		}
		src := fsbrowser.PathJoin(m.fsBrowser.CurrentPath(), e.Name)
		return m, fsDownloadCmd(m.backend, id, src, e.Name)
	}
	updated, cmd := m.fsBrowser.Update(msg)
	m.fsBrowser = updated
	return m, cmd
}

// handleConfirm drives the generic confirmation overlay: y/enter runs the
// pending action, anything else (n, q; esc is handled globally) cancels it.
func (m Model) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	action := m.confirmAction
	m.confirmAction = nil
	m.confirmPrompt = ""
	m.mode = ModeNormal
	switch msg.String() {
	case "y", "enter":
		return m, action
	}
	return m, nil
}

func (m Model) handleNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Column sorting (containers only): shift+N/S/C/M pick the column, pressing
	// the same key again reverses the order.
	if m.resource == ViewContainers {
		if f, ok := sortKeyField(key); ok {
			m.cycleSort(f)
			return m, nil
		}
	}

	// Enter is context-bound (connect / drill-down), not a remappable action.
	if key == "enter" {
		// In the hosts view, Enter connects to the selected host.
		if m.resource == ViewHosts {
			name := m.selectedID()
			if h, ok := m.hostStore.Find(name); ok {
				return m, connectCmd(m.cfg, h.Host)
			}
		}
		// In the compose view, Enter drills into the project's containers.
		if m.resource == ViewCompose {
			project := m.selectedID()
			if project != "" {
				return m, func() tea.Msg { return showComposeContainersMsg{project} }
			}
		}
		return m, nil
	}

	// Hosts view host-management keys (a/e/d). Fixed like Enter and resolved
	// before the keymap, because `a`/`e` map to actions that are inert here
	// (ToggleAll/Edit) — in the hosts view they manage saved hosts instead.
	if m.resource == ViewHosts {
		if handled, model, cmd := m.handleHostKey(key); handled {
			return model, cmd
		}
	}

	// Resolve the key to a configurable action. Built-in actions always win over
	// a same-key plugin binding.
	if action, ok := m.keys.ActionFor(key); ok {
		return m.handleAction(action)
	}

	// A key bound by a plugin, else hand it to the table for navigation.
	if p, ok := m.pluginForKey(key); ok {
		return m, m.pluginCmd(p)
	}

	// Container filesystem browser (fixed key, also :files). Placed after the
	// keymap/plugin resolution so a user binding on `f` still wins.
	if key == "f" && m.resource == ViewContainers {
		if cmd := m.openFSBrowser(""); cmd != nil {
			return m, cmd
		}
		return m, nil
	}

	m.table.Update(msg)
	return m, nil
}

// openFSBrowser starts a filesystem listing for the selected container at dir
// (defaulting to "/"), or sets a footer error when the container isn't running.
// It returns nil when there is nothing to browse (no selection).
func (m *Model) openFSBrowser(dir string) tea.Cmd {
	id := m.selectedID()
	if id == "" {
		return nil
	}
	if st := m.containerState(id); st != "" && st != "running" {
		m.err = "files: контейнер не запущен (состояние: " + st + ")"
		return nil
	}
	if dir == "" {
		dir = "/"
	}
	return fsListCmd(m.backend, id, m.containerName(id), dir)
}

// handleAction runs a configurable normal-mode action. The key that triggered
// it has already been resolved via the keymap, so the bodies here are key-
// agnostic.
func (m Model) handleAction(action keymap.Action) (tea.Model, tea.Cmd) {
	switch action {
	case keymap.Inspect:
		// Hosts are app-level config — no inspect.
		if m.resource == ViewHosts {
			return m, nil
		}
		id := m.selectedID()
		if id != "" {
			m.mode = ModeDetail
			m.relayout()
			return m, fetchInspect(m.backend, m.resource, id)
		}
	case keymap.Logs:
		if m.resource == ViewContainers {
			id := m.selectedID()
			if id != "" {
				return m, openLogs(m.backend, id, docker.LogOptions{Tail: 100})
			}
		}
		if m.resource == ViewCompose {
			project := m.selectedID()
			if project != "" {
				return m, openComposeLogs(m.backend, project, docker.LogOptions{Tail: 100})
			}
		}
	case keymap.Edit:
		if m.resource == ViewCompose {
			project := m.selectedID()
			if project != "" {
				return m, composeEditCmd(m.backend, project)
			}
		}
	case keymap.Exec:
		// Quick interactive shell in the selected (running) container.
		if m.resource == ViewContainers {
			id := m.selectedID()
			if id == "" {
				return m, nil
			}
			if st := m.containerState(id); st != "" && st != "running" {
				m.err = "exec: container is not running (state: " + st + ")"
				return m, nil
			}
			return m, execCmd(m.backend, id, m.containerName(id), nil)
		}
	case keymap.Filter:
		m.mode = ModeFilter
		m.filter.Focus()
		m.relayout()
		return m, nil
	case keymap.Command:
		m.mode = ModeCommand
		m.err = ""
		m.cmdline.Focus()
		m.relayout()
		return m, nil
	case keymap.ToggleAll:
		if m.resource == ViewContainers {
			m.showAll = !m.showAll
			return m, m.fetchCurrentResource()
		}
	case keymap.Stats:
		// Toggle the `docker stats`-style column layout for containers.
		if m.resource == ViewContainers {
			m.statsView = !m.statsView
			m.applyColumns(m.width)
			m.refreshTableRows()
			return m, nil
		}
	case keymap.Select:
		// Toggle bulk selection of the container under the cursor.
		if m.resource == ViewContainers {
			if id := m.selectedID(); id != "" {
				if m.selected == nil {
					m.selected = map[string]bool{}
				}
				if m.selected[id] {
					delete(m.selected, id)
				} else {
					m.selected[id] = true
				}
				m.refreshTableRows()
			}
			return m, nil
		}
	case keymap.Copy:
		items := m.buildCopyItems()
		if len(items) > 0 {
			m.copyItems = items
			m.copyCursor = 0
			m.mode = ModeCopy
		}
	case keymap.Refresh:
		return m, m.fetchCurrentResource()
	case keymap.Pause:
		// Freeze/resume auto-refresh. The heartbeat keeps running; resuming pulls
		// fresh data immediately rather than waiting for the next tick.
		m.paused = !m.paused
		if m.paused {
			m.copyNotif = "автообновление на паузе"
			return m, clearCopyNotifCmd()
		}
		m.copyNotif = "автообновление возобновлено"
		return m, tea.Batch(m.fetchCurrentResource(), clearCopyNotifCmd())
	case keymap.Help:
		m.mode = ModeHelp
		m.help.SetContent(m.buildHelpContent())
		m.relayout()
		return m, nil
	}
	return m, nil
}

// handleHelp drives the help overlay: q/?/esc close it, everything else scrolls.
func (m Model) handleHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "?", "esc":
		m.mode = ModeNormal
		m.relayout()
		return m, nil
	}
	updated, cmd := m.help.Update(msg)
	m.help = updated
	return m, cmd
}

func (m Model) handleDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Exit only when search is not active.
	if !m.detail.IsSearching() && !m.detail.HasSearch() {
		switch key {
		case "q", "i", "esc":
			m.mode = ModeNormal
			m.relayout()
			return m, nil
		}
	}

	updated, cmd := m.detail.Update(msg)
	m.detail = updated
	return m, cmd
}

func (m Model) handleFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = ModeNormal
		m.filter.Blur()
		m.refreshTableRows()
		m.relayout()
		return m, nil
	}
	updated, cmd := m.filter.Update(msg)
	m.filter = updated
	m.refreshTableRows() // live filter
	return m, cmd
}

func (m Model) handleCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		parsed := m.cmdline.Parse()
		if parsed == nil {
			m.mode = ModeNormal
			m.cmdline.Reset()
			m.relayout()
			return m, nil
		}
		cmd, err := m.dispatchCommand(parsed)
		if err != nil {
			m.cmdline.SetError(err.Error())
			return m, nil
		}
		m.cmdline.Reset()
		m.mode = ModeNormal
		m.relayout()
		return m, cmd
	}
	updated, cmd := m.cmdline.Update(msg)
	m.cmdline = updated
	return m, cmd
}

func (m Model) handleLogs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While typing a search query, every key goes to the logs component.
	if !m.logs.IsSearching() {
		switch msg.String() {
		case "q":
			m.mode = ModeNormal
			m.stopLogStream()
			m.opTitle = ""
			return m, nil
		case "esc":
			// esc clears an active search first (handled by the component);
			// with no search it closes the logs view.
			if !m.logs.HasSearch() {
				m.mode = ModeNormal
				m.stopLogStream()
				m.opTitle = ""
				return m, nil
			}
		case "s":
			// Save the current log buffer to a file in the working directory.
			if m.logs.LineCount() == 0 {
				m.err = "no log lines to save yet"
				return m, nil
			}
			return m, saveLogsCmd(m.logs.ContainerID(), m.logs.RawContent())
		}
	}
	updated, cmd := m.logs.Update(msg)
	m.logs = updated
	return m, cmd
}

// handleShell routes keys for the embedded terminal. While the session is live,
// every key is forwarded to the remote process — you exit by typing `exit` /
// Ctrl-D, or force-detach with Ctrl+\. Once the process has exited, q/esc/enter
// closes the panel.
func (m Model) handleEvents(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.mode = ModeNormal
		m.stopEventStream()
		return m, nil
	case "r":
		// Refresh: tear down the old subscription and open a new one.
		m.stopEventStream()
		m.eventsModel.Open()
		return m, openEvents(m.backend)
	}
	updated, cmd := m.eventsModel.Update(msg)
	m.eventsModel = updated
	return m, cmd
}

func (m Model) handleShell(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.shell.Closed() {
		switch msg.String() {
		case "q", "esc", "enter", "ctrl+c":
			return m.closeShell()
		}
		return m, nil
	}
	if msg.String() == "ctrl+\\" { // force-detach escape hatch
		return m.closeShell()
	}
	m.shell.SendKey(msg) // enqueued in order; the pump writes to the session
	return m, nil
}

// closeShell ends the session, returns to the table and refreshes (state may
// have changed while the shell was open).
func (m Model) closeShell() (tea.Model, tea.Cmd) {
	m.shell.CloseSession()
	m.mode = ModeNormal
	m.err = ""
	m.relayout()
	return m, m.fetchCurrentResource()
}

func (m *Model) dispatchCommand(cmd *cmdline.CommandMsg) (tea.Cmd, error) {
	// ── view switching (always valid) ──────────────────────────────────────────
	switch cmd.Name {
	case "containers", "c":
		return func() tea.Msg { return switchResourceMsg{ViewContainers} }, nil
	case "images", "img":
		return func() tea.Msg { return switchResourceMsg{ViewImages} }, nil
	case "networks", "net":
		return func() tea.Msg { return switchResourceMsg{ViewNetworks} }, nil
	case "volumes", "vol":
		return func() tea.Msg { return switchResourceMsg{ViewVolumes} }, nil
	case "hosts", "h", "dashboard", "dash":
		return func() tea.Msg { return switchResourceMsg{ViewHosts} }, nil
	case "compose", "co":
		return func() tea.Msg { return switchResourceMsg{ViewCompose} }, nil
	case "events":
		return openEvents(m.backend), nil
	case "system":
		// System-wide ops, available from any view: `system df` / `system prune`.
		if len(cmd.Args) == 0 {
			return nil, fmt.Errorf("usage: system df | system prune")
		}
		switch cmd.Args[0] {
		case "df":
			return systemDFCmd(m.backend), nil
		case "prune":
			prune := systemPruneCmd(m.backend)
			return func() tea.Msg {
				return openConfirmMsg{
					prompt: "Удалить остановленные контейнеры, неиспользуемые сети,\nвисячие образы и build-кэш? (тома не затрагиваются)",
					action: prune,
				}
			}, nil
		default:
			return nil, fmt.Errorf("unknown system command: %s (df | prune)", cmd.Args[0])
		}
	case "theme":
		// Switch the color scheme on the fly, no config file needed. styles.Apply
		// mutates package-level styles, which the View functions read lazily — safe
		// because dispatchCommand runs on the bubbletea event-loop goroutine, not
		// inside a tea.Cmd. Applies on top of any config-file color overrides.
		names := strings.Join(theme.Names(), ", ")
		if len(cmd.Args) == 0 {
			cur := theme.NameOf(styles.Active())
			if cur == "" {
				cur = "custom"
			}
			return nil, fmt.Errorf("usage: theme <name> (%s); current: %s", names, cur)
		}
		name := strings.ToLower(strings.TrimSpace(cmd.Args[0]))
		pal, ok := theme.ByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown theme %q (available: %s)", name, names)
		}
		styles.Apply(pal)
		m.copyNotif = "тема: " + name
		return clearCopyNotifCmd(), nil
	case "interval":
		// Auto-refresh cadence, available from any view. No arg reports the
		// current value; `pause`/`resume` toggle the freeze.
		if len(cmd.Args) == 0 {
			state := m.refreshInterval.String()
			if m.paused {
				state += " (на паузе)"
			}
			return nil, fmt.Errorf("интервал автообновления: %s; задать: interval <dur> (напр. 5s), пауза: p", state)
		}
		switch strings.ToLower(cmd.Args[0]) {
		case "pause", "off":
			m.paused = true
			m.copyNotif = "автообновление на паузе"
			return clearCopyNotifCmd(), nil
		case "resume", "on":
			m.paused = false
			m.copyNotif = "автообновление возобновлено"
			return tea.Batch(m.fetchCurrentResource(), clearCopyNotifCmd()), nil
		}
		d, err := time.ParseDuration(cmd.Args[0])
		if err != nil {
			return nil, fmt.Errorf("неверный интервал %q (примеры: 1s, 5s, 2m)", cmd.Args[0])
		}
		m.refreshInterval = clampInterval(d)
		m.paused = false
		m.copyNotif = "интервал: " + m.refreshInterval.String()
		// The running tick loop reads m.refreshInterval when it reschedules, so the
		// new cadence applies after at most one old interval. Don't start a second
		// tickCmd here — that would run two tick chains in parallel. Refresh now so
		// the change is felt immediately.
		return tea.Batch(m.fetchCurrentResource(), clearCopyNotifCmd()), nil
	case "alert":
		// Resource-usage thresholds, available from any view. No arg reports the
		// current config; `cpu`/`mem <pct>` set a metric, `off` disables both.
		if len(cmd.Args) == 0 {
			return nil, fmt.Errorf("алерты: %s; задать: alert cpu <%%> | alert mem <%%> | alert off", alertSummary(m.alerts))
		}
		switch strings.ToLower(cmd.Args[0]) {
		case "off", "none", "clear":
			m.alerts = alerts.Thresholds{}
		case "cpu":
			if err := m.setAlertThreshold("cpu", cmd.Args); err != nil {
				return nil, err
			}
		case "mem", "memory":
			if err := m.setAlertThreshold("mem", cmd.Args); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("usage: alert cpu <%%> | alert mem <%%> | alert off")
		}
		m.applyColumns(m.width)
		m.refreshTableRows()
		m.copyNotif = "алерты: " + alertSummary(m.alerts)
		return clearCopyNotifCmd(), nil
	}

	// ── user plugins (custom commands) ────────────────────────────────────────
	// Checked before per-view dispatch but only for names that aren't built-ins,
	// so built-in commands always take precedence over a same-named plugin.
	if !m.cmdline.IsBuiltin(cmd.Name) {
		if p, ok := m.plugins.Lookup(m.pluginScope(), cmd.Name); ok {
			return m.pluginCmd(p), nil
		}
	}

	// ── hosts management (own command set) ────────────────────────────────────
	if m.resource == ViewHosts {
		return m.dispatchHostCommand(cmd)
	}

	// ── compose management (own command set) ──────────────────────────────────
	if m.resource == ViewCompose {
		return m.dispatchComposeCommand(cmd)
	}

	// ── image build/tag/push/history (own command set) ────────────────────────
	if m.resource == ViewImages {
		if ok, c, err := m.dispatchImageCommand(cmd); ok {
			return c, err
		}
	}

	// ── run wizard (containers; images view pre-fills via dispatchImageCommand) ─
	if cmd.Name == "run" {
		if m.resource != ViewContainers {
			return nil, fmt.Errorf("run is not available for %s", m.resource.String())
		}
		return func() tea.Msg { return openRunFormMsg{} }, nil
	}

	// ── create (networks / volumes) ───────────────────────────────────────────
	if cmd.Name == "create" {
		switch m.resource {
		case ViewNetworks:
			return func() tea.Msg { return openNetFormMsg{} }, nil
		case ViewVolumes:
			return func() tea.Msg { return openVolFormMsg{} }, nil
		default:
			return nil, fmt.Errorf("create is not available for %s", m.resource.String())
		}
	}

	// ── prune (images / volumes) ──────────────────────────────────────────────
	if cmd.Name == "prune" {
		switch m.resource {
		case ViewImages:
			return containerAction(func() error { _, err := m.backend.PruneImages(); return err }), nil
		case ViewVolumes:
			return containerAction(func() error { _, err := m.backend.PruneVolumes(); return err }), nil
		default:
			return nil, fmt.Errorf("prune is not available for %s", m.resource.String())
		}
	}

	// ── pull (images only) ────────────────────────────────────────────────────
	if cmd.Name == "pull" {
		if m.resource != ViewImages {
			return nil, fmt.Errorf("pull is only available for images")
		}
		ref := m.selectedImageRef()
		if ref == "" {
			return nil, fmt.Errorf("no image selected")
		}
		return containerAction(func() error { return m.backend.PullImage(ref) }), nil
	}

	// ── resource remove (works for all views) ─────────────────────────────────
	if cmd.Name == "rm" {
		if m.selectedID() == "" {
			return nil, fmt.Errorf("nothing selected")
		}
		force := len(cmd.Args) > 0 && cmd.Args[0] == "-f"
		switch m.resource {
		case ViewImages:
			// Use tag reference so Docker can remove a single repo reference;
			// fall back to short ID for untagged images.
			ref := m.selectedImageRef()
			return containerAction(func() error { return m.backend.RemoveImage(ref, force) }), nil
		case ViewNetworks:
			id := m.selectedID()
			return containerAction(func() error { return m.backend.RemoveNetwork(id) }), nil
		case ViewVolumes:
			id := m.selectedID()
			return containerAction(func() error { return m.backend.RemoveVolume(id) }), nil
		default:
			ids := m.targetContainerIDs()
			if len(ids) == 0 {
				return nil, fmt.Errorf("no container selected")
			}
			return bulkAction(ids, func(id string) error { return m.backend.RemoveContainer(id, force) }), nil
		}
	}

	// ── container-only commands ───────────────────────────────────────────────
	if m.resource != ViewContainers {
		return nil, fmt.Errorf("command %q is only available for containers", cmd.Name)
	}

	ids := m.targetContainerIDs()
	if len(ids) == 0 {
		return nil, fmt.Errorf("no container selected")
	}
	id := ids[0]

	switch cmd.Name {
	case "start":
		return bulkAction(ids, m.backend.StartContainer), nil
	case "stop":
		return bulkAction(ids, m.backend.StopContainer), nil
	case "restart":
		return bulkAction(ids, m.backend.RestartContainer), nil
	case "kill":
		sig := ""
		if len(cmd.Args) > 0 {
			sig = cmd.Args[0]
		}
		return bulkAction(ids, func(id string) error { return m.backend.KillContainer(id, sig) }), nil
	case "logs":
		return openLogs(m.backend, id, parseLogOptions(cmd.Args)), nil
	case "exec":
		// exec targets a single container (the cursor), not a bulk selection.
		if st := m.containerState(id); st != "" && st != "running" {
			return nil, fmt.Errorf("exec: container is not running (state: %s)", st)
		}
		return execCmd(m.backend, id, m.containerName(id), cmd.Args), nil
	case "files", "browse":
		// Open the filesystem browser on the cursor container (optional path arg).
		dir := ""
		if len(cmd.Args) > 0 {
			dir = cmd.Args[0]
		}
		if st := m.containerState(id); st != "" && st != "running" {
			return nil, fmt.Errorf("files: container is not running (state: %s)", st)
		}
		return fsListCmd(m.backend, id, m.containerName(id), firstNonEmpty(dir, "/")), nil
	case "cp":
		// Upload a local file/dir into the cursor container:
		//   cp <local-path> <container-dir>
		if len(cmd.Args) < 2 {
			return nil, fmt.Errorf("usage: cp <local-path> <container-dir>")
		}
		local, dest := cmd.Args[0], cmd.Args[1]
		return containerAction(func() error { return m.backend.CopyToContainer(id, local, dest) }), nil
	default:
		return nil, fmt.Errorf("unknown command: %s", cmd.Name)
	}
}

// setAlertThreshold parses a percentage (or "off"/"0") from `alert <metric> <v>`
// and applies it to the named metric. The "%" suffix is tolerated.
func (m *Model) setAlertThreshold(metric string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: alert %s <percent> (or: alert %s off)", metric, metric)
	}
	raw := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(args[1]), "%"))
	var v float64
	if raw != "off" && raw != "none" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < 0 {
			return fmt.Errorf("неверный порог %q (пример: alert %s 80)", args[1], metric)
		}
		v = f
	}
	switch metric {
	case "cpu":
		m.alerts.CPU = v
	case "mem":
		m.alerts.Mem = v
	}
	return nil
}

// alertSummary renders the active thresholds for the footer/command help.
func alertSummary(t alerts.Thresholds) string {
	if !t.Active() {
		return "выключены"
	}
	parts := make([]string, 0, 2)
	if t.CPU > 0 {
		parts = append(parts, fmt.Sprintf("CPU≥%g%%", t.CPU))
	}
	if t.Mem > 0 {
		parts = append(parts, fmt.Sprintf("MEM≥%g%%", t.Mem))
	}
	return strings.Join(parts, " ")
}

// firstNonEmpty returns a if it is non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// handleHostKey resolves the hosts-view host-management keys to the same actions
// as the :add/:edit/:rm commands, so saved hosts can be managed directly from
// the dashboard: `a` opens the add form, `e` the edit form for the selected
// host, and `d` removes it after a confirmation. handled reports whether the key
// was one of these (so handleNormal can fall through otherwise).
func (m Model) handleHostKey(key string) (handled bool, model tea.Model, cmd tea.Cmd) {
	switch key {
	case "a":
		return true, m, func() tea.Msg { return openHostFormMsg{editing: false} }
	case "e":
		name := m.selectedID()
		if h, ok := m.hostStore.Find(name); ok {
			return true, m, func() tea.Msg {
				return openHostFormMsg{editing: true, name: h.Name, host: h.Host}
			}
		}
		return true, m, nil
	case "d":
		name := m.selectedID()
		if name == "" {
			return true, m, nil
		}
		store := m.hostStore
		remove := func() tea.Msg {
			if err := store.Remove(name); err != nil {
				return errMsg{err}
			}
			if err := store.Save(); err != nil {
				return errMsg{err}
			}
			return hostsUpdatedMsg{store.List()}
		}
		return true, m, func() tea.Msg {
			return openConfirmMsg{prompt: "Удалить хост " + name + " из списка?", action: remove}
		}
	}
	return false, m, nil
}

// dispatchHostCommand handles the hosts view command set: connect/add/edit/rm.
func (m *Model) dispatchHostCommand(cmd *cmdline.CommandMsg) (tea.Cmd, error) {
	switch cmd.Name {
	case "connect":
		name := m.selectedID()
		if name == "" {
			return nil, fmt.Errorf("no host selected")
		}
		h, ok := m.hostStore.Find(name)
		if !ok {
			return nil, fmt.Errorf("host %q not found", name)
		}
		return connectCmd(m.cfg, h.Host), nil

	case "add":
		// Open the modal form pre-filled with base defaults.
		return func() tea.Msg { return openHostFormMsg{editing: false} }, nil

	case "edit":
		name := m.selectedID()
		if name == "" {
			return nil, fmt.Errorf("no host selected")
		}
		h, ok := m.hostStore.Find(name)
		if !ok {
			return nil, fmt.Errorf("host %q not found", name)
		}
		// Open the modal form pre-filled with the selected host.
		return func() tea.Msg { return openHostFormMsg{editing: true, name: h.Name, host: h.Host} }, nil

	case "rm":
		name := m.selectedID()
		if name == "" {
			return nil, fmt.Errorf("no host selected")
		}
		if err := m.hostStore.Remove(name); err != nil {
			return nil, err
		}
		return m.saveHostsThenRefresh()

	default:
		return nil, fmt.Errorf("unknown hosts command: %s", cmd.Name)
	}
}

// dispatchComposeCommand handles the compose view command set. Lifecycle ops run
// over the Docker API on the project's containers; compose-engine operations
// (up/down/pull/edit/config) need SSH and arrive in a later iteration.
func (m *Model) dispatchComposeCommand(cmd *cmdline.CommandMsg) (tea.Cmd, error) {
	// create authors a brand-new project; it needs a directory, not a selection.
	if cmd.Name == "create" {
		if len(cmd.Args) == 0 {
			return nil, fmt.Errorf("create: directory required (usage: create <dir>)")
		}
		dir := cmd.Args[0]
		return func() tea.Msg { return openComposeCreateMsg{dir: dir} }, nil
	}

	project := m.selectedID()
	if project == "" {
		return nil, fmt.Errorf("no compose project selected")
	}
	switch cmd.Name {
	case "start":
		return containerAction(func() error { return m.backend.ComposeStart(project) }), nil
	case "stop":
		return containerAction(func() error { return m.backend.ComposeStop(project) }), nil
	case "restart":
		return containerAction(func() error { return m.backend.ComposeRestart(project) }), nil
	case "pause":
		return containerAction(func() error { return m.backend.ComposePause(project) }), nil
	case "unpause":
		return containerAction(func() error { return m.backend.ComposeUnpause(project) }), nil
	case "remove", "rm":
		return containerAction(func() error { return m.backend.ComposeRemove(project) }), nil
	case "up":
		return streamOpCmd(func() (<-chan string, error) { return m.backend.ComposeUp(project) }, "compose up: "+project), nil
	case "pull":
		return streamOpCmd(func() (<-chan string, error) { return m.backend.ComposePull(project) }, "compose pull: "+project), nil
	case "down":
		return streamOpCmd(func() (<-chan string, error) { return m.backend.ComposeDown(project) }, "compose down: "+project), nil
	case "config":
		return composeConfigCmd(m.backend, project), nil
	case "edit":
		return composeEditCmd(m.backend, project), nil
	case "backup":
		return backupComposeCmd(m.backend, project), nil
	case "backups":
		// Open the catalog of existing backups for this project.
		return listBackupsCmd(project), nil
	case "restore":
		// With no file, browse the catalog; with one, restore it directly.
		if len(cmd.Args) == 0 {
			return listBackupsCmd(project), nil
		}
		return restoreComposeCmd(m.backend, project, cmd.Args[0]), nil
	default:
		return nil, fmt.Errorf("unknown compose command: %s", cmd.Name)
	}
}

// dispatchImageCommand handles the image-specific command set (build/tag/push/
// history). The first return value reports whether the command was recognised
// here; when false the caller falls through to the shared pull/prune/rm logic.
func (m *Model) dispatchImageCommand(cmd *cmdline.CommandMsg) (bool, tea.Cmd, error) {
	switch cmd.Name {
	case "build":
		// build authors a new image from a local context dir; no selection needed.
		if len(cmd.Args) == 0 {
			return true, nil, fmt.Errorf("build: directory required (usage: build <dir> [tag])")
		}
		dir := cmd.Args[0]
		tag := ""
		if len(cmd.Args) > 1 {
			tag = cmd.Args[1]
		}
		return true, streamOpCmd(func() (<-chan string, error) { return m.backend.BuildImage(dir, tag) }, "build: "+dir), nil

	case "tag":
		if len(cmd.Args) == 0 {
			return true, nil, fmt.Errorf("tag: new reference required (usage: tag <new-ref>)")
		}
		ref := m.selectedImageRef()
		if ref == "" {
			return true, nil, fmt.Errorf("no image selected")
		}
		target := cmd.Args[0]
		return true, containerAction(func() error { return m.backend.TagImage(ref, target) }), nil

	case "push":
		ref := m.selectedImageRef()
		if ref == "" {
			return true, nil, fmt.Errorf("no image selected")
		}
		// Open the credentials modal; the actual push runs on submit.
		return true, func() tea.Msg { return openPushFormMsg{ref: ref} }, nil

	case "history":
		ref := m.selectedImageRef()
		if ref == "" {
			return true, nil, fmt.Errorf("no image selected")
		}
		return true, imageHistoryCmd(m.backend, ref), nil

	case "run":
		// Open the run wizard pre-filled with the selected image (if any).
		ref := m.selectedImageRef()
		return true, func() tea.Msg { return openRunFormMsg{image: ref} }, nil

	case "exec":
		// Open the one-off run wizard pre-filled with the selected image.
		ref := m.selectedImageRef()
		return true, func() tea.Msg { return openExecFormMsg{image: ref} }, nil
	}
	return false, nil, nil
}

// saveHostsThenRefresh persists the host store and returns a refresh command.
func (m *Model) saveHostsThenRefresh() (tea.Cmd, error) {
	if err := m.hostStore.Save(); err != nil {
		return nil, err
	}
	return fetchHosts(m.hostStore), nil
}

// handleHostForm drives the add/edit modal: Enter saves (validating against the
// store), Tab/arrows switch fields, everything else edits the focused field.
// Esc is handled by the global key handler and cancels without saving.
func (m Model) handleHostForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := m.hostForm.Name()
		host := m.hostForm.Host()
		var err error
		if m.hostForm.IsEditing() {
			err = m.hostStore.Edit(m.hostForm.OrigName(), name, host)
		} else {
			err = m.hostStore.Add(name, host)
		}
		if err == nil {
			err = m.hostStore.Save()
		}
		if err != nil {
			m.hostForm.SetError(err.Error())
			return m, nil
		}
		m.mode = ModeNormal
		m.relayout()
		return m, fetchHosts(m.hostStore)
	case "tab", "down":
		m.hostForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.hostForm.Prev()
		return m, nil
	}
	updated, cmd := m.hostForm.Update(msg)
	m.hostForm = updated
	return m, cmd
}

// handlePushForm drives the registry-credentials modal: Tab/arrows switch
// fields, Enter starts the push with the entered credentials (an empty username
// means anonymous), and Esc (handled globally) cancels. Credentials are
// remembered for the session keyed by registry so the next push pre-fills.
func (m Model) handlePushForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		auth := docker.RegistryAuth{
			Registry: m.pushForm.Registry(),
			Username: m.pushForm.Username(),
			Password: m.pushForm.Password(),
		}
		ref := m.pushForm.Ref()
		if m.pushAuth == nil {
			m.pushAuth = map[string]docker.RegistryAuth{}
		}
		m.pushAuth[auth.Registry] = auth
		m.mode = ModeNormal
		m.relayout()
		return m, streamOpCmd(func() (<-chan string, error) { return m.backend.PushImage(ref, auth) }, "push: "+ref)
	case "tab", "down":
		m.pushForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.pushForm.Prev()
		return m, nil
	}
	updated, cmd := m.pushForm.Update(msg)
	m.pushForm = updated
	return m, cmd
}

// handleNetForm drives the create-network modal: Tab/arrows switch fields, Enter
// creates the network with the entered options, and Esc (handled globally)
// cancels. A blank driver defaults to "bridge" in the backend.
func (m Model) handleNetForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.netForm.Name() == "" {
			m.netForm.SetError("name is required")
			return m, nil
		}
		opts := docker.NetworkCreateOptions{
			Name:    m.netForm.Name(),
			Driver:  m.netForm.Driver(),
			Subnet:  m.netForm.Subnet(),
			Gateway: m.netForm.Gateway(),
		}
		return m, containerAction(func() error { return m.backend.CreateNetwork(opts) })
	case "tab", "down":
		m.netForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.netForm.Prev()
		return m, nil
	}
	updated, cmd := m.netForm.Update(msg)
	m.netForm = updated
	return m, cmd
}

// handleVolForm drives the create-volume modal: Tab/arrows switch fields, Enter
// creates the volume with the entered options, and Esc (handled globally)
// cancels. A blank driver defaults to "local" in the backend.
func (m Model) handleVolForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.volForm.Name() == "" {
			m.volForm.SetError("name is required")
			return m, nil
		}
		opts := docker.VolumeCreateOptions{
			Name:   m.volForm.Name(),
			Driver: m.volForm.Driver(),
		}
		return m, containerAction(func() error { return m.backend.CreateVolume(opts) })
	case "tab", "down":
		m.volForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.volForm.Prev()
		return m, nil
	}
	updated, cmd := m.volForm.Update(msg)
	m.volForm = updated
	return m, cmd
}

// handleRunForm drives the run-container wizard: Tab/arrows switch fields,
// Enter creates and starts the container, Esc (handled globally) cancels. A
// backend failure is shown inside the form via the actionResultMsg branch.
func (m Model) handleRunForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.runForm.Image() == "" {
			m.runForm.SetError("image is required")
			return m, nil
		}
		opts := docker.RunOptions{
			Image:   m.runForm.Image(),
			Name:    m.runForm.Name(),
			Ports:   splitList(m.runForm.Ports()),
			Env:     splitList(m.runForm.Env()),
			Volumes: splitList(m.runForm.Volumes()),
		}
		return m, containerAction(func() error { return m.backend.RunContainer(opts) })
	case "tab", "down":
		m.runForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.runForm.Prev()
		return m, nil
	}
	updated, cmd := m.runForm.Update(msg)
	m.runForm = updated
	return m, cmd
}

// handleExecForm drives the one-off run wizard: Tab/arrows switch fields,
// Enter starts a disposable interactive container (empty command = shell) and
// opens the embedded terminal; Esc (handled globally) cancels. A backend
// failure comes back as errMsg and is shown inside the still-open form.
func (m Model) handleExecForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.execForm.Image() == "" {
			m.execForm.SetError("image is required")
			return m, nil
		}
		opts := docker.ExecRunOptions{
			Image:   m.execForm.Image(),
			Volumes: splitList(m.execForm.Volumes()),
			Cmd:     strings.Fields(m.execForm.Command()),
		}
		return m, runInteractiveCmd(m.backend, opts)
	case "tab", "down":
		m.execForm.Next()
		return m, nil
	case "shift+tab", "up":
		m.execForm.Prev()
		return m, nil
	}
	updated, cmd := m.execForm.Update(msg)
	m.execForm = updated
	return m, cmd
}

// splitList splits a comma-separated form field into trimmed, non-empty items.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// handleComposeEdit drives the compose-file editor: Ctrl+S validates YAML then
// saves; everything else edits the buffer. Esc (handled globally) cancels.
func (m Model) handleComposeEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+s" {
		content := m.composeEdit.Value()
		if err := composeedit.ValidateYAML(content); err != nil {
			m.composeEdit.SetError("invalid YAML: " + err.Error())
			return m, nil
		}
		m.composeEdit.SetError("")
		if m.composeEdit.IsCreate() {
			// Write the new file and bring it up; output streams to the console.
			return m, createComposeCmd(m.backend, m.composeEdit.CreateDir(), content)
		}
		m.composeEdit.SetSaving(true)
		return m, composeSaveCmd(m.backend, m.composeEdit.Project(), content)
	}
	updated, cmd := m.composeEdit.Update(msg)
	m.composeEdit = updated
	return m, cmd
}

func (m Model) handleCopyMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.copyCursor > 0 {
			m.copyCursor--
		}
	case "down", "j":
		if m.copyCursor < len(m.copyItems)-1 {
			m.copyCursor++
		}
	case "enter":
		if m.copyCursor < len(m.copyItems) {
			item := m.copyItems[m.copyCursor]
			_ = clipboard.WriteAll(item.Value)
			label := item.Label
			val := item.Value
			if len(val) > 30 {
				val = val[:29] + "…"
			}
			m.copyNotif = label + ": " + val
			m.mode = ModeNormal
			return m, clearCopyNotifCmd()
		}
	case "esc", "q":
		m.mode = ModeNormal
	}
	return m, nil
}

// handleBackupPicker drives the backup catalog overlay: navigate the list, Enter
// restores the highlighted archive, 'd' deletes it (a second 'd' confirms), and
// esc/q (handled globally) closes the catalog.
func (m Model) handleBackupPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.backupCursor > 0 {
			m.backupCursor--
		}
		m.backupConfirmDelete = ""
	case "down", "j":
		if m.backupCursor < len(m.backupItems)-1 {
			m.backupCursor++
		}
		m.backupConfirmDelete = ""
	case "enter":
		if m.backupCursor < len(m.backupItems) {
			item := m.backupItems[m.backupCursor]
			project := m.backupProject
			m.mode = ModeNormal
			m.backupConfirmDelete = ""
			return m, restoreComposeCmd(m.backend, project, item.path)
		}
	case "d":
		if m.backupCursor >= len(m.backupItems) {
			return m, nil
		}
		item := m.backupItems[m.backupCursor]
		if m.backupConfirmDelete != item.name {
			// First press arms the deletion; the footer asks for confirmation.
			m.backupConfirmDelete = item.name
			return m, nil
		}
		m.backupConfirmDelete = ""
		if err := os.Remove(item.path); err != nil {
			m.err = "delete backup: " + err.Error()
			m.mode = ModeNormal
			return m, nil
		}
		// Reload the catalog; an empty result drops back to the list automatically.
		return m, listBackupsCmd(m.backupProject)
	}
	return m, nil
}

// imageRefFromTags picks the first real (non-<none>) tag from a comma-separated
// tag list, falling back to the image ID. Dangling images report their tags as
// "<none>:<none>", which Docker rejects as a reference ("invalid reference
// format"), so those must be skipped in favour of the ID.
func imageRefFromTags(tags, id string) string {
	for t := range strings.SplitSeq(tags, ", ") {
		t = strings.TrimSpace(t)
		if t != "" && !strings.Contains(t, "<none>") {
			return t
		}
	}
	return id
}

// parseLogOptions reads the flags of a `:logs` command: --tail N, --since X,
// --until X. Defaults to the last 100 lines and no time bounds.
func parseLogOptions(args []string) docker.LogOptions {
	opts := docker.LogOptions{Tail: 100}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tail":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					opts.Tail = n
				}
				i++
			}
		case "--since":
			if i+1 < len(args) {
				opts.Since = args[i+1]
				i++
			}
		case "--until":
			if i+1 < len(args) {
				opts.Until = args[i+1]
				i++
			}
		}
	}
	return opts
}

// targetContainerIDs returns the IDs a container command applies to: the bulk
// selection if any, otherwise the single container under the cursor.
func (m Model) targetContainerIDs() []string {
	if len(m.selected) > 0 {
		ids := make([]string, 0, len(m.selected))
		for id := range m.selected {
			ids = append(ids, id)
		}
		return ids
	}
	if id := m.selectedID(); id != "" {
		return []string{id}
	}
	return nil
}

// sortKeyField maps a shifted sort key (N/S/C/M) to its container sort column.
func sortKeyField(key string) (uitbl.SortField, bool) {
	switch key {
	case "N":
		return uitbl.SortName, true
	case "S":
		return uitbl.SortStatus, true
	case "C":
		return uitbl.SortCPU, true
	case "M":
		return uitbl.SortMem, true
	}
	return uitbl.SortNone, false
}

// cycleSort applies a container sort column: selecting a new column sorts it
// (NAME/STATUS ascending, CPU/MEM descending so the busiest land on top);
// re-selecting the active column reverses the direction.
func (m *Model) cycleSort(field uitbl.SortField) {
	if m.sortField == field {
		m.sortDesc = !m.sortDesc
	} else {
		m.sortField = field
		m.sortDesc = field == uitbl.SortCPU || field == uitbl.SortMem
	}
	m.table.SetSort(m.sortField, m.sortDesc)
	m.refreshTableRows()
}

// containerState returns the cached State of the container with the given short
// ID, or "" when it isn't in the current list.
func (m Model) containerState(id string) string {
	for _, c := range m.containers {
		if c.ID == id {
			return c.State
		}
	}
	return ""
}

// bulkAction applies fn to every id, aggregating the outcome into a single
// actionResultMsg. On any failure it reports how many of how many failed,
// wrapping the first error.
func bulkAction(ids []string, fn func(id string) error) tea.Cmd {
	return func() tea.Msg {
		var failed int
		var firstErr error
		for _, id := range ids {
			if err := fn(id); err != nil {
				failed++
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		if failed > 0 {
			return actionResultMsg{fmt.Errorf("%d of %d failed: %w", failed, len(ids), firstErr)}
		}
		return actionResultMsg{}
	}
}

// selectedImageRef returns the pull reference (tag or ID) for the selected image.
func (m Model) selectedImageRef() string {
	id := m.selectedID()
	if id == "" {
		return ""
	}
	for _, img := range m.images {
		if img.ID == id {
			return imageRefFromTags(img.Tags, img.ID)
		}
	}
	return id
}

func (m *Model) relayout() {
	w := m.width
	h := m.height - 2 // 1 header + 1 footer

	tableHeight := h
	if m.mode == ModeFilter || m.mode == ModeCommand {
		tableHeight -= 1
	}

	switch m.mode {
	case ModeDetail:
		m.detail.SetSize(w, h)
	case ModeComposeEdit:
		m.composeEdit.SetSize(w, h)
	case ModeHelp:
		m.help.SetSize(w, h)
	case ModeShell:
		m.shell.SetSize(w, h)
	case ModeEvents:
		m.eventsModel.SetSize(w, h)
	case ModeFSBrowser:
		m.fsBrowser.SetSize(w, h)
	default:
		m.table.SetSize(w, tableHeight)
		m.applyColumns(w)
	}
	m.logs.SetSize(w, h)
}

// applyColumns sets the table columns for the active resource. It is also
// called from NewModel (with width 0) so the table always has a non-empty
// column set before any data arrives — otherwise a fast in-memory update (e.g.
// hosts) can render rows before the first WindowSizeMsg and panic in renderRow.
func (m *Model) applyColumns(w int) {
	// Colored columns (STATUS/HEALTH) are stored as plain text and colored
	// after layout via these colorizers; views with no colored column pass nil.
	switch m.resource {
	case ViewImages:
		m.table.SetColumns(uitbl.ImageColumns(w))
		m.table.SetColorizers(nil)
	case ViewNetworks:
		m.table.SetColumns(uitbl.NetworkColumns(w))
		m.table.SetColorizers(nil)
	case ViewVolumes:
		m.table.SetColumns(uitbl.VolumeColumns(w))
		m.table.SetColorizers(nil)
	case ViewHosts:
		m.table.SetColumns(uitbl.HostColumns(w))
		m.table.SetColorizers(uitbl.HostColorizers())
	case ViewCompose:
		m.table.SetColumns(uitbl.ComposeColumns(w))
		m.table.SetColorizers(uitbl.ComposeColorizers())
	default:
		if m.statsView {
			m.table.SetColumns(uitbl.ContainerStatsColumns(w))
			// The stats layout has no colored data columns; only flag the NAME's
			// alert ⚠ marker when thresholds are active.
			if m.alerts.Active() {
				m.table.SetColorizers(uitbl.ContainerStatsColorizers())
			} else {
				m.table.SetColorizers(nil)
			}
		} else {
			m.table.SetColumns(uitbl.ContainerColumns(w))
			m.table.SetColorizers(uitbl.ContainerColorizers())
		}
	}
}
