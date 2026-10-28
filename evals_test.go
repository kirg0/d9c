package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kirg0/d9c/internal/docker"
)

// evalDir is the plugin's claude plugin eval suite.
var evalDir = filepath.Join(pluginDir, "evals")

// mcpToolPrefix is how Claude Code names the tools of the plugin's d9c server.
const mcpToolPrefix = "mcp__plugin_d9c_d9c__"

// promptFields are the prompt.md frontmatter keys claude plugin eval accepts
// (an unknown key fails the case).
var promptFields = []string{"schema_version", "name", "description", "tags", "plugins", "runs",
	"expected_outcome", "model", "max_turns", "timeout_seconds", "allowed_tools", "append_system_prompt", "env"}

// graderTypes are the grader types claude plugin eval knows.
var graderTypes = []string{"regex", "tool_used", "tool_order", "file_exists", "llm", "baseline"}

var evalEnvRE = regexp.MustCompile(`^EVAL_[A-Z0-9_]*$`)

// frontmatter splits a Markdown file into its YAML frontmatter, decoded, and
// its body.
func frontmatter(t *testing.T, path string) (map[string]any, string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(front, &m); err != nil {
		t.Fatalf("%s: frontmatter: %v", path, err)
	}
	return m, string(body)
}

// mcpToolNames reads the tool names from the MCP catalog golden file.
func mcpToolNames(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("internal", "mcpserver", "testdata", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tools []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(tools))
	for i, tl := range tools {
		names[i] = tl.Name
	}
	return names
}

// checkGraderTool fails unless tool is a built-in or a tool of the plugin's
// MCP server.
func checkGraderTool(t *testing.T, where string, tool any, mcpTools []string) {
	t.Helper()
	name, _ := tool.(string)
	if m, ok := tool.(map[string]any); ok {
		name, _ = m["tool"].(string)
	}
	if mcp, ok := strings.CutPrefix(name, mcpToolPrefix); ok {
		if !slices.Contains(mcpTools, mcp) {
			t.Errorf("%s: unknown MCP tool %s", where, name)
		}
		return
	}
	if !slices.Contains([]string{"Skill", "Read", "Glob", "Grep", "Bash"}, name) {
		t.Errorf("%s: unexpected tool %q", where, name)
	}
}

// The eval suite is well formed, runs on demo scenarios that exist, names
// only real tools and skills, and covers every skill.
func TestEvalSuite(t *testing.T) {
	cases, err := filepath.Glob(filepath.Join(evalDir, "*", "prompt.md"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no eval cases: %v", err)
	}
	skills := skillBodies(t)
	mcpTools := mcpToolNames(t)
	fired := map[string]bool{}
	for _, prompt := range cases {
		dir := filepath.Dir(prompt)
		name := filepath.Base(dir)
		front, body := frontmatter(t, prompt)
		for k := range front {
			if !slices.Contains(promptFields, k) {
				t.Errorf("%s: unknown prompt.md field %q", name, k)
			}
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s: empty prompt", name)
		}
		env, _ := front["env"].(map[string]any)
		for k := range env {
			if !evalEnvRE.MatchString(k) {
				t.Errorf("%s: env %q must match EVAL_*", name, k)
			}
		}
		if sc, _ := env["EVAL_D9C_SCENARIO"].(string); sc == "" {
			t.Errorf("%s: no EVAL_D9C_SCENARIO: the real MCP server would reach a real host", name)
		} else if _, ok := docker.LookupFakeScenario(sc); !ok {
			t.Errorf("%s: unknown demo scenario %q", name, sc)
		}

		graders, _ := filepath.Glob(filepath.Join(dir, "graders", "*.md"))
		if len(graders) == 0 {
			t.Errorf("%s: no graders", name)
		}
		for _, g := range graders {
			where := name + "/" + filepath.Base(g)
			gf, gbody := frontmatter(t, g)
			typ, _ := gf["type"].(string)
			if !slices.Contains(graderTypes, typ) {
				t.Errorf("%s: grader type %q", where, gf["type"])
			}
			switch typ {
			case "llm":
				if !strings.Contains(gbody, "PASS") || !strings.Contains(gbody, "FAIL") {
					t.Errorf("%s: an llm rubric needs PASS and FAIL conditions", where)
				}
			case "tool_used":
				checkGraderTool(t, where, gf["tool"], mcpTools)
				minN, hasMin := gf["min"].(int)
				if maxN, ok := gf["max"].(int); ok && maxN == 0 && (!hasMin || minN != 0) {
					t.Errorf("%s: max 0 needs min 0 (the default min is 1)", where)
				}
				if gf["tool"] == "Skill" {
					// '"skill"\s*:\s*"(?:[\w-]+:)?<name pattern>"' — the closing
					// quote is left out to match every d9c skill by prefix.
					m, _ := gf["input_match"].(string)
					_, pat, ok := strings.Cut(m, `(?:[\w-]+:)?`)
					if !ok {
						t.Errorf("%s: the Skill input_match %q must allow the plugin prefix (?:[\\w-]+:)?", where, m)
						continue
					}
					exact := strings.HasSuffix(pat, `"`)
					re, err := regexp.Compile(`^(?:` + strings.TrimSuffix(pat, `"`) + `)`)
					if err != nil {
						t.Errorf("%s: %v", where, err)
						continue
					}
					hit := false
					for s := range skills {
						if loc := re.FindStringIndex(s); loc != nil && (!exact || loc[1] == len(s)) {
							hit = true
							if mx, ok := gf["max"].(int); !ok || mx != 0 {
								fired[s] = true
							}
						}
					}
					if !hit {
						t.Errorf("%s: %q names no shipped skill", where, m)
					}
				}
			case "tool_order":
				checkGraderTool(t, where, gf["before"], mcpTools)
				checkGraderTool(t, where, gf["after"], mcpTools)
			}
		}
	}
	for s := range skills {
		if !fired[s] {
			t.Errorf("skill %s has no eval case checking that it fires", s)
		}
	}
}

// The suite drives the plugin's own MCP server: the server key in .mcp.json is
// the one the grader tool names assume, and it receives the scenario.
func TestEvalSuiteServer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(pluginDir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	srv, ok := c.MCPServers["d9c"]
	if !ok || !strings.HasPrefix(mcpToolPrefix, "mcp__plugin_d9c_d9c") {
		t.Fatalf("servers = %+v", c.MCPServers)
	}
	// Empty outside evals: the variable must never select demo data on a
	// real installation.
	if srv.Env["D9C_DEMO_SCENARIO"] != "${EVAL_D9C_SCENARIO:-}" {
		t.Errorf("env = %v", srv.Env)
	}
}
