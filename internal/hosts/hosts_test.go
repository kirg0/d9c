package hosts

import (
	"path/filepath"
	"testing"
)

func TestAddEditRemove(t *testing.T) {
	s := &Store{path: "x"}

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
	s := &Store{path: "x"}

	if added := s.UpsertByHost("ssh://kirg@192.168.1.172"); !added {
		t.Fatal("expected first upsert to add")
	}
	if added := s.UpsertByHost("ssh://kirg@192.168.1.172"); added {
		t.Error("expected duplicate URL not to be added again")
	}
	if got := s.Hosts[0].Name; got != "kirg@192.168.1.172" {
		t.Errorf("derived name = %q, want kirg@192.168.1.172", got)
	}

	// A second host that derives the same base name gets a numeric suffix.
	s2 := &Store{path: "x", Hosts: []Host{{Name: "192.168.1.172", Host: "tcp://192.168.1.172:2375"}}}
	s2.UpsertByHost("tcp://192.168.1.172:2376")
	if got := s2.Hosts[1].Name; got != "192.168.1.172-2" {
		t.Errorf("suffixed name = %q, want 192.168.1.172-2", got)
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")

	s, err := Load(path) // missing file → empty store
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("expected empty store for missing file")
	}

	_ = s.Add("local", "tcp://localhost:2375")
	_ = s.Add("prod", "ssh://user@host")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.List()) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(reloaded.List()))
	}
	if h, ok := reloaded.Find("prod"); !ok || h.Host != "ssh://user@host" {
		t.Errorf("round-trip mismatch: %+v ok=%v", h, ok)
	}
}
