package help

import (
	"strings"
	"testing"
)

func TestSetContentRenders(t *testing.T) {
	m := New()
	m.SetSize(40, 10)
	m.SetContent("hello world")
	if !strings.Contains(m.View(), "hello") {
		t.Errorf("view should contain the set content, got %q", m.View())
	}
}
