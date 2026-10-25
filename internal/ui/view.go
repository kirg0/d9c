package ui

import (
	"fmt"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/i18n"
	"github.com/kirg0/d9c/internal/theme"
	"github.com/kirg0/d9c/internal/ui/styles"
	uitbl "github.com/kirg0/d9c/internal/ui/table"
	"github.com/kirg0/d9c/internal/version"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.width == 0 {
		return "Loading…"
	}

	header := m.viewHeader()
	footer := m.viewFooter()

	var body string
	switch m.mode {
	case ModeLogs:
		body = m.logs.View()
	case ModeDetail:
		body = m.detail.View()
	case ModeCopy:
		body = m.viewCopyOverlay()
	case ModeThemePicker:
		body = m.viewThemeOverlay()
	case ModeLangPicker:
		body = m.viewLangOverlay()
	case ModeNamespacePicker:
		body = m.viewNamespaceOverlay()
	case ModeContextPicker:
		body = m.viewContextOverlay()
	case ModeConfirm:
		body = m.viewConfirmOverlay()
	case ModeNotice:
		body = m.viewNoticeOverlay()
	case ModeBackupPicker:
		body = m.viewBackupOverlay()
	case ModeHelp:
		body = m.help.View()
	case ModeShell:
		body = m.shell.View()
	case ModeHostForm:
		body = m.hostForm.View(m.width, m.bodyHeight())
	case ModeConnectAuth:
		body = m.connForm.View(m.width, m.bodyHeight())
	case ModeConnecting:
		body = m.connWait.View(m.width, m.bodyHeight())
	case ModePushForm:
		body = m.pushForm.View(m.width, m.bodyHeight())
	case ModeNetForm:
		body = m.netForm.View(m.width, m.bodyHeight())
	case ModeVolForm:
		body = m.volForm.View(m.width, m.bodyHeight())
	case ModePullForm:
		body = m.pullForm.View(m.width, m.bodyHeight())
	case ModeBuildForm:
		body = m.buildForm.View(m.width, m.bodyHeight())
	case ModeRunForm:
		body = m.runForm.View(m.width, m.bodyHeight())
	case ModeExecForm:
		body = m.execForm.View(m.width, m.bodyHeight())
	case ModeComposeEdit:
		body = m.composeEdit.View(m.width)
	case ModeEvents:
		body = m.eventsModel.View()
	case ModeFSBrowser:
		body = m.fsBrowser.View()
	case ModeCpForm:
		body = m.cpForm.View(m.width, m.bodyHeight())
	case ModePortForwardForm:
		body = m.pfForm.View(m.width, m.bodyHeight())
	case ModePortForwards:
		body = m.viewPortForwardsOverlay()
	default:
		body = m.viewNormal()
	}

	return header + "\n" + body + "\n" + footer
}

