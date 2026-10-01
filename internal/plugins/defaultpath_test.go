package plugins

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPathPlugins(t *testing.T) {
	want := filepath.Join(".d9c", FileName)
	if got := DefaultPath(); !strings.HasSuffix(got, want) {
		t.Errorf("DefaultPath = %q, want suffix %q", got, want)
	}
}
