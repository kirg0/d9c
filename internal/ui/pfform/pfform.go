// Package pfform renders the modal "port-forward" form opened from the
// containers and compose views: pick the target container (compose projects
// offer each of their containers), the container port and the local port
// (empty = a free one).
package pfform

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kirg0/d9c/internal/i18n"
	"github.com/kirg0/d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Target is one container the form can forward to.
type Target struct {
	ID   string
	Name string
	// Ports are the container's known TCP ports; the first pre-fills the form.
	Ports []int
}

// Field indices.
const (
	fieldTarget = iota
	fieldPort
	fieldLocal
	fieldCount
)

// Model is the port-forward form.
type Model struct {
	targets []Target
	idx     int
	port    textinput.Model
	local   textinput.Model
	focus   int
	errMsg  string
	busy    bool
}

// New builds an empty form.
func New() Model {
	p := textinput.New()
	p.Placeholder = "80"
	p.CharLimit = 5
	p.Width = 12
	l := textinput.New()
	l.Placeholder = i18n.T("пусто = свободный", "empty = free port")
	l.CharLimit = 5
	l.Width = 24
	return Model{port: p, local: l}
}

// Open prepares the form for targets (at least one), selecting the first and
// pre-filling its first known port.
func (m *Model) Open(targets []Target) {
	m.targets = targets
	m.idx = 0
	m.errMsg = ""
	m.busy = false
	m.local.SetValue("")
	// Placeholders are localized at open time, after the language is known.
	m.local.Placeholder = i18n.T("пусто = свободный", "empty = free port")
	m.prefillPort()
	first := fieldPort
	if len(targets) > 1 {
		first = fieldTarget
	}
	m.focusField(first)
}

// prefillPort puts the selected target's first known port into the port field.
func (m *Model) prefillPort() {
	m.port.SetValue("")
	if t, ok := m.Target(); ok && len(t.Ports) > 0 {
		m.port.SetValue(strconv.Itoa(t.Ports[0]))
	}
}

// Target returns the selected target.
func (m Model) Target() (Target, bool) {
	if m.idx < 0 || m.idx >= len(m.targets) {
		return Target{}, false
	}
	return m.targets[m.idx], true
}

// SetError shows a message inside the form (keeps it open) and clears busy.
func (m *Model) SetError(s string) {
	m.errMsg = s
	m.busy = false
}

// SetBusy marks the form as waiting for the tunnel to open.
func (m *Model) SetBusy(b bool) { m.busy = b }

// Busy reports whether the form waits for the tunnel to open.
func (m Model) Busy() bool { return m.busy }

// focusable reports whether field i takes focus (the target selector only when
// there is a choice).
func (m Model) focusable(i int) bool {
	return i != fieldTarget || len(m.targets) > 1
}

func (m *Model) focusField(i int) {
	m.focus = (i%fieldCount + fieldCount) % fieldCount
	m.port.Blur()
	m.local.Blur()
	switch m.focus {
	case fieldPort:
		m.port.Focus()
		m.port.CursorEnd()
	case fieldLocal:
		m.local.Focus()
		m.local.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() {
	i := m.focus + 1
	for !m.focusable((i%fieldCount + fieldCount) % fieldCount) {
		i++
	}
	m.focusField(i)
}

// Prev moves focus to the previous field.
func (m *Model) Prev() {
	i := m.focus - 1
	for !m.focusable((i%fieldCount + fieldCount) % fieldCount) {
		i--
	}
	m.focusField(i)
}

// Values validates the port fields: the container port is required, the local
// port may be empty (0 = pick a free one).
func (m Model) Values() (containerPort, localPort int, err error) {
	containerPort, err = ParsePort(m.port.Value(), false)
	if err != nil {
		return 0, 0, fmt.Errorf(i18n.T("порт контейнера: %w", "container port: %w"), err)
	}
	localPort, err = ParsePort(m.local.Value(), true)
	if err != nil {
		return 0, 0, fmt.Errorf(i18n.T("локальный порт: %w", "local port: %w"), err)
	}
	return containerPort, localPort, nil
}

// ParsePort parses a TCP port 1–65535. With allowEmpty an empty value yields 0.
func ParsePort(s string, allowEmpty bool) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		if allowEmpty {
			return 0, nil
		}
		return 0, errors.New(i18n.T("обязателен", "required"))
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf(i18n.T("%q — не порт (1–65535)", "%q is not a port (1–65535)"), s)
	}
	return p, nil
}

// Update routes keys: ←/→ cycle the target on the selector, everything else
// goes to the focused text field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	if m.focus == fieldTarget {
		if k, ok := msg.(tea.KeyMsg); ok && len(m.targets) > 0 {
			switch k.String() {
			case "left", "h":
				m.idx = (m.idx - 1 + len(m.targets)) % len(m.targets)
				m.prefillPort()
			case "right", "l":
				m.idx = (m.idx + 1) % len(m.targets)
				m.prefillPort()
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	if m.focus == fieldPort {
		m.port, cmd = m.port.Update(msg)
	} else {
		m.local, cmd = m.local.Update(msg)
	}
	return m, cmd
}

// View renders the form centered within the given area.
func (m Model) View(width, height int) string {
	label := func(text string, active bool) string {
		if active {
			return "▸ " + styles.FormLabelActive.Render(text)
		}
		return "  " + styles.FormLabel.Render(text)
	}

	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Port-forward "))
	b.WriteString("\n\n")

	b.WriteString(label(i18n.T("Контейнер", "Container"), m.focus == fieldTarget))
	b.WriteString("\n  ")
	if t, ok := m.Target(); ok {
		name := t.Name
		if len(m.targets) > 1 {
			name = fmt.Sprintf("◂ %s ▸  (%d/%d)", t.Name, m.idx+1, len(m.targets))
		}
		b.WriteString(name)
		if len(t.Ports) > 0 {
			ps := make([]string, len(t.Ports))
			for i, p := range t.Ports {
				ps[i] = strconv.Itoa(p)
			}
			b.WriteString(styles.FormHint.Render("  " + i18n.T("порты: ", "ports: ") + strings.Join(ps, ", ")))
		}
	}
	b.WriteString("\n\n")

	b.WriteString(label(i18n.T("Порт контейнера", "Container port"), m.focus == fieldPort))
	b.WriteString("\n  " + m.port.View() + "\n\n")
	b.WriteString(label(i18n.T("Локальный порт (127.0.0.1)", "Local port (127.0.0.1)"), m.focus == fieldLocal))
	b.WriteString("\n  " + m.local.View() + "\n\n")

	if m.busy {
		b.WriteString(styles.FormHint.Render(i18n.T("открываю туннель…", "opening the tunnel…")) + "\n")
	}
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	hint := i18n.T("tab поле · enter пробросить · esc отмена", "tab switch · enter forward · esc cancel")
	if len(m.targets) > 1 {
		hint = i18n.T("tab поле · ←/→ контейнер · enter пробросить · esc отмена", "tab switch · ←/→ container · enter forward · esc cancel")
	}
	b.WriteString(styles.FormHint.Render(hint))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
