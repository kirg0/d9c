// Package driverfield renders a horizontal driver selector used by the
// create-network and create-volume modal forms. It cycles a list of known
// drivers with ←/→ and, on the trailing "custom…" entry, accepts a typed
// driver name (so plugin drivers outside the preset list are still reachable).
package driverfield

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// CustomLabel is the trailing option that reveals a free-text input for a
// driver name not covered by the presets.
const CustomLabel = "custom…"

// Model is a single driver-selector field: a row of preset options plus a
// "custom…" entry that switches to free-text input.
type Model struct {
	options []string // presets followed by CustomLabel
	idx     int
	custom  textinput.Model
	focused bool
}

// New builds a selector over the given preset drivers (CustomLabel is appended
// automatically). The first preset is selected initially.
func New(presets []string) Model {
	opts := make([]string, 0, len(presets)+1)
	opts = append(opts, presets...)
	opts = append(opts, CustomLabel)

	ci := textinput.New()
	ci.Placeholder = "plugin driver"
	ci.CharLimit = 32
	ci.Width = 30

	return Model{options: opts, custom: ci}
}

// Set selects the preset matching v; a non-empty value with no matching preset
// selects "custom…" pre-filled with v. An empty value selects the first preset.
func (m *Model) Set(v string) {
	m.custom.SetValue("")
	if v == "" {
		m.setIdx(0)
		return
	}
	for i, o := range m.options {
		if o != CustomLabel && o == v {
			m.setIdx(i)
			return
		}
	}
	m.custom.SetValue(v)
	m.setIdx(len(m.options) - 1)
}

func (m *Model) setIdx(i int) {
	n := len(m.options)
	m.idx = (i%n + n) % n
	if m.focused && m.IsCustom() {
		m.custom.Focus()
		m.custom.CursorEnd()
	} else {
		m.custom.Blur()
	}
}

// IsCustom reports whether the "custom…" entry is selected.
func (m Model) IsCustom() bool { return m.idx == len(m.options)-1 }

// Focus marks the field active (focusing the custom input when it is selected).
func (m *Model) Focus() {
	m.focused = true
	m.setIdx(m.idx)
}

// Blur marks the field inactive.
func (m *Model) Blur() {
	m.focused = false
	m.custom.Blur()
}

// Prev moves the selection one option to the left (wrapping).
func (m *Model) Prev() { m.setIdx(m.idx - 1) }

// Next moves the selection one option to the right (wrapping).
func (m *Model) Next() { m.setIdx(m.idx + 1) }

// Value returns the selected driver: the preset label, or the trimmed custom
// text (possibly empty) when "custom…" is selected.
func (m Model) Value() string {
	if m.IsCustom() {
		return strings.TrimSpace(m.custom.Value())
	}
	return m.options[m.idx]
}

// Update interprets ←/→ as option cycling and forwards everything else to the
// custom input while "custom…" is selected.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "left":
			m.Prev()
			return m, nil
		case "right":
			m.Next()
			return m, nil
		}
	}
	if m.IsCustom() {
		var cmd tea.Cmd
		m.custom, cmd = m.custom.Update(msg)
		return m, cmd
	}
	return m, nil
}

// View renders the option row; when "custom…" is selected it also renders the
// free-text input on the next line. active highlights the selected option.
func (m Model) View(active bool) string {
	parts := make([]string, len(m.options))
	for i, o := range m.options {
		switch {
		case i == m.idx && active:
			parts[i] = styles.FormChoiceSelected.Render(o)
		case i == m.idx:
			parts[i] = styles.FormLabelActive.Render(o)
		default:
			parts[i] = styles.FormChoice.Render(o)
		}
	}
	out := strings.Join(parts, " ")
	if m.IsCustom() {
		out += "\n  " + m.custom.View()
	}
	return out
}
