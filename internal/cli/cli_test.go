package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
)

// testRegistry declares operations that cover every argument shape the
// grammar handles: none, a positional string, a positional list, and flags of
// each type.
func testRegistry(t *testing.T) *agent.Registry {
	t.Helper()
	nop := func(context.Context, *agent.Env, agent.Args) (agent.Result, error) { return agent.Result{}, nil }
	r := agent.NewRegistry()
	r.MustRegister(
		agent.Op{Name: "info", Summary: "Engine summary.", Args: agent.NoArgs(), Tier: agent.TierRead, Handler: nop},
		agent.Op{Name: "hosts ls", Summary: "Saved hosts.", Args: agent.NoArgs(), Tier: agent.TierRead, Local: true, Handler: nop},
		agent.Op{Name: "compose ls", Summary: "Projects.", Args: agent.NoArgs(), Tier: agent.TierRead, Handler: nop},
		agent.Op{Name: "compose ps", Summary: "Project containers.", Tier: agent.TierRead, Handler: nop,
			Args: agent.Object(map[string]*agent.Schema{"project": agent.String("Project name.")}, "project")},
		agent.Op{Name: "logs", Summary: "Container logs.", Tier: agent.TierRead, Untrusted: true, Handler: nop,
			Args: agent.Object(map[string]*agent.Schema{
				"id":        agent.String("Container."),
				"tail":      agent.Integer("Lines.").WithDefault(200).WithMin(0),
				"grep":      agent.StringArray("Filters."),
				"no_redact": agent.Boolean("Show secrets."),
				"format":    agent.String("Format.").WithEnum("json", "text"),
				"all":       agent.Boolean("Every line.").WithShort("a"),
			}, "id")},
		agent.Op{Name: "rm", Summary: "Remove.", Tier: agent.TierDestructive, Handler: nop,
			Args: agent.Object(map[string]*agent.Schema{"ids": agent.StringArray("Containers.")}, "ids")},
	)
	return r
}

