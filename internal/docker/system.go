package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
)

// SystemDF returns the daemon's disk usage as a readable report (the
// `docker system df` analogue) for the detail viewer.
func (b *dockerBackend) SystemDF() (*InspectResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	du, err := b.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return nil, fmt.Errorf("disk usage: %w", err)
	}
	return &InspectResult{Name: "system df", RawYAML: formatDiskUsage(du)}, nil
}

// formatDiskUsage renders a DiskUsage response as a `docker system df`-style
// table: TYPE / TOTAL / ACTIVE / SIZE / RECLAIMABLE. Reclaimable figures are
// the same approximations the CLI shows (inactive objects' sizes).
func formatDiskUsage(du types.DiskUsage) string {
	var imgActive int
	var imgReclaim int64
	for _, img := range du.Images {
		if img.Containers > 0 {
			imgActive++
		} else {
			size := img.Size - img.SharedSize
			if size > 0 {
				imgReclaim += size
			}
		}
	}

	var ctrActive int
	var ctrSize, ctrReclaim int64
	for _, c := range du.Containers {
		ctrSize += c.SizeRw
		if c.State == "running" {
			ctrActive++
		} else {
			ctrReclaim += c.SizeRw
		}
	}

	var volActive int
	var volSize, volReclaim int64
	for _, v := range du.Volumes {
		if v.UsageData == nil {
			continue
		}
		if v.UsageData.Size > 0 {
			volSize += v.UsageData.Size
		}
		if v.UsageData.RefCount > 0 {
			volActive++
		} else if v.UsageData.Size > 0 {
			volReclaim += v.UsageData.Size
		}
	}

	var bcActive int
	var bcSize, bcReclaim int64
	for _, bc := range du.BuildCache {
		bcSize += bc.Size
		if bc.InUse {
			bcActive++
		} else {
			bcReclaim += bc.Size
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-15s %7s %8s %12s %14s\n", "TYPE", "TOTAL", "ACTIVE", "SIZE", "RECLAIMABLE")
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Images", len(du.Images), imgActive, formatBytes(du.LayersSize), formatBytes(imgReclaim))
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Containers", len(du.Containers), ctrActive, formatBytes(ctrSize), formatBytes(ctrReclaim))
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Local Volumes", len(du.Volumes), volActive, formatBytes(volSize), formatBytes(volReclaim))
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Build Cache", len(du.BuildCache), bcActive, formatBytes(bcSize), formatBytes(bcReclaim))
	return sb.String()
}

// SystemPrune removes stopped containers, unused networks, dangling images and
// the build cache (the `docker system prune` scope — volumes are left alone;
// they have their own :prune). It runs every step even when one fails and
// returns the summary alongside the first error, so partial progress is still
// reported.
func (b *dockerBackend) SystemPrune() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	var parts []string
	var reclaimed uint64
	var firstErr error
	fail := func(stage string, err error) {
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", stage, err)
		}
	}

	if r, err := b.cli.ContainersPrune(ctx, filters.Args{}); err != nil {
		fail("containers", err)
	} else {
		parts = append(parts, fmt.Sprintf("контейнеров %d", len(r.ContainersDeleted)))
		reclaimed += r.SpaceReclaimed
	}
	if r, err := b.cli.NetworksPrune(ctx, filters.Args{}); err != nil {
		fail("networks", err)
	} else {
		parts = append(parts, fmt.Sprintf("сетей %d", len(r.NetworksDeleted)))
	}
	if r, err := b.cli.ImagesPrune(ctx, filters.Args{}); err != nil { // dangling only
		fail("images", err)
	} else {
		parts = append(parts, fmt.Sprintf("образов %d", len(r.ImagesDeleted)))
		reclaimed += r.SpaceReclaimed
	}
	if r, err := b.cli.BuildCachePrune(ctx, types.BuildCachePruneOptions{}); err != nil {
		fail("build cache", err)
	} else {
		parts = append(parts, fmt.Sprintf("кэш %d", len(r.CachesDeleted)))
		reclaimed += r.SpaceReclaimed
	}

	summary := "prune: " + strings.Join(parts, ", ") + " — освобождено " + formatBytes(int64(reclaimed))
	return summary, firstErr
}
