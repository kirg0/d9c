package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/agent"
)

func TestBuildToolsCoversRegistry(t *testing.T) {
	tools, err := buildTools(agent.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(tools); n < 10 || n > 12 {
		t.Errorf("%d tools; the plan keeps the set at 10–12", n)
	}
	for _, tl := range tools {
		if !strings.HasPrefix(tl.def.name, "d9c_") || tl.def.title == "" || tl.def.summary == "" {
			t.Errorf("%s: name, title and summary are required", tl.def.name)
		}
		props := tl.schema.Properties
		if tl.single() {
			if _, ok := props[argAction]; ok {
				t.Errorf("%s: a single-action tool takes no action", tl.def.name)
			}
		} else if a := props[argAction]; a == nil || len(a.Enum) != len(tl.actions) || tl.schema.Required[0] != argAction {
			t.Errorf("%s: action must be a required enum of the actions", tl.def.name)
		}
		if _, ok := props[argDryRun]; ok != (tl.tier > agent.TierRead) {
			t.Errorf("%s: dry_run offered = %v for tier %s", tl.def.name, ok, tl.tier)
		}
		for name, p := range props {
			if p.Description == "" {
				t.Errorf("%s.%s: no description", tl.def.name, name)
			}
		}
	}
}

func TestBuildToolsRejectsBadTables(t *testing.T) {
	noop := func(context.Context, *agent.Env, agent.Args) (agent.Result, error) { return agent.Result{}, nil }
	plan := func(context.Context, *agent.Env, agent.Args) ([]string, error) { return nil, nil }
	reg := agent.NewRegistry()
	reg.MustRegister(
		agent.Op{Name: "a", Summary: "A.", Args: agent.Object(map[string]*agent.Schema{"x": agent.String("X.")}), Handler: noop},
		agent.Op{Name: "b", Summary: "B.", Args: agent.Object(map[string]*agent.Schema{"x": agent.Integer("X.")}), Handler: noop},
		agent.Op{Name: "c", Summary: "C.", Args: agent.NoArgs(), Tier: agent.TierSafe, Handler: noop, Plan: plan},
		agent.Op{Name: "d", Summary: "D.", Args: agent.Object(map[string]*agent.Schema{"action": agent.String("Oops.")}), Handler: noop},
	)
	tests := []struct {
		name string
		def  toolDef
		want string
	}{
		{"type clash", toolDef{name: "t", actions: []actionDef{{"a", "a"}, {"b", "b"}}}, "string in"},
		{"mixed tiers", toolDef{name: "t", actions: []actionDef{{"a", "a"}, {"c", "c"}}}, "share one tier"},
		{"unknown op", toolDef{name: "t", actions: []actionDef{{"z", "zzz"}}}, "unknown operation"},
		{"duplicate action", toolDef{name: "t", actions: []actionDef{{"a", "a"}, {"a", "c"}}}, "duplicate action"},
		{"reserved argument", toolDef{name: "t", actions: []actionDef{{"d", "d"}}}, "reserved"},
		{"no actions", toolDef{name: "t"}, "no actions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildTool(reg, tt.def); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMergedArguments(t *testing.T) {
	tools, err := buildTools(agent.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*tool{}
	for _, tl := range tools {
		byName[tl.def.name] = tl
	}
	logs := byName["d9c_logs"].schema.Properties
	if tail := logs["tail"]; tail.Default != 200 || strings.Contains(tail.Description, "Used by") {
		t.Errorf("tail shared by every logs action keeps its default and no 'Used by': %+v", tail)
	}
	if c := logs["container"]; !strings.Contains(c.Description, "Used by: container.") || !strings.Contains(c.Description, "Required for: container.") {
		t.Errorf("container description = %q", c.Description)
	}
	single := byName["d9c_exec"].schema
	if strings.Join(single.Required, ",") != "container,cmd" {
		t.Errorf("a single-action tool keeps the op's required list, got %v", single.Required)
	}
	if d := byName["d9c_logs"].desc; strings.Count(d, agent.UntrustedNote) != 1 {
		t.Errorf("the untrusted note must appear once: %q", d)
	}
	if d := byName["d9c_remove"].desc; !strings.Contains(d, "confirm with the user") {
		t.Errorf("destructive tool description = %q", d)
	}
}

func TestOpFor(t *testing.T) {
	tools, err := buildTools(agent.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	var containers *tool
	for _, tl := range tools {
		if tl.def.name == "d9c_containers" {
			containers = tl
		}
	}
	op, args, err := containers.opFor(map[string]any{"action": "inspect", "target": "web", "fields": []any{"x"}, "limit": 2.0})
	if err != nil || op.Name != "inspect" || len(args) != 1 || args["target"] != "web" {
		t.Errorf("opFor = %s %v %v; want inspect with only target", op.Name, args, err)
	}
}
