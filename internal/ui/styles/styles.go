package styles

import "github.com/charmbracelet/lipgloss"

// Palette is the set of base colors every style in this package is derived from.
// Swapping the palette (via Apply) re-themes the whole UI; see DefaultPalette for
// the built-in Tokyo Night scheme and the theme package for named palettes and
// config-file loading.
type Palette struct {
	Primary   lipgloss.Color // accents, keys, active elements
	Secondary lipgloss.Color // headers, labels
	Success   lipgloss.Color // running / healthy / OK
	Warning   lipgloss.Color // transitional / paused / retrying
	Danger    lipgloss.Color // errors / stopped / unhealthy
	Muted     lipgloss.Color // dim text, separators
	Bg        lipgloss.Color // primary background
	BgAlt     lipgloss.Color // raised surfaces (selection, bars, modals)
	Fg        lipgloss.Color // primary foreground text
	Border    lipgloss.Color // borders and rules
}

// DefaultPalette returns the built-in Tokyo Night color scheme used when no
// theme is configured.
func DefaultPalette() Palette {
	return Palette{
		Primary:   lipgloss.Color("#7DCFFF"),
		Secondary: lipgloss.Color("#BB9AF7"),
		Success:   lipgloss.Color("#9ECE6A"),
		Warning:   lipgloss.Color("#E0AF68"),
		Danger:    lipgloss.Color("#F7768E"),
		Muted:     lipgloss.Color("#565F89"),
		Bg:        lipgloss.Color("#1A1B26"),
		BgAlt:     lipgloss.Color("#24283B"),
		Fg:        lipgloss.Color("#C0CAF5"),
		Border:    lipgloss.Color("#3B4261"),
	}
}

// current is the palette the exported styles were last built from.
var current = DefaultPalette()

// Active returns the palette currently applied to the styles.
func Active() Palette { return current }

// Base colors derived from the active palette.
var (
	colorPrimary   lipgloss.Color
	colorSecondary lipgloss.Color
	colorSuccess   lipgloss.Color
	colorWarning   lipgloss.Color
	colorDanger    lipgloss.Color
	colorMuted     lipgloss.Color
	colorBg        lipgloss.Color
	colorBgAlt     lipgloss.Color
	colorFg        lipgloss.Color
	colorBorder    lipgloss.Color
)

// Exported styles. These are rebuilt by Apply; render code reads them lazily at
// View time, so re-theming at startup takes effect everywhere.
var (
	// Table
	TableSelected lipgloss.Style
	TableHeader   lipgloss.Style
	TableCell     lipgloss.Style

	// Status badges
	StatusRunning lipgloss.Style
	StatusStopped lipgloss.Style
	StatusExited  lipgloss.Style
	StatusOther   lipgloss.Style

	// SelectedBg is the background of the highlighted (cursor) table row; used
	// to keep a colored cell sitting on the selection highlight.
	SelectedBg lipgloss.Color

	// Bottom bar (filter / command line)
	BottomBar       lipgloss.Style
	BottomBarPrefix lipgloss.Style

	// Detail panel (kept for reference, not used as border wrapper)
	DetailPanel lipgloss.Style
	DetailTitle lipgloss.Style
	DetailKey   lipgloss.Style
	DetailValue lipgloss.Style

	// Logs panel (kept for reference)
	LogsPanel lipgloss.Style

	// Status bar
	StatusBar    lipgloss.Style
	StatusBarKey lipgloss.Style

	// Error toast
	ErrorStyle lipgloss.Style

	// App container
	App lipgloss.Style

	// ── k9s-style header bar ──────────────────────────────────────────────────
	HeaderApp      lipgloss.Style
	HeaderSep      lipgloss.Style
	HeaderResource lipgloss.Style
	HeaderInfo     lipgloss.Style
	HeaderFilter   lipgloss.Style
	HeaderHost     lipgloss.Style
	HeaderBg       lipgloss.Style

	// Server-status dot rendered next to the host in the header: green while
	// the daemon answers pings, red when it is unreachable, yellow while the
	// auto-reconnect loop is retrying.
	HeaderStatusOK    lipgloss.Style
	HeaderStatusDown  lipgloss.Style
	HeaderStatusRetry lipgloss.Style

	// HeaderReconnect is the prominent banner shown while auto-reconnecting.
	HeaderReconnect lipgloss.Style

	// HeaderPaused is the chip shown in the header while auto-refresh is paused.
	HeaderPaused lipgloss.Style

	// HeaderAlert is the chip shown in the header when one or more containers
	// breach a configured resource-usage threshold (the ⚠ count).
	HeaderAlert lipgloss.Style

	// Alert colors a container row flagged by a resource-usage threshold (the ⚠
	// NAME marker); applied after table layout.
	Alert lipgloss.Style

	// ── k9s-style footer bar ─────────────────────────────────────────────────
	FooterKey   lipgloss.Style
	FooterDesc  lipgloss.Style
	FooterBg    lipgloss.Style
	FooterError lipgloss.Style

	// ── help screen ───────────────────────────────────────────────────────────
	HelpTitle lipgloss.Style
	HelpKey   lipgloss.Style
	HelpDesc  lipgloss.Style
	HelpMuted lipgloss.Style

	// ── copy-menu overlay ────────────────────────────────────────────────────
	CopyMenuTitle    lipgloss.Style
	CopyMenuSelected lipgloss.Style
	CopyMenuLabel    lipgloss.Style
	CopyMenuValue    lipgloss.Style
	CopyMenuHint     lipgloss.Style
	CopySuccess      lipgloss.Style

	// ── modal overlay panel (copy menu, host form) ───────────────────────────
	OverlayPanel lipgloss.Style

	// ── host add/edit form ───────────────────────────────────────────────────
	FormTitle       lipgloss.Style
	FormLabel       lipgloss.Style
	FormLabelActive lipgloss.Style
	FormHint        lipgloss.Style
	FormError       lipgloss.Style

	// ── embedded shell (interactive exec terminal) ───────────────────────────
	ShellBorder lipgloss.Style
	ShellTitle  lipgloss.Style
	ShellCursor lipgloss.Style
	ShellExited lipgloss.Style

	// Outer frame — kept for reference but no longer used
	AppFrame lipgloss.Style
	AppTitle lipgloss.Style
)

