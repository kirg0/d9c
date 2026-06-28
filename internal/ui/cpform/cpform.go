// Package cpform renders the modal "upload to container" wizard shown in the
// containers view when `:cp` is invoked without arguments. It pairs a navigable
// local-filesystem picker (choose the source file/dir on this machine) with a
// text field for the destination directory inside the container.
package cpform

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"d9c/internal/i18n"
	"d9c/internal/ui/styles"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// focus targets within the form.
const (
	focusBrowser = iota // local filesystem picker
	focusDest           // container destination directory field
)

// Entry is one local filesystem entry shown in the picker.
type Entry struct {
	Name  string
	IsDir bool
}

// Model is the upload wizard: a local file picker plus a container-destination
// field. The container is fixed at open time (the cursor container).
type Model struct {
	containerID string
	name        string // container name (panel title)

	dir     string // current local directory (absolute, cleaned)
	entries []Entry
	cursor  int
	offset  int // index of the first visible row (vertical scroll)

	dest  textinput.Model
	focus int

	spinner spinner.Model
	busy    bool // an upload is in flight; show the spinner and ignore input
	errMsg  string

	width  int
	height int
}

// New builds an empty form.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "/tmp (directory inside container)"
	ti.CharLimit = 512
	ti.Width = 48
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = styles.FormBusy
	return Model{dir: "/", dest: ti, spinner: sp}
}

// Open resets the form for a fresh upload into the given container. The listing
// is supplied separately via Show (the caller loads it through a tea.Cmd).
func (m *Model) Open(containerID, name string) {
	m.containerID = containerID
	m.name = name
	m.errMsg = ""
	m.busy = false
	m.entries = nil
	m.cursor = 0
	m.offset = 0
	m.dest.SetValue("/")
	m.focusField(focusBrowser)
}

// Show loads a fresh directory listing (after open or navigation), resetting the
// cursor and clearing any error.
func (m *Model) Show(dir string, entries []Entry) {
	m.dir = dir
	m.entries = entries
	m.cursor = 0
	m.offset = 0
	m.errMsg = ""
	m.clampOffset()
}

// SetError shows a validation/operation message inside the form (keeps it open)
// and clears the busy state so the user can correct the input and retry.
func (m *Model) SetError(s string) {
	m.errMsg = s
	m.busy = false
	m.focusField(m.focus)
}

// SetSize configures the panel dimensions (used to size the picker viewport).
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.clampOffset()
}

// Running marks the form busy while the upload runs: it clears any error, blurs
// the destination field and returns the command that starts the spinner.
func (m *Model) Running() tea.Cmd {
	m.errMsg = ""
	m.busy = true
	m.dest.Blur()
	return m.spinner.Tick
}

// Busy reports whether an upload is currently in flight.
func (m Model) Busy() bool { return m.busy }

// Tick advances the spinner; used while an upload is in flight.
func (m Model) Tick(msg spinner.TickMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// ContainerID returns the target container.
func (m Model) ContainerID() string { return m.containerID }

// Name returns the container name shown in the title.
func (m Model) Name() string { return m.name }

// CurrentDir returns the local directory currently listed.
func (m Model) CurrentDir() string { return m.dir }

// OnBrowser reports whether focus is on the local file picker (as opposed to the
// destination field). The handler uses this to route navigation keys.
func (m Model) OnBrowser() bool { return m.focus == focusBrowser }

// Selected returns the entry under the cursor, or the zero value when the
// listing is empty.
func (m Model) Selected() Entry {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return Entry{}
	}
	return m.entries[m.cursor]
}

// SourcePath returns the absolute local path of the highlighted entry, or "" if
// the listing is empty.
func (m Model) SourcePath() string {
	e := m.Selected()
	if e.Name == "" {
		return ""
	}
	return filepath.Join(m.dir, e.Name)
}

// Dest returns the trimmed container destination directory.
func (m Model) Dest() string { return strings.TrimSpace(m.dest.Value()) }

// ToggleFocus switches between the picker and the destination field.
func (m *Model) ToggleFocus() { m.focusField(1 - m.focus) }

func (m *Model) focusField(i int) {
	m.focus = i
	if m.focus == focusDest {
		m.dest.Focus()
		m.dest.CursorEnd()
	} else {
		m.dest.Blur()
	}
}

// Update handles cursor movement in the picker or text entry in the destination
// field, depending on focus. While busy, input is ignored. Descend/ascend and
// confirm are driven by the root handler (they need tea.Cmds / the backend).
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	if m.focus == focusDest {
		var cmd tea.Cmd
		m.dest, cmd = m.dest.Update(msg)
		return m, cmd
	}
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

