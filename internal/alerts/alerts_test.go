package alerts

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"d9c/internal/docker"
)

func TestThresholds_Active(t *testing.T) {
	tests := []struct {
		name string
		t    Thresholds
		want bool
	}{
		{"both zero", Thresholds{}, false},
		{"cpu only", Thresholds{CPU: 80}, true},
		{"mem only", Thresholds{Mem: 90}, true},
		{"both", Thresholds{CPU: 50, Mem: 50}, true},
		{"negative ignored", Thresholds{CPU: -1}, false},
	}
	for _, tt := range tests {
		if got := tt.t.Active(); got != tt.want {
			t.Errorf("%s: Active() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestEvaluate(t *testing.T) {
	containers := []docker.Container{
		{ID: "web", Name: "web", State: "running"},
		{ID: "api", Name: "api", State: "running"},
		{ID: "db", Name: "db", State: "exited"}, // no sample
	}
	stats := map[string]docker.ContainerStats{
		"web": {ID: "web", CPUPerc: 95, MemPerc: 40},
		"api": {ID: "api", CPUPerc: 10, MemPerc: 92},
	}

	tests := []struct {
		name string
		thr  Thresholds
		want []Breach
	}{
		{"disabled", Thresholds{}, nil},
		{
			"cpu only flags web",
			Thresholds{CPU: 80},
			[]Breach{{ID: "web", Name: "web", CPU: true}},
		},
		{
			"mem only flags api",
			Thresholds{Mem: 90},
			[]Breach{{ID: "api", Name: "api", Mem: true}},
		},
		{
			"both flag both, sorted by name",
			Thresholds{CPU: 80, Mem: 90},
			[]Breach{
				{ID: "api", Name: "api", Mem: true},
				{ID: "web", Name: "web", CPU: true},
			},
		},
		{
			"threshold is inclusive (>=)",
			Thresholds{CPU: 95},
			[]Breach{{ID: "web", Name: "web", CPU: true}},
		},
		{
			"high threshold flags nothing",
			Thresholds{CPU: 200, Mem: 200},
			[]Breach{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(containers, stats, tt.thr)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Evaluate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBreachSet(t *testing.T) {
	if got := BreachSet(nil); got != nil {
		t.Errorf("BreachSet(nil) = %v, want nil", got)
	}
	breaches := []Breach{{ID: "a"}, {ID: "b", CPU: true}}
	got := BreachSet(breaches)
	want := map[string]bool{"a": true, "b": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BreachSet() = %v, want %v", got, want)
	}
}

func TestResolve(t *testing.T) {
	if _, err := Resolve(-1, 0); err == nil {
		t.Error("Resolve(-1, 0) expected error for negative cpu")
	}
	if _, err := Resolve(0, -5); err == nil {
		t.Error("Resolve(0, -5) expected error for negative mem")
	}
	got, err := Resolve(80, 90)
	if err != nil {
		t.Fatalf("Resolve(80, 90) error: %v", err)
	}
	if got != (Thresholds{CPU: 80, Mem: 90}) {
		t.Errorf("Resolve(80, 90) = %+v", got)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	// Missing file → disabled, no error.
	got, err := Load(filepath.Join(dir, "absent.yaml"))
	if err != nil || got.Active() {
		t.Errorf("Load(absent) = %+v, %v; want disabled, nil", got, err)
	}

	// File without an alerts section → disabled.
	noSection := filepath.Join(dir, "no-section.yaml")
	if err := os.WriteFile(noSection, []byte("theme: dracula\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Load(noSection)
	if err != nil || got.Active() {
		t.Errorf("Load(no-section) = %+v, %v; want disabled, nil", got, err)
	}

	// Valid alerts section.
	valid := filepath.Join(dir, "valid.yaml")
	if err := os.WriteFile(valid, []byte("alerts:\n  cpu: 80\n  mem: 90\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Load(valid)
	if err != nil {
		t.Fatalf("Load(valid) error: %v", err)
	}
	if got != (Thresholds{CPU: 80, Mem: 90}) {
		t.Errorf("Load(valid) = %+v, want {80 90}", got)
	}

	// Negative value → error.
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("alerts:\n  cpu: -1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("Load(bad) expected error for negative threshold")
	}
}
