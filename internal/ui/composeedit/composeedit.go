// Package composeedit provides the embedded editor for compose files, with
// YAML syntax validation before saving.
package composeedit

import (
	"fmt"
	"strings"

	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// Model is the compose-file editor.
type Model struct {
	area      textarea.Model
	project   string
	path      string
	createDir string // non-empty => creating a new project in this directory
	errMsg    string
	saving    bool
}

// New builds an empty editor.
func New() Model {
	ta := textarea.New()
	ta.CharLimit = 0 // unlimited
	ta.ShowLineNumbers = true
	ta.Prompt = ""
	return Model{area: ta}
}

// SetContent loads a project's compose file into the editor and focuses it.
func (m *Model) SetContent(project, path, content string) tea.Cmd {
	m.project = project
	m.path = path
	m.createDir = ""
	m.errMsg = ""
	m.saving = false
	m.area.SetValue(content)
	return m.area.Focus()
}

// composeTemplate is the starter content offered when creating a new project.
const composeTemplate = `services:
  app:
    image: nginx:latest
    ports:
      - "8080:80"
    restart: unless-stopped
`

// SetCreate opens the editor to author a new docker-compose.yaml in dir,
// pre-filled with a starter template.
func (m *Model) SetCreate(dir string) tea.Cmd {
	m.project = ""
	m.createDir = dir
	m.path = dir + "/docker-compose.yaml"
	m.errMsg = ""
	m.saving = false
	m.area.SetValue(composeTemplate)
	return m.area.Focus()
}

// IsCreate reports whether the editor is authoring a new project.
func (m Model) IsCreate() bool { return m.createDir != "" }

// CreateDir returns the target directory for a new project.
func (m Model) CreateDir() string { return m.createDir }

// SetSize resizes the editing area, leaving room for title/footer lines.
func (m *Model) SetSize(width, height int) {
	m.area.SetWidth(width)
	h := height - 3 // title + status line
	if h < 3 {
		h = 3
	}
	m.area.SetHeight(h)
}

// SetError shows a message under the editor (e.g. validation failure).
func (m *Model) SetError(s string) { m.errMsg = s }

// SetSaving toggles the "saving…" indicator.
func (m *Model) SetSaving(v bool) { m.saving = v }

// Value returns the current editor text.
func (m Model) Value() string { return m.area.Value() }

// Project returns the project being edited.
func (m Model) Project() string { return m.project }

// Path returns the compose file path being edited.
func (m Model) Path() string { return m.path }

// Update forwards events to the textarea.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	return m, cmd
}

// View renders the editor with a title, the text area, and a status line.
func (m Model) View(width int) string {
	label := "edit: "
	if m.IsCreate() {
		label = "create: "
	}
	title := styles.FormTitle.Render(" " + label + m.path + " ")

	var status string
	switch {
	case m.saving:
		status = styles.FormHint.Render("saving…")
	case m.errMsg != "":
		status = styles.FormError.Render("✖ " + m.errMsg)
	default:
		status = styles.FormHint.Render("ctrl+s save · esc cancel")
	}

	return lipgloss.JoinVertical(lipgloss.Left, title, m.area.View(), status)
}

// ValidateYAML reports whether content is syntactically valid YAML.
func ValidateYAML(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("file is empty")
	}
	var v any
	if err := yaml.Unmarshal([]byte(content), &v); err != nil {
		return err
	}
	return nil
}