func TestIsCommand(t *testing.T) {
	r := testRegistry(t)
	tests := []struct {
		word string
		want bool
	}{
		{"info", true},
		{"hosts", true},
		{"compose", true},
		{"help", true},
		{"ls", false}, // a verb, not a noun
		{"ps", false},
		{"", false},
		{"-demo", false},
	}
	for _, tt := range tests {
		if got := IsCommand(r, tt.word); got != tt.want {
			t.Errorf("IsCommand(%q) = %v, want %v", tt.word, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantOp   string
		wantArgs agent.Args
		wantHelp bool
		wantCfg  config.Config
	}{
		{"bare noun", []string{"info"}, "info", agent.Args{}, false, config.Config{}},
		{"two-word command", []string{"hosts", "ls"}, "hosts ls", agent.Args{}, false, config.Config{}},
		{"positional", []string{"compose", "ps", "web"}, "compose ps", agent.Args{"project": "web"}, false, config.Config{}},
		{"positional then flags", []string{"logs", "api", "--tail", "50", "--grep=err", "-grep", "warn"}, "logs",
			agent.Args{"id": "api", "tail": "50", "grep": []string{"err", "warn"}}, false, config.Config{}},
		{"flags then positional", []string{"logs", "--tail=5", "api"}, "logs", agent.Args{"id": "api", "tail": "5"}, false, config.Config{}},
		{"required as flag", []string{"logs", "--id", "api"}, "logs", agent.Args{"id": "api"}, false, config.Config{}},
		{"bool flag alone", []string{"logs", "api", "--no-redact"}, "logs", agent.Args{"id": "api", "no_redact": "true"}, false, config.Config{}},
		{"bool flag value", []string{"logs", "api", "--no_redact=false"}, "logs", agent.Args{"id": "api", "no_redact": "false"}, false, config.Config{}},
		{"short alias", []string{"logs", "api", "-a"}, "logs", agent.Args{"id": "api", "all": "true"}, false, config.Config{}},
		{"double dash", []string{"logs", "--", "-weird-name"}, "logs", agent.Args{"id": "-weird-name"}, false, config.Config{}},
		{"list swallows rest", []string{"rm", "a", "b", "c"}, "rm", agent.Args{"ids": []string{"a", "b", "c"}}, false, config.Config{}},
		{"globals after command", []string{"info", "-H", "tcp://x:2375", "--demo", "-read-only", "--context=lab"}, "info", agent.Args{},
			false, config.Config{Host: "tcp://x:2375", HostFlagSet: true, Demo: true, ReadOnly: true, Context: "lab"}},
		{"saved host", []string{"info", "--host", "prod"}, "info", agent.Args{}, false, config.Config{SavedHost: "prod"}},
		{"demo=false", []string{"info", "-demo=false"}, "info", agent.Args{}, false, config.Config{}},
		{"op help", []string{"logs", "-h"}, "logs", agent.Args{}, true, config.Config{}},
		{"op help long", []string{"info", "--help"}, "info", agent.Args{}, true, config.Config{}},
		{"help", []string{"help"}, "", nil, true, config.Config{}},
		{"help command", []string{"help", "compose", "ps"}, "compose ps", nil, true, config.Config{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg config.Config
			inv, err := Parse(testRegistry(t), &cfg, tt.args)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", tt.args, err)
			}
			if inv.Op.Name != tt.wantOp || inv.Help != tt.wantHelp {
				t.Errorf("op = %q help = %v, want %q %v", inv.Op.Name, inv.Help, tt.wantOp, tt.wantHelp)
			}
			if !reflect.DeepEqual(inv.Args, tt.wantArgs) {
				t.Errorf("args = %#v, want %#v", inv.Args, tt.wantArgs)
			}
			if cfg != tt.wantCfg {
				t.Errorf("cfg = %+v, want %+v", cfg, tt.wantCfg)
			}
		})
	}
}

// The parsed raw arguments must pass the registry's normalization, so the CLI
// and MCP end up with identical typed values.
func TestParseNormalizes(t *testing.T) {
	r := testRegistry(t)
	inv, err := Parse(r, &config.Config{}, []string{"logs", "api", "--tail", "50", "--no-redact", "--grep", "x"})
	if err != nil {
		t.Fatal(err)
	}
	op, _ := r.Lookup("logs")
	got, err := op.Args.Normalize(inv.Args)
	if err != nil {
		t.Fatal(err)
	}
	want := agent.Args{"id": "api", "tail": 50, "no_redact": true, "grep": []string{"x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalized = %#v, want %#v", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantMsg  string
		wantHint string
	}{
		{"empty", nil, "missing command", "d9c help"},
		{"flags only", []string{"--demo"}, "missing command", "d9c help"},
		{"noun without verb", []string{"compose"}, `unknown command "compose"`, "compose ls, compose ps"},
		{"unknown verb", []string{"compose", "up"}, `unknown command "compose up"`, "compose ls, compose ps"},
		{"unknown noun", []string{"nope"}, `unknown command "nope"`, "d9c help"},
		{"unknown flag", []string{"info", "--bogus"}, `unknown flag --bogus for "info"`, "usage: d9c info"},
		{"extra positional", []string{"info", "extra"}, `unexpected argument "extra"`, "usage: d9c info"},
		{"too many positional", []string{"compose", "ps", "a", "b"}, `unexpected argument "b"`, "usage: d9c compose ps <project>"},
		{"missing value", []string{"logs", "api", "--tail"}, "flag --tail needs a value", "[--tail int]"},
		{"global missing value", []string{"info", "-H"}, "flag -H needs a value", ""},
		{"bad global bool", []string{"info", "--demo=maybe"}, "want true or false", ""},
		{"unknown format", []string{"info", "-o", "xml"}, `unknown format "xml"`, "json, text, ndjson"},
		{"format missing value", []string{"info", "--output"}, "flag --output needs a value", ""},
		{"bad limit", []string{"info", "--limit", "-1"}, "want a whole number", ""},
		{"limit not a number", []string{"info", "--limit=ten"}, "want a whole number", ""},
		{"help unknown", []string{"help", "nope"}, `unknown command "nope"`, ""},
		{"help extra", []string{"help", "info", "x"}, `unexpected argument "x"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(testRegistry(t), &config.Config{}, tt.args)
			var ae *agent.Error
			if !errors.As(err, &ae) || ae.Code != agent.CodeUsage {
				t.Fatalf("Parse(%q) err = %v, want a usage *agent.Error", tt.args, err)
			}
			if !strings.Contains(ae.Message, tt.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", ae.Message, tt.wantMsg)
			}
			if !strings.Contains(ae.Hint, tt.wantHint) {
				t.Errorf("hint = %q, want it to contain %q", ae.Hint, tt.wantHint)
			}
		})
	}
}

func TestWriteHelp(t *testing.T) {
	r := testRegistry(t)

	var buf bytes.Buffer
	if err := WriteHelp(&buf, r, Invocation{Help: true}, agent.TierRead); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"usage: d9c", "compose ps", "Project containers.", "hosts ls", "--host <saved name>", "-demo"} {
		if !strings.Contains(out, want) {
			t.Errorf("command list lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Remove.") {
		t.Errorf("command list shows an op above the ceiling:\n%s", out)
	}

	buf.Reset()
	op, _ := r.Lookup("logs")
	if err := WriteHelp(&buf, r, Invocation{Op: op, Help: true}, agent.TierRead); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	for _, want := range []string{
		"usage: d9c logs <id> [--all] [--format value] [--grep value (repeatable)] [--no_redact] [--tail int]",
		"-a, --all (boolean): Every line.",
		agent.UntrustedNote,
		"--tail (integer): Lines. (default 200)",
		"--format (string): Format. (one of json, text)",
		"Global flags:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("op help lacks %q:\n%s", want, out)
		}
	}
	// Required arguments come first.
	if !strings.Contains(out, "--grep (list of strings): Filters.") {
		t.Errorf("list argument type not spelled out:\n%s", out)
	}
	if strings.Index(out, "  --id (") > strings.Index(out, "  --format (") {
		t.Errorf("required argument not listed first:\n%s", out)
	}
}

func TestWriteResult(t *testing.T) {
	var buf bytes.Buffer
	env := agent.NewEnvelope("prod", docker.RuntimePodman, agent.Result{Data: map[string]string{"ports": "8080->80/tcp"}})
	if err := WriteResult(&buf, &buf, env, Output{}); err != nil {
		t.Fatal(err)
	}
	want := `{"schema":1,"host":"prod","runtime":"podman","data":{"ports":"8080->80/tcp"},"truncated":false}` + "\n"
	if buf.String() != want {
		t.Errorf("WriteResult = %q, want %q", buf.String(), want)
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantExit int
	}{
		{"usage", agent.Errorf(agent.CodeUsage, "bad").WithHint("fix it"), "usage", 2},
		{"not found", errors.New("Error response from daemon: No such container: x"), "not_found", 3},
		{"connection", errors.New("dial tcp 1.2.3.4:2375: connect: connection refused"), "connection", 4},
		{"internal", errors.New("boom"), "internal", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := WriteError(&buf, tt.err); got != tt.wantExit {
				t.Errorf("exit = %d, want %d", got, tt.wantExit)
			}
			if strings.Count(buf.String(), "\n") != 1 {
				t.Errorf("want one JSON line, got %q", buf.String())
			}
			var doc struct {
				Error struct{ Code, Message, Hint string }
			}
			if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
				t.Fatalf("stderr is not JSON: %v (%q)", err, buf.String())
			}
			if doc.Error.Code != tt.wantCode || doc.Error.Message == "" {
				t.Errorf("error = %+v, want code %q", doc.Error, tt.wantCode)
			}
		})
	}
}
