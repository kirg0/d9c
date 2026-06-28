package ui

import (
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"d9c/internal/docker"
	"d9c/internal/i18n"
	"d9c/internal/keymap"
	"d9c/internal/ui/composeedit"
	"d9c/internal/ui/fsbrowser"
	"d9c/internal/ui/logs"
	"d9c/internal/ui/shell"
	uitbl "d9c/internal/ui/table"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
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

	case spinner.TickMsg:
		// Drive the pull-image modal's spinner while a pull is in flight; the
		// spinner stops ticking once the form leaves the busy state.
		if m.mode == ModePullForm && m.pullForm.Busy() {
			var cmd tea.Cmd
			m.pullForm, cmd = m.pullForm.Tick(msg)
			return m, cmd
		}
		if m.mode == ModeRunForm && m.runForm.Busy() {
			var cmd tea.Cmd
			m.runForm, cmd = m.runForm.Tick(msg)
			return m, cmd
		}
		if m.mode == ModeCpForm && m.cpForm.Busy() {
			var cmd tea.Cmd
			m.cpForm, cmd = m.cpForm.Tick(msg)
			return m, cmd
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
			m.table.SetImages(m.images, m.filter.Value(), m.selected)
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
			// Restore the selection after returning from a drill-down.
			if m.composeReselect != "" {
				m.table.SelectComposeRow(m.composeReselect)
				m.composeReselect = ""
			}
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
			if docker.IsHostKeyError(msg.err) {
				title, body := hostKeyNoticeText(msg.host)
				return m, func() tea.Msg { return openNoticeMsg{title: title, body: body} }
			}
			if docker.IsHostNotFoundError(msg.err) {
				title, body := hostNotFoundNoticeText(msg.host)
				return m, func() tea.Msg { return openNoticeMsg{title: title, body: body} }
			}
			if docker.IsSocketError(msg.err) {
				title, body := socketNoticeText(msg.host, msg.err)
				return m, func() tea.Msg { return openNoticeMsg{title: title, body: body} }
			}
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
		m.applyComposeCapability()
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
		m.logStop = msg.stop // closing the console tears the progress stream down
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
			m.logs.AddLine(i18n.T("— готово (q/esc чтобы закрыть) —", "— done (q/esc to close) —"))
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

	case openPullFormMsg:
		m.mode = ModePullForm
		m.relayout()
		if msg.image != "" {
			// Reference known up front: open busy and start the pull immediately,
			// showing the spinner so the window doesn't look frozen.
			spin := m.pullForm.OpenPulling(msg.image)
			ref := msg.image
			return m, tea.Batch(spin, containerAction(func() error { return m.backend.PullImage(ref) }))
		}
		m.pullForm.Open()
		return m, nil

	case openBuildFormMsg:
		m.buildForm.Open(msg.dir, msg.tag)
		m.mode = ModeBuildForm
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

	case openCpFormMsg:
		m.cpForm.Open(msg.containerID, msg.name)
		m.mode = ModeCpForm
		m.relayout()
		// Load the initial local listing (working directory) off the event loop.
		return m, cpListCmd(".")

	case cpListedMsg:
		if msg.err != nil {
			m.cpForm.SetError(msg.err.Error())
			return m, nil
		}
		m.cpForm.Show(msg.dir, msg.entries)
		return m, nil

	case openThemePickerMsg:
		m.openThemePicker()
		return m, nil

	case openLangPickerMsg:
		m.openLangPicker()
		return m, nil

	case openConfirmMsg:
		m.confirmPrompt = msg.prompt
		m.confirmAction = msg.action
		m.mode = ModeConfirm
		return m, nil

	case openNoticeMsg:
		m.noticeTitle = msg.title
		m.noticeBody = msg.body
		m.err = ""
		m.mode = ModeNotice
		m.relayout()
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
			m.fsBrowser.SetError(i18n.T("копирование: ", "copy: ") + msg.err.Error())
			return m, nil
		}
		m.copyNotif = i18n.T("скопировано: ./", "downloaded: ./") + msg.name
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
			case ModePullForm:
				m.pullForm.SetError(msg.err.Error())
				return m, nil
			case ModeRunForm:
				m.runForm.SetError(msg.err.Error())
				return m, nil
			case ModeCpForm:
				m.cpForm.SetError(msg.err.Error())
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
			m.err = "no backups found for " + msg.name + " (use :backup first)"
			m.mode = ModeNormal
			return m, nil
		}
		m.err = ""
		m.backupItems = msg.items
		m.backupProject = msg.project
		m.backupName = msg.name
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
		m.applyComposeCapability()
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
		m.table.SetImages(m.images, m.filter.Value(), m.selected)
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
		// Cancel the theme picker: roll the live preview back to the palette that
		// was active when it opened.
		if m.mode == ModeThemePicker {
			m.cancelThemePicker()
			return m, nil
		}
		// Cancel the language picker: roll the live preview back to the language
		// that was active when it opened.
		if m.mode == ModeLangPicker {
			m.cancelLangPicker()
			return m, nil
		}
		// Esc first clears a pending bulk selection (before popping any view).
		if m.mode == ModeNormal && len(m.selected) > 0 {
			m.selected = nil
			m.refreshTableRows()
			return m, nil
		}
		// Esc in a compose drill-down (normal mode) pops back to the project list,
		// restoring the cursor to the deployment it was opened from.
		if m.mode == ModeNormal && m.composeFilter != "" {
			m.composeReselect = m.composeFilter
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
			if m.mode == ModeNotice {
				m.noticeTitle = ""
				m.noticeBody = ""
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
	case ModeThemePicker:
		return m.handleThemePicker(msg)
	case ModeLangPicker:
		return m.handleLangPicker(msg)
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
	case ModePullForm:
		return m.handlePullForm(msg)
	case ModeBuildForm:
		return m.handleBuildForm(msg)
	case ModeRunForm:
		return m.handleRunForm(msg)
	case ModeExecForm:
		return m.handleExecForm(msg)
	case ModeConfirm:
		return m.handleConfirm(msg)
	case ModeNotice:
		return m.handleNotice(msg)
	case ModeFSBrowser:
		return m.handleFSBrowser(msg)
	case ModeCpForm:
		return m.handleCpForm(msg)
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

// handleNotice closes the informational modal on any key (esc is handled
// globally). The notice carries no action — it just informs.
func (m Model) handleNotice(_ tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.noticeTitle = ""
	m.noticeBody = ""
	m.mode = ModeNormal
	m.relayout()
	return m, nil
}

// hostKeyNoticeText assembles the user-facing message shown when an SSH host
// key check fails (the host was re-provisioned, etc.). The body is in Russian
// to match the rest of the in-app dialogs, and the actual known_hosts path is
// embedded so the user knows exactly which file to clean.
func hostKeyNoticeText(host string) (title, body string) {
	title = i18n.T(" Ключ хоста изменился ", " Host key changed ")
	path := docker.KnownHostsPath()
	if path == "" {
		path = "~/.ssh/known_hosts"
	}
	body = fmt.Sprintf(i18n.T(
		"Не удалось подключиться к %s:\n"+
			"SSH-ключ хоста не совпадает с сохранённым в known_hosts.\n\n"+
			"Скорее всего, удалённый хост был пересоздан и теперь предъявляет\n"+
			"новый ключ. Если вы доверяете этому хосту, удалите устаревшую\n"+
			"запись (или весь файл) и подключитесь заново:\n\n"+
			"    %s\n\n"+
			"После очистки повторите :connect — d9c примет новый ключ\n"+
			"и сохранит его при следующем подключении.",
		"Could not connect to %s:\n"+
			"the host's SSH key does not match the one saved in known_hosts.\n\n"+
			"The remote host was most likely re-provisioned and now presents\n"+
			"a new key. If you trust this host, remove the stale entry\n"+
			"(or the whole file) and connect again:\n\n"+
			"    %s\n\n"+
			"After cleaning it up, repeat :connect — d9c will accept the new\n"+
			"key and save it on the next connection."),
		host, path)
	return title, body
}

// hostNotFoundNoticeText assembles the user-facing message shown when the
// target host name cannot be resolved ("no such host") — usually a mistyped
// address or a host that no longer exists. Same Russian style as the other
// in-app dialogs; the failing host is embedded so the user can spot a typo.
func hostNotFoundNoticeText(host string) (title, body string) {
	title = i18n.T(" Хост не найден ", " Host not found ")
	body = fmt.Sprintf(i18n.T(
		"Не удалось подключиться к %s:\n"+
			"не удаётся определить адрес хоста (no such host).\n\n"+
			"Скорее всего, имя хоста указано с опечаткой или такого хоста\n"+
			"не существует. Проверьте, что адрес введён верно и хост\n"+
			"доступен из сети, затем повторите :connect.",
		"Could not connect to %s:\n"+
			"the host address cannot be resolved (no such host).\n\n"+
			"The host name is most likely mistyped or no longer exists.\n"+
			"Check that the address is correct and the host is reachable\n"+
			"from the network, then repeat :connect."),
		host)
	return title, body
}

// socketNoticeText assembles the user-facing message shown when a unix://
// socket path is invalid (missing, a directory, or not a socket). Same Russian
// style as the other in-app dialogs; the underlying validation error is embedded
// so the user sees exactly what is wrong with the path.
func socketNoticeText(host string, err error) (title, body string) {
	title = i18n.T(" Неверный путь до сокета ", " Invalid socket path ")
	body = fmt.Sprintf(i18n.T(
		"Не удалось подключиться к %s:\n"+
			"%v\n\n"+
			"Проверьте путь до unix-сокета Docker. Обычно это\n"+
			"    unix:///var/run/docker.sock\n\n"+
			"Убедитесь, что демон Docker запущен и сокет существует,\n"+
			"затем повторите :connect.",
		"Could not connect to %s:\n"+
			"%v\n\n"+
			"Check the Docker unix socket path. It is usually\n"+
			"    unix:///var/run/docker.sock\n\n"+
			"Make sure the Docker daemon is running and the socket exists,\n"+
			"then repeat :connect."),
		host, err)
	return title, body
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

	// In the Images view with a pending bulk selection, `r` removes the marked
	// images after a confirmation. Resolved before the keymap so it overrides the
	// global Refresh binding while a selection is active.
	if m.resource == ViewImages && len(m.selected) > 0 {
		if handled, model, cmd := m.handleImageSelectionKey(key); handled {
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
		m.err = i18n.T("files: контейнер не запущен (состояние: ", "files: container is not running (state: ") + st + ")"
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
			if !m.composeHostOps {
				// Editing the compose file needs host filesystem access (SSH only).
				m.err = "edit requires an SSH connection (use -H ssh://...)"
				return m, nil
			}
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
		// Toggle bulk selection of the row under the cursor (containers and images).
		if m.resource == ViewContainers || m.resource == ViewImages {
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
			m.copyNotif = i18n.T("автообновление на паузе", "auto-refresh paused")
			return m, clearCopyNotifCmd()
		}
		m.copyNotif = i18n.T("автообновление возобновлено", "auto-refresh resumed")
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
	switch msg.String() {
	case "ctrl+d", "ctrl+\\":
		// Ctrl-D exits the shell, Ctrl+\ force-detaches; both tear the session
		// down locally. Handling Ctrl-D here (rather than forwarding 0x04 and
		// relying on the remote shell to honour EOF) makes "exit" reliable across
		// shells and platforms, matching the on-screen hint.
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
			return openConfirmMsg{prompt: fmt.Sprintf(i18n.T("Удалить хост %s из списка?", "Remove host %s from the list?"), name), action: remove}
		}
	}
	return false, m, nil
}

// handleImageSelectionKey handles keys active only while images are bulk-selected
// in the Images view. `r` opens a confirmation overlay that, on accept, removes
// every marked image. handled is false for other keys so handleNormal falls
// through to the normal keymap resolution.
func (m Model) handleImageSelectionKey(key string) (handled bool, model tea.Model, cmd tea.Cmd) {
	switch key {
	case "r":
		refs := m.targetImageRefs()
		if len(refs) == 0 {
			return true, m, nil
		}
		remove := bulkAction(refs, func(ref string) error { return m.backend.RemoveImage(ref, false) })
		prompt := fmt.Sprintf(i18n.T("Удалить выбранные образы (%d)?", "Remove selected images (%d)?"), len(refs))
		return true, m, func() tea.Msg { return openConfirmMsg{prompt: prompt, action: remove} }
	}
	return false, m, nil
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
			val := truncateRunes(item.Value, 30)
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
		// Restore reuploads the archive and runs `docker compose up` on the host —
		// SSH only. Over tcp:// the catalog stays browsable (view/delete are local),
		// but restore is unavailable, so Enter is a no-op with a friendly hint.
		if !m.composeHostOps {
			m.backupConfirmDelete = ""
			m.err = "restore requires an SSH connection (use -H ssh://...)"
			return m, nil
		}
		if m.backupCursor < len(m.backupItems) {
			item := m.backupItems[m.backupCursor]
			project := m.backupProject
			m.mode = ModeNormal
			m.backupConfirmDelete = ""
			return m, restoreComposeCmd(m.backend, project, m.backupName, item.path)
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
		return m, listBackupsCmd(m.backupProject, m.backupName)
	}
	return m, nil
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
	case ModeCpForm:
		m.cpForm.SetSize(w, h)
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