// viewHeader renders the top bar: app name + breadcrumb + host (k9s style).
func (m Model) viewHeader() string {
	w := m.width

	appBlock := styles.HeaderApp.Render(" d9c ")
	verBlock := styles.HeaderInfo.Render(" " + version.String() + " ")
	sep := styles.HeaderSep.Render(" › ")

	var breadcrumb string
	switch m.mode {
	case ModeDetail:
		breadcrumb = styles.HeaderResource.Render(" "+m.resource.String()+" ") +
			sep + styles.HeaderResource.Render(" inspect: "+m.detail.ContainerName()+" ")
	case ModeLogs:
		if m.opTitle != "" {
			breadcrumb = styles.HeaderResource.Render(" "+m.resource.String()+" ") +
				sep + styles.HeaderResource.Render(" "+m.opTitle+" ")
		} else {
			breadcrumb = styles.HeaderResource.Render(" "+m.resource.String()+" ") +
				sep + styles.HeaderResource.Render(" logs: "+m.logs.ContainerID()+" ")
		}
	case ModeComposeEdit:
		sub := " edit: " + m.composeEdit.Project() + " "
		if m.composeEdit.IsCreate() {
			sub = " create: " + m.composeEdit.CreateDir() + " "
		}
		breadcrumb = styles.HeaderResource.Render(" Compose ") +
			sep + styles.HeaderResource.Render(sub)
	case ModeBackupPicker:
		breadcrumb = styles.HeaderResource.Render(" Compose ") +
			sep + styles.HeaderResource.Render(" backups: "+m.backupProject+" ")
	case ModeHelp:
		breadcrumb = styles.HeaderResource.Render(" Help ") +
			sep + styles.HeaderResource.Render(" "+m.resource.String()+" ")
	case ModeShell:
		breadcrumb = styles.HeaderResource.Render(" Containers ") +
			sep + styles.HeaderResource.Render(" shell: "+m.shell.Title()+" ")
	case ModeEvents:
		breadcrumb = styles.HeaderResource.Render(" Events ")
	case ModeFSBrowser:
		breadcrumb = styles.HeaderResource.Render(" Containers ") +
			sep + styles.HeaderResource.Render(" files: "+m.fsBrowser.Name()+" ")
	case ModePushForm:
		breadcrumb = styles.HeaderResource.Render(" Images ") +
			sep + styles.HeaderResource.Render(" push: "+m.pushForm.Ref()+" ")
	case ModeNetForm:
		breadcrumb = styles.HeaderResource.Render(" Networks ") +
			sep + styles.HeaderResource.Render(" create ")
	case ModeVolForm:
		breadcrumb = styles.HeaderResource.Render(" Volumes ") +
			sep + styles.HeaderResource.Render(" create ")
	case ModePullForm:
		breadcrumb = styles.HeaderResource.Render(" Images ") +
			sep + styles.HeaderResource.Render(" pull ")
	case ModeBuildForm:
		breadcrumb = styles.HeaderResource.Render(" Images ") +
			sep + styles.HeaderResource.Render(" build ")
	case ModeRunForm:
		breadcrumb = styles.HeaderResource.Render(" Containers ") +
			sep + styles.HeaderResource.Render(" run ")
	case ModeExecForm:
		breadcrumb = styles.HeaderResource.Render(" Images ") +
			sep + styles.HeaderResource.Render(" exec (one-off) ")
	case ModeCpForm:
		breadcrumb = styles.HeaderResource.Render(" Containers ") +
			sep + styles.HeaderResource.Render(" cp: "+m.cpForm.Name()+" ")
	case ModePortForwardForm:
		breadcrumb = styles.HeaderResource.Render(" "+m.resource.String()+" ") +
			sep + styles.HeaderResource.Render(" port-forward ")
	default:
		resName := m.resource.String()
		if m.resource == ViewContainers && m.showAll {
			resName = "Containers (all)"
		}
		if m.resource == ViewContainers && m.statsView {
			resName += " · stats"
		}
		if m.resource == ViewContainers && m.sortField != uitbl.SortNone {
			arrow := "↑"
			if m.sortDesc {
				arrow = "↓"
			}
			resName += " · " + arrow + m.sortField.String()
		}
		if (m.resource == ViewContainers || m.resource == ViewImages) && len(m.selected) > 0 {
			resName += fmt.Sprintf(" · %d selected", len(m.selected))
		}
		shown := len(m.table.Table().Rows())
		total := m.currentResourceLen()
		breadcrumb = styles.HeaderResource.Render(" "+resName+" ") +
			styles.HeaderInfo.Render(fmt.Sprintf(" [%d/%d] ", shown, total))
		if m.resource == ViewContainers && m.composeFilter != "" {
			breadcrumb += sep + styles.HeaderResource.Render(" compose: "+m.composeFilterLabel()+" ")
		}
		if m.filter.Value() != "" {
			breadcrumb += styles.HeaderFilter.Render(" </" + m.filter.Value() + "> ")
		}
		// Auto-refresh state: a paused chip, or the live cadence.
		if m.paused {
			breadcrumb += styles.HeaderPaused.Render(" ⏸ paused ")
		} else {
			breadcrumb += styles.HeaderInfo.Render(fmt.Sprintf(" ↻ %s ", m.refreshInterval))
		}
		// Resource-usage alerts: count of containers breaching a threshold.
		if m.resource == ViewContainers && m.alerts.Active() {
			if n := len(m.containerAlertSet()); n > 0 {
				breadcrumb += styles.HeaderAlert.Render(fmt.Sprintf(" ⚠ %d ", n))
			}
		}
	}
	// Open port-forward tunnels, from any view (they outlive view switches).
	if n := m.pf.Len(); n > 0 {
		breadcrumb += styles.HeaderInfo.Render(fmt.Sprintf(" ⇄ %d ", n))
	}

	left := appBlock + verBlock + sep + breadcrumb
	status := styles.HeaderStatusDown
	if m.serverUp {
		status = styles.HeaderStatusOK
	}
	right := status.Render(" ● ") + styles.HeaderHost.Render(m.cfg.Host+" ")
	// Surface a non-Docker engine (Podman/containerd/CRI) so the user can confirm
	// the connection lands on the expected runtime; Docker is the implied default.
	// For containerd, append the active namespace ("containerd:k8s.io").
	if m.runtime != docker.RuntimeUnknown && m.runtime != docker.RuntimeDocker {
		label := m.runtime.Label()
		if nb, ok := m.backend.(docker.NamespacedBackend); ok {
			label += ":" + nb.CurrentNamespace()
		}
		right = styles.HeaderRuntime.Render(" "+label+" ") + right
	}
	// Read-only mode: a prominent chip so the user always knows mutating actions
	// are disabled (kept visible even while reconnecting).
	roChip := ""
	if m.readOnly() {
		roChip = styles.HeaderReadOnly.Render("RO") + " "
	}
	right = roChip + right
	if m.reconnecting {
		right = roChip + styles.HeaderStatusRetry.Render(" ● ") +
			styles.HeaderReconnect.Render(fmt.Sprintf(" ⟳ reconnecting… (attempt %d) ", m.reconnectAttempt))
	}

	gap := max(w-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return left + styles.HeaderBg.Render(strings.Repeat(" ", gap)) + right
}

// viewFooter renders the bottom bar with context-sensitive key hints,
// wrapped onto as many lines as footerLines allows for the window width.
func (m Model) viewFooter() string {
	return strings.Join(m.footerLines(), "\n")
}

// footerLines lays the footer hints out to the window width: hints that do not
// fit on one line wrap onto the next (up to maxFooterLines), and every line is
// padded with the footer background.
func (m Model) footerLines() []string {
	lines := wrapSegments(m.footerSegments(), m.width, m.maxFooterLines())
	for i, l := range lines {
		gap := max(m.width-lipgloss.Width(l), 0)
		lines[i] = l + styles.FooterBg.Render(strings.Repeat(" ", gap))
	}
	return lines
}

// footerSegments returns the footer as indivisible pieces (one key hint, one
// command name, the error badge…), the units wrapSegments breaks lines between.
func (m Model) footerSegments() []string {
	var segs []string
	add := func(s ...string) { segs = append(segs, s...) }

	if m.err != "" {
		add(styles.FooterError.Render(" ✖ " + m.err + " "))
	}
	if m.copyNotif != "" {
		add(styles.CopySuccess.Render(" ✔ " + m.copyNotif + " "))
	}

	switch m.mode {
	case ModeDetail:
		switch {
		case m.detail.IsSearching():
			add(keyHint("type", "Query"))
			add(keyHint("enter", "Confirm"))
			add(keyHint("esc", "Cancel"))
		case m.detail.HasSearch():
			add(keyHint("n/N", "Next/Prev"))
			add(keyHint("/", "New search"))
			add(keyHint("esc", "Clear"))
			add(keyHint("q", "Back"))
		default:
			add(keyHint("↑↓", "Scroll"))
			add(keyHint("/", "Search"))
			add(keyHint("PgUp/PgDn", "Page"))
			add(keyHint("q/esc", "Back"))
		}
	case ModeLogs:
		switch {
		case m.logs.IsSearching():
			add(keyHint("type", "Query"))
			add(keyHint("enter", "Confirm"))
			add(keyHint("esc", "Cancel"))
		case m.logs.HasSearch():
			add(keyHint("n/N", "Next/Prev"))
			add(keyHint("/", "New search"))
			add(keyHint("esc", "Clear"))
			add(keyHint("q", "Back"))
		default:
			add(keyHint("↑↓", "Scroll"))
			follow := "Follow: off"
			if m.logs.IsFollowing() {
				follow = "Follow: on"
			}
			add(keyHint("f", follow))
			add(keyHint("/", "Search"))
			add(keyHint("s", "Save"))
			add(keyHint("q/esc", "Back"))
		}
	case ModeFilter:
		add(keyHint("type", "Filter"))
		add(keyHint("enter", "Apply"))
		add(keyHint("esc", "Cancel"))
	case ModeCommand:
		switch {
		case m.readOnly() && m.resource != ViewHosts:
			// Read-only: list only the commands still available in this view.
			add(cmdHints(m.readOnlyCommandList()...)...)
		case m.resource == ViewImages:
			add(cmdHints("run", "exec", "build <dir> [tag]", "tag <new-ref>", "push", "history", "pull", "rm [-f]", "prune")...)
		case m.resource == ViewNetworks:
			add(cmdHints("create", "rm")...)
		case m.resource == ViewVolumes:
			add(cmdHints("create", "rm", "prune")...)
		case m.resource == ViewHosts:
			add(cmdHints("connect", "add <name> <url>", "edit <name> <url>", "rm", "import contexts")...)
		case m.resource == ViewCompose:
			if m.composeHostOps {
				add(cmdHints("create <dir>", "up", "down", "pull", "config", "edit", "backup", "backups", "restore [file]", "start", "stop", "restart", "pause", "unpause", "remove")...)
			} else {
				// tcp://: SSH-only ops (create/up/down/pull/config/edit/backup/restore) hidden.
				add(cmdHints("backups", "start", "stop", "restart", "pause", "unpause", "remove")...)
			}
		default:
			add(cmdHints("run", "start", "stop", "restart", "kill", "rm", "logs", "exec", "files [path]", "cp <local> <ctr-dir>")...)
		}
		add(keyHint("enter", "Run"))
		add(keyHint("esc", "Cancel"))
	case ModeCopy:
		add(keyHint("↑↓", "Select"))
		add(keyHint("enter", "Copy"))
		add(keyHint("esc", "Cancel"))
	case ModeThemePicker:
		add(keyHint("↑↓", "Preview"))
		add(keyHint("enter", "Apply"))
		add(keyHint("q/esc", "Cancel"))
	case ModeLangPicker:
		add(keyHint("↑↓", "Preview"))
		add(keyHint("enter", "Apply"))
		add(keyHint("q/esc", "Cancel"))
	case ModeNamespacePicker:
		add(keyHint("↑↓", "Select"))
		add(keyHint("enter", "Switch"))
		add(keyHint("q/esc", "Cancel"))
	case ModeContextPicker:
		add(keyHint("↑↓", "Select"))
		add(keyHint("space", "Toggle"))
		add(keyHint("a", "All"))
		add(keyHint("enter", "Import"))
		add(keyHint("q/esc", "Cancel"))
	case ModeConfirm:
		add(keyHint("y/enter", "Confirm"))
		add(keyHint("n/esc", "Cancel"))
	case ModeNotice:
		add(keyHint("enter/esc", "Close"))
	case ModeBackupPicker:
		if m.backupConfirmDelete != "" {
			add(styles.FooterError.Render(" delete " + m.backupConfirmDelete + "? "))
			add(keyHint("d", "Confirm"))
			add(keyHint("esc", "Cancel"))
		} else {
			add(keyHint("↑↓", "Select"))
			// Restore is SSH-only; over tcp:// the catalog is view/delete only.
			if m.composeHostOps && !m.readOnly() {
				add(keyHint("enter", "Restore"))
			}
			add(keyHint("d", "Delete"))
			add(keyHint("esc", "Close"))
		}
	case ModeHostForm:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Save"))
		add(keyHint("esc", "Cancel"))
	case ModeConnectAuth:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Connect"))
		add(keyHint("esc", "Cancel"))
	case ModeConnecting:
		if m.connWait.Busy() {
			add(keyHint("esc", "Cancel"))
		} else {
			add(keyHint("enter", "Retry"))
			add(keyHint("esc", "Close"))
		}
	case ModePushForm:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Push"))
		add(keyHint("esc", "Cancel"))
	case ModeNetForm, ModeVolForm:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Create"))
		add(keyHint("esc", "Cancel"))
	case ModeRunForm, ModeExecForm:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Run"))
		add(keyHint("esc", "Cancel"))
	case ModeBuildForm:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Build"))
		add(keyHint("esc", "Cancel"))
	case ModePullForm:
		add(keyHint("enter", "Pull"))
		add(keyHint("esc", "Cancel"))
	case ModeCpForm:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Open/Upload"))
		add(keyHint("esc", "Cancel"))
	case ModePortForwardForm:
		add(keyHint("tab", "Switch field"))
		add(keyHint("enter", "Forward"))
		add(keyHint("esc", "Cancel"))
	case ModePortForwards:
		add(keyHint("↑↓", "Select"))
		add(keyHint("s", "Stop/Start"))
		add(keyHint("d", "Delete"))
		add(keyHint("y", "Copy"))
		add(keyHint("q/esc", "Close"))
	case ModeComposeEdit:
		add(keyHint("ctrl+s", "Save"))
		add(keyHint("esc", "Cancel"))
	case ModeHelp:
		add(keyHint("↑↓", "Scroll"))
		add(keyHint("PgUp/PgDn", "Page"))
		add(keyHint("q/esc", "Close"))
	case ModeShell:
		if m.shell.Closed() {
			add(styles.FooterDesc.Render(i18n.T("  процесс завершён  ", "  process finished  ")))
			add(keyHint("q/esc/enter", "Close"))
		} else {
			add(styles.FooterDesc.Render(i18n.T("  ввод → контейнер  ", "  input → container  ")))
			add(keyHint("exit/Ctrl-D", "Quit shell"))
			add(keyHint("ctrl+\\", "Detach"))
		}
	case ModeEvents:
		add(keyHint("↑↓", "Scroll"))
		add(keyHint("r", "Refresh"))
		add(keyHint("q/esc", "Back"))
	case ModeFSBrowser:
		add(keyHint("↑↓", "Navigate"))
		add(keyHint("enter/l", "Open"))
		add(keyHint("bksp/h", "Up"))
		add(keyHint("d", "Download"))
		add(keyHint("q/esc", "Back"))
	default:
		// While images are bulk-selected, collapse the footer to the actions that
		// apply to the selection: navigate, remove (with confirmation), clear, quit.
		if m.resource == ViewImages && len(m.selected) > 0 {
			add(keyHint("↑↓", "Navigate"))
			if !m.readOnly() {
				add(keyHint("r", "Remove"))
			}
			add(keyHint("esc", "Clear"))
			add(keyHint("q", "Quit"))
			break
		}
		add(keyHint("↑↓", "Navigate"))
		switch m.resource {
		case ViewHosts:
			add(keyHint("enter", "Connect"))
			add(keyHint("a", "Add"))
			add(keyHint("e", "Edit"))
			add(keyHint("d", "Delete"))
		case ViewCompose:
			add(keyHint("enter", "Containers"))
			add(keyHint("i", "Inspect"))
			add(keyHint("l", "Logs"))
			add(keyHint("F", "Forward"))
			if m.composeHostOps && !m.readOnly() {
				add(keyHint("e", "Edit"))
			}
		default:
			add(keyHint("i", "Inspect"))
		}
		if m.resource == ViewContainers {
			add(keyHint("l", "Logs"))
			if !m.readOnly() {
				add(keyHint("x", "Shell"))
			}
			add(keyHint("f", "Files"))
			add(keyHint("F", "Forward"))
			add(keyHint("a", "All"))
			add(keyHint("s", "Stats"))
			add(keyHint("⇧N/S/C/M", "Sort"))
			add(keyHint("space", "Select"))
			if m.composeFilter != "" {
				add(keyHint("esc", "Back"))
			}
		}
		if m.resource == ViewImages {
			add(keyHint("space", "Select"))
		}
		for _, p := range m.scopedPluginsWithKeys() {
			add(keyHint(p.Key, p.Name))
		}
		add(keyHint("y", "Copy"))
		add(keyHint("/", "Filter"))
		add(keyHint(":", "Cmd"))
		add(keyHint("r", "Refresh"))
		if m.paused {
			add(keyHint("p", "Resume"))
		} else {
			add(keyHint("p", "Pause"))
		}
		add(keyHint("?", "Help"))
		add(keyHint("q", "Quit"))
	}

	return segs
}

func (m Model) currentResourceLen() int {
	switch m.resource {
	case ViewImages:
		return len(m.images)
	case ViewNetworks:
		return len(m.networks)
	case ViewVolumes:
		return len(m.volumes)
	case ViewHosts:
		return len(m.hosts)
	case ViewCompose:
		return len(m.composes)
	default:
		return len(m.containers)
	}
}

func keyHint(key, desc string) string {
	return styles.FooterKey.Render(" <"+key+">") + styles.FooterDesc.Render(" "+desc+"  ")
}

// viewCopyOverlay renders the copy-menu panel centered in the body area.
func (m Model) viewCopyOverlay() string {
	bodyH := m.bodyHeight()

	var rows []string
	maxLabel := 0
	for _, it := range m.copyItems {
		if len(it.Label) > maxLabel {
			maxLabel = len(it.Label)
		}
	}

	for i, it := range m.copyItems {
		label := fmt.Sprintf("%-*s", maxLabel, it.Label)
		val := truncateRunes(it.Value, 50)
		if i == m.copyCursor {
			line := " ▶  " + styles.CopyMenuSelected.Render(" "+label+"  "+val+" ")
			rows = append(rows, line)
		} else {
			line := "    " + styles.CopyMenuLabel.Render(label) + "  " + styles.CopyMenuValue.Render(val)
			rows = append(rows, line)
		}
	}

	hint := styles.CopyMenuHint.Render("  ↑/↓ select   enter copy   esc cancel")

	title := styles.CopyMenuTitle.Render(" Copy to clipboard ")
	content := title + "\n\n" +
		strings.Join(rows, "\n") +
		"\n\n" + hint

	panel := styles.OverlayPanel.Render(content)

	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, panel)
}

