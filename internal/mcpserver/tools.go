package mcpserver

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/kirg0/d9c/internal/agent"
)

// Argument names the tools add on top of the operation arguments. agent
// reserves fields/limit/dry_run, so no operation can declare them; action is
// checked when the tools are built.
const (
	argAction = "action"
	argFields = "fields"
	argLimit  = "limit"
	argDryRun = "dry_run"
)

// toolDef groups registry operations of one tier into one MCP tool: fewer,
// broader tools keep the agent's context small. A tool with one action takes
// no action argument.
type toolDef struct {
	name    string
	title   string
	summary string
	actions []actionDef
	// idempotent is the idempotentHint of a write tool: repeating a call with
	// the same arguments changes nothing more.
	idempotent bool
	// openWorld is set for tools that reach beyond the engine (registries).
	openWorld bool
}

// actionDef maps an action value onto a registry operation.
type actionDef struct {
	action string
	op     string
}

// toolTable is the MCP tool set. Every registry operation appears in exactly
// one tool, and the operations of a tool share their tier, so the tool's
// annotations describe every action truthfully.
var toolTable = []toolDef{
	{name: "d9c_hosts", title: "Saved hosts", summary: "List the saved hosts d9c knows and the write tier the policy admits on each.",
		actions: []actionDef{{"ls", "hosts ls"}}},
	{name: "d9c_system", title: "Engine and usage", summary: "Engine summary, disk usage, the event log and live container CPU/memory.",
		actions: []actionDef{{"info", "info"}, {"df", "system df"}, {"events", "events"}, {"stats", "stats"}}},
	{name: "d9c_containers", title: "Containers", summary: "List and inspect containers, or wait for one to reach a state.",
		actions: []actionDef{{"ps", "ps"}, {"inspect", "inspect"}, {"wait", "wait"}}},
	{name: "d9c_logs", title: "Logs", summary: "Read a bounded window of container or compose project logs.",
		actions: []actionDef{{"container", "logs"}, {"compose", "compose logs"}}},
	{name: "d9c_resources", title: "Images, networks, volumes", summary: "List images, networks or volumes, or show an image's layers.",
		actions: []actionDef{{"images", "images"}, {"networks", "networks"}, {"volumes", "volumes"}, {"image_history", "image history"}}},
	{name: "d9c_compose", title: "Compose projects", summary: "List compose projects, their containers and their configuration.",
		actions: []actionDef{{"ls", "compose ls"}, {"ps", "compose ps"}, {"config", "compose config"}}},
	{name: "d9c_lifecycle", title: "Start/stop containers", summary: "Start, stop or restart containers by ID, name or filter.",
		actions: []actionDef{{"start", "start"}, {"stop", "stop"}, {"restart", "restart"}}},
	{name: "d9c_deploy", title: "Pull, build and deploy", summary: "Pull, build, tag and push images; bring compose projects up, pull, restart or back them up.",
		actions: []actionDef{{"pull", "pull"}, {"image_build", "image build"}, {"image_tag", "image tag"}, {"image_push", "image push"},
			{"compose_up", "compose up"}, {"compose_pull", "compose pull"},
			{"compose_restart", "compose restart"}, {"compose_backup", "compose backup"}},
		openWorld: true},
	{name: "d9c_create", title: "Create networks/volumes", summary: "Create a network or a volume.",
		actions: []actionDef{{"network", "network create"}, {"volume", "volume create"}}},
	{name: "d9c_remove", title: "Kill/remove", summary: "Kill or remove containers, remove an image, take a compose project down or restore it from a backup.",
		actions: []actionDef{{"kill", "kill"}, {"rm", "rm"}, {"image_rm", "image rm"}, {"compose_down", "compose down"}, {"compose_restore", "compose restore"}}},
	{name: "d9c_prune", title: "Prune", summary: "Delete unused images or volumes, or prune the whole system.",
		actions:    []actionDef{{"images", "image prune"}, {"volumes", "volume prune"}, {"system", "system prune"}},
		idempotent: true},
	{name: "d9c_exec", title: "Exec in container", summary: "Run a command in a running container without a TTY.",
		actions: []actionDef{{"run", "exec"}}},
}

