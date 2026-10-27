package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/config"
)

// updateGolden rewrites the golden files from the current output:
//
//	go test -run 'TestCLIGolden|TestOpCatalogGolden' -update .
var updateGolden = flag.Bool("update", false, "rewrite testdata/golden from the current CLI output")

const goldenDir = "testdata/golden"

// timestampRE matches the RFC3339 times the demo backend derives from
// time.Now (created fields, compose working state); golden files hold a
// placeholder instead so they stay stable from run to run.
var timestampRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

// goldenCase is one CLI invocation on the demo backend. The golden file holds
// the stream the contract defines for the outcome — the stdout envelope on
// success, the stderr error document on failure — so any change of field
// names, nesting or error codes shows up as a diff.
type goldenCase struct {
	name string
	args []string // -demo is appended unless --host picks a saved host
	exit int
	// write raises the CLI tier ceiling to destructive for the case; without
	// it a write command answers forbidden, as it does for users today.
	write bool
}

// cliGoldenCases covers every built-in operation at least once (checked by
// TestCLIGoldenCoversRegistry) plus each error code the demo backend can
// produce.
var cliGoldenCases = []goldenCase{
	{"hosts_ls", []string{"hosts", "ls"}, 0, false},
	{"info", []string{"info"}, 0, false},
	{"ps", []string{"ps"}, 0, false},
	{"ps_all", []string{"ps", "-a"}, 0, false},
	{"ps_fields_limit", []string{"ps", "-a", "--fields", "name,state", "--limit", "1"}, 0, false},
	{"inspect_container", []string{"inspect", "web"}, 0, false},
	{"inspect_image", []string{"inspect", "--type", "image", "a08c488a9779"}, 0, false},
	{"logs_tail", []string{"logs", "web", "-n", "3"}, 0, false},
	{"logs_grep", []string{"logs", "web", "--grep", "ERROR"}, 0, false},
	{"stats", []string{"stats"}, 0, false},
	{"images", []string{"images"}, 0, false},
	{"networks", []string{"networks"}, 0, false},
	{"volumes", []string{"volumes"}, 0, false},
	{"system_df", []string{"system", "df"}, 0, false},
	{"events", []string{"events", "--since", "2026-01-01T00:00:00Z", "--until", "2026-01-01T01:00:00Z"}, 0, false},
	{"compose_ls", []string{"compose", "ls"}, 0, false},
	{"compose_ps", []string{"compose", "ps", "webapp"}, 0, false},
	{"compose_config", []string{"compose", "config", "webapp"}, 0, false},
	{"compose_logs", []string{"compose", "logs", "webapp", "--tail", "2"}, 0, false},
	{"start", []string{"start", "db"}, 0, true},
	{"stop_targets", []string{"stop", "web", "api"}, 0, true},
	{"stop_filter", []string{"stop", "--filter", "label=env=prod"}, 0, true},
	{"restart_prefix", []string{"restart", "9ae"}, 0, true},
	{"kill_signal", []string{"kill", "web", "-s", "TERM"}, 0, true},
	{"rm_force", []string{"rm", "-f", "web"}, 0, true},
	{"partial_stop", []string{"stop", "web", "ghost"}, agent.ExitPartial, true},

	{"err_usage_flag", []string{"ps", "--bogus"}, 2, false},
	{"err_usage_field", []string{"ps", "--fields", "bogus"}, 2, false},
	{"err_usage_missing_arg", []string{"inspect"}, 2, false},
	{"err_not_found_container", []string{"inspect", "ghost"}, 3, false},
	{"err_not_found_compose", []string{"compose", "ps", "ghost"}, 3, false},
	{"err_not_found_host", []string{"info", "--host", "nope"}, 3, false},
	{"err_auth_required", []string{"info", "--host", "pw"}, 6, false},
	{"err_forbidden", []string{"stop", "web"}, 5, false},
	{"err_usage_no_targets", []string{"stop"}, 2, true},
	{"err_not_found_target", []string{"start", "ghost"}, 3, true},
}