func init() { Apply(DefaultPalette()) }

// Apply rebuilds every exported style from palette p and records it as the
// active palette. Call it once at startup before the UI renders; the bubbletea
// View functions read these package-level styles lazily, so a re-theme takes
// effect across the whole interface.
func Apply(p Palette) {
	current = p

	colorPrimary = p.Primary
	colorSecondary = p.Secondary
	colorSuccess = p.Success
	colorWarning = p.Warning
	colorDanger = p.Danger
	colorMuted = p.Muted
	colorBg = p.Bg
	colorBgAlt = p.BgAlt
	colorFg = p.Fg
	colorBorder = p.Border

	// Table
	TableSelected = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorPrimary).
		Bold(true)

	TableHeader = lipgloss.NewStyle().
		Foreground(colorSecondary).
		Bold(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(colorBorder)

	TableCell = lipgloss.NewStyle().
		Foreground(colorFg)

	// Status badges
	StatusRunning = lipgloss.NewStyle().Foreground(colorSuccess)
	StatusStopped = lipgloss.NewStyle().Foreground(colorMuted)
	StatusExited = lipgloss.NewStyle().Foreground(colorDanger)
	StatusOther = lipgloss.NewStyle().Foreground(colorWarning)

	SelectedBg = colorBgAlt

	// Bottom bar (filter / command line)
	BottomBar = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorFg).
		Padding(0, 1)

	BottomBarPrefix = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorPrimary).
		Bold(true).
		Padding(0, 1)

	// Detail panel (kept for reference, not used as border wrapper)
	DetailPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1)

	DetailTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)

	DetailKey = lipgloss.NewStyle().
		Foreground(colorSecondary).
		Bold(true)

	DetailValue = lipgloss.NewStyle().
		Foreground(colorFg)

	// Logs panel (kept for reference)
	LogsPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1)

	// Status bar
	StatusBar = lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorMuted).
		Padding(0, 1)

	StatusBarKey = lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorPrimary).
		Bold(true)

	// Error toast
	ErrorStyle = lipgloss.NewStyle().
		Foreground(colorDanger).
		Bold(true).
		Padding(0, 1)

	// App container
	App = lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorFg)

	// ── k9s-style header bar ──────────────────────────────────────────────────

	HeaderApp = lipgloss.NewStyle().
		Background(colorPrimary).
		Foreground(colorBg).
		Bold(true).
		Padding(0, 1)

	HeaderSep = lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorMuted)

	HeaderResource = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorFg).
		Bold(true).
		Padding(0, 1)

	HeaderInfo = lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorMuted)

	HeaderFilter = lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorWarning).
		Bold(true)

	HeaderHost = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorMuted).
		Padding(0, 1)

	HeaderBg = lipgloss.NewStyle().
		Background(colorBg)

	HeaderStatusOK = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorSuccess)

	HeaderStatusDown = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorDanger)

	HeaderStatusRetry = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorWarning)

	HeaderReconnect = lipgloss.NewStyle().
		Background(colorWarning).
		Foreground(colorBg).
		Bold(true).
		Padding(0, 1)

	HeaderPaused = lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorWarning).
		Bold(true)

	HeaderAlert = lipgloss.NewStyle().
		Background(colorDanger).
		Foreground(colorBg).
		Bold(true)

	Alert = lipgloss.NewStyle().
		Foreground(colorDanger).
		Bold(true)

	// ── k9s-style footer bar ─────────────────────────────────────────────────

	FooterKey = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorPrimary).
		Bold(true)

	FooterDesc = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorMuted)

	FooterBg = lipgloss.NewStyle().
		Background(colorBgAlt)

	FooterError = lipgloss.NewStyle().
		Background(colorDanger).
		Foreground(colorBg).
		Bold(true).
		Padding(0, 1)

	// ── help screen ───────────────────────────────────────────────────────────

	HelpTitle = lipgloss.NewStyle().
		Foreground(colorSecondary).
		Bold(true)

	HelpKey = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)

	HelpDesc = lipgloss.NewStyle().
		Foreground(colorFg)

	HelpMuted = lipgloss.NewStyle().
		Foreground(colorMuted)

	// ── copy-menu overlay ────────────────────────────────────────────────────

	CopyMenuTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)

	CopyMenuSelected = lipgloss.NewStyle().
		Background(colorBgAlt).
		Foreground(colorPrimary).
		Bold(true)

	CopyMenuLabel = lipgloss.NewStyle().
		Foreground(colorSecondary)

	CopyMenuValue = lipgloss.NewStyle().
		Foreground(colorFg)

	CopyMenuHint = lipgloss.NewStyle().
		Foreground(colorMuted)

	CopySuccess = lipgloss.NewStyle().
		Foreground(colorSuccess).
		Bold(true)

	// ── modal overlay panel (copy menu, host form) ───────────────────────────

	OverlayPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 2)

	// ── host add/edit form ───────────────────────────────────────────────────

	FormTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)

	FormLabel = lipgloss.NewStyle().
		Foreground(colorMuted)

	FormLabelActive = lipgloss.NewStyle().
		Foreground(colorSecondary).
		Bold(true)

	FormHint = lipgloss.NewStyle().
		Foreground(colorMuted)

	FormError = lipgloss.NewStyle().
		Foreground(colorDanger).
		Bold(true)

	// ── embedded shell (interactive exec terminal) ───────────────────────────

	ShellBorder = lipgloss.NewStyle().
		Foreground(colorBorder)

	ShellTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)

	ShellCursor = lipgloss.NewStyle().
		Reverse(true)

	ShellExited = lipgloss.NewStyle().
		Foreground(colorMuted).
		Italic(true)

	// Outer frame — kept for reference but no longer used
	AppFrame = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder)

	AppTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)
}

// StateColor returns a style matching a container state string.
func StateColor(state string) lipgloss.Style {
	switch state {
	case "running":
		return StatusRunning
	case "exited":
		return StatusExited
	case "paused", "created":
		return StatusOther
	default:
		return StatusStopped
	}
}

// HealthColor returns a style matching a container healthcheck verdict.
func HealthColor(health string) lipgloss.Style {
	switch health {
	case "healthy":
		return StatusRunning
	case "unhealthy":
		return StatusExited
	case "starting":
		return StatusOther
	default: // no healthcheck
		return StatusStopped
	}
}

// ComposeStatusColor returns a style matching a compose project status.
func ComposeStatusColor(status string) lipgloss.Style {
	switch status {
	case "running":
		return StatusRunning
	case "error":
		return StatusExited
	case "paused", "partial":
		return StatusOther
	default: // stopped, unknown
		return StatusStopped
	}
}