// viewThemeOverlay renders the theme picker centered over the current view, so
// the live preview (header/footer/list re-colored by the highlighted theme)
// stays visible around the modal.
func (m Model) viewThemeOverlay() string {
	bodyH := m.bodyHeight()

	maxName := 0
	for _, name := range m.themeNames {
		if len(name) > maxName {
			maxName = len(name)
		}
	}

	var rows []string
	for i, name := range m.themeNames {
		swatch := ""
		if pal, ok := theme.ByName(name); ok {
			swatch = styles.Swatch(pal)
		}
		label := fmt.Sprintf("%-*s", maxName, name)
		if i == m.themeCursor {
			rows = append(rows, " ▶  "+styles.CopyMenuSelected.Render(" "+label+" ")+"  "+swatch)
		} else {
			rows = append(rows, "    "+styles.CopyMenuLabel.Render(label)+"  "+swatch)
		}
	}

	hint := styles.CopyMenuHint.Render(i18n.T("  ↑/↓ превью   enter применить   q/esc отмена", "  ↑/↓ preview   enter apply   q/esc cancel"))
	title := styles.CopyMenuTitle.Render(i18n.T(" Тема ", " Theme "))
	content := title + "\n\n" +
		strings.Join(rows, "\n") +
		"\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return overlayCenter(m.viewNormal(), panel, m.width, bodyH)
}

