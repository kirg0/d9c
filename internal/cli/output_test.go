package cli

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"
)

func TestParseOutput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Output
	}{
		{"default", []string{"info"}, Output{}},
		{"short", []string{"info", "-o", "text"}, Output{Format: FormatText}},
		{"long with equals", []string{"info", "--output=NDJSON"}, Output{Format: FormatNDJSON}},
		{"fields comma list", []string{"info", "--fields", "id, name,,id"}, Output{Shape: agent.Shape{Fields: []string{"id", "name"}}}},
		{"fields repeated", []string{"info", "--fields=id", "--fields", "state"}, Output{Shape: agent.Shape{Fields: []string{"id", "state"}}}},
		{"limit", []string{"info", "--limit", "5"}, Output{Shape: agent.Shape{Limit: 5}}},
		{"mixed with op args", []string{"logs", "api", "-o", "ndjson", "--tail", "3", "--limit=0"}, Output{Format: FormatNDJSON}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv, err := Parse(testRegistry(t), &config.Config{}, tt.args)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", tt.args, err)
			}
			if !reflect.DeepEqual(inv.Output, tt.want) {
				t.Errorf("output = %+v, want %+v", inv.Output, tt.want)
			}
		})
	}
}

// The output flags stay out of the operation arguments.
func TestParseOutputNotAnArg(t *testing.T) {
	inv, err := Parse(testRegistry(t), &config.Config{}, []string{"logs", "api", "-o", "text", "--fields", "x", "--limit", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (agent.Args{"id": "api"}); !reflect.DeepEqual(inv.Args, want) {
		t.Errorf("args = %#v, want %#v", inv.Args, want)
	}
}

type row struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Ports string   `json:"ports,omitempty"`
	Nets  []string `json:"networks,omitempty"`
}

func envelope(data any, truncated bool, hint string) agent.Envelope {
	return agent.NewEnvelope("prod", docker.RuntimeDocker, agent.Result{Data: data, Truncated: truncated, Hint: hint})
}

func TestWriteResultFormats(t *testing.T) {
	rows := []row{
		{ID: "c1", Name: "web", Ports: "8080->80/tcp", Nets: []string{"front", "back"}},
		{ID: "c2", Name: "db"},
	}
	logs := agent.LogsData{Target: "web", Lines: []string{"2026-01-01T00:00:00Z hello\x1b[31m red", "\tindented\r"}}
	info := agent.InfoData{Name: "box", Version: "29.5", MemTotal: 17179869184}

	tests := []struct {
		name     string
		env      agent.Envelope
		out      Output
		wantOut  string
		wantWarn string
	}{
		{"json envelope", envelope(rows[:1], false, ""), Output{},
			`{"schema":1,"host":"prod","runtime":"docker","data":[{"id":"c1","name":"web","ports":"8080->80/tcp","networks":["front","back"]}],"truncated":false}` + "\n", ""},
		{"json fields and limit", envelope(rows, false, ""), Output{Shape: agent.Shape{Fields: []string{"name"}, Limit: 1}},
			`{"schema":1,"host":"prod","runtime":"docker","data":[{"name":"web"}],"truncated":true,"hint":"showing the first 1 of 2 items; raise --limit (0 = no limit)"}` + "\n", ""},
		{"text table", envelope(rows, false, ""), Output{Format: FormatText},
			"ID  NAME  PORTS         NETWORKS\n" +
				"c1  web   8080->80/tcp  front,back\n" +
				"c2  db    -             -\n", ""},
		{"text table follows fields order", envelope(rows, false, ""), Output{Format: FormatText, Shape: agent.Shape{Fields: []string{"name", "id"}}},
			"NAME  ID\nweb   c1\ndb    c2\n", ""},
		{"text empty list keeps header", envelope([]row{}, false, ""), Output{Format: FormatText},
			"ID  NAME  PORTS  NETWORKS\n", ""},
		{"text lines sanitized", envelope(logs, true, "older lines may exist"), Output{Format: FormatText},
			"2026-01-01T00:00:00Z hello[31m red\n\tindented\n", "truncated: older lines may exist\n"},
		{"text empty wrapped list shows the rest", envelope(agent.LogsData{Target: "web", Lines: []string{}}, false, ""), Output{Format: FormatText},
			"target: web\n", ""},
		{"text object as yaml", envelope(info, false, ""), Output{Format: FormatText, Shape: agent.Shape{Fields: []string{"name", "mem_total"}}},
			"mem_total: 17179869184\nname: box\n", ""},
		{"ndjson items", envelope(rows, false, ""), Output{Format: FormatNDJSON, Shape: agent.Shape{Fields: []string{"id"}}},
			`{"id":"c1"}` + "\n" + `{"id":"c2"}` + "\n", ""},
		{"ndjson wrapped lines with trailer", envelope(agent.LogsData{Target: "web", Lines: []string{"a", "b", "c"}}, false, ""),
			Output{Format: FormatNDJSON, Shape: agent.Shape{Limit: 2}},
			`"a"` + "\n" + `"b"` + "\n" + `{"truncated":true,"hint":"showing the first 2 of 3 items; raise --limit (0 = no limit)"}` + "\n", ""},
		{"ndjson object is one line", envelope(map[string]int{"n": 1}, false, ""), Output{Format: FormatNDJSON},
			`{"n":1}` + "\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, warn bytes.Buffer
			if err := WriteResult(&out, &warn, tt.env, tt.out); err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.wantOut {
				t.Errorf("stdout =\n%q\nwant\n%q", out.String(), tt.wantOut)
			}
			if warn.String() != tt.wantWarn {
				t.Errorf("warn = %q, want %q", warn.String(), tt.wantWarn)
			}
		})
	}
}

// A shaping error is reported before anything reaches stdout.
func TestWriteResultShapeError(t *testing.T) {
	var out bytes.Buffer
	err := WriteResult(&out, &out, envelope([]row{{ID: "c1"}}, false, ""), Output{Format: FormatText, Shape: agent.Shape{Fields: []string{"naem"}}})
	var ae *agent.Error
	if !errors.As(err, &ae) || ae.Code != agent.CodeUsage || !strings.Contains(ae.Hint, "name") {
		t.Fatalf("err = %v, want usage error listing the fields", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q before failing", out.String())
	}
}

func TestCell(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, "-"},
		{"", "-"},
		{"a\tb\x1b", "a b"},
		{true, "true"},
		{[]any{"x", "y"}, "x,y"},
		{[]any{}, "-"},
		{map[string]any{"k": "v"}, `{"k":"v"}`},
	}
	for _, tt := range tests {
		if got := cell(tt.in); got != tt.want {
			t.Errorf("cell(%#v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
