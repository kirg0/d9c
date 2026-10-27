package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// logsSchema is a representative argument schema used across the tests.
func logsSchema() *Schema {
	return Object(map[string]*Schema{
		"id":     String("container ID or name"),
		"tail":   Integer("lines from the end").WithDefault(200).WithMin(0),
		"follow": Boolean("stream new lines"),
		"format": String("output format").WithEnum("json", "text").WithDefault("json"),
		"fields": StringArray("fields to keep"),
	}, "id")
}

func TestSchemaNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      Args
		want    Args
		wantErr string
	}{
		{
			name: "defaults applied",
			in:   Args{"id": "web"},
			want: Args{"id": "web", "tail": 200, "format": "json"},
		},
		{
			name: "json decoded values",
			in:   Args{"id": "web", "tail": float64(50), "follow": true, "fields": []any{"a", "b"}},
			want: Args{"id": "web", "tail": 50, "follow": true, "format": "json", "fields": []string{"a", "b"}},
		},
		{
			name: "cli string values",
			in:   Args{"id": "web", "tail": "10", "follow": "true", "format": "text"},
			want: Args{"id": "web", "tail": 10, "follow": true, "format": "text"},
		},
		{
			name: "json.Number and nil treated as absent",
			in:   Args{"id": "web", "tail": json.Number("5"), "follow": nil},
			want: Args{"id": "web", "tail": 5, "format": "json"},
		},
		{name: "missing required", in: Args{}, wantErr: `missing required argument "id"`},
		{name: "unknown argument", in: Args{"id": "x", "bogus": 1}, wantErr: `unknown argument "bogus"`},
		{name: "fractional integer", in: Args{"id": "x", "tail": 1.5}, wantErr: "want an integer"},
		{name: "integer as word", in: Args{"id": "x", "tail": "ten"}, wantErr: "want an integer"},
		{name: "below minimum", in: Args{"id": "x", "tail": -1}, wantErr: "below the minimum"},
		{name: "enum violation", in: Args{"id": "x", "format": "yaml"}, wantErr: "is not one of"},
		{name: "wrong string type", in: Args{"id": 3}, wantErr: "want a string"},
		{name: "wrong bool", in: Args{"id": "x", "follow": "maybe"}, wantErr: "want a boolean"},
		{name: "array item type", in: Args{"id": "x", "fields": []any{"a", 1}}, wantErr: "item 1"},
		{name: "array type", in: Args{"id": "x", "fields": "a"}, wantErr: "want a list of strings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := logsSchema().Normalize(tt.in)
			if tt.wantErr != "" {
				var ae *Error
				if !errors.As(err, &ae) || ae.Code != CodeUsage || !strings.Contains(ae.Message, tt.wantErr) {
					t.Fatalf("err = %v, want usage error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestNormalizeUnknownArgHint(t *testing.T) {
	_, err := logsSchema().Normalize(Args{"x": 1})
	var ae *Error
	if !errors.As(err, &ae) || ae.Hint != "valid arguments: fields, follow, format, id, tail" {
		t.Fatalf("hint = %v", err)
	}
	_, err = NoArgs().Normalize(Args{"x": 1})
	if !errors.As(err, &ae) || ae.Hint != "valid arguments: none" {
		t.Fatalf("no-args hint = %v", err)
	}
	_, err = logsSchema().Normalize(Args{})
	if !errors.As(err, &ae) || ae.Hint != "id (string): container ID or name" {
		t.Fatalf("missing-arg hint = %v", err)
	}
	_, err = logsSchema().Normalize(Args{"id": "x", "fields": 1})
	if !errors.As(err, &ae) || ae.Hint != "fields (list of strings): fields to keep" {
		t.Fatalf("bad-type hint = %v", err)
	}
}

func TestNormalizeDoesNotAliasInput(t *testing.T) {
	in := Args{"id": "x", "fields": []string{"a"}}
	out, err := logsSchema().Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	out.Strings("fields")[0] = "changed"
	if in["fields"].([]string)[0] != "a" {
		t.Error("Normalize returned a slice aliasing the input")
	}
}

func TestSchemaCheck(t *testing.T) {
	tests := []struct {
		name    string
		schema  *Schema
		wantErr string
	}{
		{"valid", logsSchema(), ""},
		{"no args", NoArgs(), ""},
		{"nil", nil, "must be an object"},
		{"not object", String("x"), "must be an object"},
		{"nil property", Object(map[string]*Schema{"a": nil}), "nil schema"},
		{"no description", Object(map[string]*Schema{"a": String("")}), "missing description"},
		{"unsupported type", Object(map[string]*Schema{"a": {Type: "number", Description: "d"}}), "unsupported type"},
		{"object array", Object(map[string]*Schema{"a": {Type: TypeArray, Description: "d", Items: Integer("")}}), "only string arrays"},
		{"array without items", Object(map[string]*Schema{"a": {Type: TypeArray, Description: "d"}}), "only string arrays"},
		{"enum on int", Object(map[string]*Schema{"a": Integer("d").WithEnum("1")}), "enum is only supported"},
		{"min on string", Object(map[string]*Schema{"a": String("d").WithMin(1)}), "minimum is only supported"},
		{"bad default", Object(map[string]*Schema{"a": Integer("d").WithDefault("many")}), "bad default"},
		{"default off enum", Object(map[string]*Schema{"a": String("d").WithEnum("x").WithDefault("y")}), "bad default"},
		{"undeclared required", Object(map[string]*Schema{"a": String("d")}, "b"), `"b" is not declared`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.schema.check()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestSchemaJSON pins the JSON Schema rendering MCP clients receive.
func TestSchemaJSON(t *testing.T) {
	s := Object(map[string]*Schema{
		"id":   String("container ID or name"),
		"tail": Integer("lines").WithDefault(200).WithMin(0),
		"mode": String("m").WithEnum("a", "b"),
		"keys": StringArray("k"),
	}, "id")
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{` +
		`"id":{"type":"string","description":"container ID or name"},` +
		`"keys":{"type":"array","description":"k","items":{"type":"string"}},` +
		`"mode":{"type":"string","description":"m","enum":["a","b"]},` +
		`"tail":{"type":"integer","description":"lines","default":200,"minimum":0}},` +
		`"required":["id"],"additionalProperties":false}`
	if string(b) != want {
		t.Errorf("schema json:\n got %s\nwant %s", b, want)
	}
	b, _ = json.Marshal(NoArgs())
	if string(b) != `{"type":"object","additionalProperties":false}` {
		t.Errorf("NoArgs json = %s", b)
	}
}

func TestArgsGetters(t *testing.T) {
	a := Args{"s": "v", "n": 3, "b": true, "l": []string{"x"}}
	if a.String("s") != "v" || a.Int("n") != 3 || !a.Bool("b") || !reflect.DeepEqual(a.Strings("l"), []string{"x"}) {
		t.Errorf("getters returned wrong values for %v", a)
	}
	if a.String("n") != "" || a.Int("s") != 0 || a.Bool("missing") || a.Strings("s") != nil {
		t.Error("getters must return zero values on type mismatch or absence")
	}
	if !a.Has("s") || a.Has("missing") {
		t.Error("Has is wrong")
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		in      any
		want    int
		wantErr bool
	}{
		{7, 7, false},
		{int32(7), 7, false},
		{int64(7), 7, false},
		{float64(7), 7, false},
		{json.Number("7"), 7, false},
		{"7", 7, false},
		{json.Number("7.5"), 0, true},
		{float64(7.5), 0, true},
		{"x", 0, true},
		{true, 0, true},
	}
	for _, tt := range tests {
		got, err := toInt(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("toInt(%#v) = %d, %v", tt.in, got, err)
		}
	}
}
