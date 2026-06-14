package docker

import "testing"

func TestParseSSHHost(t *testing.T) {
	tests := []struct {
		raw      string
		wantUser string
		wantHost string
		wantPort string
	}{
		{"ssh://user@host", "user", "host", "22"},
		{"ssh://user@host:2222", "user", "host", "2222"},
		{"ssh://host", "", "host", "22"},
		{"user@host", "user", "host", "22"},
		{"host", "", "host", "22"},
	}

	for _, tt := range tests {
		user, host, port := parseSSHHost(tt.raw)
		if user != tt.wantUser {
			t.Errorf("parseSSHHost(%q) user = %q, want %q", tt.raw, user, tt.wantUser)
		}
		if host != tt.wantHost {
			t.Errorf("parseSSHHost(%q) host = %q, want %q", tt.raw, host, tt.wantHost)
		}
		if port != tt.wantPort {
			t.Errorf("parseSSHHost(%q) port = %q, want %q", tt.raw, port, tt.wantPort)
		}
	}
}