// listHeight is the number of picker rows that fit; conservative so the
// destination field, title and hints always stay visible.
func (m Model) listHeight() int {
	h := m.height - 12 // title, dir header, selected line, dest label+field, hints
	if h < 3 {
		return 3
	}
	if h > 14 {
		return 14
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

// View renders the form centered within the given area.
func (m Model) View(width, height int) string {
	var b strings.Builder
	b.WriteString(styles.FormTitle.Render(" Upload to container: " + m.name + " "))
	b.WriteString("\n\n")

	// ── local file picker ──────────────────────────────────────────────────
	b.WriteString(m.pickerLabel() + "\n")
	b.WriteString(m.dirHeader() + "\n")
	b.WriteString(m.pickerBody())
	b.WriteString("\n\n")

	// Chosen source, shown regardless of focus so the upload target is clear.
	b.WriteString("  " + styles.FormLabel.Render("Selected: ") + styles.CopyMenuValue.Render(m.sourceLabel()))
	b.WriteString("\n\n")

	// ── destination field ──────────────────────────────────────────────────
	b.WriteString(m.destLabel() + "\n  " + m.dest.View())
	b.WriteString("\n\n")

	switch {
	case m.busy:
		b.WriteString(m.spinner.View() + " " + styles.FormBusy.Render("uploading…") + "\n")
		b.WriteString(styles.FormHint.Render("esc cancel"))
	case m.errMsg != "":
		b.WriteString(styles.FormError.Render("✖ "+m.errMsg) + "\n")
		b.WriteString(m.hint())
	default:
		b.WriteString(m.hint())
	}

	panel := styles.OverlayPanel.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m Model) pickerLabel() string {
	if m.focus == focusBrowser {
		return "▸ " + styles.FormLabelActive.Render("Local source")
	}
	return "  " + styles.FormLabel.Render("Local source")
}

func (m Model) destLabel() string {
	if m.focus == focusDest {
		return "▸ " + styles.FormLabelActive.Render("Container directory")
	}
	return "  " + styles.FormLabel.Render("Container directory")
}

func (m Model) dirHeader() string {
	return "  " + styles.CopyMenuHint.Render(m.dir)
}

// sourceLabel names the highlighted local entry (the upload source), or "(none)"
// when the listing is empty.
func (m Model) sourceLabel() string {
	e := m.Selected()
	if e.Name == "" {
		return "(none)"
	}
	if e.IsDir {
		return e.Name + "/"
	}
	return e.Name
}

// pickerBody renders the windowed local listing (or an empty marker).
func (m Model) pickerBody() string {
	if len(m.entries) == 0 {
		return "  " + styles.CopyMenuHint.Render(i18n.T("(пусто)", "(empty)"))
	}
	vis := m.listHeight()
	end := min(m.offset+vis, len(m.entries))
	var rows []string
	for i := m.offset; i < end; i++ {
		rows = append(rows, m.renderRow(i))
	}
	return strings.Join(rows, "\n")
}

// renderRow renders one entry, marking directories and the chosen source. The
// cursor row stays marked even when focus moves to the destination field (a
// dimmer ● instead of the active ▶), so the picked source is always visible.
func (m Model) renderRow(i int) string {
	e := m.entries[i]
	label := e.Name
	if e.IsDir {
		label += "/"
	}
	switch {
	case i == m.cursor && m.focus == focusBrowser:
		return " ▶ " + styles.TableSelected.Render(" "+label+" ")
	case i == m.cursor:
		return " ● " + styles.CopyMenuSelected.Render(" "+label+" ")
	case e.IsDir:
		return "   " + styles.StatusRunning.Render(label)
	default:
		return "   " + styles.TableCell.Render(label)
	}
}

func (m Model) hint() string {
	if m.focus == focusBrowser {
		return styles.FormHint.Render("↑↓ select · enter/l open dir · ⌫ up · tab → dir · esc cancel")
	}
	return styles.FormHint.Render("enter upload · tab ← picker · esc cancel")
}

// ── local filesystem helpers (pure-ish, this host) ────────────────────────────

// ReadLocalDir lists dir on the local machine, returning the cleaned absolute
// path and its entries (directories first, then files, each alphabetical). It is
// called from a tea.Cmd so the blocking disk read stays out of Update.
func ReadLocalDir(dir string) (string, []Entry, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir, nil, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return abs, nil, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		entries = append(entries, Entry{Name: de.Name(), IsDir: de.IsDir()})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // directories first
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return abs, entries, nil
}

// Parent returns the parent directory of dir (a root is its own parent).
func Parent(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return filepath.Dir(abs)
}
