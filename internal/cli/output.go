package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/kirg0/d9c/internal/agent"
)

// Format is the stdout rendering of a result (-o/--output).
type Format string

// The output formats. FormatJSON, the default, is one envelope document;
// FormatNDJSON writes list items one JSON value per line (for line-by-line
// consumers of logs and events); FormatText is a compact table for people.
const (
	FormatJSON   Format = "json"
	FormatText   Format = "text"
	FormatNDJSON Format = "ndjson"
)

var formats = []Format{FormatJSON, FormatText, FormatNDJSON}

// Output holds the output options of an invocation: the format and the
// agent.Shape (--fields, --limit) applied before rendering.
type Output struct {
	Format Format
	Shape  agent.Shape
}

// outputUsage lists the output flags for help texts.
const outputUsage = "-o, --output json|text|ndjson, --fields a,b, --limit N"

// parseFlag consumes an output flag: ok is false when name is not one.
// value fetches the flag's value; tok is the flag as typed, for messages.
func (o *Output) parseFlag(name, tok string, value func() (string, error)) (ok bool, err error) {
	switch name {
	case "o", "output":
		v, err := value()
		if err != nil {
			return true, err
		}
		f := Format(strings.ToLower(v))
		if !slices.Contains(formats, f) {
			return true, agent.Errorf(agent.CodeUsage, "flag %s: unknown format %q", tok, v).
				WithHint("one of json, text, ndjson")
		}
		o.Format = f
	case "fields":
		v, err := value()
		if err != nil {
			return true, err
		}
		for f := range strings.SplitSeq(v, ",") {
			if f = strings.TrimSpace(f); f != "" && !slices.Contains(o.Shape.Fields, f) {
				o.Shape.Fields = append(o.Shape.Fields, f)
			}
		}
	case "limit":
		v, err := value()
		if err != nil {
			return true, err
		}
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 0 {
			return true, agent.Errorf(agent.CodeUsage, "flag %s: want a whole number of items (0 = no limit), got %q", tok, v)
		}
		o.Shape.Limit = n
	default:
		return false, nil
	}
	return true, nil
}

// WriteResult renders env on w in out.Format after applying out.Shape. A
// shaping error (unknown field, --limit on an object) is returned before
// anything is written. In text mode the truncation/partial-failure notice
// goes to warn so the table stays clean for pipes; JSON formats carry it
// in-band.
func WriteResult(w, warn io.Writer, env agent.Envelope, out Output) error {
	key := agent.ItemsKey(env.Data)
	cols := out.Shape.Fields
	if len(cols) == 0 {
		cols = agent.ItemFields(env.Data)
	}
	res, err := out.Shape.Apply(agent.Result{Data: env.Data, Truncated: env.Truncated, Hint: env.Hint})
	if err != nil {
		return err
	}
	env.Data, env.Truncated, env.Hint = res.Data, res.Truncated, res.Hint

	switch out.Format {
	case FormatNDJSON:
		return writeNDJSON(w, env, key)
	case FormatText:
		if err := writeText(w, env.Data, key, cols); err != nil {
			return err
		}
		var heads []string
		if env.Partial {
			heads = append(heads, "partial failure")
		}
		if env.Truncated {
			heads = append(heads, "truncated")
		}
		if len(heads) > 0 {
			_, err = fmt.Fprintln(warn, joinNotice(strings.Join(heads, ", "), env.Hint))
		}
		return err
	default:
		return newEncoder(w).Encode(env)
	}
}

func joinNotice(head, hint string) string {
	if hint == "" {
		return head
	}
	return head + ": " + hint
}

