package hosts

import (
	"reflect"
	"testing"
)

func TestAddEditRemove(t *testing.T) {
	s := &Store{}

	if err := s.Add("prod", "ssh://user@host"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add("prod", "tcp://other"); err == nil {
		t.Error("expected duplicate-name error")
	}
	if err := s.Add("", "tcp://x"); err == nil {
		t.Error("expected empty-name error")
	}

	if err := s.Edit("prod", "production", "ssh://user@newhost"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if _, ok := s.Find("prod"); ok {
		t.Error("old name should be gone after edit")
	}
	h, ok := s.Find("production")
	if !ok || h.Host != "ssh://user@newhost" {
		t.Errorf("edit did not apply: %+v ok=%v", h, ok)
	}
	if err := s.Edit("missing", "a", "b"); err == nil {
		t.Error("expected not-found error on edit")
	}

	if err := s.Remove("production"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(s.List()) != 0 {
		t.Errorf("expected empty store, got %d", len(s.List()))
	}
	if err := s.Remove("production"); err == nil {
		t.Error("expected not-found error on remove")
	}
}

func TestUpsertByHost(t *testing.T) {
	s := &Store{}

	if added := s.UpsertByHost("ssh://deploy@10.0.0.5"); !added {
		t.Fatal("expected first upsert to add")
	}
	if added := s.UpsertByHost("ssh://deploy@10.0.0.5"); added {
		t.Error("expected duplicate URL not to be added again")
	}
	if got := s.Hosts[0].Name; got != "deploy@10.0.0.5" {
		t.Errorf("derived name = %q, want deploy@10.0.0.5", got)
	}

	// A second host that derives the same base name gets a numeric suffix.
	s2 := &Store{Hosts: []Host{{Name: "10.0.0.5", Host: "tcp://10.0.0.5:2375"}}}
	s2.UpsertByHost("tcp://10.0.0.5:2376")
	if got := s2.Hosts[1].Name; got != "10.0.0.5-2" {
		t.Errorf("suffixed name = %q, want 10.0.0.5-2", got)
	}
}

func TestAddEditHostAuthMetadata(t *testing.T) {
	s := &Store{}

	// Key auth with a custom path is preserved verbatim.
	if err := s.AddHost(Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: SSHAuthKey, SSHKeyPath: "/keys/id"}); err != nil {
		t.Fatalf("AddHost key: %v", err)
	}
	h, _ := s.Find("lab")
	if h.SSHAuth != SSHAuthKey || h.SSHKeyPath != "/keys/id" {
		t.Errorf("key auth not stored: %+v", h)
	}

	// Password auth drops any stray key path (never stored for password).
	if err := s.AddHost(Host{Name: "prod", Host: "ssh://me@prod", SSHAuth: SSHAuthPassword, SSHKeyPath: "/keys/id"}); err != nil {
		t.Fatalf("AddHost password: %v", err)
	}
	h, _ = s.Find("prod")
	if h.SSHAuth != SSHAuthPassword || h.SSHKeyPath != "" {
		t.Errorf("password auth should drop key path: %+v", h)
	}

	// Non-ssh hosts carry no SSH auth metadata even if supplied.
	if err := s.AddHost(Host{Name: "tcp", Host: "tcp://x:2375", SSHAuth: SSHAuthKey, SSHKeyPath: "/k"}); err != nil {
		t.Fatalf("AddHost tcp: %v", err)
	}
	h, _ = s.Find("tcp")
	if h.SSHAuth != "" || h.SSHKeyPath != "" {
		t.Errorf("non-ssh host should have no auth metadata: %+v", h)
	}

	// EditHost updates the method and clears the key path when switching to password.
	if err := s.EditHost("lab", Host{Name: "lab", Host: "ssh://me@lab", SSHAuth: SSHAuthPassword}); err != nil {
		t.Fatalf("EditHost: %v", err)
	}
	h, _ = s.Find("lab")
	if h.SSHAuth != SSHAuthPassword || h.SSHKeyPath != "" {
		t.Errorf("edit to password failed: %+v", h)
	}

	// Legacy Edit clears auth metadata (the old 3-arg path).
	if err := s.Edit("prod", "prod", "ssh://me@prod"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	h, _ = s.Find("prod")
	if h.SSHAuth != "" {
		t.Errorf("legacy Edit should clear auth: %+v", h)
	}
}

func TestSSHUserHelpers(t *testing.T) {
	cases := []struct {
		url, user, want string
	}{
		{"ssh://deploy@host:22", "", "deploy"},
		{"ssh://host", "", ""},
		{"tcp://host:2375", "", ""},
		{"nerdctl+ssh://cont@192.168.1.249", "", "cont"},
		{"nerdctl+ssh://host", "", ""},
		{"nerdctl://", "", ""},
	}
	for _, c := range cases {
		if got := SSHUser(c.url); got != c.want {
			t.Errorf("SSHUser(%q) = %q, want %q", c.url, got, c.want)
		}
	}

	repl := []struct {
		url, user, want string
	}{
		{"ssh://old@host:22", "new", "ssh://new@host:22"},
		{"ssh://host:22", "new", "ssh://new@host:22"},
		{"ssh://old@host", "", "ssh://host"},
		{"tcp://host:2375", "new", "tcp://host:2375"},
		{"nerdctl+ssh://old@host:22", "new", "nerdctl+ssh://new@host:22"},
		{"nerdctl+ssh://host", "cont", "nerdctl+ssh://cont@host"},
	}
	for _, c := range repl {
		if got := WithSSHUser(c.url, c.user); got != c.want {
			t.Errorf("WithSSHUser(%q,%q) = %q, want %q", c.url, c.user, got, c.want)
		}
	}
}

func TestIsSSHAndAuthKept(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"ssh://user@host", true},
		{"nerdctl+ssh://cont@host", true},
		{"nerdctl://", false},
		{"tcp://host:2375", false},
		{"unix:///run/docker.sock", false},
	} {
		if got := IsSSH(c.url); got != c.want {
			t.Errorf("IsSSH(%q) = %v, want %v", c.url, got, c.want)
		}
	}

	// A nerdctl+ssh:// host must keep its password-auth metadata (it used to be
	// wiped because normalized() only recognized ssh://).
	s := NewStore(nil, nil)
	if err := s.AddHost(Host{Name: "c", Host: "nerdctl+ssh://cont@host", SSHAuth: SSHAuthPassword}); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Find("c")
	if h.SSHAuth != SSHAuthPassword {
		t.Errorf("SSHAuth = %q, want password (nerdctl+ssh auth was dropped)", h.SSHAuth)
	}
}

// TestPersistCallback verifies Save forwards the current list to the injected
// callback, and that a zero-value store (no callback) treats Save as a no-op.
func TestPersistCallback(t *testing.T) {
	var saved []Host
	s := NewStore([]Host{{Name: "seed", Host: "tcp://seed:2375"}}, func(list []Host) error {
		saved = list
		return nil
	})
	if _, ok := s.Find("seed"); !ok {
		t.Fatal("NewStore did not seed initial hosts")
	}
	_ = s.Add("prod", "ssh://user@host")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := []Host{{Name: "seed", Host: "tcp://seed:2375"}, {Name: "prod", Host: "ssh://user@host"}}
	if !reflect.DeepEqual(saved, want) {
		t.Errorf("persisted = %+v, want %+v", saved, want)
	}

	// Zero-value store: Save must not panic and must be a no-op.
	if err := (&Store{}).Save(); err != nil {
		t.Errorf("zero-value Save: %v", err)
	}
}
