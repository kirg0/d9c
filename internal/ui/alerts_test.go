package ui

import (
	"testing"

	"d9c/internal/alerts"
	"d9c/internal/config"
	"d9c/internal/docker"
	"d9c/internal/ui/cmdline"
)

// TestAlertCommand exercises :alert — setting CPU/MEM thresholds, partial and
// full disabling, reporting (no-arg), and rejecting bad input.
func TestAlertCommand(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)

	// No arg reports state rather than changing it (disabled by default).
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "alert"}); err == nil {
		t.Error("alert without args should report current state as an error")
	}
	if m.alerts.Active() {
		t.Error("reporting must not enable alerts")
	}

	// Set CPU threshold.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "alert", Args: []string{"cpu", "80"}}); err != nil {
		t.Fatalf("alert cpu 80: %v", err)
	}
	if m.alerts.CPU != 80 {
		t.Errorf("CPU = %v, want 80", m.alerts.CPU)
	}

	// Set MEM threshold, tolerating a trailing %.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "alert", Args: []string{"mem", "90%"}}); err != nil {
		t.Fatalf("alert mem 90%%: %v", err)
	}
	if m.alerts.Mem != 90 {
		t.Errorf("Mem = %v, want 90", m.alerts.Mem)
	}

	// Disable a single metric.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "alert", Args: []string{"cpu", "off"}}); err != nil {
		t.Fatalf("alert cpu off: %v", err)
	}
	if m.alerts.CPU != 0 || m.alerts.Mem != 90 {
		t.Errorf("after cpu off: CPU=%v Mem=%v, want 0/90", m.alerts.CPU, m.alerts.Mem)
	}

	// Bad value leaves thresholds untouched.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "alert", Args: []string{"mem", "soon"}}); err == nil {
		t.Error("bad threshold should error")
	}
	if m.alerts.Mem != 90 {
		t.Errorf("failed parse must not change Mem (= %v)", m.alerts.Mem)
	}

	// Full disable.
	if _, err := m.dispatchCommand(&cmdline.CommandMsg{Name: "alert", Args: []string{"off"}}); err != nil {
		t.Fatalf("alert off: %v", err)
	}
	if m.alerts.Active() {
		t.Error("alert off should disable all metrics")
	}
}

// TestContainerAlertSet flags only breaching running containers and stays nil
// when alerts are disabled.
func TestContainerAlertSet(t *testing.T) {
	m := NewModel(&config.Config{}, docker.NewFakeBackend(), nil, nil, false)
	m.containers = []docker.Container{
		{ID: "web", Name: "web", State: "running"},
		{ID: "api", Name: "api", State: "running"},
	}
	m.stats = map[string]docker.ContainerStats{
		"web": {ID: "web", CPUPerc: 95},
		"api": {ID: "api", CPUPerc: 5},
	}

	if got := m.containerAlertSet(); got != nil {
		t.Errorf("disabled alerts: set = %v, want nil", got)
	}

	m.alerts = alerts.Thresholds{CPU: 80}
	got := m.containerAlertSet()
	if !got["web"] || got["api"] {
		t.Errorf("set = %v, want only web flagged", got)
	}
}
