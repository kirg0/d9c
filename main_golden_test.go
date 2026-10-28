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

// goldenDir is absolute: some cases run in a temporary working directory.
var goldenDir = func() string {
	dir, err := filepath.Abs(filepath.Join("testdata", "golden"))
	if err != nil {
		panic(err)
	}
	return dir
}()

// timestampRE matches the RFC3339 times the demo backend derives from
// time.Now (created fields, compose working state); golden files hold a
// placeholder instead so they stay stable from run to run.
var timestampRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

// backupPathRE matches a backup archive path as a JSON string: it carries the
// temporary directory and the time of the run.
var backupPathRE = regexp.MustCompile(`"[^"]*-\d{8}-\d{6}\.tar\.gz"`)

// goldenCase is one CLI invocation on the demo backend. The golden file holds
// the stream the contract defines for the outcome — the stdout envelope on
// success, the stderr error document on failure — so any change of field
// names, nesting or error codes shows up as a diff.
type goldenCase struct {
	name string
	args []string // -demo is appended unless --host picks a saved host
	exit int
	// write runs the case with a config that sets agent: {allowWrite: true,
	// maxTier: destructive}; without it a write command answers forbidden,
	// as it does under the default policy.
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
	{"stats_positional", []string{"stats", "web"}, 0, false},
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
	{"kill_signal", []string{"kill", "web", "-s", "TERM", "-y"}, 0, true},
	{"rm_force", []string{"rm", "-f", "web", "--yes"}, 0, true},
	{"partial_stop", []string{"stop", "web", "ghost"}, agent.ExitPartial, true},
	{"pull", []string{"pull", "nginx:1.27"}, 0, true},
	{"compose_up", []string{"compose", "up", "legacy"}, 0, true},
	{"compose_down", []string{"compose", "down", "webapp", "--yes"}, 0, true},
	{"compose_pull", []string{"compose", "pull", "webapp"}, 0, true},
	{"compose_restart", []string{"compose", "restart", "/srv/monitoring"}, 0, true},
	{"compose_backup", []string{"compose", "backup", "webapp"}, 0, true},
	{"compose_restore", []string{"compose", "restore", "webapp", "webapp.tar.gz", "--yes"}, 0, true},
	{"network_create", []string{"network", "create", "edge", "--subnet", "172.30.0.0/16", "--gateway", "172.30.0.1"}, 0, true},
	{"volume_create", []string{"volume", "create", "uploads"}, 0, true},
	{"image_prune", []string{"image", "prune", "--yes"}, 0, true},
	{"volume_prune", []string{"volume", "prune", "--yes"}, 0, true},
	{"system_prune", []string{"system", "prune", "--yes"}, 0, true},

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
	{"err_not_found_backup", []string{"compose", "restore", "webapp", "nope.tar.gz"}, 3, true},
	{"err_forbidden_compose_down", []string{"compose", "down", "webapp"}, 5, false},
	{"err_usage_network_exists", []string{"network", "create", "app-net"}, 2, true},
	{"err_usage_gateway_no_subnet", []string{"network", "create", "edge", "--gateway", "172.30.0.1"}, 2, true},
	{"err_forbidden_system_prune", []string{"system", "prune"}, 5, false},
	{"stop_allow_write", []string{"stop", "web", "--allow-write"}, 0, false},
	{"err_forbidden_rm_max_tier", []string{"rm", "web", "--allow-write"}, 5, false},
	{"err_forbidden_read_only", []string{"start", "db", "-read-only"}, 5, true},
	{"dry_run_stop", []string{"stop", "web", "db", "ghost", "--dry-run"}, 0, true},
	{"dry_run_rm", []string{"rm", "--filter", "label=env=prod", "--dry-run"}, 0, true},
	{"dry_run_compose_down", []string{"compose", "down", "webapp", "--dry-run"}, 0, true},
	{"dry_run_system_prune", []string{"system", "prune", "--dry-run"}, 0, false},
	{"dry_run_network_create", []string{"network", "create", "edge", "--dry-run"}, 0, false},
	{"err_confirmation_rm", []string{"rm", "web", "db"}, 7, true},
	{"err_confirmation_kill_yes_false", []string{"kill", "web", "--yes=false"}, 7, true},
	{"err_usage_dry_run_read", []string{"ps", "--dry-run"}, 2, false},
	{"exec", []string{"exec", "web", "--yes", "--", "echo", "hello"}, 0, true},
	{"exec_exit_code", []string{"exec", "web", "-y", "--", "false"}, 0, true},
	{"exec_truncated", []string{"exec", "web", "-y", "--max-bytes", "4", "--", "seq", "5"}, 0, true},
	{"dry_run_exec", []string{"exec", "web", "--dry-run", "--", "sh", "-c", "rm -rf /tmp/cache"}, 0, false},
	{"err_confirmation_exec", []string{"exec", "web", "--", "printenv"}, 7, true},
	{"err_usage_exec_not_running", []string{"exec", "db", "-y", "--", "echo"}, 2, true},
	{"err_timeout_exec", []string{"exec", "web", "-y", "--timeout", "1", "--", "sleep", "60"}, 9, true},
}

// goldenSetup prepares the working directory of cases that touch local files:
// the demo backup lands in it, and restore reads an archive from it.
var goldenSetup = map[string]func(t *testing.T){
	"compose_backup": func(t *testing.T) { t.Chdir(t.TempDir()) },
	"compose_restore": func(t *testing.T) {
		t.Chdir(t.TempDir())
		if err := os.WriteFile("webapp.tar.gz", []byte("demo"), 0o644); err != nil {
			t.Fatal(err)
		}
	},
	"err_not_found_backup": func(t *testing.T) { t.Chdir(t.TempDir()) },
}

func TestCLIGolden(t *testing.T) {
	hostsYAML := "  - name: prod\n    host: tcp://127.0.0.1:1\n    read_only: true\n  - name: pw\n    host: ssh://u@pw.invalid\n    ssh_auth: password\n"
	cfgPath := writeCLIConfig(t, hostsYAML)
	writePath := writeCLIConfig(t, hostsYAML+"agent:\n  allowWrite: true\n  maxTier: destructive\n")
	for _, tc := range cliGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{}, tc.args...)
			// -demo would win over --host; those cases fail before dialing.
			if !slices.Contains(args, "--host") {
				// Before a `--`, which hands the rest to the command.
				at := len(args)
				if i := slices.Index(args, "--"); i >= 0 {
					at = i
				}
				args = slices.Insert(args, at, "-demo")
			}
			if setup := goldenSetup[tc.name]; setup != nil {
				setup(t)
			}
			path := cfgPath
			if tc.write {
				path = writePath
			}
			code, out, errOut := cliRun(t, &config.Config{ConfigFile: path}, args...)
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
			compareGolden(t, tc.name, []byte(maskPath(got, path, "<config>")))
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
// maskPath replaces the JSON spelling of path in out with placeholder: the
// policy hints name the temporary config file.
func maskPath(out, path, placeholder string) string {
	quoted, err := json.Marshal(path)
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(out, strings.Trim(string(quoted), `"`), placeholder)
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	norm := timestampRE.ReplaceAll(bytes.TrimSpace(got), []byte("<time>"))
	norm = backupPathRE.ReplaceAll(norm, []byte(`"<backup>"`))
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