func TestCLIGolden(t *testing.T) {
	cfgPath := writeCLIConfig(t, "  - name: prod\n    host: tcp://127.0.0.1:1\n    read_only: true\n  - name: pw\n    host: ssh://u@pw.invalid\n    ssh_auth: password\n")
	for _, tc := range cliGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{}, tc.args...)
			// -demo would win over --host; those cases fail before dialing.
			if !slices.Contains(args, "--host") {
				args = append(args, "-demo")
			}
			if tc.write {
				cliCeiling = agent.TierDestructive
				t.Cleanup(func() { cliCeiling = agent.TierRead })
			}
			code, out, errOut := cliRun(t, &config.Config{ConfigFile: cfgPath}, args...)
			if code != tc.exit {
				t.Fatalf("exit %d, want %d (stdout %q, stderr %q)", code, tc.exit, out, errOut)
			}
			got, other := out, errOut
			// A partial result is still an envelope on stdout.
			if tc.exit != 0 && tc.exit != agent.ExitPartial {
				got, other = errOut, out
			}
			if other != "" {
				t.Errorf("unexpected output on the other stream: %q", other)
			}
			if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Errorf("want exactly one JSON line, got %q", got)
			}
			compareGolden(t, tc.name, []byte(got))
		})
	}
}

// Every registered operation needs a golden case: a new op without one fails
// here instead of shipping an unpinned output schema.
func TestCLIGoldenCoversRegistry(t *testing.T) {
	reg := agent.Builtin()
	covered := map[string]bool{}
	for _, tc := range cliGoldenCases {
		if tc.exit != 0 {
			continue
		}
		inv := tc.args
		for n := len(inv); n > 0; n-- {
			if _, ok := reg.Lookup(strings.Join(inv[:n], " ")); ok {
				covered[strings.Join(inv[:n], " ")] = true
				break
			}
		}
	}
	for _, op := range reg.Ops() {
		if !covered[op.Name] {
			t.Errorf("operation %q has no successful golden case in cliGoldenCases", op.Name)
		}
	}
}

// opCatalogEntry is the frontend-facing declaration of an operation — what
// help lists and what MCP will publish as a tool.
type opCatalogEntry struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Tier        agent.Tier    `json:"tier"`
	Local       bool          `json:"local,omitempty"`
	Args        *agent.Schema `json:"args"`
	// Short maps a property to its one-letter CLI alias, which the JSON
	// Schema leaves out.
	Short map[string]string `json:"cli_short,omitempty"`
}

// The operation catalog (names, tiers, argument schemas) is a contract of its
// own: renaming an argument breaks every agent script that passes it.
func TestOpCatalogGolden(t *testing.T) {
	var cat []opCatalogEntry
	for _, op := range agent.Builtin().Ops() {
		e := opCatalogEntry{Name: op.Name, Description: op.Description(), Tier: op.Tier, Local: op.Local, Args: op.Args}
		for name, p := range op.Args.Properties {
			if p.Short != "" {
				if e.Short == nil {
					e.Short = map[string]string{}
				}
				e.Short[name] = p.Short
			}
		}
		cat = append(cat, e)
	}
	raw, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "op_catalog", raw)
}

// compareGolden checks got (one JSON document) against testdata/golden/<name>.json
// after masking timestamps; -update rewrites the file. Files are indented so a
// schema change reads as a small diff in review.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	norm := timestampRE.ReplaceAll(bytes.TrimSpace(got), []byte("<time>"))
	var buf bytes.Buffer
	if err := json.Indent(&buf, norm, "", "  "); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, got)
	}
	buf.WriteByte('\n')

	path := filepath.Join(goldenDir, name+".json")
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file (run with -update): %v", err)
	}
	// Git on Windows may check text files out with CRLF.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("%s differs from the golden file (run with -update if the change is intended)\n--- got\n%s\n--- want\n%s", path, buf.Bytes(), want)
	}
}
