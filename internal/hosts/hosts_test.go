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
		{"crio+ssh://core@node1", "", "core"},
		{"cri+ssh://core@node1/run/crio/crio.sock", "", "core"},
		{"crio://", "", ""},
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
		{"crio+ssh://old@node1", "core", "crio+ssh://core@node1"},
		{"cri+ssh://node1", "core", "cri+ssh://core@node1"},
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
		{"crio+ssh://core@node1", true},
		{"cri+ssh://core@node1", true},
		{"nerdctl://", false},
		{"crio://", false},
		{"cri://", false},
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

func TestReadOnlyFor(t *testing.T) {
	s := NewStore([]Host{
		{Name: "prod", Host: "ssh://root@prod", ReadOnly: true},
		{Name: "dev", Host: "tcp://dev:2375"},
	}, nil)
	tests := []struct {
		url  string
		want bool
	}{
		{"ssh://root@prod", true},
		{"tcp://dev:2375", false},
		{"tcp://adhoc:2375", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := s.ReadOnlyFor(tt.url); got != tt.want {
			t.Errorf("ReadOnlyFor(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
	var nilStore *Store
	if nilStore.ReadOnlyFor("ssh://root@prod") {
		t.Error("nil store must not report read-only")
	}
}

// ReadOnly lives only in the config file (the host form doesn't expose it), so
// an edit from the UI must keep it rather than silently clearing it.
func TestEditHostKeepsReadOnly(t *testing.T) {
	s := NewStore([]Host{{Name: "prod", Host: "ssh://root@prod", ReadOnly: true}}, nil)
	if err := s.EditHost("prod", Host{Name: "prod2", Host: "ssh://admin@prod"}); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Find("prod2"); !h.ReadOnly {
		t.Error("EditHost dropped read_only")
	}
	if err := s.Edit("prod2", "prod3", "ssh://root@prod"); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Find("prod3"); !h.ReadOnly {
		t.Error("Edit dropped read_only")
	}
}

func TestHostTLSNormalization(t *testing.T) {
	tls := Host{TLSCACert: "ca", TLSCert: "cert", TLSKey: "key"}
	if !tls.HasTLS() || (Host{}).HasTLS() {
		t.Fatal("HasTLS mismatch")
	}
	tests := []struct {
		url     string
		keepTLS bool
	}{
		{"tcp://prod:2376", true},
		{"ssh://ops@prod", false},
		{"unix:///var/run/docker.sock", false},
		{"npipe:////./pipe/docker_engine", false},
	}
	for _, tt := range tests {
		h := tls
		h.Name, h.Host = "x", tt.url
		if got := h.normalized().HasTLS(); got != tt.keepTLS {
			t.Errorf("normalized(%s).HasTLS = %v, want %v", tt.url, got, tt.keepTLS)
		}
	}
}

// TLS files are not exposed by the host form, so a UI edit keeps them — but
// only while the host stays tcp://.
func TestEditHostKeepsTLS(t *testing.T) {
	s := NewStore([]Host{{Name: "prod", Host: "tcp://prod:2376", TLSCACert: "ca", TLSCert: "cert", TLSKey: "key"}}, nil)
	if err := s.EditHost("prod", Host{Name: "prod", Host: "tcp://prod2:2376"}); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Find("prod"); h.TLSCACert != "ca" || h.TLSCert != "cert" || h.TLSKey != "key" {
		t.Errorf("EditHost dropped TLS: %+v", h)
	}
	// Explicit TLS in the edit wins over the stored one.
	if err := s.EditHost("prod", Host{Name: "prod", Host: "tcp://prod2:2376", TLSCACert: "ca2"}); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Find("prod"); h.TLSCACert != "ca2" || h.TLSCert != "" {
		t.Errorf("explicit TLS not applied: %+v", h)
	}
	if err := s.EditHost("prod", Host{Name: "prod", Host: "ssh://ops@prod"}); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Find("prod"); h.HasTLS() {
		t.Errorf("TLS must be dropped for ssh://: %+v", h)
	}
}

func TestFindByURL(t *testing.T) {
	s := NewStore([]Host{{Name: "a", Host: "tcp://a:2375"}, {Name: "b", Host: "tcp://a:2375"}}, nil)
	if h, ok := s.FindByURL("tcp://a:2375"); !ok || h.Name != "a" {
		t.Errorf("FindByURL = %+v, %v; want the first match", h, ok)
	}
	if _, ok := s.FindByURL("tcp://none"); ok {
		t.Error("unknown URL found")
	}
	var nilStore *Store
	if _, ok := nilStore.FindByURL("tcp://a:2375"); ok {
		t.Error("nil store found a host")
	}
}

func TestImport(t *testing.T) {
	s := NewStore([]Host{{Name: "prod", Host: "tcp://old:2375"}, {Name: "lab", Host: "ssh://ops@lab"}}, nil)
	tests := []struct {
		name     string
		in       Host
		wantRes  ImportResult
		wantName string
	}{
		{"new", Host{Name: " dev ", Host: "tcp://dev:2376", TLSCACert: "ca"}, Imported, "dev"},
		{"name clash", Host{Name: "prod", Host: "tcp://prod:2376"}, Imported, "prod-2"},
		{"same URL", Host{Name: "other", Host: "ssh://ops@lab"}, ImportSkipped, "lab"},
		{"empty name", Host{Host: "tcp://x"}, ImportInvalid, ""},
		{"empty url", Host{Name: "x"}, ImportInvalid, ""},
	}
	for _, tt := range tests {
		res, name := s.Import(tt.in)
		if res != tt.wantRes || name != tt.wantName {
			t.Errorf("%s: Import = %v, %q; want %v, %q", tt.name, res, name, tt.wantRes, tt.wantName)
		}
	}
	if len(s.Hosts) != 4 {
		t.Fatalf("hosts = %+v, want 4", s.Hosts)
	}
	if h, _ := s.Find("dev"); h.TLSCACert != "ca" {
		t.Errorf("imported TLS lost: %+v", h)
	}
}
