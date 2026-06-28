// Package runform renders the modal "run container" wizard shown in the
// containers view: image plus optional name, ports, env and volumes.
package runform

import (
	"strings"

	"d9c/internal/i18n"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const fieldCount = 5 // image, name, ports, env, volumes

// Model is the five-field (Image, Name, Ports, Env, Volumes) run wizard.
type Model struct {
	image   textinput.Model
	name    textinput.Model
	ports   textinput.Model
	env     textinput.Model
	volumes textinput.Model
	focus   int // 0 = image … 4 = volumes
	spinner spinner.Model
	busy    bool // a create/run is in flight; show the spinner and ignore input
	errMsg  string
}

// New builds an empty form.
func New() Model {
	mk := func(placeholder string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.CharLimit = 512
		ti.Width = 52
		return ti
	}
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = styles.FormBusy
	return Model{
		image:   mk("nginx:latest"),
		name:    mk("my-app (optional)"),
		ports:   mk("8080:80, 9443:443/udp (optional)"),
		env:     mk("KEY=value, OTHER=x (optional)"),
		volumes: mk("/host/path:/ctr/path, myvol:/data (optional)"),
		spinner: sp,
	}
}

// Open resets the form for a fresh run, optionally pre-filling the image.
func (m *Model) Open(image string) {
	m.errMsg = ""
	m.busy = false
	m.image.SetValue(image)
	m.name.SetValue("")
	m.ports.SetValue("")
	m.env.SetValue("")
	m.volumes.SetValue("")
	m.focusField(0)
}

// Running marks the form busy while the create/run runs: it clears any error,
// blurs the focused field and returns the command that starts the spinner
// animation. The image being launched stays visible in the form.
func (m *Model) Running() tea.Cmd {
	m.errMsg = ""
	m.busy = true
	for _, f := range m.fields() {
		f.Blur()
	}
	return m.spinner.Tick
}

// Busy reports whether a create/run is currently in flight.
func (m Model) Busy() bool { return m.busy }

// Tick advances the spinner; used while a create/run is in flight.
func (m Model) Tick(msg spinner.TickMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// SetError shows a validation/operation message inside the form (keeps it open)
// and clears the busy state so the user can correct the input and retry.
func (m *Model) SetError(s string) {
	m.errMsg = s
	m.busy = false
	m.focusField(m.focus)
}

func (m *Model) fields() []*textinput.Model {
	return []*textinput.Model{&m.image, &m.name, &m.ports, &m.env, &m.volumes}
}

func (m *Model) focusField(i int) {
	m.focus = (i%fieldCount + fieldCount) % fieldCount
	for j, f := range m.fields() {
		if j == m.focus {
			f.Focus()
			f.CursorEnd()
		} else {
			f.Blur()
		}
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// Image returns the trimmed image field.
func (m Model) Image() string { return strings.TrimSpace(m.image.Value()) }

// Name returns the trimmed name field.
func (m Model) Name() string { return strings.TrimSpace(m.name.Value()) }

// Ports returns the raw ports field (comma-separated specs).
func (m Model) Ports() string { return m.ports.Value() }

// Env returns the raw env field (comma-separated KEY=VALUE pairs).
func (m Model) Env() string { return m.env.Value() }

// Volumes returns the raw volumes field (comma-separated bind specs).
func (m Model) Volumes() string { return m.volumes.Value() }

// Update forwards key events to the focused field. While busy, input is ignored.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	var cmd tea.Cmd
	f := m.fields()[m.focus]
	*f, cmd = f.Update(msg)
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

	labels := []string{"Image", "Name", "Ports", "Env", "Volumes"}
	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Run container "))
	b.WriteString("\n\n")
	for i, ti := range []textinput.Model{m.image, m.name, m.ports, m.env, m.volumes} {
		b.WriteString(field(labels[i], ti, m.focus == i))
		b.WriteString("\n\n")
	}
	switch {
	case m.busy:
		b.WriteString(m.spinner.View() + " " + styles.FormBusy.Render("running "+m.Image()+"…") + "\n")
		b.WriteString(styles.FormHint.Render(i18n.T("образ скачается, если его нет на хосте · esc cancel", "the image is pulled if it's not on the host · esc cancel")))
	case m.errMsg != "":
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
		b.WriteString(styles.FormHint.Render("tab switch · enter run · esc cancel"))
	default:
		b.WriteString(styles.FormHint.Render("tab switch · enter run · esc cancel"))
	}

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
