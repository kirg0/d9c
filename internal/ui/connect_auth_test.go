package ui

import (
	"errors"
	"testing"

	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/hosts"

	tea "github.com/charmbracelet/bubbletea"
)

// A password-auth SSH host opens the credential prompt (login pre-filled,
// editable) instead of connecting directly.
func TestBeginConnectPasswordOpensPrompt(t *testing.T) {
	h := hosts.Host{Name: "prod", Host: "ssh://deploy@prod", SSHAuth: hosts.SSHAuthPassword}
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)

	model, cmd := m.beginConnect(h)
	got := model.(Model)
	if got.mode != ModeConnectAuth {
		t.Fatalf("mode = %v, want ModeConnectAuth", got.mode)
	}
	if cmd != nil {
		t.Errorf("expected no connect cmd while prompting for credentials")
	}
	if got.connForm.Login() != "deploy" {
		t.Errorf("login = %q, want deploy (pre-filled from URL)", got.connForm.Login())
	}
	if got.connForm.HostURL() != "ssh://deploy@prod" {
		t.Errorf("host url = %q", got.connForm.HostURL())
	}
}

// A key-auth SSH host connects directly, stashing the stored key path on the
// live config for the SSH dialer and a later auto-reconnect.
func TestBeginConnectKeyConnectsDirectly(t *testing.T) {
	h := hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey, SSHKeyPath: "/keys/id"}
	cfg := &config.Config{SSHPassword: "stale"}
	m := NewModel(cfg, docker.NewFakeBackend(), nil, nil, false)

	model, cmd := m.beginConnect(h)
	got := model.(Model)
	if got.mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal", got.mode)
	}
	if cmd == nil {
		t.Fatal("expected a connect cmd for key auth")
	}
	if got.cfg.SSHKeyFile != "/keys/id" {
		t.Errorf("SSHKeyFile = %q, want /keys/id", got.cfg.SSHKeyFile)
	}
	if got.cfg.SSHPassword != "" {
		t.Errorf("SSHPassword = %q, want cleared", got.cfg.SSHPassword)
	}
}

// Submitting the credential prompt rewrites the URL with the edited login,
// stashes the password on the config (never on disk) and connects.
func TestConnectAuthSubmitRewritesLoginAndConnects(t *testing.T) {
	h := hosts.Host{Name: "prod", Host: "ssh://deploy@prod", SSHAuth: hosts.SSHAuthPassword}
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)

	model, _ := m.beginConnect(h)
	m = model.(Model)

	// Edit the login and type a password, then submit. With a login pre-filled
	// the password field is focused, so typed runes land there.
	m.connForm.Open("prod", "ssh://deploy@prod", "root")
	for _, r := range "s3cret" {
		m.connForm, _ = m.connForm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	model, cmd := m.handleConnectAuth(tea.KeyMsg{Type: tea.KeyEnter})
	got := model.(Model)
	// The modal stays open showing a "connecting…" status until the result lands.
	if got.mode != ModeConnectAuth {
		t.Fatalf("mode = %v, want ModeConnectAuth (prompt stays open while dialing)", got.mode)
	}
	if !got.connForm.Busy() {
		t.Error("expected the prompt to be in the connecting/busy state after submit")
	}
	if cmd == nil {
		t.Fatal("expected a connect cmd after submit")
	}
	if got.cfg.Host != "ssh://root@prod" {
		t.Errorf("host = %q, want ssh://root@prod (login rewritten)", got.cfg.Host)
	}
	if got.cfg.SSHPassword != "s3cret" {
		t.Errorf("SSHPassword = %q, want s3cret (held in memory only)", got.cfg.SSHPassword)
	}
}

// A failed connect from the credential prompt keeps the modal open with the
// error shown inline (so the user can fix the credentials and retry).
func TestConnectAuthErrorStaysInModal(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.mode = ModeConnectAuth
	m.connForm.Open("prod", "ssh://deploy@prod", "deploy")
	_ = m.connForm.Connecting()

	authErr := errors.New("SSH tunnel: ssh: handshake failed: unable to authenticate")
	model, cmd := m.Update(connectResultMsg{err: authErr, host: "ssh://deploy@prod"})
	got := model.(Model)
	if got.mode != ModeConnectAuth {
		t.Fatalf("mode = %v, want ModeConnectAuth (stay open on failure)", got.mode)
	}
	if got.connForm.Busy() {
		t.Error("busy state should clear after a failed connect")
	}
	if cmd != nil {
		t.Errorf("expected no follow-up cmd after inline error")
	}
}

// An empty login keeps the prompt open with an error instead of connecting.
func TestConnectAuthRequiresLogin(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.mode = ModeConnectAuth
	m.connForm.Open("prod", "ssh://prod", "")

	model, cmd := m.handleConnectAuth(tea.KeyMsg{Type: tea.KeyEnter})
	got := model.(Model)
	if got.mode != ModeConnectAuth {
		t.Errorf("mode = %v, want ModeConnectAuth (stay open)", got.mode)
	}
	if cmd != nil {
		t.Errorf("expected no connect cmd with empty login")
	}
}
