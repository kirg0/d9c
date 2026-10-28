package main

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

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
var nonRegistryCommands = map[string]bool{"-version": true, mcpCommand: true, "skills": true, cli.HelpCommand: true}

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

// Every scenario skill works in both channels: it names CLI commands and MCP
// tools.
func TestSkillsCoverBothChannels(t *testing.T) {
	for name, body := range skillBodies(t) {
		if !cliSpanRE.MatchString(body) || !mcpToolRE.MatchString(body) {
			t.Errorf("%s names no CLI command or no MCP tool; each skill must work in both channels", name)
		}
	}
}
