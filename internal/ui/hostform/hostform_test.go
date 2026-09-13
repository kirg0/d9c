package hostform

import (
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/hosts"

	tea "github.com/charmbracelet/bubbletea"
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

func TestOpenAddDefaults(t *testing.T) {
	m := New()
	m.SetError("stale")
	m.OpenAdd()
	if m.IsEditing() || m.OrigName() != "" {
		t.Errorf("add mode: editing=%v orig=%q, want false/empty", m.IsEditing(), m.OrigName())
	}
	if got := m.Host(); got != defaultHostValue {
		t.Errorf("host = %q, want %q", got, defaultHostValue)
	}
	if got := m.Auth(); got != hosts.SSHAuthKey {
		t.Errorf("auth = %q, want key", got)
	}
	if m.Name() != "" || m.KeyPath() != "" || m.errMsg != "" {
		t.Errorf("name/keypath/errMsg should be empty, got %q/%q/%q", m.Name(), m.KeyPath(), m.errMsg)
	}
}

func TestOpenEditPasswordAuth(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthPassword})
	if got := m.Auth(); got != hosts.SSHAuthPassword {
		t.Errorf("auth = %q, want password", got)
	}
	// Password auth hides the key-path field: name, host, auth.
	if n := len(m.fields()); n != 3 {
		t.Errorf("fields() = %d, want 3 under password auth", n)
	}
}

func TestPrevWrapsToKeyPath(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey})
	m.Prev() // wraps from name to the last field (key path)
	if got := m.current(); got != fKeyPath {
		t.Errorf("current after Prev = %d, want fKeyPath", got)
	}
}

func TestCurrentOutOfRangeFallsBackToName(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthKey})
	m.focus = 3 // key path, valid for ssh+key
	// Shrink the field list under the cursor by switching to password auth.
	m.auth = hosts.SSHAuthPassword
	if got := m.current(); got != fName {
		t.Errorf("current out of range = %d, want fName fallback", got)
	}
}

func TestTypingRoutesToFocusedField(t *testing.T) {
	m := New()
	m.OpenAdd()
	typeRunes := func(s string) {
		for _, r := range s {
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	typeRunes("box")
	if got := m.Name(); got != "box" {
		t.Errorf("name = %q, want box", got)
	}
	m.Next() // host
	typeRunes("!")
	if got := m.Host(); got != defaultHostValue+"!" {
		t.Errorf("host = %q, want appended rune", got)
	}
	m.host.SetValue("ssh://me@lab")
	m.Next() // auth (no text input — Update is a no-op there)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.Next() // key path
	typeRunes("/keys/id")
	if got := m.KeyPath(); got != "/keys/id" {
		t.Errorf("keyPath = %q, want /keys/id", got)
	}
}

func TestToggleAuthBackToKey(t *testing.T) {
	m := New()
	m.OpenEdit(hosts.Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: hosts.SSHAuthPassword})
	m.Next()
	m.Next() // auth
	if !m.OnAuthField() {
		t.Fatal("expected auth field focused")
	}
	m.ToggleAuth()
	if got := m.Auth(); got != hosts.SSHAuthKey {
		t.Errorf("auth = %q, want key after toggle back", got)
	}
}

func TestViewBranches(t *testing.T) {
	m := New()
	m.OpenAdd()
	m.SetError("boom")
	got := m.View(100, 40)
	for _, want := range []string{"Add host", "Name", "Host", "Authentication", "Key path", "boom", "tab switch"} {
		if !strings.Contains(got, want) {
			t.Errorf("view should contain %q", want)
		}
	}

	// Auth field focused: hint changes, selector markers render.
	m.Next()
	m.Next()
	got = m.View(100, 40)
	if !strings.Contains(got, "←/→/space choose") {
		t.Error("view should show the auth-selector hint")
	}
	if !strings.Contains(got, "● Key") || !strings.Contains(got, "○ Password") {
		t.Error("view should mark the selected auth option")
	}
	m.ToggleAuth()
	got = m.View(100, 40)
	if !strings.Contains(got, "● Password") || strings.Contains(got, "Key path") {
		t.Error("password auth should be selected and hide the key path")
	}

	// Non-ssh host: no auth block at all.
	m.host.SetValue("tcp://host:2375")
	got = m.View(100, 40)
	if strings.Contains(got, "Authentication") {
		t.Error("tcp host should not render the auth selector")
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
