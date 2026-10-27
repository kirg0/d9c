package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// JSON Schema type names supported by Schema.
const (
	TypeObject  = "object"
	TypeString  = "string"
	TypeInteger = "integer"
	TypeBoolean = "boolean"
	TypeArray   = "array"
)

// Schema is the subset of JSON Schema used to describe operation arguments.
// It marshals to a valid JSON Schema document (the MCP tool inputSchema) and
// also drives argument validation for every frontend, so the CLI and MCP agree
// on what an operation accepts. Arguments are flat: an operation's schema is an
// object of scalar or string-array properties.
type Schema struct {
	Type        string             `json:"type"`
	Description string             `json:"description,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
	Default     any                `json:"default,omitempty"`
	Minimum     *int               `json:"minimum,omitempty"`
	// Short is a one-letter CLI alias of a property (`-a` for "all"). It is a
	// command-line convenience only and stays out of the JSON Schema.
	Short string `json:"-"`
	// AdditionalProperties is always false for argument objects: an unknown
	// argument is a usage error, not something to ignore silently.
	AdditionalProperties *bool `json:"additionalProperties,omitempty"`
}

// ReservedArgs are the argument names frontends keep for the output options
// (format, Shape fields and limit); no operation may declare them.
var ReservedArgs = []string{"output", "fields", "limit"}

// Object describes an argument object with the given properties; required
// lists the properties that must be present.
func Object(props map[string]*Schema, required ...string) *Schema {
	no := false
	return &Schema{Type: TypeObject, Properties: props, Required: required, AdditionalProperties: &no}
}

// NoArgs is the schema of an operation that takes no arguments.
func NoArgs() *Schema { return Object(nil) }

// String describes a string argument.
func String(desc string) *Schema { return &Schema{Type: TypeString, Description: desc} }

// Integer describes an integer argument.
func Integer(desc string) *Schema { return &Schema{Type: TypeInteger, Description: desc} }

// Boolean describes a boolean argument.
func Boolean(desc string) *Schema { return &Schema{Type: TypeBoolean, Description: desc} }

// StringArray describes a list-of-strings argument.
func StringArray(desc string) *Schema {
	return &Schema{Type: TypeArray, Description: desc, Items: &Schema{Type: TypeString}}
}

// WithDefault sets the value applied when the argument is omitted.
func (s *Schema) WithDefault(v any) *Schema {
	s.Default = v
	return s
}

// WithEnum restricts a string argument to the given values.
func (s *Schema) WithEnum(values ...string) *Schema {
	s.Enum = values
	return s
}

// WithShort sets the one-letter CLI alias of the argument.
func (s *Schema) WithShort(letter string) *Schema {
	s.Short = letter
	return s
}

// WithMin sets the inclusive lower bound of an integer argument.
func (s *Schema) WithMin(n int) *Schema {
	s.Minimum = &n
	return s
}

// check validates the schema declaration itself (used by Registry.Register),
// catching mistakes like a default of the wrong type at startup rather than on
// the first call.
func (s *Schema) check() error {
	if s == nil || s.Type != TypeObject {
		return fmt.Errorf("argument schema must be an object")
	}
	shorts := map[string]string{}
	for name, p := range s.Properties {
		if p == nil {
			return fmt.Errorf("property %q: nil schema", name)
		}
		if slices.Contains(ReservedArgs, name) {
			return fmt.Errorf("property %q: name is reserved for the output options", name)
		}
		if p.Short != "" {
			// -h is help, -H the global host flag and -o the output format on
			// every command.
			if len(p.Short) != 1 || !unicode.IsLetter(rune(p.Short[0])) || strings.EqualFold(p.Short, "h") || p.Short == "o" {
				return fmt.Errorf("property %q: short alias must be one letter other than h/H/o, got %q", name, p.Short)
			}
			if other, dup := shorts[p.Short]; dup {
				return fmt.Errorf("property %q: short alias -%s already used by %q", name, p.Short, other)
			}
			shorts[p.Short] = name
		}
		switch p.Type {
		case TypeString, TypeInteger, TypeBoolean:
		case TypeArray:
			if p.Items == nil || p.Items.Type != TypeString {
				return fmt.Errorf("property %q: only string arrays are supported", name)
			}
		default:
			return fmt.Errorf("property %q: unsupported type %q", name, p.Type)
		}
		if p.Description == "" {
			return fmt.Errorf("property %q: missing description", name)
		}
		if len(p.Enum) > 0 && p.Type != TypeString {
			return fmt.Errorf("property %q: enum is only supported on strings", name)
		}
		if p.Minimum != nil && p.Type != TypeInteger {
			return fmt.Errorf("property %q: minimum is only supported on integers", name)
		}
		if p.Default != nil {
			if _, err := p.coerce(p.Default); err != nil {
				return fmt.Errorf("property %q: bad default: %w", name, err)
			}
		}
	}
	for _, r := range s.Required {
		if _, ok := s.Properties[r]; !ok {
			return fmt.Errorf("required property %q is not declared", r)
		}
	}
	return nil
}

// Normalize validates args against the object schema s and returns a new Args
// with defaults applied and values converted to their canonical Go types:
// string, int, bool and []string. Numbers may arrive as float64 (encoding/json),
// json.Number, any Go integer, or a decimal string (CLI flags); booleans as
// bool or "true"/"false". Every violation is a CodeUsage *Error.
func (s *Schema) Normalize(args Args) (Args, error) {
	for name := range args {
		if _, ok := s.Properties[name]; !ok {
			return nil, Errorf(CodeUsage, "unknown argument %q", name).
				WithHint("valid arguments: " + s.propertyList())
		}
	}
	out := make(Args, len(s.Properties))
	for name, p := range s.Properties {
		raw, ok := args[name]
		if !ok || raw == nil {
			if p.Default != nil {
				v, _ := p.coerce(p.Default) // validated by check
				out[name] = v
			}
			continue
		}
		v, err := p.coerce(raw)
		if err != nil {
			return nil, Errorf(CodeUsage, "argument %q: %v", name, err).WithHint(p.usage(name))
		}
		out[name] = v
	}
	for _, r := range s.Required {
		if _, ok := out[r]; !ok {
			return nil, Errorf(CodeUsage, "missing required argument %q", r).
				WithHint(s.Properties[r].usage(r))
		}
	}
	return out, nil
}

// usage renders one property for a usage hint: `tail (integer): lines…`.
func (s *Schema) usage(name string) string {
	typ := s.Type
	if typ == TypeArray {
		typ = "list of strings"
	}
	return fmt.Sprintf("%s (%s): %s", name, typ, s.Description)
}

// propertyList renders the sorted property names for a usage hint.
func (s *Schema) propertyList() string {
	if len(s.Properties) == 0 {
		return "none"
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// coerce converts one raw value into the canonical Go type of property s and
// checks enum/minimum constraints.
func (s *Schema) coerce(v any) (any, error) {
	switch s.Type {
	case TypeString:
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want a string, got %T", v)
		}
		if len(s.Enum) > 0 && !slices.Contains(s.Enum, str) {
			return nil, fmt.Errorf("%q is not one of %v", str, s.Enum)
		}
		return str, nil
	case TypeInteger:
		n, err := toInt(v)
		if err != nil {
			return nil, err
		}
		if s.Minimum != nil && n < *s.Minimum {
			return nil, fmt.Errorf("%d is below the minimum %d", n, *s.Minimum)
		}
		return n, nil
	case TypeBoolean:
		switch b := v.(type) {
		case bool:
			return b, nil
		case string:
			if p, err := strconv.ParseBool(b); err == nil {
				return p, nil
			}
		}
		return nil, fmt.Errorf("want a boolean, got %v", v)
	case TypeArray:
		switch a := v.(type) {
		case []string:
			return slices.Clone(a), nil
		case []any:
			out := make([]string, len(a))
			for i, e := range a {
				str, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("item %d: want a string, got %T", i, e)
				}
				out[i] = str
			}
			return out, nil
		}
		return nil, fmt.Errorf("want a list of strings, got %T", v)
	}
	return nil, fmt.Errorf("unsupported type %q", s.Type)
}

// toInt accepts the integer encodings the frontends produce.
func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case float64:
		if n != math.Trunc(n) || math.IsInf(n, 0) {
			return 0, fmt.Errorf("want an integer, got %v", n)
		}
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("want an integer, got %s", n)
		}
		return int(i), nil
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("want an integer, got %q", n)
		}
		return i, nil
	}
	return 0, fmt.Errorf("want an integer, got %T", v)
}

// Args is a normalized argument set handed to a Handler. Values have the
// canonical types produced by Schema.Normalize, so the getters below never
// need to re-validate; a missing optional argument yields the zero value.
type Args map[string]any

// String returns the string argument name ("" when absent).
func (a Args) String(name string) string {
	s, _ := a[name].(string)
	return s
}

// Int returns the integer argument name (0 when absent).
func (a Args) Int(name string) int {
	n, _ := a[name].(int)
	return n
}

// Bool returns the boolean argument name (false when absent).
func (a Args) Bool(name string) bool {
	b, _ := a[name].(bool)
	return b
}

// Strings returns the string-list argument name (nil when absent).
func (a Args) Strings(name string) []string {
	s, _ := a[name].([]string)
	return s
}

// Has reports whether name is set (explicitly or through a default).
func (a Args) Has(name string) bool {
	_, ok := a[name]
	return ok
}
