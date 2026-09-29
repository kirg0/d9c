package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/dockerctx"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/i18n"
	"github.com/kirg0/d9c/internal/ui/styles"
)

// tlsFiles is a client TLS triple (CA, certificate, key paths).
type tlsFiles struct{ ca, cert, key string }

// cfgTLS reads the TLS triple currently set on cfg.
func cfgTLS(cfg *config.Config) tlsFiles {
	return tlsFiles{cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey}
}

// setCfgTLS writes t onto cfg.
func setCfgTLS(cfg *config.Config, t tlsFiles) {
	cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey = t.ca, t.cert, t.key
}

// hostTLS returns the TLS to dial h with: its own files when it carries any
// (e.g. imported from a Docker context), otherwise the session baseline (the
// -tls* flags).
func hostTLS(h hosts.Host, base tlsFiles) tlsFiles {
	if h.HasTLS() {
		return tlsFiles{h.TLSCACert, h.TLSCert, h.TLSKey}
	}
	return base
}

// baselineTLS derives the session's global TLS (the -tls* flags) from the
// startup config. TLS that came from the startup Docker context or from the
// saved entry of the startup host is per-host, not global, so it must not leak
// onto other hosts the user later connects to.
func baselineTLS(cfg *config.Config, store *hosts.Store) tlsFiles {
	cur := cfgTLS(cfg)
	if cfg.Context != "" {
		return tlsFiles{}
	}
	if h, ok := store.FindByURL(cfg.Host); ok && h.HasTLS() && hostTLS(h, tlsFiles{}) == cur {
		return tlsFiles{}
	}
	return cur
}

// contextsLoadedMsg carries the Docker CLI contexts read off the event loop.
// names holds the contexts requested on the command line (`:import contexts a
// b`); empty means "open the picker".
type contextsLoadedMsg struct {
	contexts []dockerctx.Context
	names    []string
	err      error
}

// loadContextsCmd lists the Docker CLI contexts in dir (file IO, so it runs as
// a tea.Cmd) and reports them via contextsLoadedMsg.
func loadContextsCmd(dir string, names []string) tea.Cmd {
	return func() tea.Msg {
		list, err := dockerctx.List(dir)
		return contextsLoadedMsg{contexts: list, names: names, err: err}
	}
}

// dispatchImportCommand handles `:import contexts [name...]` in the hosts view.
func (m *Model) dispatchImportCommand(args []string) (tea.Cmd, error) {
	if len(args) == 0 || !strings.EqualFold(args[0], "contexts") {
		return nil, fmt.Errorf("usage: import contexts [name...]")
	}
	return loadContextsCmd(m.dockerCfgDir, args[1:]), nil
}

// handleContextsLoaded either imports the explicitly named contexts or opens
// the multi-select picker over all of them.
func (m Model) handleContextsLoaded(msg contextsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	if len(msg.names) > 0 {
		byName := make(map[string]dockerctx.Context, len(msg.contexts))
		for _, c := range msg.contexts {
			byName[c.Name] = c
		}
		var picked []dockerctx.Context
		var missing []string
		for _, n := range msg.names {
			if c, ok := byName[n]; ok {
				picked = append(picked, c)
			} else {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			m.err = fmt.Sprintf(i18n.T("docker context не найден: %s", "docker context not found: %s"), strings.Join(missing, ", "))
			return m, nil
		}
		return m.importContexts(picked)
	}
	if len(msg.contexts) == 0 {
		m.copyNotif = fmt.Sprintf(i18n.T("docker contexts не найдены в %s", "no docker contexts found in %s"), m.dockerCfgDir)
		return m, clearCopyNotifCmd()
	}
	m.ctxItems = msg.contexts
	m.ctxChecked = make([]bool, len(msg.contexts))
	m.ctxCursor = 0
	for i, c := range msg.contexts {
		if c.Current {
			m.ctxCursor = i
			break
		}
	}
	m.mode = ModeContextPicker
	return m, nil
}

// handleContextPicker drives the context picker: ↑/↓ move, space toggles the
// highlighted context, a toggles all, Enter imports the checked contexts (or
// just the highlighted one when none is checked), q cancels (esc is global).
func (m Model) handleContextPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.ctxCursor > 0 {
			m.ctxCursor--
		}
	case "down", "j":
		if m.ctxCursor < len(m.ctxItems)-1 {
			m.ctxCursor++
		}
	case " ":
		if m.ctxCursor < len(m.ctxChecked) {
			m.ctxChecked[m.ctxCursor] = !m.ctxChecked[m.ctxCursor]
		}
	case "a":
		all := true
		for _, c := range m.ctxChecked {
			all = all && c
		}
		for i := range m.ctxChecked {
			m.ctxChecked[i] = !all
		}
	case "enter":
		var picked []dockerctx.Context
		for i, c := range m.ctxItems {
			if m.ctxChecked[i] {
				picked = append(picked, c)
			}
		}
		if len(picked) == 0 && m.ctxCursor < len(m.ctxItems) {
			picked = []dockerctx.Context{m.ctxItems[m.ctxCursor]}
		}
		m.closeContextPicker()
		return m.importContexts(picked)
	case "q":
		m.closeContextPicker()
	}
	return m, nil
}

