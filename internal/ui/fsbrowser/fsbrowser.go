// Package fsbrowser provides a navigable view of a container's filesystem for
// the d9c TUI. It renders one directory at a time (entries supplied by the
// caller via docker.Backend.ListPath) with a cursor and vertical scrolling; the
// root model drives navigation (descend/ascend) and downloads.
package fsbrowser

import (
	"path"
	"strings"

	"d9c/internal/docker"
	"d9c/internal/i18n"
	"d9c/internal/ui/styles"

	tea "github.com/charmbracelet/bubbletea"
)

// Model displays a single container directory listing with a cursor.
type Model struct {
	containerID string
	name        string // container name (panel title)
	path        string // current directory, POSIX, always cleaned
	entries     []docker.FileEntry
	cursor      int
	offset      int // index of the first visible row (vertical scroll)
	errMsg      string
	width       int
	height      int
}

// New creates an empty filesystem browser.
func New() Model { return Model{path: "/"} }

// SetSize configures the panel dimensions.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.clampOffset()
}

// Show loads a fresh directory listing, resetting the cursor and clearing any
// previous error. It is called both on open and after navigation.
func (m *Model) Show(containerID, name, dir string, entries []docker.FileEntry) {
	m.containerID = containerID
	m.name = name
	m.path = cleanPath(dir)
	m.entries = entries
	m.cursor = 0
	m.offset = 0
	m.errMsg = ""
}

// SetError shows msg inside the panel without changing the current listing (a
// failed descent stays at the directory the user can still see).
func (m *Model) SetError(msg string) { m.errMsg = msg }

// ContainerID returns the container being browsed.
func (m Model) ContainerID() string { return m.containerID }

// Name returns the container name shown in the panel title.
func (m Model) Name() string { return m.name }

// CurrentPath returns the directory currently listed.
func (m Model) CurrentPath() string { return m.path }

// Selected returns the entry under the cursor, or the zero value when the
// listing is empty.
func (m Model) Selected() docker.FileEntry {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return docker.FileEntry{}
	}
	return m.entries[m.cursor]
}

// Update handles cursor movement (j/k/↑/↓, g/G, page keys).
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = len(m.entries) - 1
	case "pgup":
		m.cursor -= m.pageSize()
	case "pgdown":
		m.cursor += m.pageSize()
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.entries) {
		m.cursor = max(0, len(m.entries)-1)
	}
	m.clampOffset()
	return m, nil
}

// listHeight is the number of entry rows that fit below the path header.
func (m Model) listHeight() int {
	h := m.height - 1 // path header line
	if h < 1 {
		return 1
	}
	return h
}

func (m Model) pageSize() int {
	if n := m.listHeight(); n > 1 {
		return n - 1
	}
	return 1
}

// clampOffset keeps the cursor within the visible window.
func (m *Model) clampOffset() {
	vis := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+vis {
		m.offset = m.cursor - vis + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// View renders the path header and the windowed listing.
func (m Model) View() string {
	header := styles.TableHeader.Render(" " + m.name + ":" + m.path + " ")
	if m.errMsg != "" {
		header = styles.TableHeader.Render(" "+m.name+":"+m.path+" ") +
			"  " + styles.FooterError.Render(" "+m.errMsg+" ")
	}

	if len(m.entries) == 0 {
		empty := styles.CopyMenuHint.Render(i18n.T("  (пусто)", "  (empty)"))
		return header + "\n" + empty
	}

	vis := m.listHeight()
	end := min(m.offset+vis, len(m.entries))
	var rows []string
	for i := m.offset; i < end; i++ {
		rows = append(rows, m.renderRow(i))
	}
	return header + "\n" + strings.Join(rows, "\n")
}

// renderRow renders one entry, marking directories and highlighting the cursor.
func (m Model) renderRow(i int) string {
	e := m.entries[i]
	label := e.Name
	if e.IsDir {
		label += "/"
	}
	if i == m.cursor {
		return " ▶ " + styles.TableSelected.Render(" "+label+" ")
	}
	if e.IsDir {
		return "   " + styles.StatusRunning.Render(label)
	}
	return "   " + styles.TableCell.Render(label)
}

// ── pure path helpers (POSIX, container-side) ──────────────────────────────────

// cleanPath normalizes a container path to an absolute, slash-cleaned form.
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// PathJoin descends from base into child name (a directory entry).
func PathJoin(base, name string) string {
	return cleanPath(path.Join(cleanPath(base), name))
}

// PathParent returns the parent directory of p ("/" is its own parent).
func PathParent(p string) string {
	c := cleanPath(p)
	if c == "/" {
		return "/"
	}
	return cleanPath(path.Dir(c))
}
