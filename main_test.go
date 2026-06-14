package main

import (
	"testing"

	"d9c/internal/config"
)

func TestHostConfigured(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"", false},
		{config.DefaultHost, false},
		{"tcp://10.0.0.5:2375", true},
		{"ssh://user@host", true},
	}
	for _, tt := range tests {
		if got := hostConfigured(tt.host); got != tt.want {
			t.Errorf("hostConfigured(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}
