package main

import (
	"testing"

	"github.com/kirg0/d9c/internal/config"
)

func TestHostConfigured(t *testing.T) {
	tests := []struct {
		host     string
		explicit bool
		want     bool
	}{
		{"", false, false},
		{"", true, false},
		{config.DefaultHost, false, false},
		// An explicit -H pointing at the default socket (the container image
		// with a mounted docker.sock) must connect instead of opening Hosts.
		{config.DefaultHost, true, true},
		{"tcp://10.0.0.5:2375", false, true},
		{"ssh://user@host", false, true},
		{"ssh://user@host", true, true},
	}
	for _, tt := range tests {
		if got := hostConfigured(tt.host, tt.explicit); got != tt.want {
			t.Errorf("hostConfigured(%q, %v) = %v, want %v", tt.host, tt.explicit, got, tt.want)
		}
	}
}
