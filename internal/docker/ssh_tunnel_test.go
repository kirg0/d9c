package docker

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return sshPub
}

// hostKeyCallbackFor must behave like StrictHostKeyChecking=accept-new: trust a
// new host on first use (persisting its key), accept the same key afterwards,
// reject a changed key, and — crucially — trust the host again once its stale
// entry is removed, so the "clean known_hosts and reconnect" advice works.
func TestHostKeyCallbackAcceptNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	remote := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 22}
	const host = "example.com:22"

	cb := hostKeyCallbackFor(path)
	keyA := testPublicKey(t)

	// First use: unknown host is trusted and recorded.
	if err := cb(host, remote, keyA); err != nil {
		t.Fatalf("first connect (TOFU) = %v, want nil", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("known_hosts not written: %v", err)
	}

	// Same key on the next connect verifies cleanly.
	if err := hostKeyCallbackFor(path)(host, remote, keyA); err != nil {
		t.Errorf("reconnect with same key = %v, want nil", err)
	}

	// A different key for a known host is a mismatch and must be rejected with
	// an error the UI classifies as a host-key problem.
	keyB := testPublicKey(t)
	err := hostKeyCallbackFor(path)(host, remote, keyB)
	if err == nil {
		t.Fatal("changed key accepted, want rejection")
	}
	if !IsHostKeyError(err) {
		t.Errorf("mismatch err = %v, want IsHostKeyError", err)
	}

	// After the user cleans the stale entry, the host is unknown again and the
	// new key is trusted — the bug was that this kept failing.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("truncate known_hosts: %v", err)
	}
	if err := hostKeyCallbackFor(path)(host, remote, keyB); err != nil {
		t.Errorf("reconnect after cleaning entry = %v, want nil", err)
	}
	if err := hostKeyCallbackFor(path)(host, remote, keyB); err != nil {
		t.Errorf("key not persisted after re-trust: %v", err)
	}
}

// An empty path disables verification (no known_hosts location available).
func TestHostKeyCallbackNoPath(t *testing.T) {
	if cb := hostKeyCallbackFor(""); cb == nil {
		t.Fatal("nil callback for empty path")
	}
	if err := hostKeyCallbackFor("")("example.com:22", &net.TCPAddr{}, testPublicKey(t)); err != nil {
		t.Errorf("insecure callback = %v, want nil", err)
	}
}

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
