// Package buildform renders the modal "build image" form shown in the images
// view when the build command is invoked without a context directory.
package buildform

import (
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const fieldCount = 2 // dir, tag

// Model is the two-field (Context dir, Tag) build-image form. On submit the
// caller starts the build in the streaming progress console, so this form keeps
// no busy/spinner state of its own.
type Model struct {
	dir    textinput.Model
	tag    textinput.Model
	focus  int // 0 = dir, 1 = tag
	errMsg string
}

// New builds an empty form.
func New() Model {
	d := textinput.New()
	d.Placeholder = "./path/to/context"
	d.CharLimit = 512
	d.Width = 44

	t := textinput.New()
	t.Placeholder = "myapp:latest (optional)"
	t.CharLimit = 256
	t.Width = 44

	return Model{dir: d, tag: t}
}

// Open prepares the form to build an image, pre-filling the context directory
// and tag (either may be empty when invoked without arguments).
func (m *Model) Open(dir, tag string) {
	m.errMsg = ""
	m.dir.SetValue(dir)
	m.tag.SetValue(tag)
	m.focusField(0)
}

// SetError shows a validation/operation message inside the form (keeps it open).
func (m *Model) SetError(s string) { m.errMsg = s }

func (m *Model) focusField(i int) {
	m.focus = (i%fieldCount + fieldCount) % fieldCount
	m.dir.Blur()
	m.tag.Blur()
	switch m.focus {
	case 0:
		m.dir.Focus()
		m.dir.CursorEnd()
	default:
		m.tag.Focus()
		m.tag.CursorEnd()
	}
}

// Next moves focus to the following field.
func (m *Model) Next() { m.focusField(m.focus + 1) }

// Prev moves focus to the previous field.
func (m *Model) Prev() { m.focusField(m.focus - 1) }

// Dir returns the trimmed context-directory field.
func (m Model) Dir() string { return strings.TrimSpace(m.dir.Value()) }

// Tag returns the trimmed tag field.
func (m Model) Tag() string { return strings.TrimSpace(m.tag.Value()) }

// Update forwards key events to the focused field.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focus {
	case 0:
		m.dir, cmd = m.dir.Update(msg)
	default:
		m.tag, cmd = m.tag.Update(msg)
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

	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Build image "))
	b.WriteString("\n\n")
	b.WriteString(field("Context dir", m.dir, m.focus == 0))
	b.WriteString("\n\n")
	b.WriteString(field("Tag", m.tag, m.focus == 1))
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
	}
	b.WriteString(styles.FormHint.Render("tab switch · enter build · esc cancel"))

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
