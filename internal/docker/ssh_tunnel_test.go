package docker

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// The strict callback (agent CLI) verifies known hosts like accept-new does but
// never records an unknown one: it fails with ErrUnknownHostKey and leaves
// known_hosts untouched, while a changed key stays a plain mismatch.
func TestStrictHostKeyCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	remote := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 22}
	const host = "example.com:22"
	keyA, keyB := testPublicKey(t), testPublicKey(t)

	// No known_hosts file at all.
	err := strictHostKeyCallbackFor(path)(host, remote, keyA)
	if !IsUnknownHostKeyError(err) {
		t.Fatalf("missing file: err = %v, want ErrUnknownHostKey", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("strict callback created known_hosts (stat err %v)", statErr)
	}

	// Empty file: host unknown, still rejected and not appended.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := strictHostKeyCallbackFor(path)(host, remote, keyA); !IsUnknownHostKeyError(err) {
		t.Fatalf("unknown host: err = %v, want ErrUnknownHostKey", err)
	}
	if data, _ := os.ReadFile(path); len(data) != 0 {
		t.Fatalf("strict callback appended to known_hosts: %q", data)
	}

	// Recorded (by the TOFU callback, as the TUI would): verifies cleanly.
	if err := hostKeyCallbackFor(path)(host, remote, keyA); err != nil {
		t.Fatal(err)
	}
	if err := strictHostKeyCallbackFor(path)(host, remote, keyA); err != nil {
		t.Errorf("known host: err = %v, want nil", err)
	}

	// Changed key: mismatch, not "unknown".
	err = strictHostKeyCallbackFor(path)(host, remote, keyB)
	if err == nil || IsUnknownHostKeyError(err) || !IsHostKeyError(err) {
		t.Errorf("changed key: err = %v, want a host-key mismatch", err)
	}

	// No known_hosts location: reject instead of disabling verification.
	if err := strictHostKeyCallbackFor("")(host, remote, keyA); !IsUnknownHostKeyError(err) {
		t.Errorf("empty path: err = %v, want ErrUnknownHostKey", err)
	}
}

// SetStrictHostKeys selects which callback hostKeyCallback hands to SSH.
func TestSetStrictHostKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { SetStrictHostKeys(false) })
	remote := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 22}

	SetStrictHostKeys(true)
	if err := hostKeyCallback()("strict.example:22", remote, testPublicKey(t)); !IsUnknownHostKeyError(err) {
		t.Errorf("strict: err = %v, want ErrUnknownHostKey", err)
	}
	SetStrictHostKeys(false)
	if err := hostKeyCallback()("tofu.example:22", remote, testPublicKey(t)); err != nil {
		t.Errorf("accept-new: err = %v, want nil", err)
	}
}

// pickDialStdioCmd must fall through to sudo when the SSH user can't reach the
// daemon directly, and fail loudly when neither prefix works — the original bug
// was that the fallback never advanced past the first (failing) command.
func TestPickDialStdioCmd(t *testing.T) {
	prefixes := []string{"", "sudo "}

	// User in docker group: the plain command wins.
	cmd, err := pickDialStdioCmd(prefixes, func(string) error { return nil })
	if err != nil || cmd != "docker system dial-stdio" {
		t.Fatalf("in-group: cmd=%q err=%v, want plain dial-stdio", cmd, err)
	}

	// User not in docker group but passwordless sudo works: fall through to sudo.
	cmd, err = pickDialStdioCmd(prefixes, func(prefix string) error {
		if prefix == "" {
			return errors.New("permission denied")
		}
		return nil
	})
	if err != nil || cmd != "sudo docker system dial-stdio" {
		t.Fatalf("sudo fallback: cmd=%q err=%v, want sudo dial-stdio", cmd, err)
	}

	// Neither works: a wrapped error, no usable command.
	cmd, err = pickDialStdioCmd(prefixes, func(string) error { return errors.New("permission denied") })
	if err == nil {
		t.Fatal("no-access: want error, got nil")
	}
	if cmd != "" {
		t.Errorf("no-access: cmd=%q, want empty", cmd)
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("no-access err = %v, want an 'unreachable' hint", err)
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
