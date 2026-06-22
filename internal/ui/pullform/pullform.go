// Package pullform renders the modal "pull image" form shown in the images
// view when the pull command is invoked without a selected image.
package pullform

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the single-field (Image) pull-image form.
type Model struct {
	image  textinput.Model
	errMsg string
}

// New builds an empty form.
func New() Model {
	i := textinput.New()
	i.Placeholder = "nginx:latest"
	i.CharLimit = 256
	i.Width = 44
	return Model{image: i}
}

// Open prepares the form to pull a new image, clearing any previous input.
func (m *Model) Open() {
	m.errMsg = ""
	m.image.SetValue("")
	m.image.Focus()
	m.image.CursorEnd()
}

// SetError shows a validation/operation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

// Image returns the trimmed image reference.
func (m Model) Image() string { return strings.TrimSpace(m.image.Value()) }

// Update forwards key events to the image field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.image, cmd = m.image.Update(msg)
	return m, cmd
}

// View renders the form centered within the given area.
func (m Model) View(width, height int) string {
	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Pull image "))
	b.WriteString("\n\n")
	b.WriteString("▸ " + styles.FormLabelActive.Render("Image") + "\n  " + m.image.View())
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("enter pull · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
