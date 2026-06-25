package ui

import (
	"d9c/internal/theme"
	"d9c/internal/ui/styles"

	tea "github.com/charmbracelet/bubbletea"
)

// openThemePicker opens the theme selector modal. It snapshots the active
// palette (to roll back on cancel), positions the cursor on the current
// built-in theme when there is one, and applies that theme as a live preview so
// the highlighted row already matches what the UI shows.
func (m *Model) openThemePicker() {
	m.themeNames = theme.Names()
	m.themeOriginal = styles.Active()
	m.themeCursor = 0
	if cur := theme.NameOf(m.themeOriginal); cur != "" {
		for i, name := range m.themeNames {
			if name == cur {
				m.themeCursor = i
				break
			}
		}
	}
	m.mode = ModeThemePicker
	m.applyThemeAt(m.themeCursor)
}

// applyThemeAt re-themes the whole UI to the built-in palette at index i (a live
// preview as the cursor moves). Out-of-range indices are ignored.
func (m *Model) applyThemeAt(i int) {
	if i < 0 || i >= len(m.themeNames) {
		return
	}
	if pal, ok := theme.ByName(m.themeNames[i]); ok {
		styles.Apply(pal)
	}
}

// cancelThemePicker closes the picker and restores the palette that was active
// when it opened, discarding the preview.
func (m *Model) cancelThemePicker() {
	styles.Apply(m.themeOriginal)
	m.mode = ModeNormal
}

// handleThemePicker drives the theme selector: the cursor previews the
// highlighted theme live, Enter keeps it, and q/esc cancels (esc is also caught
// by the global handler, which restores the original palette).
func (m Model) handleThemePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.themeCursor > 0 {
			m.themeCursor--
			m.applyThemeAt(m.themeCursor)
		}
	case "down", "j":
		if m.themeCursor < len(m.themeNames)-1 {
			m.themeCursor++
			m.applyThemeAt(m.themeCursor)
		}
	case "enter":
		if m.themeCursor < len(m.themeNames) {
			m.copyNotif = "тема: " + m.themeNames[m.themeCursor]
			m.mode = ModeNormal
			return m, clearCopyNotifCmd()
		}
	case "q":
		m.cancelThemePicker()
	}
	return m, nil
}