// tool is a toolDef resolved against a registry.
type tool struct {
	def     toolDef
	tier    agent.Tier
	ops     map[string]agent.Op // by action
	local   bool                // every action reads only local configuration
	schema  *agent.Schema       // the tool's inputSchema
	desc    string
	actions []string // in declaration order
}

// single reports whether the tool takes no action argument.
func (t *tool) single() bool { return len(t.actions) == 1 }

// buildTools resolves toolTable against reg, checking that the table covers
// every operation exactly once and that each tool is consistent.
func buildTools(reg *agent.Registry) ([]*tool, error) {
	seen := map[string]string{}
	out := make([]*tool, 0, len(toolTable))
	for _, def := range toolTable {
		t, err := buildTool(reg, def)
		if err != nil {
			return nil, err
		}
		for _, op := range t.ops {
			if other, dup := seen[op.Name]; dup {
				return nil, fmt.Errorf("operation %q is in both %s and %s", op.Name, other, def.name)
			}
			seen[op.Name] = def.name
		}
		out = append(out, t)
	}
	for _, op := range reg.Ops() {
		if _, ok := seen[op.Name]; !ok {
			return nil, fmt.Errorf("operation %q is in no MCP tool", op.Name)
		}
	}
	return out, nil
}

func buildTool(reg *agent.Registry, def toolDef) (*tool, error) {
	if len(def.actions) == 0 {
		return nil, fmt.Errorf("tool %s: no actions", def.name)
	}
	t := &tool{def: def, ops: map[string]agent.Op{}, local: true}
	for i, a := range def.actions {
		op, ok := reg.Lookup(a.op)
		if !ok {
			return nil, fmt.Errorf("tool %s: unknown operation %q", def.name, a.op)
		}
		if _, dup := t.ops[a.action]; dup {
			return nil, fmt.Errorf("tool %s: duplicate action %q", def.name, a.action)
		}
		if _, clash := op.Args.Properties[argAction]; clash {
			return nil, fmt.Errorf("tool %s: operation %q declares the reserved argument %q", def.name, op.Name, argAction)
		}
		if i == 0 {
			t.tier = op.Tier
		} else if op.Tier != t.tier {
			return nil, fmt.Errorf("tool %s: %q is %s but %q is %s; a tool's actions share one tier",
				def.name, op.Name, op.Tier, def.actions[0].op, t.tier)
		}
		t.ops[a.action] = op
		t.actions = append(t.actions, a.action)
		t.local = t.local && op.Local
	}
	schema, err := mergeSchemas(t)
	if err != nil {
		return nil, fmt.Errorf("tool %s: %w", def.name, err)
	}
	t.schema = schema
	t.desc = describe(t)
	return t, nil
}

// propUse is one operation's declaration of a merged property.
type propUse struct {
	action   string
	schema   *agent.Schema
	required bool
}

// mergeSchemas builds the tool's flat input schema: the action selector, the
// union of the actions' arguments (a property shared by several actions must
// have one type in all of them), the output shaping options and, for write
// tools, dry_run. Per-action requirements cannot be expressed in a flat
// schema, so they go into the descriptions and are enforced by the
// operation's own schema on each call.
func mergeSchemas(t *tool) (*agent.Schema, error) {
	uses := map[string][]propUse{}
	for _, action := range t.actions {
		op := t.ops[action]
		for name, p := range op.Args.Properties {
			uses[name] = append(uses[name], propUse{action, p, slices.Contains(op.Args.Required, name)})
		}
	}
	props := map[string]*agent.Schema{}
	for name, list := range uses {
		p, err := mergeProp(t, list)
		if err != nil {
			return nil, fmt.Errorf("argument %q: %w", name, err)
		}
		props[name] = p
	}
	var required []string
	if !t.single() {
		props[argAction] = agent.String("What to do; see the tool description.").WithEnum(t.actions...)
		required = append(required, argAction)
	} else {
		required = append(required, t.ops[t.actions[0]].Args.Required...)
	}
	props[argFields] = agent.StringArray("Keep only these fields of each listed item (saves tokens), e.g. [\"name\",\"state\"].")
	props[argLimit] = agent.Integer("Return at most this many list items; the result says when more exist.").WithMin(1)
	if t.tier > agent.TierRead {
		props[argDryRun] = agent.Boolean("Return the plan of what would change instead of changing anything.")
	}
	return agent.Object(props, required...), nil
}

