// Package alerts flags containers whose live resource usage crosses
// user-configured thresholds (CPU% / memory%). The result drives the ⚠ row
// markers in the containers table and the alert count in the header. Thresholds
// load from the same d9c-config.yaml the theme and keybindings use (an "alerts:"
// section) and can be changed at runtime via the :alert command.
//
// Example d9c-config.yaml:
//
//	alerts:          # optional; omit or set 0 to disable a metric
//	  cpu: 80        # mark a container when CPU% ≥ 80
//	  mem: 90        # mark a container when MEM% ≥ 90
package alerts

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"d9c/internal/docker"
)

// Thresholds defines the resource-usage limits that mark a running container as
// alerting. A non-positive value disables that metric. CPU is a percentage of
// total host capacity (matching `docker stats`, so it may exceed 100 on
// multi-core hosts); Mem is a percentage of the container's memory limit.
type Thresholds struct {
	CPU float64
	Mem float64
}

// Active reports whether any metric is enabled.
func (t Thresholds) Active() bool { return t.CPU > 0 || t.Mem > 0 }

// Breach records which thresholds a single container exceeded.
type Breach struct {
	ID   string
	Name string
	CPU  bool // CPU% threshold exceeded
	Mem  bool // MEM% threshold exceeded
}

// Evaluate returns the containers whose live sample meets or exceeds a configured
// threshold, sorted by name. Containers without a stats sample (stopped, or not
// yet sampled) never alert. With no active threshold the result is empty.
func Evaluate(containers []docker.Container, stats map[string]docker.ContainerStats, t Thresholds) []Breach {
	if !t.Active() {
		return nil
	}
	out := make([]Breach, 0)
	for _, c := range containers {
		s, ok := stats[c.ID]
		if !ok {
			continue
		}
		b := Breach{ID: c.ID, Name: c.Name}
		if t.CPU > 0 && s.CPUPerc >= t.CPU {
			b.CPU = true
		}
		if t.Mem > 0 && s.MemPerc >= t.Mem {
			b.Mem = true
		}
		if b.CPU || b.Mem {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// BreachSet reduces breaches to the set of breaching container IDs, for the
// table row markers. Returns nil when there are none.
func BreachSet(breaches []Breach) map[string]bool {
	if len(breaches) == 0 {
		return nil
	}
	set := make(map[string]bool, len(breaches))
	for _, b := range breaches {
		set[b.ID] = true
	}
	return set
}

// config is the on-disk shape of the "alerts:" section. A pointer distinguishes
// an absent section (disabled) from one with zero values.
type config struct {
	Alerts *struct {
		CPU float64 `yaml:"cpu"`
		Mem float64 `yaml:"mem"`
	} `yaml:"alerts"`
}

// Load reads the "alerts:" section from the config file at path. A missing file
// or absent section yields disabled thresholds (not an error); malformed YAML or
// a negative value is an error.
func Load(path string) (Thresholds, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Thresholds{}, nil
		}
		return Thresholds{}, fmt.Errorf("read config file: %w", err)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Thresholds{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	if cfg.Alerts == nil {
		return Thresholds{}, nil
	}
	return Resolve(cfg.Alerts.CPU, cfg.Alerts.Mem)
}

// Resolve validates raw CPU/MEM threshold percentages. A negative value is an
// error; zero disables the metric.
func Resolve(cpu, mem float64) (Thresholds, error) {
	if cpu < 0 {
		return Thresholds{}, fmt.Errorf("alerts: cpu threshold %g is negative", cpu)
	}
	if mem < 0 {
		return Thresholds{}, fmt.Errorf("alerts: mem threshold %g is negative", mem)
	}
	return Thresholds{CPU: cpu, Mem: mem}, nil
}
