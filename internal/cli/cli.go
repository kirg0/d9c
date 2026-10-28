// Package cli is the non-interactive command-line frontend over the agent
// operation registry: `d9c info`, `d9c hosts ls`, `d9c info --host prod`. It
// owns the command-line grammar (command path, flags, positional arguments)
// and the output contract (JSON envelope on stdout, JSON error on stderr,
// exit status by error code). Connecting to a host is left to the caller.
//
// All output is English and free of ANSI styling: it is read by scripts and AI
// agents, not rendered by the TUI.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kirg0/d9c/internal/agent"
	"github.com/kirg0/d9c/internal/config"
)

// HelpCommand is the built-in noun that lists the commands (`d9c help`) or
// describes one (`d9c help info`).
const HelpCommand = "help"

// IsCommand reports whether word starts a CLI command: the first word of a
// registered operation, or HelpCommand. Anything else on the command line
// leaves d9c in TUI mode.
func IsCommand(reg *agent.Registry, word string) bool {
	if word == HelpCommand {
		return true
	}
	for _, op := range reg.Ops() {
		if noun, _, _ := strings.Cut(op.Name, " "); noun == word {
			return true
		}
	}
	return false
}

// Invocation is a parsed CLI command line.
type Invocation struct {
	// Op is the operation to run; zero for `d9c help` without a command.
	Op agent.Op
	// Args holds the raw operation arguments (strings, booleans, string
	// lists); Registry.Invoke normalizes them against Op.Args.
	Args agent.Args
	// Help asks for usage text instead of running Op (-h/--help, `d9c help`).
	Help bool
	// Output holds -o/--output, --fields and --limit.
	Output Output
}

// global is a connection flag accepted anywhere after the command (before it,
// config.Load parses the same flags). Each writes straight onto the config.
type global struct {
	name   string
	usage  string
	isBool bool
	set    func(cfg *config.Config, v string) error
}

// globals are the flags that work in both TUI and CLI mode.
var globals = []global{
	{name: "H", usage: "-H <url>", set: func(c *config.Config, v string) error {
		c.Host, c.HostFlagSet = v, true
		return nil
	}},
	{name: "host", usage: "--host <saved name>", set: func(c *config.Config, v string) error {
		c.SavedHost = v
		return nil
	}},
	{name: "context", usage: "-context <docker context>", set: func(c *config.Config, v string) error {
		c.Context = v
		return nil
	}},
	{name: "demo", usage: "-demo", isBool: true, set: func(c *config.Config, v string) error {
		b, err := strconv.ParseBool(v)
		c.Demo = b
		return err
	}},
	{name: "read-only", usage: "-read-only", isBool: true, set: func(c *config.Config, v string) error {
		b, err := strconv.ParseBool(v)
		c.ReadOnly = b
		return err
	}},
	{name: "allow-write", usage: "--allow-write", isBool: true, set: func(c *config.Config, v string) error {
		b, err := strconv.ParseBool(v)
		c.AllowWrite = b
		return err
	}},
}

func findGlobal(name string) (global, bool) {
	for _, g := range globals {
		if g.name == name {
			return g, true
		}
	}
	return global{}, false
}

// Parse resolves the command path at the start of args (the words after any
// leading global flags) against reg and splits the rest into global flags,
// applied to cfg, and operation arguments. Flags take one or two dashes and
// `--name value` or `--name=value`; a boolean flag alone means true. Positional
// arguments fill the operation's required properties in declaration order
// (a required string list takes all remaining ones); `--` ends the flags.
// Every failure is a CodeUsage *agent.Error.
func Parse(reg *agent.Registry, cfg *config.Config, args []string) (Invocation, error) {
	if len(args) > 0 && args[0] == HelpCommand {
		inv := Invocation{Help: true}
		if len(args) == 1 {
			return inv, nil
		}
		op, rest, err := resolveOp(reg, args[1:])
		if err != nil {
			return Invocation{}, err
		}
		if len(rest) > 0 {
			return Invocation{}, agent.Errorf(agent.CodeUsage, "unexpected argument %q after %q", rest[0], HelpCommand+" "+op.Name)
		}
		inv.Op = op
		return inv, nil
	}

	op, rest, err := resolveOp(reg, args)
	if err != nil {
		return Invocation{}, err
	}
	inv := Invocation{Op: op, Args: agent.Args{}}
	var positional []string
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if tok == "--" {
			positional = append(positional, rest[i+1:]...)
			break
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			positional = append(positional, tok)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(tok, "-"), "-"), "=")
		if name == "h" || name == "help" {
			inv.Help = true
			continue
		}
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(rest) {
				return "", agent.Errorf(agent.CodeUsage, "flag %s needs a value", tok).WithHint(usageLine(op))
			}
			i++
			return rest[i], nil
		}

		if ok, err := inv.Output.parseFlag(name, tok, takeValue); ok {
			if err != nil {
				return Invocation{}, err
			}
			continue
		}

		if g, ok := findGlobal(name); ok {
			v := "true"
			if !g.isBool || hasValue {
				if v, err = takeValue(); err != nil {
					return Invocation{}, err
				}
			}
			if err := g.set(cfg, v); err != nil {
				return Invocation{}, agent.Errorf(agent.CodeUsage, "flag %s: want true or false, got %q", tok, v)
			}
			continue
		}

		prop, schema, ok := lookupProp(op.Args, name)
		if !ok {
			return Invocation{}, agent.Errorf(agent.CodeUsage, "unknown flag %s for %q", tok, op.Name).WithHint(usageLine(op))
		}
		switch schema.Type {
		case agent.TypeBoolean:
			if !hasValue {
				value = "true"
			}
			inv.Args[prop] = value
		case agent.TypeArray:
			v, err := takeValue()
			if err != nil {
				return Invocation{}, err
			}
			list, _ := inv.Args[prop].([]string)
			inv.Args[prop] = append(list, v)
		default:
			v, err := takeValue()
			if err != nil {
				return Invocation{}, err
			}
			inv.Args[prop] = v
		}
	}
	if err := assignPositional(op, inv.Args, positional); err != nil {
		return Invocation{}, err
	}
	return inv, nil
}

