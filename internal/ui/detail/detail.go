package detail

import (
	"fmt"
	"strings"

	"d9c/internal/docker"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	keyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7DCFFF"))
	strStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A"))
	boolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#BB9AF7"))
	nullStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#565F89"))
	numStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0AF68"))

	matchLineStyle   = lipgloss.NewStyle().Background(lipgloss.Color("#2D3250"))
	currentLineStyle = lipgloss.NewStyle().Background(lipgloss.Color("#BB9AF7")).Foreground(lipgloss.Color("#1A1B26")).Bold(true)
)

type Model struct {
	viewport viewport.Model
	resource *docker.InspectResult
	height   int
	width    int

	searching   bool
	searchInput textinput.Model
	searchQuery string
	matches     []int // line indices in RawYAML
	matchIdx    int
}

func New() Model {
	ti := textinput.New()
	ti.Placeholder = "search…"
	ti.CharLimit = 128
	return Model{
		viewport:    viewport.New(0, 0),
		searchInput: ti,
	}
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport.Width = width
	m.viewport.Height = m.vpHeight()
	if m.resource != nil {
		m.viewport.SetContent(m.renderContent())
	}
}

func (m *Model) SetContent(c *docker.InspectResult) {
	m.resource = c
	m.searching = false
	m.searchQuery = ""
	m.searchInput.Reset()
	m.matches = nil
	m.matchIdx = 0
	m.viewport.SetContent(m.renderContent())
	m.viewport.GotoTop()
}

func (m Model) ContainerName() string {
	if m.resource == nil {
		return "…"
	}
	return m.resource.Name
}

func (m Model) IsSearching() bool { return m.searching }
func (m Model) HasSearch() bool   { return m.searchQuery != "" }

// vpHeight returns viewport height accounting for scrollbar and optional search bar.
func (m Model) vpHeight() int {
	h := m.height - 1 // scrollbar
	if m.searching {
		h-- // search bar
	}
	if h < 1 {
		return 1
	}
	return h
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()

	if m.searching {
		switch key {
		case "enter":
			m.searching = false
			m.viewport.Height = m.vpHeight()
			return m, nil
		case "esc":
			m.searching = false
			m.searchQuery = ""
			m.searchInput.Reset()
			m.matches = nil
			m.matchIdx = 0
			m.viewport.Height = m.vpHeight()
			m.viewport.SetContent(m.renderContent())
			return m, nil
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.searchQuery = m.searchInput.Value()
			m.recomputeMatches()
			return m, cmd
		}
	}

	// Search bar closed
	switch key {
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.viewport.Height = m.vpHeight()
		return m, nil
	case "n":
		m.stepMatch(+1)
		return m, nil
	case "N":
		m.stepMatch(-1)
		return m, nil
	case "esc":
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.searchInput.Reset()
			m.matches = nil
			m.matchIdx = 0
			m.viewport.SetContent(m.renderContent())
			return m, nil
		}
		// No active search: fall through so the top-level can exit detail.
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *Model) recomputeMatches() {
	if m.resource == nil || m.searchQuery == "" {
		m.matches = nil
		m.matchIdx = 0
		m.viewport.SetContent(m.renderContent())
		return
	}
	q := strings.ToLower(m.searchQuery)
	lines := strings.Split(m.resource.RawYAML, "\n")
	m.matches = nil
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), q) {
			m.matches = append(m.matches, i)
		}
	}
	m.matchIdx = 0
	m.viewport.SetContent(m.renderContent())
	if len(m.matches) > 0 {
		m.viewport.SetYOffset(m.matches[0])
	}
}

func (m *Model) stepMatch(dir int) {
	if len(m.matches) == 0 {
		return
	}
	m.matchIdx = (m.matchIdx + dir + len(m.matches)) % len(m.matches)
	m.viewport.SetContent(m.renderContent())
	m.viewport.SetYOffset(m.matches[m.matchIdx])
}

func (m Model) View() string {
	if m.resource == nil {
		return lipgloss.NewStyle().
			Width(m.viewport.Width).
			Padding(0, 1).
			Render("Loading…")
	}

	pctStr := fmt.Sprintf(" %3.0f%% ", m.viewport.ScrollPercent()*100)
	if m.viewport.ScrollPercent() >= 1.0 {
		pctStr = " END "
	}
	dashLen := m.viewport.Width - lipgloss.Width(pctStr)
	if dashLen < 0 {
		dashLen = 0
	}
	scrollBar := styles.StatusBar.Render(strings.Repeat("─", dashLen)) +
		lipgloss.NewStyle().
			Background(lipgloss.Color("#1A1B26")).
			Foreground(lipgloss.Color("#565F89")).
			Render(pctStr)

	parts := []string{m.viewport.View()}

	if m.searching {
		matchInfo := ""
		if m.searchQuery != "" {
			if len(m.matches) > 0 {
				matchInfo = fmt.Sprintf("  %d/%d  ", m.matchIdx+1, len(m.matches))
			} else {
				matchInfo = "  no match  "
			}
		}
		infoRendered := lipgloss.NewStyle().
			Background(lipgloss.Color("#24283B")).
			Foreground(lipgloss.Color("#565F89")).
			Render(matchInfo)
		prefix := styles.BottomBarPrefix.Render("/")
		inputW := m.viewport.Width - lipgloss.Width(prefix) - lipgloss.Width(infoRendered)
		if inputW < 0 {
			inputW = 0
		}
		inputView := styles.BottomBar.Width(inputW).Render(m.searchInput.View())
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Left, prefix, inputView, infoRendered))
	}

	parts = append(parts, scrollBar)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderContent builds the YAML display with search highlights applied.
func (m Model) renderContent() string {
	if m.resource == nil {
		return ""
	}
	lines := strings.Split(m.resource.RawYAML, "\n")

	matchSet := make(map[int]bool, len(m.matches))
	for _, idx := range m.matches {
		matchSet[idx] = true
	}
	currentLine := -1
	if len(m.matches) > 0 {
		currentLine = m.matches[m.matchIdx]
	}

	var sb strings.Builder
	for i, line := range lines {
		if matchSet[i] {
			if i == currentLine {
				sb.WriteString(currentLineStyle.Width(m.width).Render(line))
			} else {
				sb.WriteString(matchLineStyle.Width(m.width).Render(line))
			}
		} else {
			sb.WriteString(colorizeLine(line))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func colorizeLine(line string) string {
	colonIdx := strings.Index(line, ": ")
	dashIdx := strings.Index(strings.TrimLeft(line, " "), "- ")
	switch {
	case colonIdx > 0:
		key := line[:colonIdx+1]
		val := line[colonIdx+1:]
		return keyStyle.Render(key) + colorizeValue(val)
	case dashIdx == 0:
		indent := len(line) - len(strings.TrimLeft(line, " "))
		rest := strings.TrimLeft(line, " ")
		return strings.Repeat(" ", indent) + strStyle.Render("- ") + colorizeValue(rest[2:])
	default:
		return line
	}
}

func colorizeValue(val string) string {
	v := strings.TrimSpace(val)
	switch v {
	case "true", "false":
		return " " + boolStyle.Render(v)
	case "null", "~", "":
		return " " + nullStyle.Render(v)
	}
	isNum := true
	for _, c := range v {
		if (c < '0' || c > '9') && c != '.' && c != '-' {
			isNum = false
			break
		}
	}
	if isNum && v != "" {
		return " " + numStyle.Render(v)
	}
	return strStyle.Render(" " + v)
}
