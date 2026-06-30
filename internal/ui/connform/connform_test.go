package connform

import "testing"

func TestOpenPrefillsLoginAndFocusesPassword(t *testing.T) {
	m := New()
	m.Open("prod", "ssh://deploy@prod", "deploy")

	if m.Login() != "deploy" {
		t.Errorf("Login() = %q, want deploy", m.Login())
	}
	if m.HostName() != "prod" || m.HostURL() != "ssh://deploy@prod" {
		t.Errorf("host metadata wrong: %q %q", m.HostName(), m.HostURL())
	}
	// With a known login the password field is focused first (focus == 1).
	if m.focus != 1 {
		t.Errorf("focus = %d, want 1 (password) when login is pre-filled", m.focus)
	}
}

func TestOpenFocusesLoginWhenEmpty(t *testing.T) {
	m := New()
	m.Open("prod", "ssh://prod", "")
	if m.focus != 0 {
		t.Errorf("focus = %d, want 0 (login) when no login is known", m.focus)
	}
}

func TestPasswordNotTrimmed(t *testing.T) {
	m := New()
	m.Open("prod", "ssh://deploy@prod", "deploy")
	m.password.SetValue(" secret ")
	if got := m.Password(); got != " secret " {
		t.Errorf("Password() = %q, want spaces preserved", got)
	}
}
