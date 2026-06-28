package ui

import (
	"d9c/internal/i18n"

	tea "github.com/charmbracelet/bubbletea"
)

// openLangPicker opens the language selector modal. It snapshots the active
// language (to roll back on cancel), positions the cursor on the current
// language, and applies it as a live preview so the surrounding UI already
// reflects the highlighted choice.
func (m *Model) openLangPicker() {
	m.langNames = i18n.Names()
	m.langOriginal = i18n.Current()
	m.langCursor = 0
	for i, l := range m.langNames {
		if l == m.langOriginal {
			m.langCursor = i
			break
		}
	}
	m.mode = ModeLangPicker
	m.applyLangAt(m.langCursor)
}

// applyLangAt switches the active UI language to the entry at index i (a live
// preview as the cursor moves). Out-of-range indices are ignored.
func (m *Model) applyLangAt(i int) {
	if i < 0 || i >= len(m.langNames) {
		return
	}
	i18n.Set(m.langNames[i])
}

// cancelLangPicker closes the picker and restores the language that was active
// when it opened, discarding the preview.
func (m *Model) cancelLangPicker() {
	i18n.Set(m.langOriginal)
	m.mode = ModeNormal
}

// handleLangPicker drives the language selector: the cursor previews the
// highlighted language live, Enter keeps it (and persists it to the config),
// and q/esc cancels (esc is also caught by the global handler, which restores
// the original language).
func (m Model) handleLangPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.langCursor > 0 {
			m.langCursor--
			m.applyLangAt(m.langCursor)
		}
	case "down", "j":
		if m.langCursor < len(m.langNames)-1 {
			m.langCursor++
			m.applyLangAt(m.langCursor)
		}
	case "enter":
		if m.langCursor < len(m.langNames) {
			lang := m.langNames[m.langCursor]
			i18n.Set(lang)
			m.copyNotif = i18n.T("язык: ", "language: ") + lang.Display()
			if m.settings != nil {
				if err := m.settings.SetLang(string(lang)); err != nil {
					m.copyNotif = i18n.T("язык применён, но не сохранён: ", "language applied but not saved: ") + err.Error()
				}
			}
			m.mode = ModeNormal
			return m, clearCopyNotifCmd()
		}
	case "q":
		m.cancelLangPicker()
	}
	return m, nil
}
