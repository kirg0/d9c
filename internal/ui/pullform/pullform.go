// Package pullform renders the modal "pull image" form shown in the images
// view when the pull command is invoked without a selected image.
package pullform

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the single-field (Image) pull-image form.
type Model struct {
	image   textinput.Model
	spinner spinner.Model
	busy    bool // a pull is in flight; show the spinner and ignore input
	errMsg  string
}

// New builds an empty form.
func New() Model {
	i := textinput.New()
	i.Placeholder = "nginx:latest"
	i.CharLimit = 256
	i.Width = 44
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = styles.FormBusy
	return Model{image: i, spinner: sp}
}

// Open prepares the form to pull a new image, clearing any previous input.
func (m *Model) Open() {
	m.errMsg = ""
	m.busy = false
	m.image.SetValue("")
	m.image.Focus()
	m.image.CursorEnd()
}

// OpenPulling opens the form already pulling the given reference: it pre-fills
// the image and enters the busy state without an input step, used when the
// reference is known up front (pull <image> or pulling the selected image).
// Returns the spinner-start command.
func (m *Model) OpenPulling(ref string) tea.Cmd {
	m.image.SetValue(ref)
	return m.Pulling()
}

// Pulling marks the form busy while the pull runs: it clears any error, blurs
// the input and returns the command that starts the spinner animation. Render
// continues to show the image being pulled.
func (m *Model) Pulling() tea.Cmd {
	m.errMsg = ""
	m.busy = true
	m.image.Blur()
	return m.spinner.Tick
}

// Busy reports whether a pull is currently in flight.
func (m Model) Busy() bool { return m.busy }

// SetError shows a validation/operation message inside the form (keeps it open)
// and clears the busy state so the user can correct the input and retry.
func (m *Model) SetError(s string) {
	m.errMsg = s
	m.busy = false
	m.image.Focus()
}

// Image returns the trimmed image reference.
func (m Model) Image() string { return strings.TrimSpace(m.image.Value()) }

// Tick advances the spinner; used while a pull is in flight.
func (m Model) Tick(msg spinner.TickMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// Update forwards key events to the image field. While busy, input is ignored.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
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
	switch {
	case m.busy:
		b.WriteString(m.spinner.View() + " " + styles.FormBusy.Render("pulling "+m.Image()+"…") + "\n")
		b.WriteString(styles.FormHint.Render("это может занять время · esc cancel"))
	case m.errMsg != "":
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
		b.WriteString(styles.FormHint.Render("enter pull · esc cancel"))
	default:
		b.WriteString(styles.FormHint.Render("enter pull · esc cancel"))
	}

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
