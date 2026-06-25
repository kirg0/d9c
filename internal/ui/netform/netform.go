// Package netform renders the modal "create network" form shown in the
// networks view.
package netform

import (
	"strings"

	"d9c/internal/ui/driverfield"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	fieldCount        = 4 // name, driver, subnet, gateway
	defaultDriver     = "bridge"
	subnetPlaceholder = "172.30.0.0/16 (optional)"
)

// netDrivers are the built-in network drivers offered in the driver selector.
var netDrivers = []string{"bridge", "host", "overlay", "macvlan", "ipvlan", "none"}

// Model is the four-field (Name, Driver, Subnet, Gateway) create-network form.
type Model struct {
	name    textinput.Model
	driver  driverfield.Model
	subnet  textinput.Model
	gateway textinput.Model
	focus   int // 0 = name, 1 = driver, 2 = subnet, 3 = gateway
	errMsg  string
}

// New builds an empty form.
func New() Model {
	n := textinput.New()
	n.Placeholder = "my-net"
	n.CharLimit = 64
	n.Width = 44

	s := textinput.New()
	s.Placeholder = subnetPlaceholder
	s.CharLimit = 64
	s.Width = 44

	g := textinput.New()
	g.Placeholder = "172.30.0.1 (optional)"
	g.CharLimit = 64
	g.Width = 44

	return Model{name: n, driver: driverfield.New(netDrivers), subnet: s, gateway: g}
}

// Open prepares the form to create a new network, pre-filled with the default
// driver.
func (m *Model) Open() {
	m.errMsg = ""
	m.name.SetValue("")
	m.driver.Set(defaultDriver)
	m.subnet.SetValue("")
	m.gateway.SetValue("")
	m.focusField(0)
}

// SetError shows a validation/operation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

func (m *Model) focusField(i int) {
	m.focus = (i%fieldCount + fieldCount) % fieldCount
	m.name.Blur()
	m.driver.Blur()
	m.subnet.Blur()
	m.gateway.Blur()
	switch m.focus {
	case 0:
		m.name.Focus()
		m.name.CursorEnd()
	case 1:
		m.driver.Focus()
	case 2:
		m.subnet.Focus()
		m.subnet.CursorEnd()
	default:
		m.gateway.Focus()
		m.gateway.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// Name returns the trimmed name field.
func (m Model) Name() string { return strings.TrimSpace(m.name.Value()) }

// Driver returns the selected driver.
func (m Model) Driver() string { return m.driver.Value() }

// Subnet returns the trimmed subnet field.
func (m Model) Subnet() string { return strings.TrimSpace(m.subnet.Value()) }

// Gateway returns the trimmed gateway field.
func (m Model) Gateway() string { return strings.TrimSpace(m.gateway.Value()) }

// Update forwards key events to the focused field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focus {
	case 0:
		m.name, cmd = m.name.Update(msg)
	case 1:
		m.driver, cmd = m.driver.Update(msg)
	case 2:
		m.subnet, cmd = m.subnet.Update(msg)
	default:
		m.gateway, cmd = m.gateway.Update(msg)
	}
	return m, cmd
}

// View renders the form centered within the given area.
func (m Model) View(width, height int) string {
	field := func(label string, ti textinput.Model, active bool) string {
		labelStyle := styles.FormLabel
		marker := "  "
		if active {
			labelStyle = styles.FormLabelActive
			marker = "▸ "
		}
		return marker + labelStyle.Render(label) + "\n  " + ti.View()
	}

	choice := func(label string, df driverfield.Model, active bool) string {
		labelStyle := styles.FormLabel
		marker := "  "
		if active {
			labelStyle = styles.FormLabelActive
			marker = "▸ "
		}
		return marker + labelStyle.Render(label) + "\n  " + df.View(active)
	}

	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Create network "))
	b.WriteString("\n\n")
	b.WriteString(field("Name", m.name, m.focus == 0))
	b.WriteString("\n\n")
	b.WriteString(choice("Driver", m.driver, m.focus == 1))
	b.WriteString("\n\n")
	b.WriteString(field("Subnet", m.subnet, m.focus == 2))
	b.WriteString("\n\n")
	b.WriteString(field("Gateway", m.gateway, m.focus == 3))
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("tab switch · ←/→ driver · enter create · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
