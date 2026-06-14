package docker

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
)

func TestFormatDiskUsage(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 600 * 1024 * 1024,
		Images: []*image.Summary{
			{Containers: 2, Size: 400 * 1024 * 1024, SharedSize: 100 * 1024 * 1024},
			{Containers: 0, Size: 300 * 1024 * 1024, SharedSize: 100 * 1024 * 1024}, // reclaimable 200MB
		},
		Containers: []*types.Container{
			{State: "running", SizeRw: 10 * 1024 * 1024},
			{State: "exited", SizeRw: 5 * 1024 * 1024}, // reclaimable
		},
		Volumes: []*volume.Volume{
			{UsageData: &volume.UsageData{RefCount: 1, Size: 50 * 1024 * 1024}},
			{UsageData: &volume.UsageData{RefCount: 0, Size: 20 * 1024 * 1024}}, // reclaimable
			{UsageData: nil}, // size unknown — must not panic
		},
		BuildCache: []*types.BuildCache{
			{InUse: false, Size: 30 * 1024 * 1024}, // reclaimable
			{InUse: true, Size: 10 * 1024 * 1024},
		},
	}

	got := formatDiskUsage(du)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("report has %d lines, want 5 (header + 4 types):\n%s", len(lines), got)
	}
	checks := []struct{ line, wants string }{
		{lines[0], "RECLAIMABLE"},
		{lines[1], "Images"},
		{lines[2], "Containers"},
		{lines[3], "Local Volumes"},
		{lines[4], "Build Cache"},
	}
	for _, c := range checks {
		if !strings.Contains(c.line, c.wants) {
			t.Errorf("line %q missing %q", c.line, c.wants)
		}
	}
	// Spot-check the computed figures.
	for _, want := range []struct{ line, sub, what string }{
		{lines[1], "600.0 MB", "images size (LayersSize)"},
		{lines[1], "200.0 MB", "images reclaimable (unused minus shared)"},
		{lines[2], "15.0 MB", "containers size (sum SizeRw)"},
		{lines[2], "5.0 MB", "containers reclaimable (stopped)"},
		{lines[3], "70.0 MB", "volumes size"},
		{lines[3], "20.0 MB", "volumes reclaimable (refcount 0)"},
		{lines[4], "40.0 MB", "build cache size"},
		{lines[4], "30.0 MB", "build cache reclaimable (not in use)"},
	} {
		if !strings.Contains(want.line, want.sub) {
			t.Errorf("%s: line %q missing %q", want.what, want.line, want.sub)
		}
	}
}

func TestFakeSystemDFAndPrune(t *testing.T) {
	f := NewFakeBackend()

	res, err := f.SystemDF()
	if err != nil {
		t.Fatalf("system df: %v", err)
	}
	if !strings.Contains(res.RawYAML, "Images") || !strings.Contains(res.RawYAML, "RECLAIMABLE") {
		t.Errorf("df report looks wrong:\n%s", res.RawYAML)
	}

	// Prune drops stopped demo containers and dangling images.
	summary, err := f.SystemPrune()
	if err != nil {
		t.Fatalf("system prune: %v", err)
	}
	if !strings.Contains(summary, "контейнеров 1") || !strings.Contains(summary, "образов 1") {
		t.Errorf("summary = %q, want 1 stopped container and 1 dangling image pruned", summary)
	}
	for _, c := range f.Containers {
		if c.State != "running" {
			t.Errorf("stopped container %s survived the prune", c.Name)
		}
	}
	for _, img := range f.Images {
		if strings.Contains(img.Tags, "<none>") {
			t.Errorf("dangling image %s survived the prune", img.ID)
		}
	}
}
