package styles

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestNoHardcodedHexOutsideStyles enforces the project convention that every
// color literal lives in this package: a hex color hardcoded elsewhere under
// internal/ui silently ignores `:theme` (it stays Tokyo Night under any
// palette — the P2 tech-debt item). Test files are exempt (they assert
// against literal colors), as is this package itself. Colors converted
// dynamically (e.g. the vt10x terminal emulator mapping content colors)
// don't match the literal pattern and stay allowed.
func TestNoHardcodedHexOutsideStyles(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	stylesDir := filepath.Dir(self)
	uiRoot := filepath.Dir(stylesDir) // …/internal/ui

	hexLiteral := regexp.MustCompile(`lipgloss\.Color\("#`)

	err := filepath.WalkDir(uiRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == stylesDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if hexLiteral.MatchString(line) {
				t.Errorf("%s:%d: hardcoded hex color outside styles package (breaks :theme): %s",
					path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", uiRoot, err)
	}
}