// mergeProp combines the declarations of one property across actions.
func mergeProp(t *tool, list []propUse) (*agent.Schema, error) {
	sort.Slice(list, func(i, j int) bool {
		return slices.Index(t.actions, list[i].action) < slices.Index(t.actions, list[j].action)
	})
	first := list[0].schema
	out := &agent.Schema{Type: first.Type, Items: first.Items, Enum: first.Enum, Default: first.Default, Minimum: first.Minimum}
	sameDesc := true
	var actions, requiredBy []string
	for _, u := range list {
		s := u.schema
		if s.Type != first.Type {
			return nil, fmt.Errorf("%s in %q but %s in %q", first.Type, list[0].action, s.Type, u.action)
		}
		if !slices.Equal(s.Enum, first.Enum) {
			out.Enum = nil
		}
		if !reflect.DeepEqual(s.Default, first.Default) {
			out.Default = nil
		}
		if s.Minimum == nil || first.Minimum == nil || *s.Minimum != *first.Minimum {
			out.Minimum = nil
		}
		sameDesc = sameDesc && s.Description == first.Description
		actions = append(actions, u.action)
		if u.required {
			requiredBy = append(requiredBy, u.action)
		}
	}
	var desc []string
	if sameDesc {
		desc = append(desc, first.Description)
	} else {
		for _, u := range list {
			desc = append(desc, u.action+": "+u.schema.Description)
		}
	}
	if !t.single() && len(actions) < len(t.actions) {
		desc = append(desc, "Used by: "+strings.Join(actions, ", ")+".")
	}
	if !t.single() && len(requiredBy) > 0 {
		desc = append(desc, "Required for: "+strings.Join(requiredBy, ", ")+".")
	}
	if out.Default == nil && first.Default != nil {
		for _, u := range list {
			if u.schema.Default != nil {
				desc = append(desc, fmt.Sprintf("Default for %s: %v.", u.action, u.schema.Default))
			}
		}
	}
	out.Description = strings.Join(desc, " ")
	return out, nil
}

// describe renders the tool description: the summary, one line per action
// and, once, the untrusted-data warning.
func describe(t *tool) string {
	var b strings.Builder
	b.WriteString(t.def.summary)
	untrusted := false
	if t.single() {
		op := t.ops[t.actions[0]]
		b.WriteString(" " + op.Summary)
		untrusted = op.Untrusted
	} else {
		b.WriteString("\nActions:")
		for _, action := range t.actions {
			op := t.ops[action]
			fmt.Fprintf(&b, "\n- %s: %s", action, op.Summary)
			untrusted = untrusted || op.Untrusted
		}
	}
	switch t.tier {
	case agent.TierSafe:
		b.WriteString("\nChanges state (recoverable); dry_run returns the plan without changing anything.")
	case agent.TierDestructive:
		b.WriteString("\nDestructive: call with dry_run first, show the user the plan and make the real call only after " +
			"their explicit yes to it, even when the request already asked for this change; dry_run changes nothing.")
	}
	if untrusted {
		b.WriteString("\n" + agent.UntrustedNote)
	}
	return b.String()
}

// opFor returns the operation an action selects and the operation arguments
// left after taking out the tool's own options.
func (t *tool) opFor(args map[string]any) (agent.Op, agent.Args, error) {
	opArgs := maps.Clone(args)
	if opArgs == nil {
		opArgs = map[string]any{}
	}
	for _, k := range []string{argAction, argFields, argLimit, argDryRun} {
		delete(opArgs, k)
	}
	if t.single() {
		if _, ok := args[argAction]; ok {
			return agent.Op{}, nil, agent.Errorf(agent.CodeUsage, "%s takes no action argument", t.def.name)
		}
		return t.ops[t.actions[0]], opArgs, nil
	}
	action, _ := args[argAction].(string)
	op, ok := t.ops[action]
	if !ok {
		e := agent.Errorf(agent.CodeUsage, "%s: unknown action %q", t.def.name, action)
		if action == "" {
			e = agent.Errorf(agent.CodeUsage, "%s: missing argument %q", t.def.name, argAction)
		}
		return agent.Op{}, nil, e.WithHint("one of: " + strings.Join(t.actions, ", "))
	}
	return op, opArgs, nil
}