// closeContextPicker leaves the picker and drops its state.
func (m *Model) closeContextPicker() {
	m.ctxItems, m.ctxChecked, m.ctxCursor = nil, nil, 0
	m.mode = ModeNormal
}

// importContexts adds the contexts to the host store (skipping URLs that are
// already saved), persists it and reports a one-line summary.
func (m Model) importContexts(list []dockerctx.Context) (tea.Model, tea.Cmd) {
	imported, skipped, insecure := 0, 0, 0
	for _, c := range list {
		res, _ := m.hostStore.Import(c.SavedHost())
		switch res {
		case hosts.Imported:
			imported++
			if c.SkipTLSVerify {
				insecure++
			}
		case hosts.ImportSkipped:
			skipped++
		case hosts.ImportInvalid:
		}
	}
	if imported > 0 {
		if err := m.hostStore.Save(); err != nil {
			m.err = err.Error()
			return m, fetchHosts(m.hostStore)
		}
	}
	m.copyNotif = importSummary(imported, skipped, insecure)
	return m, tea.Batch(fetchHosts(m.hostStore), clearCopyNotifCmd())
}

// importSummary renders the footer notice for an import run.
func importSummary(imported, skipped, insecure int) string {
	s := fmt.Sprintf(i18n.T("импортировано контекстов: %d", "imported contexts: %d"), imported)
	if skipped > 0 {
		s += fmt.Sprintf(i18n.T(", уже сохранены: %d", ", already saved: %d"), skipped)
	}
	if insecure > 0 {
		s += fmt.Sprintf(i18n.T(" (SkipTLSVerify не поддерживается — сертификат будет проверяться: %d)",
			" (SkipTLSVerify unsupported — certificate will be verified: %d)"), insecure)
	}
	return s
}

// contextPickerRow renders one picker line (without the cursor marker).
func (m Model) contextPickerRow(i, nameW int) string {
	c := m.ctxItems[i]
	box := "[ ]"
	if m.ctxChecked[i] {
		box = "[x]"
	}
	label := box + " " + c.Name + strings.Repeat(" ", nameW-lipgloss.Width(c.Name)) + "  " + c.Host
	var tags []string
	if c.Current {
		tags = append(tags, i18n.T("текущий", "current"))
	}
	if c.HasTLS() {
		tags = append(tags, "TLS")
	}
	if _, ok := m.hostStore.FindByURL(c.Host); ok {
		tags = append(tags, i18n.T("сохранён", "saved"))
	}
	if len(tags) > 0 {
		label += "  (" + strings.Join(tags, ", ") + ")"
	}
	return label
}

// viewContextOverlay renders the Docker context picker centered over the
// hosts view.
func (m Model) viewContextOverlay() string {
	bodyH := m.height - 2 // header + footer

	nameW := 0
	for _, c := range m.ctxItems {
		if w := lipgloss.Width(c.Name); w > nameW {
			nameW = w
		}
	}
	rows := make([]string, 0, len(m.ctxItems))
	for i := range m.ctxItems {
		label := m.contextPickerRow(i, nameW)
		if i == m.ctxCursor {
			rows = append(rows, " ▶  "+styles.CopyMenuSelected.Render(" "+label+" "))
		} else {
			rows = append(rows, "    "+styles.CopyMenuLabel.Render(label))
		}
	}

	hint := styles.CopyMenuHint.Render(i18n.T("  ↑/↓ выбор   space отметить   a все   enter импорт   q/esc отмена",
		"  ↑/↓ select   space toggle   a all   enter import   q/esc cancel"))
	title := styles.CopyMenuTitle.Render(i18n.T(" Импорт Docker contexts ", " Import Docker contexts "))
	content := title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + hint

	panel := styles.OverlayPanel.Render(content)
	return overlayCenter(m.viewNormal(), panel, m.width, bodyH)
}
