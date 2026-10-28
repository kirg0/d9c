package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/kirg0/d9c/internal/version"
)

// runSkillsJSON runs `d9c skills args...` on the embedded skills and decodes
// the envelope data into v.
func runSkillsJSON(t *testing.T, v any, args ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := runSkills(skills(), args, &out, &errb); code != 0 {
		t.Fatalf("skills %v: exit %d, stderr %s", args, code, errb.String())
	}
	var env struct {
		Schema int             `json:"schema"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil || env.Schema != 1 {
		t.Fatalf("stdout %q: %v", out.String(), err)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsLs(t *testing.T) {
	var d SkillsListData
	runSkillsJSON(t, &d, "ls")
	if d.Version != version.Version || len(d.Skills) < 10 || d.Skills[0].Name != "d9c" || d.Skills[0].Description == "" {
		t.Errorf("ls = %+v", d)
	}
	if len(d.Targets) != len(skillTargets) {
		t.Errorf("targets = %+v", d.Targets)
	}
}

func TestSkillsInstallDir(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	var dry SkillsInstallData
	runSkillsJSON(t, &dry, "install", "--dir", dir, "--dry-run")
	if !dry.DryRun || len(dry.Skills) < 10 || dry.Skills[0].Result != "created" {
		t.Fatalf("dry run = %+v", dry)
	}
	if _, err := os.Stat(filepath.Join(dir, "d9c")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote files: %v", err)
	}

	var first SkillsInstallData
	runSkillsJSON(t, &first, "install", "--dir", dir)
	if first.Dir != dir || first.Version != version.Version {
		t.Errorf("install = %+v", first)
	}
	for _, s := range first.Skills {
		raw, err := os.ReadFile(s.Path)
		if err != nil || s.Result != "created" {
			t.Fatalf("%s: %v %s", s.Name, err, s.Result)
		}
		front, body, err := splitFrontmatter(raw)
		var meta struct {
			Name     string            `yaml:"name"`
			Metadata map[string]string `yaml:"metadata"`
		}
		if err != nil || yaml.Unmarshal(front, &meta) != nil || meta.Name != s.Name ||
			meta.Metadata["d9c-version"] != version.Version || len(bytes.TrimSpace(body)) == 0 {
			t.Errorf("%s: frontmatter %+v, %v", s.Name, meta, err)
		}
	}

	// A changed copy is replaced, an identical one left as it is.
	if err := os.WriteFile(first.Skills[1].Path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	var second SkillsInstallData
	runSkillsJSON(t, &second, "install", "--dir="+dir)
	if second.Skills[0].Result != "unchanged" || second.Skills[1].Result != "updated" {
		t.Errorf("reinstall = %+v", second.Skills[:2])
	}
	if raw, _ := os.ReadFile(other); string(raw) != "mine" {
		t.Errorf("another skill was touched: %q", raw)
	}
}

func TestSkillsInstallTarget(t *testing.T) {
	home := t.TempDir()
	old := userHome
	userHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHome = old })
	var d SkillsInstallData
	runSkillsJSON(t, &d, "install", "--target", "opencode")
	want := filepath.Join(home, ".config", "opencode", "skills")
	if d.Dir != want {
		t.Errorf("dir = %s, want %s", d.Dir, want)
	}
	if _, err := os.Stat(filepath.Join(want, "d9c-logs", "SKILL.md")); err != nil {
		t.Error(err)
	}
}

func TestSkillsErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"install"}, "pass --target"},
		{[]string{"install", "--target", "claude", "--dir", "x"}, "not both"},
		{[]string{"install", "--target", "vim"}, "unknown target"},
		{[]string{"install", "--bogus"}, "flag provided but not defined"},
		{[]string{"install", "--dir", "x", "extra"}, "unexpected argument"},
		{[]string{"ls", "x"}, "unexpected argument"},
		{[]string{"remove"}, "unknown skills command"},
	}
	for _, tt := range tests {
		var out, errb bytes.Buffer
		code := runSkills(skills(), tt.args, &out, &errb)
		if code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), tt.want) || !strings.Contains(errb.String(), `"code":"usage"`) {
			t.Errorf("%v: exit %d, stderr %s", tt.args, code, errb.String())
		}
	}
}

func TestSkillsHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"-h"}, {"help"}, {"install", "-h"}} {
		var out, errb bytes.Buffer
		if code := runSkills(skills(), args, &out, &errb); code != 0 || !strings.Contains(out.String(), "opencode") || errb.Len() != 0 {
			t.Errorf("%v: exit %d, out %q, err %q", args, code, out.String(), errb.String())
		}
	}
}

func TestSkillsBrokenEmbed(t *testing.T) {
	bad := fstest.MapFS{"x/SKILL.md": {Data: []byte("no frontmatter")}}
	var out, errb bytes.Buffer
	if code := runSkills(bad, []string{"ls"}, &out, &errb); code == 0 {
		t.Error("a skill without frontmatter was listed")
	}
	if _, err := stampVersion([]byte("---\nname: x\n"), "1"); err == nil {
		t.Error("unterminated frontmatter stamped")
	}
}

// run() hands the skills word to runSkills (a usage error proves it got there
// without starting the TUI).
func TestRunDispatchesSkills(t *testing.T) {
	err := runWithArgs(t, "skills", "bogus")
	if code, ok := err.(exitCode); !ok || code != 2 {
		t.Errorf("run skills bogus = %v, want exit status 2", err)
	}
}