// viewLangOverlay renders the language picker centered over the current view,
// mirroring the theme picker: the highlighted language is applied live, so the
// surrounding UI text already reflects the choice.
func (m Model) viewLangOverlay() string {
	bodyH := m.bodyHeight()

	maxName := 0
	for _, l := range m.langNames {
		if w := lipgloss.Width(l.Display()); w > maxName {
			maxName = w
		}
	}

	var rows []string
	for i, l := range m.langNames {
		label := l.Display() + strings.Repeat(" ", maxName-lipgloss.Width(l.Display()))
		if i == m.langCursor {
			rows = append(rows, " ▶  "+styles.CopyMenuSelected.Render(" "+label+" "))
		} else {
			rows = append(rows, "    "+styles.CopyMenuLabel.Render(label))
		}
	}

	hint := styles.CopyMenuHint.Render(i18n.T("  ↑/↓ превью   enter применить   q/esc отмена", "  ↑/↓ preview   enter apply   q/esc cancel"))
	title := styles.CopyMenuTitle.Render(i18n.T(" Язык ", " Language "))
	content := title + "\n\n" +
		strings.Join(rows, "\n") +
		"\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return overlayCenter(m.viewNormal(), panel, m.width, bodyH)
}

// viewNamespaceOverlay renders the containerd namespace selector centered over
// the current view. There is no live preview (switching re-queries the daemon),
// so the highlight simply moves until Enter commits.
func (m Model) viewNamespaceOverlay() string {
	bodyH := m.bodyHeight()

	maxName := 0
	for _, n := range m.nsNames {
		if w := lipgloss.Width(n); w > maxName {
			maxName = w
		}
	}

	var rows []string
	if len(m.nsNames) == 0 {
		rows = append(rows, "    "+styles.CopyMenuLabel.Render(i18n.T("нет namespaces", "no namespaces")))
	}
	for i, n := range m.nsNames {
		label := n + strings.Repeat(" ", maxName-lipgloss.Width(n))
		if i == m.nsCursor {
			rows = append(rows, " ▶  "+styles.CopyMenuSelected.Render(" "+label+" "))
		} else {
			rows = append(rows, "    "+styles.CopyMenuLabel.Render(label))
		}
	}

	hint := styles.CopyMenuHint.Render(i18n.T("  ↑/↓ выбор   enter переключить   q/esc отмена", "  ↑/↓ select   enter switch   q/esc cancel"))
	title := styles.CopyMenuTitle.Render(i18n.T(" Namespace ", " Namespace "))
	content := title + "\n\n" +
		strings.Join(rows, "\n") +
		"\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return overlayCenter(m.viewNormal(), panel, m.width, bodyH)
}

