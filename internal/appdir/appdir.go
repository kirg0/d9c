// Package appdir locates d9c's per-user directory (~/.d9c, %USERPROFILE%\.d9c
// on Windows) where the config and plugins files live by default, and migrates
// files that older versions kept next to the binary.
//
// Keeping user data out of the binary's directory matters for package-manager
// installs: Scoop and Homebrew put each version in its own directory, so a file
// written next to the executable is lost on upgrade.
package appdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DirName is the name of the per-user directory inside the home directory.
const DirName = ".d9c"

// Dir returns the per-user d9c directory. When the home directory cannot be
// determined it falls back to the directory of the running binary (the
// pre-1.31 location).
func Dir() string {
	return dirFrom(os.UserHomeDir, os.Executable)
}

// dirFrom is Dir with the home/executable lookups injected for tests.
func dirFrom(home, exe func() (string, error)) string {
	if h, err := home(); err == nil && h != "" {
		return filepath.Join(h, DirName)
	}
	return legacyDirFrom(exe)
}

// legacyDirFrom returns the binary's directory, or "" (the current directory)
// when the executable path is unavailable.
func legacyDirFrom(exe func() (string, error)) string {
	p, err := exe()
	if err != nil {
		return ""
	}
	return filepath.Dir(p)
}

// Path returns the default location of the named file inside Dir.
func Path(name string) string {
	return filepath.Join(Dir(), name)
}

// LegacyPath returns where versions before 1.31 kept the named file: next to
// the running binary, or the bare name (current directory) if the executable
// path is unavailable.
func LegacyPath(name string) string {
	return filepath.Join(legacyDirFrom(os.Executable), name)
}

// Migrate copies the file at legacy to dst once: only when dst does not exist
// yet and legacy does. The legacy file is left in place (its directory may be
// read-only, and an older binary may still use it). It reports whether a copy
// was made; the parent directory of dst is created as needed.
func Migrate(dst, legacy string) (bool, error) {
	if filepath.Clean(dst) == filepath.Clean(legacy) {
		return false, nil
	}
	if _, err := os.Stat(dst); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("check %s: %w", dst, err)
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", legacy, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return false, fmt.Errorf("write %s: %w", dst, err)
	}
	return true, nil
}