// resolveOp finds the longest registered operation name formed by the leading
// non-flag words of args and returns it with the remaining arguments.
func resolveOp(reg *agent.Registry, args []string) (agent.Op, []string, error) {
	words := 0
	for words < len(args) && !strings.HasPrefix(args[words], "-") {
		words++
	}
	for n := words; n > 0; n-- {
		if op, ok := reg.Lookup(strings.Join(args[:n], " ")); ok {
			return op, args[n:], nil
		}
	}
	if words == 0 {
		return agent.Op{}, nil, agent.Errorf(agent.CodeUsage, "missing command").
			WithHint("run `d9c help` to list the commands")
	}
	// A known noun with a missing or unknown verb: list what the noun offers.
	noun := args[0]
	var verbs []string
	for _, op := range reg.Ops() {
		if strings.HasPrefix(op.Name, noun+" ") {
			verbs = append(verbs, op.Name)
		}
	}
	typed := strings.Join(args[:words], " ")
	if len(verbs) > 0 {
		return agent.Op{}, nil, agent.Errorf(agent.CodeUsage, "unknown command %q", typed).
			WithHint("available: " + strings.Join(verbs, ", "))
	}
	return agent.Op{}, nil, agent.Errorf(agent.CodeUsage, "unknown command %q", typed).
		WithHint("run `d9c help` to list the commands")
}

// lookupProp finds the argument property for a flag name; `--no-redact`
// matches a property spelled either "no-redact" or "no_redact", and a single
// letter also matches a property's Short alias (`-a` for "all").
func lookupProp(s *agent.Schema, name string) (string, *agent.Schema, bool) {
	if s == nil {
		return "", nil, false
	}
	for _, cand := range []string{name, strings.ReplaceAll(name, "-", "_")} {
		if p, ok := s.Properties[cand]; ok {
			return cand, p, true
		}
	}
	if len(name) == 1 {
		for prop, p := range s.Properties {
			if p.Short == name {
				return prop, p, true
			}
		}
	}
	return "", nil, false
}

// assignPositional maps positional arguments onto the required properties of
// op that were not given as flags, in declaration order; what is left goes to
// the op's Positional list property, after any values given as flags.
func assignPositional(op agent.Op, args agent.Args, positional []string) error {
	for _, name := range op.Args.Required {
		if len(positional) == 0 {
			break
		}
		if _, set := args[name]; set {
			continue
		}
		if op.Args.Properties[name].Type == agent.TypeArray {
			args[name] = slices.Clone(positional)
			positional = nil
			break
		}
		args[name] = positional[0]
		positional = positional[1:]
	}
	if name := positionalProp(op.Args); name != "" && len(positional) > 0 {
		list, _ := args[name].([]string)
		args[name] = append(list, positional...)
		positional = nil
	}
	if len(positional) > 0 {
		return agent.Errorf(agent.CodeUsage, "unexpected argument %q for %q", positional[0], op.Name).
			WithHint(usageLine(op))
	}
	return nil
}

// positionalProp returns the name of the schema's Positional property, or "".
func positionalProp(s *agent.Schema) string {
	for name, p := range s.Properties {
		if p.Positional {
			return name
		}
	}
	return ""
}