// viewConfirmOverlay renders the generic confirmation panel centered over the
// current resource view, so the underlying list stays visible around the modal.
func (m Model) viewConfirmOverlay() string {
	bodyH := m.bodyHeight()

	title := styles.CopyMenuTitle.Render(i18n.T(" Подтверждение ", " Confirm "))
	hint := styles.CopyMenuHint.Render(i18n.T("  y/enter подтвердить   n/esc отмена", "  y/enter confirm   n/esc cancel"))
	content := title + "\n\n" + m.confirmPrompt + "\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return overlayCenter(m.viewNormal(), panel, m.width, bodyH)
}

// viewNoticeOverlay renders the informational modal centered in the body area:
// a title plus a free-form body. Any key dismisses it; no action is attached.
func (m Model) viewNoticeOverlay() string {
	bodyH := m.bodyHeight()

	title := styles.CopyMenuTitle.Render(m.noticeTitle)
	hint := styles.CopyMenuHint.Render(i18n.T("  enter/esc закрыть", "  enter/esc close"))
	content := title + "\n\n" + m.noticeBody + "\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, panel)
}

// viewBackupOverlay renders the backup catalog panel centered in the body area.
func (m Model) viewBackupOverlay() string {
	bodyH := m.bodyHeight()

	maxName := 0
	for _, it := range m.backupItems {
		if len(it.name) > maxName {
			maxName = len(it.name)
		}
	}

	var rows []string
	for i, it := range m.backupItems {
		name := fmt.Sprintf("%-*s", maxName, it.name)
		meta := fmt.Sprintf("%10s   %s", humanSize(it.size), it.modTime.Format("2006-01-02 15:04"))
		switch {
		case it.name == m.backupConfirmDelete:
			rows = append(rows, " ▶  "+styles.FooterError.Render(" "+name+"  "+meta+" "))
		case i == m.backupCursor:
			rows = append(rows, " ▶  "+styles.CopyMenuSelected.Render(" "+name+"  "+meta+" "))
		default:
			rows = append(rows, "    "+styles.CopyMenuLabel.Render(name)+"  "+styles.CopyMenuValue.Render(meta))
		}
	}

	hint := styles.CopyMenuHint.Render("  ↑/↓ select   enter restore   d delete   esc close")
	title := styles.CopyMenuTitle.Render(" Backups · " + m.backupProject + " ")
	content := title + "\n\n" +
		strings.Join(rows, "\n") +
		"\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, panel)
}

func (m Model) viewNormal() string {
	body := m.table.View()
	if m.statsPanelHeight() > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, body, m.viewStatsPanel())
	}
	switch m.mode {
	case ModeFilter:
		return lipgloss.JoinVertical(lipgloss.Left, body, m.filter.View(m.width))
	case ModeCommand:
		return lipgloss.JoinVertical(lipgloss.Left, body, m.cmdline.View(m.width))
	}
	return body
}
