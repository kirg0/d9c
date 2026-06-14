package composeedit

import (
	"strings"
	"testing"
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
