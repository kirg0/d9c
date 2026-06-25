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
