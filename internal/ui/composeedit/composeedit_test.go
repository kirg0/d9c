package composeedit

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSetCreate(t *testing.T) {
	m := New()
	m.SetCreate("/srv/newapp")
	if !m.IsCreate() {
		t.Error("IsCreate() = false, want true")
	}
	if m.CreateDir() != "/srv/newapp" {
		t.Errorf("CreateDir() = %q, want /srv/newapp", m.CreateDir())
	}
	if m.Path() != "/srv/newapp/docker-compose.yaml" {
		t.Errorf("Path() = %q, want /srv/newapp/docker-compose.yaml", m.Path())
	}
	if !strings.Contains(m.Value(), "services:") {
		t.Errorf("template missing 'services:':\n%s", m.Value())
	}
	if err := ValidateYAML(m.Value()); err != nil {
		t.Errorf("starter template is not valid YAML: %v", err)
	}
	// Switching to edit mode clears create state.
	m.SetContent("proj", "/p/docker-compose.yml", "services: {}\n")
	if m.IsCreate() {
		t.Error("IsCreate() should be false after SetContent")
	}
}

func TestSetContentAndProject(t *testing.T) {
	m := New()
	m.SetError("stale")
	m.SetSaving(true)
	if cmd := m.SetContent("proj", "/p/docker-compose.yml", "services: {}\n"); cmd == nil {
		t.Error("SetContent should return the textarea focus cmd")
	}
	if got := m.Project(); got != "proj" {
		t.Errorf("Project() = %q, want proj", got)
	}
	if m.errMsg != "" || m.saving {
		t.Error("SetContent should reset error and saving state")
	}
}

func TestSetSize(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	if got := m.area.Height(); got != 17 {
		t.Errorf("area height = %d, want 17 (height-3)", got)
	}
	m.SetSize(80, 4) // floor kicks in
	if got := m.area.Height(); got != 3 {
		t.Errorf("area height = %d, want floor 3", got)
	}
}

func TestUpdateTypes(t *testing.T) {
	m := New()
	m.SetContent("proj", "/p/docker-compose.yml", "")
	for _, r := range "x: 1" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Value(); got != "x: 1" {
		t.Errorf("Value() = %q, want typed text", got)
	}
}

func TestViewStatusLine(t *testing.T) {
	m := New()
	m.SetSize(60, 12)
	m.SetContent("proj", "/p/docker-compose.yml", "services: {}\n")
	got := m.View(60)
	if !strings.Contains(got, "edit: /p/docker-compose.yml") || !strings.Contains(got, "ctrl+s save") {
		t.Error("edit view should show title and default hint")
	}
	m.SetError("bad yaml")
	if got := m.View(60); !strings.Contains(got, "bad yaml") {
		t.Error("view should show the error message")
	}
	m.SetSaving(true) // saving wins over error
	if got := m.View(60); !strings.Contains(got, "saving…") {
		t.Error("view should show the saving indicator")
	}
	m.SetCreate("/srv/app")
	if got := m.View(60); !strings.Contains(got, "create: /srv/app/docker-compose.yaml") {
		t.Error("create view should show the create title")
	}
}

func TestValidateYAML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"valid", "services:\n  web:\n    image: nginx\n", false},
		{"empty", "   \n", true},
		{"bad indent / mapping", "services:\n  web:\n   image: nginx\n  - oops\n", true},
		{"tab indentation", "services:\n\tweb: x\n", true},
		{"scalar is valid yaml", "just a string", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateYAML(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateYAML(%q) err=%v, wantErr=%v", tt.content, err, tt.wantErr)
			}
		})
	}
}
