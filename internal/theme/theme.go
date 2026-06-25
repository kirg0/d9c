// Package theme loads the UI color scheme from a small YAML config file, the
// same way plugins and saved hosts are persisted next to the d9c binary. It
// ships a set of named palettes (Tokyo Night, Dracula, Nord, …) and lets the
// user pick one and/or override individual base colors, then hands the result
// to the styles package via styles.Apply.
//
// Example d9c-config.yaml:
//
//	theme: dracula            # built-in palette name (optional; default tokyonight)
//	colors:                   # optional per-color overrides (hex #rrggbb or ANSI 0-255)
//	  primary: "#ff79c6"
//	  danger: "#ff5555"
package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"d9c/internal/ui/styles"
)

// DefaultName is the built-in palette used when the config file is absent or
// does not name a theme.
const DefaultName = "tokyonight"

// builtins holds the named palettes selectable via the "theme:" key.
var builtins = map[string]styles.Palette{
	"tokyonight": styles.DefaultPalette(),
	"dracula": {
		Primary:   "#8BE9FD",
		Secondary: "#BD93F9",
		Success:   "#50FA7B",
		Warning:   "#F1FA8C",
		Danger:    "#FF5555",
		Muted:     "#6272A4",
		Bg:        "#282A36",
		BgAlt:     "#343746",
		Fg:        "#F8F8F2",
		Border:    "#44475A",
	},
	"nord": {
		Primary:   "#88C0D0",
		Secondary: "#B48EAD",
		Success:   "#A3BE8C",
		Warning:   "#EBCB8B",
		Danger:    "#BF616A",
		Muted:     "#4C566A",
		Bg:        "#2E3440",
		BgAlt:     "#3B4252",
		Fg:        "#ECEFF4",
		Border:    "#434C5E",
	},
	"gruvbox": {
		Primary:   "#83A598",
		Secondary: "#D3869B",
		Success:   "#B8BB26",
		Warning:   "#FABD2F",
		Danger:    "#FB4934",
		Muted:     "#928374",
		Bg:        "#282828",
		BgAlt:     "#3C3836",
		Fg:        "#EBDBB2",
		Border:    "#504945",
	},
	"solarized": {
		Primary:   "#268BD2",
		Secondary: "#6C71C4",
		Success:   "#859900",
		Warning:   "#B58900",
		Danger:    "#DC322F",
		Muted:     "#586E75",
		Bg:        "#002B36",
		BgAlt:     "#073642",
		Fg:        "#93A1A1",
		Border:    "#094956",
	},
	"catppuccin": {
		Primary:   "#89B4FA",
		Secondary: "#CBA6F7",
		Success:   "#A6E3A1",
		Warning:   "#F9E2AF",
		Danger:    "#F38BA8",
		Muted:     "#6C7086",
		Bg:        "#1E1E2E",
		BgAlt:     "#313244",
		Fg:        "#CDD6F4",
		Border:    "#45475A",
	},
	// k9s evokes the stock k9s skin: a true-black body with a vivid aqua accent,
	// an orange logo/highlight color, and bright, saturated status colors on a
	// dodgerblue border.
	"k9s": {
		Primary:   "#00E5FF", // aqua accents / active keys
		Secondary: "#FF9800", // orange logo / labels & headers
		Success:   "#00E676", // bright green (running / healthy)
		Warning:   "#FFD600", // bright yellow (transitional)
		Danger:    "#FF1744", // bright red (errors / stopped)
		Muted:     "#5F9EA0", // cadetblue dim text
		Bg:        "#000000", // true-black body
		BgAlt:     "#1A1A1A", // raised surfaces (selection, modals)
		Fg:        "#E0F7FA", // light cyan-white text
		Border:    "#1E90FF", // dodgerblue rules
	},
}

// Config is the on-disk config-file format read by this package. The same file
// also carries a "keys:" section read independently by the keymap package, so
// the two concerns stay decoupled (yaml ignores fields it doesn't know).
type Config struct {
	Theme  string            `yaml:"theme"`
	Colors map[string]string `yaml:"colors"`
}

// ByName returns the built-in palette with the given name (case-insensitive),
// reporting whether it exists. It is the entry point for switching themes at
// runtime without a config file.
func ByName(name string) (styles.Palette, bool) {
	p, ok := builtins[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// NameOf returns the built-in theme name whose palette equals p, or "" if p does
// not match any built-in (e.g. a config with custom color overrides).
func NameOf(p styles.Palette) string {
	for name, pal := range builtins {
		if pal == p {
			return name
		}
	}
	return ""
}

// Names returns the built-in theme names in sorted order (for docs/help).
func Names() []string {
	out := make([]string, 0, len(builtins))
	for name := range builtins {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DefaultPath returns the config file location next to the running binary,
// falling back to the current directory if the executable path is unavailable.
func DefaultPath() string {
	const name = "d9c-config.yaml"
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exe), name)
}

// Load reads the config file at path and resolves it to a palette: the named
// built-in theme (default tokyonight) with any per-color overrides applied. A
// missing file yields the default palette (not an error); malformed YAML, an
// unknown theme name, an unknown color key, or an invalid color value is an
// error.
func Load(path string) (styles.Palette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return styles.DefaultPalette(), nil
		}
		return styles.Palette{}, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return styles.Palette{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return Resolve(cfg)
}

// Resolve turns a parsed Config into a concrete palette, validating the theme
// name and every color override.
func Resolve(cfg Config) (styles.Palette, error) {
	name := strings.ToLower(strings.TrimSpace(cfg.Theme))
	if name == "" {
		name = DefaultName
	}
	pal, ok := builtins[name]
	if !ok {
		return styles.Palette{}, fmt.Errorf("unknown theme %q (available: %s)", name, strings.Join(Names(), ", "))
	}
	for key, val := range cfg.Colors {
		field := fieldFor(&pal, strings.ToLower(strings.TrimSpace(key)))
		if field == nil {
			return styles.Palette{}, fmt.Errorf("unknown color %q (valid: %s)", key, strings.Join(colorKeys(), ", "))
		}
		color, err := parseColor(val)
		if err != nil {
			return styles.Palette{}, fmt.Errorf("color %q: %w", key, err)
		}
		*field = color
	}
	return pal, nil
}

// colorKeys lists the override keys accepted under "colors:".
func colorKeys() []string {
	return []string{"primary", "secondary", "success", "warning", "danger", "muted", "bg", "bgalt", "fg", "border"}
}

// fieldFor maps an override key to the matching palette field, or nil if the key
// is not a known color.
func fieldFor(p *styles.Palette, key string) *lipgloss.Color {
	switch key {
	case "primary":
		return &p.Primary
	case "secondary":
		return &p.Secondary
	case "success":
		return &p.Success
	case "warning":
		return &p.Warning
	case "danger":
		return &p.Danger
	case "muted":
		return &p.Muted
	case "bg":
		return &p.Bg
	case "bgalt":
		return &p.BgAlt
	case "fg":
		return &p.Fg
	case "border":
		return &p.Border
	default:
		return nil
	}
}

var hexColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// parseColor validates a color string: a hex triplet (#rgb or #rrggbb) or an
// ANSI palette index (0-255).
func parseColor(s string) (lipgloss.Color, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty color")
	}
	if hexColor.MatchString(s) {
		return lipgloss.Color(s), nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 || n > 255 {
			return "", fmt.Errorf("ANSI index %d out of range 0-255", n)
		}
		return lipgloss.Color(s), nil
	}
	return "", fmt.Errorf("invalid color %q (use #rgb, #rrggbb, or an ANSI index 0-255)", s)
}
