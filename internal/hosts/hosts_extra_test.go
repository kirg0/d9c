package hosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyDefaultPath(t *testing.T) {
	if got := LegacyDefaultPath(); !strings.HasSuffix(got, "d9c-hosts.json") {
		t.Errorf("LegacyDefaultPath = %q", got)
	}
}

func TestLoadLegacy(t *testing.T) {
	dir := t.TempDir()

	// Missing file: no hosts, no error.
	hs, err := LoadLegacy(filepath.Join(dir, "ghost.json"))
	if err != nil || hs != nil {
		t.Errorf("missing file = %v / %v, want nil/nil", hs, err)
	}

	// Valid file.
	path := filepath.Join(dir, "d9c-hosts.json")
	if err := os.WriteFile(path, []byte(`{"hosts":[{"name":"lab","host":"ssh://me@lab"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hs, err = LoadLegacy(path)
	if err != nil || len(hs) != 1 || hs[0].Name != "lab" {
		t.Errorf("valid file = %v / %v", hs, err)
	}

	// Malformed JSON.
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacy(path); err == nil {
		t.Error("malformed JSON should fail")
	}
}

func TestAddHostBranches(t *testing.T) {
	s := NewStore([]Host{{Name: "lab", Host: "ssh://me@lab"}}, nil)
	if err := s.AddHost(Host{}); err == nil {
		t.Error("empty host should fail")
	}
	if err := s.AddHost(Host{Name: "lab", Host: "tcp://x"}); err == nil {
		t.Error("duplicate name should fail")
	}
	// Non-ssh host drops the auth metadata.
	if err := s.AddHost(Host{Name: "tcp", Host: "tcp://x:2375", SSHAuth: SSHAuthKey, SSHKeyPath: "/k"}); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Find("tcp")
	if h.SSHAuth != "" || h.SSHKeyPath != "" {
		t.Errorf("tcp host kept ssh metadata: %+v", h)
	}
	// Password auth never keeps a key path.
	if err := s.AddHost(Host{Name: "pw", Host: "ssh://u@h", SSHAuth: SSHAuthPassword, SSHKeyPath: "/k"}); err != nil {
		t.Fatal(err)
	}
	h, _ = s.Find("pw")
	if h.SSHKeyPath != "" {
		t.Errorf("password host kept key path: %+v", h)
	}
}

func TestEditHostBranches(t *testing.T) {
	s := NewStore([]Host{{Name: "a", Host: "tcp://a"}, {Name: "b", Host: "tcp://b"}}, nil)
	if err := s.EditHost("a", Host{}); err == nil {
		t.Error("empty fields should fail")
	}
	if err := s.EditHost("ghost", Host{Name: "x", Host: "tcp://x"}); err == nil {
		t.Error("unknown host should fail")
	}
	if err := s.EditHost("a", Host{Name: "b", Host: "tcp://x"}); err == nil {
		t.Error("rename onto an existing name should fail")
	}
	if err := s.EditHost("a", Host{Name: "a2", Host: "tcp://a2"}); err != nil {
		t.Errorf("edit: %v", err)
	}
}

func TestUpsertByHostNaming(t *testing.T) {
	s := NewStore(nil, nil)
	if s.UpsertByHost("  ") {
		t.Error("blank URL should not be added")
	}
	if !s.UpsertByHost("ssh://me@lab") {
		t.Error("new URL should be added")
	}
	if s.UpsertByHost("ssh://me@lab") {
		t.Error("existing URL should not be re-added")
	}
	if h := s.Hosts[0]; h.Name != "me@lab" {
		t.Errorf("derived name = %q, want me@lab", h.Name)
	}
	// A colliding derived name gets a numeric suffix.
	if !s.UpsertByHost("nerdctl+ssh://me@lab") {
		t.Error("same host under another scheme should be added")
	}
	if got := s.Hosts[1].Name; got != "me@lab-2" {
		t.Errorf("unique name = %q, want me@lab-2", got)
	}
}

func TestDeriveNameFallbacks(t *testing.T) {
	// Unparseable / hostname-less URLs fall back to the raw string.
	if got := deriveName("not a url"); got != "not a url" {
		t.Errorf("deriveName = %q", got)
	}
	if got := deriveName("tcp://box:2375"); got != "box" {
		t.Errorf("deriveName = %q", got)
	}
	// uniqueName with an empty base falls back to "host".
	s := NewStore(nil, nil)
	if got := uniqueName(s, ""); got != "host" {
		t.Errorf("uniqueName = %q", got)
	}
}
