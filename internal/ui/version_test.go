package ui

import (
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
	"github.com/kirg0/d9c/internal/hosts"
	"github.com/kirg0/d9c/internal/version"
)

// TestHeaderShowsVersion verifies the app version is rendered in the top bar.
func TestHeaderShowsVersion(t *testing.T) {
	m := NewModel(&config.Config{Demo: true}, docker.NewFakeBackend(), &hosts.Store{}, nil, false)
	header := m.viewHeader()
	if !strings.Contains(header, version.String()) {
		t.Fatalf("header does not contain version %q:\n%s", version.String(), header)
	}
}