// usageLine renders `usage: d9c <op> <required…> [<positional>...] [--flag …]`.
func usageLine(op agent.Op) string {
	var b strings.Builder
	b.WriteString("usage: d9c ")
	b.WriteString(op.Name)
	for _, name := range op.Args.Required {
		b.WriteString(" <" + name + ">")
	}
	pos := positionalProp(op.Args)
	if pos != "" {
		b.WriteString(" [<" + pos + ">...]")
	}
	for _, name := range optionalProps(op.Args) {
		if name == pos {
			continue
		}
		b.WriteString(" [" + flagName(name))
		if t := op.Args.Properties[name].Type; t != agent.TypeBoolean {
			b.WriteString(" " + typeWord(t))
		}
		b.WriteString("]")
	}
	return b.String()
}

// flagName spells an argument property as a CLI flag: snake_case schema
// names (kept for MCP) read as kebab-case, so no_redact is --no-redact.
func flagName(prop string) string {
	return "--" + strings.ReplaceAll(prop, "_", "-")
}

// optionalProps returns the non-required property names, sorted.
func optionalProps(s *agent.Schema) []string {
	var out []string
	for name := range s.Properties {
		if !slices.Contains(s.Required, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func typeWord(t string) string {
	switch t {
	case agent.TypeInteger:
		return "int"
	case agent.TypeArray:
		return "value (repeatable)"
	default:
		return "value"
	}
}

// globalUsage lists the global flags for help texts.
func globalUsage() string {
	parts := make([]string, len(globals))
	for i, g := range globals {
		parts[i] = g.usage
	}
	return strings.Join(parts, ", ")
}

// WriteHelp prints the usage text for inv: the command list for a bare
// `d9c help`, the operation's arguments otherwise. Only operations under
// ceiling are listed.
func WriteHelp(w io.Writer, reg *agent.Registry, inv Invocation, ceiling agent.Tier) error {
	var b strings.Builder
	if inv.Op.Name == "" {
		ops := reg.Allowed(ceiling)
		width := 0
		for _, op := range ops {
			width = max(width, len(op.Name))
		}
		b.WriteString("usage: d9c [global flags] <command> [arguments]\n")
		b.WriteString("Run d9c without a command to open the interactive interface.\n\nCommands:\n")
		for _, op := range ops {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, op.Name, op.Summary)
		}
		if hidden := len(reg.Ops()) - len(ops); hidden > 0 {
			fmt.Fprintf(&b, "\n%d commands that change state are hidden (allowed tier: %s). Write commands are off\n"+
				"by default: with the user's consent pass --allow-write, or set agent: {allowWrite: true,\n"+
				"maxTier: safe|destructive} in the config, globally or under a saved host.\n", hidden, ceiling)
		}
		b.WriteString("\nGlobal flags: " + globalUsage() + "\n")
		b.WriteString("Output flags: " + outputUsage + "\n")
		b.WriteString("Output: one JSON envelope {\"schema\",\"host\",\"runtime\",\"data\",\"truncated\"} on stdout by default;\n" +
			"-o ndjson writes list items one per line, -o text a compact table;\n" +
			"errors as JSON on stderr with a non-zero exit status.\n")
		b.WriteString("Run `d9c help <command>` for the arguments of a command.\n")
	} else {
		op := inv.Op
		b.WriteString(usageLine(op) + "\n\n" + op.Description() + "\n")
		if len(op.Args.Properties) > 0 {
			b.WriteString("\nArguments:\n")
			names := append(slices.Clone(op.Args.Required), optionalProps(op.Args)...)
			for _, name := range names {
				p := op.Args.Properties[name]
				typ := p.Type
				if typ == agent.TypeArray {
					typ = "list of strings"
				}
				flag := flagName(name)
				if p.Short != "" {
					flag = "-" + p.Short + ", " + flag
				}
				fmt.Fprintf(&b, "  %s (%s): %s", flag, typ, p.Description)
				if p.Default != nil {
					fmt.Fprintf(&b, " (default %v)", p.Default)
				}
				if len(p.Enum) > 0 {
					fmt.Fprintf(&b, " (one of %s)", strings.Join(p.Enum, ", "))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\nGlobal flags: " + globalUsage() + "\n")
		b.WriteString("Output flags: " + outputUsage + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// newEncoder returns a JSON encoder that keeps <, > and & literal: port maps
// like "8080->80/tcp" stay readable (and shorter) for the agent.
func newEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

// WriteError writes err as the stderr error envelope
// {"error":{"code","message","hint"}} (classifying it first) and returns the
// process exit status for its code.
func WriteError(w io.Writer, err error) int {
	e := agent.Classify(err)
	_ = newEncoder(w).Encode(agent.ErrorEnvelope{Error: e})
	return e.Code.ExitCode()
}
