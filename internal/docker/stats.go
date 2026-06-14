package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
)

// ContainerStats is a point-in-time resource sample for one container, derived
// from the Docker Stats API and reduced to the figures the UI displays.
type ContainerStats struct {
	ID         string
	CPUPerc    float64 // CPU usage as a percentage of total host capacity (matches `docker stats`)
	MemUsage   uint64  // memory usage in bytes, page cache excluded
	MemLimit   uint64  // memory limit in bytes
	MemPerc    float64 // MemUsage / MemLimit * 100
	NetRx      uint64  // total bytes received across all networks
	NetTx      uint64  // total bytes sent across all networks
	BlockRead  uint64  // total bytes read from block devices
	BlockWrite uint64  // total bytes written to block devices
}

// maxStatsConcurrency bounds the number of parallel /stats requests so hosts
// with many containers aren't hit with a burst of simultaneous calls.
const maxStatsConcurrency = 8

// cpuSample is one point of the container's cumulative CPU counters, kept
// between batches to compute CPU% across refresh ticks.
type cpuSample struct {
	total  uint64 // CPUStats.CPUUsage.TotalUsage
	system uint64 // CPUStats.SystemUsage
}

// ContainerStats fetches a one-shot resource sample for each given container ID
// concurrently. Stats are best-effort: a container whose sample can't be read
// (stopped, vanished mid-call) is simply omitted from the result rather than
// failing the whole batch.
//
// The one-shot endpoint returns instantly but carries no precpu data, so CPU%
// is derived from the previous batch's sample cached on the backend — exactly
// how `docker stats` streams compute it, just with our refresh interval as the
// window. The first batch after (re)connect therefore reports CPU as 0.
func (b *dockerBackend) ContainerStats(ids []string) (map[string]ContainerStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out := make(map[string]ContainerStats, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxStatsConcurrency)

	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			st, err := b.containerStat(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			out[id] = st
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	// Drop cached samples of containers that are gone (stopped/removed) so the
	// cache doesn't grow without bound on a long-running session.
	requested := make(map[string]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	b.statsMu.Lock()
	for id := range b.statsPrev {
		if !requested[id] {
			delete(b.statsPrev, id)
		}
	}
	b.statsMu.Unlock()

	return out, nil
}

// containerStat reads and decodes a single one-shot stats sample, deriving
// CPU% from the previously cached counters when the daemon sent no precpu.
func (b *dockerBackend) containerStat(ctx context.Context, id string) (ContainerStats, error) {
	resp, err := b.cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return ContainerStats{}, fmt.Errorf("container stats: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var s container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return ContainerStats{}, fmt.Errorf("decode stats: %w", err)
	}
	st := statsFromResponse(id, s)

	if s.PreCPUStats.CPUUsage.TotalUsage == 0 { // one-shot: no daemon precpu
		cur := cpuSample{total: s.CPUStats.CPUUsage.TotalUsage, system: s.CPUStats.SystemUsage}
		b.statsMu.Lock()
		if b.statsPrev == nil {
			b.statsPrev = map[string]cpuSample{}
		}
		prev, ok := b.statsPrev[id]
		b.statsPrev[id] = cur
		b.statsMu.Unlock()
		if ok {
			st.CPUPerc = cpuPercentBetween(prev, s)
		}
	}
	return st, nil
}

// statsFromResponse reduces a raw Docker stats sample to our compact form.
func statsFromResponse(id string, s container.StatsResponse) ContainerStats {
	usage, limit := memUsageLimit(s)
	memPerc := 0.0
	if limit > 0 {
		memPerc = float64(usage) / float64(limit) * 100.0
	}
	var rx, tx uint64
	for _, n := range s.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	read, write := blkioReadWrite(s)
	return ContainerStats{
		ID:         id,
		CPUPerc:    cpuPercent(s),
		MemUsage:   usage,
		MemLimit:   limit,
		MemPerc:    memPerc,
		NetRx:      rx,
		NetTx:      tx,
		BlockRead:  read,
		BlockWrite: write,
	}
}

// blkioReadWrite sums the per-device block I/O byte counters into total read and
// write figures (matching the BLOCK I/O column of `docker stats`). The daemon
// normalises cgroup v1/v2 into IoServiceBytesRecursive with "read"/"write" ops.
func blkioReadWrite(s container.StatsResponse) (read, write uint64) {
	for _, e := range s.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(e.Op) {
		case "read":
			read += e.Value
		case "write":
			write += e.Value
		}
	}
	return read, write
}

// cpuPercent computes CPU utilisation from the daemon-provided precpu sample
// (present in streaming/primed responses). It returns 0 when either delta is
// non-positive, e.g. for a sample where precpu stats are absent.
func cpuPercent(s container.StatsResponse) float64 {
	prev := cpuSample{total: s.PreCPUStats.CPUUsage.TotalUsage, system: s.PreCPUStats.SystemUsage}
	return cpuPercentBetween(prev, s)
}

// cpuPercentBetween computes CPU utilisation as a percentage of total host
// capacity (the same figure `docker stats` reports) from the delta between the
// current response and an arbitrary previous sample — for one-shot responses
// that's the counters cached from the previous refresh tick.
func cpuPercentBetween(prev cpuSample, s container.StatsResponse) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(prev.total)
	systemDelta := float64(s.CPUStats.SystemUsage) - float64(prev.system)
	if cpuDelta <= 0 || systemDelta <= 0 {
		return 0
	}
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpus == 0 {
		cpus = 1
	}
	return (cpuDelta / systemDelta) * cpus * 100.0
}

// memUsageLimit returns the cache-excluded memory usage and the limit, matching
// what `docker stats` reports. cgroup v1 records reclaimable page cache under
// "cache"; cgroup v2 under "inactive_file".
func memUsageLimit(s container.StatsResponse) (usage, limit uint64) {
	usage = s.MemoryStats.Usage
	if v, ok := s.MemoryStats.Stats["inactive_file"]; ok {
		if usage > v {
			usage -= v
		}
	} else if v, ok := s.MemoryStats.Stats["cache"]; ok {
		if usage > v {
			usage -= v
		}
	}
	return usage, s.MemoryStats.Limit
}

// CPUString formats CPU usage like "12.3%".
func (s ContainerStats) CPUString() string {
	return fmt.Sprintf("%.1f%%", s.CPUPerc)
}

// MemString formats memory usage like "45.2 MB".
func (s ContainerStats) MemString() string {
	return formatBytes(int64(s.MemUsage))
}

// MemPercString formats memory utilisation like "9.4%".
func (s ContainerStats) MemPercString() string {
	return fmt.Sprintf("%.1f%%", s.MemPerc)
}

// NetString formats network I/O as "rx / tx", e.g. "1.0 MB / 512.0 KB".
func (s ContainerStats) NetString() string {
	return formatBytes(int64(s.NetRx)) + " / " + formatBytes(int64(s.NetTx))
}

// BlockString formats block I/O as "read / write", e.g. "8.0 MB / 2.0 MB".
func (s ContainerStats) BlockString() string {
	return formatBytes(int64(s.BlockRead)) + " / " + formatBytes(int64(s.BlockWrite))
}
