package plugins

import (
	"strings"
	"testing"
)

func TestDefaultPathPlugins(t *testing.T) {
	if got := DefaultPath(); !strings.HasSuffix(got, "d9c-plugins.yaml") {
		t.Errorf("DefaultPath = %q", got)
	}
}
