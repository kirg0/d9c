// Package execform renders the modal one-off-run wizard: start a disposable
// interactive container from an image (`docker run --rm -it` analogue) with
// optional volume mounts and a command (empty = shell).
package execform

import (
	"strings"

	"d9c/internal/i18n"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const fieldCount = 3 // image, volumes, command

// Model is the three-field (Image, Volumes, Command) exec wizard.
type Model struct {
	image   textinput.Model
	volumes textinput.Model
	command textinput.Model
	focus   int // 0 = image, 1 = volumes, 2 = command
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
	return Model{
		image:   mk("alpine:latest"),
		volumes: mk("/host/path:/ctr/path, myvol:/data (optional)"),
		command: mk(i18n.T("команда (пусто = shell)", "command (empty = shell)")),
	}
}

// Open resets the form for a fresh run, optionally pre-filling the image.
func (m *Model) Open(image string) {
	m.errMsg = ""
	m.image.SetValue(image)
	m.volumes.SetValue("")
	m.command.SetValue("")
	m.focusField(0)
}

// SetError shows a validation/operation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

func (m *Model) fields() []*textinput.Model {
	return []*textinput.Model{&m.image, &m.volumes, &m.command}
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

// Volumes returns the raw volumes field (comma-separated bind specs).
func (m Model) Volumes() string { return m.volumes.Value() }

// Command returns the trimmed command field (empty = shell).
func (m Model) Command() string { return strings.TrimSpace(m.command.Value()) }

// Update forwards key events to the focused field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
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

	labels := []string{"Image", "Volumes", "Command"}
	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Run one-off container (--rm -it) "))
	b.WriteString("\n\n")
	for i, ti := range []textinput.Model{m.image, m.volumes, m.command} {
		b.WriteString(field(labels[i], ti, m.focus == i))
		b.WriteString("\n\n")
	}
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("tab switch · enter run · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
