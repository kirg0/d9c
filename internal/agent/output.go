package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// Lister is implemented by Data types that wrap their item list in an object
// (`logs` lines, `events`): ItemsKey names the JSON key of that list, so output
// shaping and list renderers (NDJSON, text) treat it as the items. Data that is
// itself a slice needs no marker.
type Lister interface {
	ItemsKey() string
}

// Fielder is implemented by Data whose items are maps (no struct to reflect
// on) but have a known key order, such as the `system df` columns.
type Fielder interface {
	ItemFields() []string
}

// Shape holds the output options every frontend applies to a Result: Fields
// keeps only the named keys of each item (of the object itself when the output
// is not a list), Limit keeps the first Limit items (0 = no limit).
type Shape struct {
	Fields []string
	Limit  int
}

// IsZero reports whether s changes nothing.
func (s Shape) IsZero() bool { return len(s.Fields) == 0 && s.Limit == 0 }

// Apply returns r shaped by s. A non-zero shape converts Data into generic
// JSON values (map[string]any, []any, string, json.Number, bool, nil), so the
// projected objects marshal with sorted keys. Cutting items sets Truncated and
// adds a hint. Asking for an unknown field, fields of plain-string items or a
// limit on non-list output is a CodeUsage error.
func (s Shape) Apply(r Result) (Result, error) {
	if s.IsZero() {
		return r, nil
	}
	if s.Limit < 0 {
		return Result{}, Errorf(CodeUsage, "--limit must be 0 (no limit) or more, got %d", s.Limit)
	}
	key := ItemsKey(r.Data)
	known := ItemFields(r.Data)
	g, err := Generic(r.Data)
	if err != nil {
		return Result{}, err
	}
	items, isList := SplitItems(g, key)

	if s.Limit > 0 {
		if !isList {
			return Result{}, Errorf(CodeUsage, "--limit applies to list output; this command returns one object").
				WithHint("drop --limit")
		}
		if len(items) > s.Limit {
			total := len(items)
			items = items[:s.Limit]
			r.Truncated = true
			r.Hint = joinHints(r.Hint, fmt.Sprintf("showing the first %d of %d items; raise --limit (0 = no limit)", s.Limit, total))
		}
	}

	if len(s.Fields) > 0 {
		if !isList {
			obj, ok := g.(map[string]any)
			if !ok {
				return Result{}, Errorf(CodeUsage, "--fields applies to object output")
			}
			if err := checkFields(s.Fields, known, []any{obj}); err != nil {
				return Result{}, err
			}
			g = project(obj, s.Fields)
		} else {
			if err := checkFields(s.Fields, known, items); err != nil {
				return Result{}, err
			}
			out := make([]any, len(items))
			for i, it := range items {
				out[i] = project(it.(map[string]any), s.Fields)
			}
			items = out
		}
	}

	if isList {
		g = joinItems(g, key, items)
	}
	r.Data = g
	return r, nil
}

// joinHints appends extra to an existing hint.
func joinHints(hint, extra string) string {
	if hint == "" {
		return extra
	}
	return hint + "; " + extra
}

// checkFields verifies every requested field is a declared key of the items or
// present in one of them, and that the items are objects at all.
func checkFields(fields, known []string, items []any) error {
	avail := slices.Clone(known)
	for _, it := range items {
		obj, ok := it.(map[string]any)
		if !ok {
			return Errorf(CodeUsage, "--fields needs object items; this command lists plain values").
				WithHint("drop --fields")
		}
		for k := range obj {
			if !slices.Contains(avail, k) {
				avail = append(avail, k)
			}
		}
	}
	for _, f := range fields {
		if !slices.Contains(avail, f) {
			sorted := slices.Clone(avail)
			sort.Strings(sorted)
			return Errorf(CodeUsage, "unknown field %q", f).
				WithHint("available fields: " + strings.Join(sorted, ", "))
		}
	}
	return nil
}

// project keeps only fields of obj; a field the object omits stays absent.
func project(obj map[string]any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := obj[f]; ok {
			out[f] = v
		}
	}
	return out
}

// Generic converts data to generic JSON values through a JSON round trip;
// numbers stay exact as json.Number.
func Generic(data any) (any, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode output: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var g any
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("decode output: %w", err)
	}
	return g, nil
}

// ItemsKey returns the Lister key of data, or "" when data does not wrap a
// list.
func ItemsKey(data any) string {
	if l, ok := data.(Lister); ok {
		return l.ItemsKey()
	}
	return ""
}

// SplitItems finds the item list in generic data g: g itself when it is a
// list, else g[key] for a non-empty key. ok is false for non-list output.
func SplitItems(g any, key string) (items []any, ok bool) {
	if list, ok := g.([]any); ok {
		return list, true
	}
	if obj, isObj := g.(map[string]any); isObj && key != "" {
		if list, ok := obj[key].([]any); ok {
			return list, true
		}
	}
	return nil, false
}

// joinItems puts items back where SplitItems found them.
func joinItems(g any, key string, items []any) any {
	if obj, ok := g.(map[string]any); ok && key != "" {
		obj[key] = items
		return obj
	}
	return items
}

// ItemFields returns the JSON keys the type of data declares for its items —
// the element struct of a slice or of a Lister's list, or data's own struct
// for single-object output — in declaration order. It is nil when the items
// are not structs (plain strings, maps) and data is not a Fielder.
func ItemFields(data any) []string {
	if f, ok := data.(Fielder); ok {
		return f.ItemFields()
	}
	if data == nil {
		return nil
	}
	t := reflect.TypeOf(data)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if key := ItemsKey(data); key != "" && t.Kind() == reflect.Struct {
		for i := range t.NumField() {
			if name, ok := jsonName(t.Field(i)); ok && name == key {
				t = t.Field(i).Type
				break
			}
		}
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := range t.NumField() {
		if name, ok := jsonName(t.Field(i)); ok {
			out = append(out, name)
		}
	}
	return out
}

// jsonName is the JSON key encoding/json uses for f (ok false when skipped).
func jsonName(f reflect.StructField) (string, bool) {
	if !f.IsExported() {
		return "", false
	}
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	switch name {
	case "-":
		return "", false
	case "":
		return f.Name, true
	}
	return name, true
}
