// Package settings owns the unified d9c configuration file (d9c-config.yaml):
// the single place every persistent setting lives — UI theme and color
// overrides, normal-mode keybindings, resource-alert thresholds, and the list
// of saved hosts. Plugins are intentionally NOT here; they stay in their own
// d9c-plugins.yaml.
//
// This package is the only reader/writer of the file. It delegates validation
// of each section to the owning package's pure resolver (theme.Resolve,
// keymap.Resolve, alerts.Resolve), so those packages keep their domain logic
// without each opening the file independently. Saving rewrites the whole file
// from the in-memory model, so a host edit can never clobber the theme section
// and vice versa.
package settings

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"d9c/internal/alerts"
	"d9c/internal/hosts"
	"d9c/internal/i18n"
	"d9c/internal/keymap"
	"d9c/internal/theme"
	"d9c/internal/ui/styles"
)

// File is the on-disk shape of d9c-config.yaml. Every section is optional;
// omitempty keeps a freshly written file free of empty noise.
type File struct {
	Lang   string            `yaml:"lang,omitempty"`
	Theme  string            `yaml:"theme,omitempty"`
	Colors map[string]string `yaml:"colors,omitempty"`
	Keys   map[string]string `yaml:"keys,omitempty"`
	Alerts *AlertsSection    `yaml:"alerts,omitempty"`
	Hosts  []hosts.Host      `yaml:"hosts,omitempty"`
}

// AlertsSection mirrors the "alerts:" block (CPU/MEM thresholds, percent).
type AlertsSection struct {
	CPU float64 `yaml:"cpu"`
	Mem float64 `yaml:"mem"`
}

// Store is the loaded config bound to its file path.
type Store struct {
	path string
	File File
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

// Load reads the config file at path. A missing file yields an empty store bound
// to that path (so a later Save creates it); malformed YAML is an error. The
// individual sections are validated lazily by Palette/Keymap/Alerts so a single
// bad section is reported in context.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}
	if err := yaml.Unmarshal(data, &s.File); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return s, nil
}

// Path returns the file the store is bound to.
func (s *Store) Path() string { return s.path }

// Save writes the whole config back to its file, preserving every section.
func (s *Store) Save() error {
	data, err := yaml.Marshal(&s.File)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

// Lang resolves the configured UI language (default RU when the section is
// empty or invalid handling is delegated to i18n.Resolve).
func (s *Store) Lang() (i18n.Lang, error) {
	return i18n.Resolve(s.File.Lang)
}

// SetLang records the UI language code and persists the file.
func (s *Store) SetLang(name string) error {
	s.File.Lang = name
	return s.Save()
}

// Palette resolves the theme/colors section into a concrete palette (default
// palette when the section is empty).
func (s *Store) Palette() (styles.Palette, error) {
	return theme.Resolve(theme.Config{Theme: s.File.Theme, Colors: s.File.Colors})
}

// Keymap resolves the keys section on top of the defaults.
func (s *Store) Keymap() (keymap.Map, error) {
	return keymap.Resolve(s.File.Keys)
}

// Alerts resolves the alert thresholds (disabled when the section is absent).
func (s *Store) Alerts() (alerts.Thresholds, error) {
	if s.File.Alerts == nil {
		return alerts.Thresholds{}, nil
	}
	return alerts.Resolve(s.File.Alerts.CPU, s.File.Alerts.Mem)
}

// SetTheme records a built-in theme name as the active theme and persists the
// file. Selecting a complete built-in palette clears any stale per-color
// overrides so the saved theme renders exactly as previewed.
func (s *Store) SetTheme(name string) error {
	s.File.Theme = name
	s.File.Colors = nil
	return s.Save()
}

// Hosts returns a host store backed by this config file: its CRUD persists by
// rewriting the unified file (preserving the other sections).
func (s *Store) Hosts() *hosts.Store {
	return hosts.NewStore(s.File.Hosts, func(list []hosts.Host) error {
		s.File.Hosts = list
		return s.Save()
	})
}

// SetHosts replaces the host list in-memory without saving. Used by migration
// before the first explicit Save.
func (s *Store) SetHosts(list []hosts.Host) {
	s.File.Hosts = list
}

// HasHosts reports whether the config already carries saved hosts.
func (s *Store) HasHosts() bool { return len(s.File.Hosts) > 0 }