// truncatedRecord is the last NDJSON line of a cut list or a partially
// failed command.
type truncatedRecord struct {
	Truncated bool   `json:"truncated"`
	Partial   bool   `json:"partial,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// writeNDJSON writes list items one per line (a single line for an object)
// and, when the output was cut or some targets failed, a final
// {"truncated":…,"partial":…,"hint":…} record.
func writeNDJSON(w io.Writer, env agent.Envelope, key string) error {
	g, err := agent.Generic(env.Data)
	if err != nil {
		return err
	}
	enc := newEncoder(w)
	if items, ok := agent.SplitItems(g, key); ok {
		for _, it := range items {
			if err := enc.Encode(it); err != nil {
				return err
			}
		}
	} else if err := enc.Encode(g); err != nil {
		return err
	}
	if env.Truncated || env.Partial {
		return enc.Encode(truncatedRecord{Truncated: env.Truncated, Partial: env.Partial, Hint: env.Hint})
	}
	return nil
}

// writeText renders data for people: a table for a list of objects, one
// line per item for a list of plain values, YAML for a single object (a
// wrapped list that came back empty shows its other fields instead).
// Control characters from container-controlled strings are dropped.
func writeText(w io.Writer, data any, key string, cols []string) error {
	g, err := agent.Generic(data)
	if err != nil {
		return err
	}
	items, ok := agent.SplitItems(g, key)
	switch {
	case ok && (len(items) > 0 || key == ""):
		return writeItems(w, items, cols)
	case ok:
		obj := g.(map[string]any)
		delete(obj, key)
		return writeYAML(w, obj)
	default:
		return writeYAML(w, g)
	}
}

// writeItems writes a table when every item is an object, else one line per
// item.
func writeItems(w io.Writer, items []any, cols []string) error {
	// An empty list still gets the header when the item type is known.
	objects := len(items) > 0 || len(cols) > 0
	for _, it := range items {
		if _, isObj := it.(map[string]any); !isObj {
			objects = false
			break
		}
	}
	if !objects {
		var b strings.Builder
		for _, it := range items {
			s, isStr := it.(string)
			if !isStr {
				s = cell(it)
			}
			b.WriteString(sanitize(s))
			b.WriteByte('\n')
		}
		_, err := io.WriteString(w, b.String())
		return err
	}

	cols = tableColumns(items, cols)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = strings.ToUpper(c)
	}
	_, _ = fmt.Fprintln(tw, strings.Join(head, "\t")) // write errors surface on Flush
	row := make([]string, len(cols))
	for _, it := range items {
		obj := it.(map[string]any)
		for i, c := range cols {
			row[i] = cell(obj[c])
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

// tableColumns returns cols followed by any keys the items carry beyond them
// (sorted) — the whole key set when the item type declares no fields.
func tableColumns(items []any, cols []string) []string {
	out := slices.Clone(cols)
	var extra []string
	for _, it := range items {
		for k := range it.(map[string]any) {
			if !slices.Contains(out, k) && !slices.Contains(extra, k) {
				extra = append(extra, k)
			}
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// cell renders one table value on a single line; absent and empty values
// show as "-" so columns stay splittable by whitespace.
func cell(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
	case string:
		s = x
	case json.Number:
		s = x.String()
	case bool:
		s = strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = cell(e)
		}
		s = strings.Join(parts, ",")
	default:
		raw, _ := json.Marshal(x)
		s = string(raw)
	}
	s = strings.ReplaceAll(sanitize(s), "\t", " ")
	if s == "" {
		return "-"
	}
	return s
}

// sanitize drops control characters (escape sequences, carriage returns)
// except tabs, so untrusted container output cannot drive the terminal.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// writeYAML writes v as YAML, the text form of a single object.
func writeYAML(w io.Writer, v any) error {
	raw, err := yaml.Marshal(yamlReady(v))
	if err != nil {
		return fmt.Errorf("encode text output: %w", err)
	}
	_, err = w.Write(raw)
	return err
}

// yamlReady turns json.Number into int64/float64 so YAML prints numbers
// rather than quoted strings.
func yamlReady(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = yamlReady(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = yamlReady(e)
		}
		return x
	default:
		return v
	}
}
