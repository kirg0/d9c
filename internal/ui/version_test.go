package ui

import (
	"strings"
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"
	"d9c/internal/version"
)

// TestHeaderShowsVersion verifies the app version is rendered in the top bar.
func TestHeaderShowsVersion(t *testing.T) {
	m := NewModel(&config.Config{Demo: true}, docker.NewFakeBackend(), &hosts.Store{}, nil, false)
	header := m.viewHeader()
	if !strings.Contains(header, version.String()) {
		t.Fatalf("header does not contain version %q:\n%s", version.String(), header)
	}
}
