package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "d9c-plugins.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFile(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(s.All()) != 0 {
		t.Errorf("want empty set, got %d", len(s.All()))
	}
}

func TestLoadAndNormalize(t *testing.T) {
	path := writeYAML(t, `
plugins:
  - name: dive
    key: Ctrl+D
    scope: Images
    command: dive
    args: ["${ID}"]
  - name: df
    command: docker
    args: ["system", "df"]
    background: true
`)
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("got %d plugins, want 2", len(all))
	}
	dive := all[0]
	if dive.Key != "ctrl+d" {
		t.Errorf("key = %q, want lowercased ctrl+d", dive.Key)
	}
	if dive.Scope != "images" {
		t.Errorf("scope = %q, want lowercased images", dive.Scope)
	}
	df := all[1]
	if df.Scope != "*" {
		t.Errorf("default scope = %q, want *", df.Scope)
	}
	if !df.Background {
		t.Error("df.Background = false, want true")
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]string{
		"missing name":    "plugins:\n  - command: dive\n",
		"missing command": "plugins:\n  - name: dive\n",
		"bad scope":       "plugins:\n  - name: dive\n    command: dive\n    scope: pods\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeYAML(t, content)); err == nil {
				t.Errorf("%s: expected an error", name)
			}
		})
	}
}

func TestForScopeAndLookup(t *testing.T) {
	s := New([]Plugin{
		{Name: "a", Scope: "containers", Command: "x"},
		{Name: "b", Scope: "*", Command: "x", Key: "ctrl+b"},
		{Name: "c", Scope: "images", Command: "x"},
	})

	if got := s.ForScope("containers"); len(got) != 2 {
		t.Errorf("ForScope(containers) = %d plugins, want 2 (a + wildcard b)", len(got))
	}
	if _, ok := s.Lookup("containers", "b"); !ok {
		t.Error("wildcard plugin b should be found in containers scope")
	}
	if _, ok := s.Lookup("containers", "c"); ok {
		t.Error("images-scoped plugin c must not be found in containers scope")
	}
	if _, ok := s.Lookup("images", "c"); !ok {
		t.Error("plugin c should be found in its own scope")
	}
	if _, ok := s.ByKey("volumes", "ctrl+b"); !ok {
		t.Error("wildcard key ctrl+b should be found in any scope")
	}
	if _, ok := s.ByKey("containers", "ctrl+z"); ok {
		t.Error("unbound key must not match")
	}
}

func TestSubstitute(t *testing.T) {
	p := Plugin{Command: "echo", Args: []string{"${NAME}", "x-${ID}-y", "${UNKNOWN}"}}
	cmd, args := Substitute(p, map[string]string{"NAME": "web", "ID": "abc"})
	if cmd != "echo" {
		t.Errorf("command = %q, want echo", cmd)
	}
	want := []string{"web", "x-abc-y", "${UNKNOWN}"}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestNilSetSafe ensures all accessors tolerate a nil *Set.
func TestNilSetSafe(t *testing.T) {
	var s *Set
	if s.All() != nil || s.ForScope("containers") != nil {
		t.Error("nil set should return nil slices")
	}
	if _, ok := s.Lookup("containers", "x"); ok {
		t.Error("nil set Lookup should report not found")
	}
	if _, ok := s.ByKey("containers", "x"); ok {
		t.Error("nil set ByKey should report not found")
	}
}
