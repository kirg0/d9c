package hostform

import (
	"testing"

	"d9c/internal/hosts"
)

func TestOpenEditPrefillAndResult(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey, SSHKeyPath: "/keys/id"})

	if !m.IsEditing() || m.OrigName() != "lab" {
		t.Fatalf("editing state wrong: editing=%v orig=%q", m.IsEditing(), m.OrigName())
	}
	got := m.Result()
	want := hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey, SSHKeyPath: "/keys/id"}
	if got != want {
		t.Errorf("Result() = %+v, want %+v", got, want)
	}
}

func TestAuthToggleDropsKeyPath(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey, SSHKeyPath: "/keys/id"})

	// Walk focus to the auth selector: name -> host -> auth.
	m.Next()
	m.Next()
	if !m.OnAuthField() {
		t.Fatalf("expected auth field focused after two Next()")
	}
	m.ToggleAuth()

	if got := m.Auth(); got != hosts.SSHAuthPassword {
		t.Errorf("Auth() = %q, want password", got)
	}
	if got := m.KeyPath(); got != "" {
		t.Errorf("KeyPath() = %q, want empty under password auth", got)
	}
	res := m.Result()
	if res.SSHAuth != hosts.SSHAuthPassword || res.SSHKeyPath != "" {
		t.Errorf("Result() = %+v, want password auth without key path", res)
	}
}

func TestNonSSHHostHasNoAuth(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "tcp", Host: "tcp://host:2375"})

	if m.isSSH() {
		t.Fatal("tcp host should not be ssh")
	}
	if got := m.Auth(); got != "" {
		t.Errorf("Auth() = %q, want empty for tcp host", got)
	}
	// Only Name and Host are focusable for a non-ssh host.
	if n := len(m.fields()); n != 2 {
		t.Errorf("fields() = %d, want 2 for tcp host", n)
	}
}

func TestToggleAuthNoOpOffAuthField(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey})
	// Focus is on Name; toggling must not change the method.
	m.ToggleAuth()
	if got := m.Auth(); got != hosts.SSHAuthKey {
		t.Errorf("Auth() = %q, want key (toggle off-field is a no-op)", got)
	}
}
