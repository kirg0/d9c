package main

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/cli"
	"github.com/kirg0/d9c/internal/config"
)

// cliSpanRE finds the CLI commands a skill names: inline code that starts
// with "d9c ".
var cliSpanRE = regexp.MustCompile("`(d9c [^`]+)`")

// mcpToolRE finds the MCP tools a skill names.
var mcpToolRE = regexp.MustCompile("`d9c_[a-z_]+`")

// nonRegistryCommands are the d9c commands outside the agent registry that
// skills may name, by their first word.
var nonRegistryCommands = map[string]bool{"-version": true, mcpCommand: true, skillsCommand: true, cli.HelpCommand: true}

// shellWords splits a command line on spaces, keeping single- or
// double-quoted words together (without the quotes).
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func TestShellWords(t *testing.T) {
	got := shellWords(`d9c logs web --grep 'timeout|refused'  --since "1 h"`)
	want := []string{"d9c", "logs", "web", "--grep", "timeout|refused", "--since", "1 h"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("shellWords = %q", got)
	}
}

// skillBodies reads every shipped SKILL.md by skill name.
func skillBodies(t *testing.T) map[string]string {
	t.Helper()
	files, err := fs.Glob(skills(), "*/SKILL.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("skills: %v %v", files, err)
	}
	out := map[string]string{}
	for _, f := range files {
		raw, err := fs.ReadFile(skills(), f)
		if err != nil {
			t.Fatal(err)
		}
		out[path.Dir(f)] = string(raw)
	}
	return out
}

// Every CLI command a skill names parses against the registry: the command
// exists and takes the flags shown.
func TestSkillCLICommandsExist(t *testing.T) {
	reg := agent.Builtin()
	for name, body := range skillBodies(t) {
		for _, m := range cliSpanRE.FindAllStringSubmatch(body, -1) {
			words := shellWords(m[1])[1:]
			if len(words) == 0 || nonRegistryCommands[words[0]] || strings.HasPrefix(words[0], "<") {
				continue // another command, or the generic `d9c <command>` form
			}
			if _, err := cli.Parse(reg, &config.Config{}, words); err != nil {
				t.Errorf("%s: `%s`: %v", name, m[1], err)
			}
		}
	}
}

// skillNameRE is the Agent Skills name rule: lowercase letters, digits and
// single hyphens, not at either end.
var skillNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// skillFields are the frontmatter keys of the open Agent Skills format.
var skillFields = []string{"name", "description", "license", "compatibility", "metadata", "allowed-tools"}

// The shipped skills follow the Agent Skills format, so every agent that reads
// it (and `claude plugin validate`) accepts them.
func TestSkillsFollowAgentSkillsFormat(t *testing.T) {
	for dir, body := range skillBodies(t) {
		front, rest, err := splitFrontmatter([]byte(body))
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal(front, &m); err != nil {
			t.Errorf("%s: frontmatter is not YAML (a \": \" in an unquoted description?): %v", dir, err)
			continue
		}
		for k := range m {
			if !slices.Contains(skillFields, k) {
				t.Errorf("%s: frontmatter key %q is not in the Agent Skills format", dir, k)
			}
		}
		name, _ := m["name"].(string)
		if name != dir || len(name) > 64 || !skillNameRE.MatchString(name) {
			t.Errorf("%s: name %q must equal the directory, be at most 64 chars, lowercase and hyphenated", dir, name)
		}
		desc, _ := m["description"].(string)
		if desc == "" || len(desc) > 1024 || !strings.Contains(desc, "Use when") {
			t.Errorf("%s: description must say what the skill does and when to use it (\"Use when\"), at most 1024 chars", dir)
		}
		// Keep the body small enough to load whole (the format suggests
		// under 500 lines).
		if n := strings.Count(string(rest), "\n"); n > 500 {
			t.Errorf("%s: body has %d lines", dir, n)
		}
		if strings.Contains(body, "\r") {
			t.Errorf("%s: CRLF line endings", dir)
		}
	}
}

// destructiveRE finds a destructive MCP tool or CLI command in a skill.
var destructiveRE = regexp.MustCompile("`d9c_(?:remove|prune|exec)`|`d9c (?:rm|kill|image rm|image prune|volume prune|system prune|compose down|compose restore|exec) ")

// A skill that names a destructive command states the confirmation rule: the
// plan, then the user's explicit yes (an agent was seen removing containers
// right after a dry run when only the client's confirmation stood guard).
func TestSkillsStateTheConfirmationRule(t *testing.T) {
	for name, body := range skillBodies(t) {
		if destructiveRE.MatchString(body) && !strings.Contains(body, "explicit yes") {
			t.Errorf("%s names a destructive command but not the \"explicit yes\" rule", name)
		}
	}
}

// Every scenario skill works in both channels: it names CLI commands and MCP
// tools.
func TestSkillsCoverBothChannels(t *testing.T) {
	for name, body := range skillBodies(t) {
		if !cliSpanRE.MatchString(body) || !mcpToolRE.MatchString(body) {
			t.Errorf("%s names no CLI command or no MCP tool; each skill must work in both channels", name)
		}
	}
}
