package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestShapeApply(t *testing.T) {
	ps := []ContainerEntry{
		{ID: "c1", Name: "web", State: "running"},
		{ID: "c2", Name: "db", State: "exited"},
		{ID: "c3", Name: "cache", State: "running"},
	}
	logs := LogsData{Target: "web", Lines: []string{"a", "b", "c"}}

	tests := []struct {
		name          string
		shape         Shape
		in            Result
		wantJSON      string
		wantTruncated bool
		wantHint      string
	}{
		{"zero shape keeps data", Shape{}, Result{Data: ps[:1]},
			`[{"id":"c1","name":"web","image":"","state":"running","status":""}]`, false, ""},
		{"limit cuts", Shape{Limit: 2}, Result{Data: ps},
			`[{"id":"c1","image":"","name":"web","state":"running","status":""},{"id":"c2","image":"","name":"db","state":"exited","status":""}]`,
			true, "showing the first 2 of 3 items; raise --limit (0 = no limit)"},
		{"limit not reached", Shape{Limit: 3}, Result{Data: ps[:1]},
			`[{"id":"c1","image":"","name":"web","state":"running","status":""}]`, false, ""},
		{"fields project", Shape{Fields: []string{"name", "state"}}, Result{Data: ps[:2]},
			`[{"name":"web","state":"running"},{"name":"db","state":"exited"}]`, false, ""},
		{"declared but omitted field", Shape{Fields: []string{"health"}}, Result{Data: ps[:1]},
			`[{}]`, false, ""},
		{"wrapped list limit keeps the rest", Shape{Limit: 1}, Result{Data: logs, Truncated: true, Hint: "older lines may exist"},
			`{"lines":["a"],"target":"web"}`, true, "older lines may exist; showing the first 1 of 3 items; raise --limit (0 = no limit)"},
		{"object fields", Shape{Fields: []string{"version"}}, Result{Data: InfoData{Name: "x", Version: "29"}},
			`{"version":"29"}`, false, ""},
		{"map rows fields", Shape{Fields: []string{"type"}}, Result{Data: SystemDFData{Rows: []map[string]string{{"type": "Images", "size": "1GB"}}}},
			`{"rows":[{"type":"Images"}]}`, false, ""},
		{"big numbers stay exact", Shape{Fields: []string{"mem_total"}}, Result{Data: InfoData{MemTotal: 9007199254740993}},
			`{"mem_total":9007199254740993}`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.shape.Apply(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(got.Data)
			if string(raw) != tt.wantJSON {
				t.Errorf("data = %s, want %s", raw, tt.wantJSON)
			}
			if got.Truncated != tt.wantTruncated || got.Hint != tt.wantHint {
				t.Errorf("truncated = %v hint = %q, want %v %q", got.Truncated, got.Hint, tt.wantTruncated, tt.wantHint)
			}
		})
	}
}

func TestShapeApplyErrors(t *testing.T) {
	tests := []struct {
		name     string
		shape    Shape
		data     any
		wantMsg  string
		wantHint string
	}{
		{"negative limit", Shape{Limit: -1}, []ContainerEntry{}, "--limit must be 0", ""},
		{"limit on object", Shape{Limit: 1}, InfoData{}, "--limit applies to list output", "drop --limit"},
		{"unknown field", Shape{Fields: []string{"naem"}}, []ContainerEntry{{ID: "x"}}, `unknown field "naem"`, "health, id, image, name"},
		{"unknown field on empty list", Shape{Fields: []string{"nope"}}, []ContainerEntry{}, `unknown field "nope"`, "available fields"},
		{"fields of strings", Shape{Fields: []string{"x"}}, LogsData{Lines: []string{"a"}}, "--fields needs object items", "drop --fields"},
		{"unknown object field", Shape{Fields: []string{"nope"}}, InfoData{}, `unknown field "nope"`, "mem_total"},
		{"fields of a scalar", Shape{Fields: []string{"x"}}, "plain", "--fields applies to object output", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.shape.Apply(Result{Data: tt.data})
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != CodeUsage {
				t.Fatalf("err = %v, want a usage *Error", err)
			}
			if !strings.Contains(ae.Message, tt.wantMsg) || !strings.Contains(ae.Hint, tt.wantHint) {
				t.Errorf("error = %q (hint %q), want %q (hint %q)", ae.Message, ae.Hint, tt.wantMsg, tt.wantHint)
			}
		})
	}
}

func TestItemFields(t *testing.T) {
	type tagged struct {
		A      string `json:"a"`
		B      int    `json:"b,omitempty"`
		Skip   string `json:"-"`
		Plain  string
		hidden string
	}
	_ = tagged{}.hidden
	tests := []struct {
		name string
		data any
		want []string
	}{
		{"nil", nil, nil},
		{"slice of structs", []tagged{}, []string{"a", "b", "Plain"}},
		{"pointer struct", &tagged{}, []string{"a", "b", "Plain"}},
		{"lister of strings", LogsData{}, nil},
		{"lister of maps", SystemDFData{}, nil},
		{"fielder", parseDF("TYPE  TOTAL  SIZE\nImages  2  1 GB\n"), []string{"type", "total", "size"}},
		{"strings", []string{"x"}, nil},
		{"object", HostEntry{}, []string{"name", "host", "read_only"}},
	}
	for _, tt := range tests {
		if got := ItemFields(tt.data); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: ItemFields = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestItemsKey(t *testing.T) {
	tests := []struct {
		data any
		want string
	}{
		{LogsData{}, "lines"},
		{EventsData{}, "events"},
		{SystemDFData{}, "rows"},
		{InfoData{}, ""},
		{"not a lister", ""},
	}
	for _, tt := range tests {
		if got := ItemsKey(tt.data); got != tt.want {
			t.Errorf("ItemsKey(%T) = %q, want %q", tt.data, got, tt.want)
		}
	}
}

// No built-in operation may shadow the output flags.
func TestBuiltinAvoidsReservedArgs(t *testing.T) {
	for _, op := range Builtin().Ops() {
		for name, p := range op.Args.Properties {
			for _, r := range ReservedArgs {
				if name == r {
					t.Errorf("%s: argument %q is reserved", op.Name, name)
				}
			}
			if p.Short == "o" {
				t.Errorf("%s: -o is reserved", op.Name)
			}
		}
	}
}
