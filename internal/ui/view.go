package ui

import (
	"fmt"
	"strings"

	"d9c/internal/ui/styles"
	uitbl "d9c/internal/ui/table"
	"d9c/internal/version"

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
		body = m.hostForm.View(m.width, m.height-2)
	case ModePushForm:
		body = m.pushForm.View(m.width, m.height-2)
	case ModeNetForm:
		body = m.netForm.View(m.width, m.height-2)
	case ModeVolForm:
		body = m.volForm.View(m.width, m.height-2)
	case ModePullForm:
		body = m.pullForm.View(m.width, m.height-2)
	case ModeBuildForm:
		body = m.buildForm.View(m.width, m.height-2)
	case ModeRunForm:
		body = m.runForm.View(m.width, m.height-2)
	case ModeExecForm:
		body = m.execForm.View(m.width, m.height-2)
	case ModeComposeEdit:
		body = m.composeEdit.View(m.width)
	case ModeEvents:
		body = m.eventsModel.View()
	case ModeFSBrowser:
		body = m.fsBrowser.View()
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

	left := appBlock + verBlock + sep + breadcrumb
	status := styles.HeaderStatusDown
	if m.serverUp {
		status = styles.HeaderStatusOK
	}
	right := status.Render(" ● ") + styles.HeaderHost.Render(m.cfg.Host+" ")
	if m.reconnecting {
		right = styles.HeaderStatusRetry.Render(" ● ") +
			styles.HeaderReconnect.Render(fmt.Sprintf(" ⟳ reconnecting… (attempt %d) ", m.reconnectAttempt))
	}

	gap := max(w-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return left + styles.HeaderBg.Render(strings.Repeat(" ", gap)) + right
}

// viewFooter renders the bottom bar with context-sensitive key hints.
func (m Model) viewFooter() string {
	w := m.width

	var sb strings.Builder

	if m.err != "" {
		sb.WriteString(styles.FooterError.Render(" ✖ " + m.err + " "))
	}
	if m.copyNotif != "" {
		sb.WriteString(styles.CopySuccess.Render(" ✔ " + m.copyNotif + " "))
	}

	switch m.mode {
	case ModeDetail:
		switch {
		case m.detail.IsSearching():
			sb.WriteString(keyHint("type", "Query"))
			sb.WriteString(keyHint("enter", "Confirm"))
			sb.WriteString(keyHint("esc", "Cancel"))
		case m.detail.HasSearch():
			sb.WriteString(keyHint("n/N", "Next/Prev"))
			sb.WriteString(keyHint("/", "New search"))
			sb.WriteString(keyHint("esc", "Clear"))
			sb.WriteString(keyHint("q", "Back"))
		default:
			sb.WriteString(keyHint("↑↓", "Scroll"))
			sb.WriteString(keyHint("/", "Search"))
			sb.WriteString(keyHint("PgUp/PgDn", "Page"))
			sb.WriteString(keyHint("q/esc", "Back"))
		}
	case ModeLogs:
		switch {
		case m.logs.IsSearching():
			sb.WriteString(keyHint("type", "Query"))
			sb.WriteString(keyHint("enter", "Confirm"))
			sb.WriteString(keyHint("esc", "Cancel"))
		case m.logs.HasSearch():
			sb.WriteString(keyHint("n/N", "Next/Prev"))
			sb.WriteString(keyHint("/", "New search"))
			sb.WriteString(keyHint("esc", "Clear"))
			sb.WriteString(keyHint("q", "Back"))
		default:
			sb.WriteString(keyHint("↑↓", "Scroll"))
			follow := "Follow: off"
			if m.logs.IsFollowing() {
				follow = "Follow: on"
			}
			sb.WriteString(keyHint("f", follow))
			sb.WriteString(keyHint("/", "Search"))
			sb.WriteString(keyHint("s", "Save"))
			sb.WriteString(keyHint("q/esc", "Back"))
		}
	case ModeFilter:
		sb.WriteString(keyHint("type", "Filter"))
		sb.WriteString(keyHint("enter", "Apply"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeCommand:
		switch m.resource {
		case ViewImages:
			sb.WriteString(styles.FooterDesc.Render("  run · exec · build <dir> [tag] · tag <new-ref> · push · history · pull · rm [-f] · prune  "))
		case ViewNetworks:
			sb.WriteString(styles.FooterDesc.Render("  create · rm  "))
		case ViewVolumes:
			sb.WriteString(styles.FooterDesc.Render("  create · rm · prune  "))
		case ViewHosts:
			sb.WriteString(styles.FooterDesc.Render("  connect · add <name> <url> · edit <name> <url> · rm  "))
		case ViewCompose:
			if m.composeHostOps {
				sb.WriteString(styles.FooterDesc.Render("  create <dir> · up · down · pull · config · edit · backup · backups · restore [file] · start · stop · restart · pause · unpause · remove  "))
			} else {
				// tcp://: SSH-only ops (create/up/down/pull/config/edit/backup/restore) hidden.
				sb.WriteString(styles.FooterDesc.Render("  backups · start · stop · restart · pause · unpause · remove  "))
			}
		default:
			sb.WriteString(styles.FooterDesc.Render("  run · start · stop · restart · kill · rm · logs · exec · files [path] · cp <local> <ctr-dir>  "))
		}
		sb.WriteString(keyHint("enter", "Run"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeCopy:
		sb.WriteString(keyHint("↑↓", "Select"))
		sb.WriteString(keyHint("enter", "Copy"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeConfirm:
		sb.WriteString(keyHint("y/enter", "Confirm"))
		sb.WriteString(keyHint("n/esc", "Cancel"))
	case ModeNotice:
		sb.WriteString(keyHint("enter/esc", "Close"))
	case ModeBackupPicker:
		if m.backupConfirmDelete != "" {
			sb.WriteString(styles.FooterError.Render(" delete " + m.backupConfirmDelete + "? "))
			sb.WriteString(keyHint("d", "Confirm"))
			sb.WriteString(keyHint("esc", "Cancel"))
		} else {
			sb.WriteString(keyHint("↑↓", "Select"))
			// Restore is SSH-only; over tcp:// the catalog is view/delete only.
			if m.composeHostOps {
				sb.WriteString(keyHint("enter", "Restore"))
			}
			sb.WriteString(keyHint("d", "Delete"))
			sb.WriteString(keyHint("esc", "Close"))
		}
	case ModeHostForm:
		sb.WriteString(keyHint("tab", "Switch field"))
		sb.WriteString(keyHint("enter", "Save"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModePushForm:
		sb.WriteString(keyHint("tab", "Switch field"))
		sb.WriteString(keyHint("enter", "Push"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeNetForm, ModeVolForm:
		sb.WriteString(keyHint("tab", "Switch field"))
		sb.WriteString(keyHint("enter", "Create"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeRunForm, ModeExecForm:
		sb.WriteString(keyHint("tab", "Switch field"))
		sb.WriteString(keyHint("enter", "Run"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeBuildForm:
		sb.WriteString(keyHint("tab", "Switch field"))
		sb.WriteString(keyHint("enter", "Build"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModePullForm:
		sb.WriteString(keyHint("enter", "Pull"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeComposeEdit:
		sb.WriteString(keyHint("ctrl+s", "Save"))
		sb.WriteString(keyHint("esc", "Cancel"))
	case ModeHelp:
		sb.WriteString(keyHint("↑↓", "Scroll"))
		sb.WriteString(keyHint("PgUp/PgDn", "Page"))
		sb.WriteString(keyHint("q/esc", "Close"))
	case ModeShell:
		if m.shell.Closed() {
			sb.WriteString(styles.FooterDesc.Render("  процесс завершён  "))
			sb.WriteString(keyHint("q/esc/enter", "Close"))
		} else {
			sb.WriteString(styles.FooterDesc.Render("  ввод → контейнер  "))
			sb.WriteString(keyHint("exit/Ctrl-D", "Quit shell"))
			sb.WriteString(keyHint("ctrl+\\", "Detach"))
		}
	case ModeEvents:
		sb.WriteString(keyHint("↑↓", "Scroll"))
		sb.WriteString(keyHint("r", "Refresh"))
		sb.WriteString(keyHint("q/esc", "Back"))
	case ModeFSBrowser:
		sb.WriteString(keyHint("↑↓", "Navigate"))
		sb.WriteString(keyHint("enter/l", "Open"))
		sb.WriteString(keyHint("bksp/h", "Up"))
		sb.WriteString(keyHint("d", "Download"))
		sb.WriteString(keyHint("q/esc", "Back"))
	default:
		sb.WriteString(keyHint("↑↓", "Navigate"))
		switch m.resource {
		case ViewHosts:
			sb.WriteString(keyHint("enter", "Connect"))
			sb.WriteString(keyHint("a", "Add"))
			sb.WriteString(keyHint("e", "Edit"))
			sb.WriteString(keyHint("d", "Delete"))
		case ViewCompose:
			sb.WriteString(keyHint("enter", "Containers"))
			sb.WriteString(keyHint("i", "Inspect"))
			sb.WriteString(keyHint("l", "Logs"))
			if m.composeHostOps {
				sb.WriteString(keyHint("e", "Edit"))
			}
		default:
			sb.WriteString(keyHint("i", "Inspect"))
		}
		if m.resource == ViewContainers {
			sb.WriteString(keyHint("l", "Logs"))
			sb.WriteString(keyHint("x", "Shell"))
			sb.WriteString(keyHint("f", "Files"))
			sb.WriteString(keyHint("a", "All"))
			sb.WriteString(keyHint("s", "Stats"))
			sb.WriteString(keyHint("⇧N/S/C/M", "Sort"))
			sb.WriteString(keyHint("space", "Select"))
			if m.composeFilter != "" {
				sb.WriteString(keyHint("esc", "Back"))
			}
		}
		if m.resource == ViewImages {
			sb.WriteString(keyHint("space", "Select"))
		}
		for _, p := range m.scopedPluginsWithKeys() {
			sb.WriteString(keyHint(p.Key, p.Name))
		}
		sb.WriteString(keyHint("y", "Copy"))
		sb.WriteString(keyHint("/", "Filter"))
		sb.WriteString(keyHint(":", "Cmd"))
		sb.WriteString(keyHint("r", "Refresh"))
		if m.paused {
			sb.WriteString(keyHint("p", "Resume"))
		} else {
			sb.WriteString(keyHint("p", "Pause"))
		}
		sb.WriteString(keyHint("?", "Help"))
		sb.WriteString(keyHint("q", "Quit"))
	}

	content := sb.String()
	gap := max(w-lipgloss.Width(content), 0)
	return content + styles.FooterBg.Render(strings.Repeat(" ", gap))
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
	bodyH := m.height - 2 // header + footer

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

// viewConfirmOverlay renders the generic confirmation panel centered in the
// body area: the prompt plus a y/esc hint.
func (m Model) viewConfirmOverlay() string {
	bodyH := m.height - 2 // header + footer

	title := styles.CopyMenuTitle.Render(" Подтверждение ")
	hint := styles.CopyMenuHint.Render("  y/enter подтвердить   n/esc отмена")
	content := title + "\n\n" + m.confirmPrompt + "\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, panel)
}

// viewNoticeOverlay renders the informational modal centered in the body area:
// a title plus a free-form body. Any key dismisses it; no action is attached.
func (m Model) viewNoticeOverlay() string {
	bodyH := m.height - 2 // header + footer

	title := styles.CopyMenuTitle.Render(m.noticeTitle)
	hint := styles.CopyMenuHint.Render("  enter/esc закрыть")
	content := title + "\n\n" + m.noticeBody + "\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, panel)
}

// viewBackupOverlay renders the backup catalog panel centered in the body area.
func (m Model) viewBackupOverlay() string {
	bodyH := m.height - 2 // header + footer

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
	switch m.mode {
	case ModeFilter:
		return lipgloss.JoinVertical(lipgloss.Left, body, m.filter.View(m.width))
	case ModeCommand:
		return lipgloss.JoinVertical(lipgloss.Left, body, m.cmdline.View(m.width))
	}
	return body
}
