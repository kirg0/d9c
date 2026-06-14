package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestCPUPercent(t *testing.T) {
	mk := func(cur, pre, sysCur, sysPre uint64, cpus uint32) container.StatsResponse {
		var s container.StatsResponse
		s.CPUStats.CPUUsage.TotalUsage = cur
		s.PreCPUStats.CPUUsage.TotalUsage = pre
		s.CPUStats.SystemUsage = sysCur
		s.PreCPUStats.SystemUsage = sysPre
		s.CPUStats.OnlineCPUs = cpus
		return s
	}
	tests := []struct {
		name string
		in   container.StatsResponse
		want float64
	}{
		// cpuDelta=100, systemDelta=1000, 2 cpus -> 0.1*2*100 = 20%
		{"two cpus", mk(200, 100, 2000, 1000, 2), 20},
		// cpuDelta=50, systemDelta=1000, 1 cpu -> 0.05*1*100 = 5%
		{"one cpu", mk(150, 100, 2000, 1000, 1), 5},
		{"no cpu delta", mk(100, 100, 2000, 1000, 2), 0},
		{"no system delta", mk(200, 100, 1000, 1000, 2), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpuPercent(tt.in); got != tt.want {
				t.Errorf("cpuPercent = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCPUPercentBetween covers the one-shot path: CPU% derived from counters
// cached at the previous refresh tick instead of daemon-provided precpu.
func TestCPUPercentBetween(t *testing.T) {
	mk := func(cur, sysCur uint64, cpus uint32) container.StatsResponse {
		var s container.StatsResponse
		s.CPUStats.CPUUsage.TotalUsage = cur
		s.CPUStats.SystemUsage = sysCur
		s.CPUStats.OnlineCPUs = cpus
		return s
	}
	tests := []struct {
		name string
		prev cpuSample
		in   container.StatsResponse
		want float64
	}{
		// cpuDelta=100, systemDelta=1000, 2 cpus -> 20%
		{"two cpus", cpuSample{total: 100, system: 1000}, mk(200, 2000, 2), 20},
		{"one cpu", cpuSample{total: 100, system: 1000}, mk(150, 2000, 1), 5},
		{"no cpu delta", cpuSample{total: 100, system: 1000}, mk(100, 2000, 2), 0},
		{"no system delta", cpuSample{total: 100, system: 1000}, mk(200, 1000, 2), 0},
		// Container restarted between ticks: counters went backwards -> 0.
		{"counters went backwards", cpuSample{total: 500, system: 5000}, mk(100, 2000, 1), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpuPercentBetween(tt.prev, tt.in); got != tt.want {
				t.Errorf("cpuPercentBetween = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCPUPercent_OnlineCPUsFallback(t *testing.T) {
	// OnlineCPUs is 0, so it falls back to len(PercpuUsage)=2.
	var s container.StatsResponse
	s.CPUStats.CPUUsage.TotalUsage = 200
	s.PreCPUStats.CPUUsage.TotalUsage = 100
	s.CPUStats.SystemUsage = 2000
	s.PreCPUStats.SystemUsage = 1000
	s.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 1}
	if got := cpuPercent(s); got != 20 {
		t.Errorf("cpuPercent with percpu fallback = %v, want 20", got)
	}
}

func TestMemUsageLimit(t *testing.T) {
	tests := []struct {
		name             string
		usage, limit     uint64
		stats            map[string]uint64
		wantUse, wantLim uint64
	}{
		{"cgroup v2 inactive_file", 100, 1000, map[string]uint64{"inactive_file": 30}, 70, 1000},
		{"cgroup v1 cache", 100, 1000, map[string]uint64{"cache": 40}, 60, 1000},
		{"no stats map", 100, 1000, nil, 100, 1000},
		{"cache exceeds usage is ignored", 20, 1000, map[string]uint64{"inactive_file": 50}, 20, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s container.StatsResponse
			s.MemoryStats.Usage = tt.usage
			s.MemoryStats.Limit = tt.limit
			s.MemoryStats.Stats = tt.stats
			use, lim := memUsageLimit(s)
			if use != tt.wantUse || lim != tt.wantLim {
				t.Errorf("memUsageLimit = (%d,%d), want (%d,%d)", use, lim, tt.wantUse, tt.wantLim)
			}
		})
	}
}

func TestStatsFromResponse(t *testing.T) {
	var s container.StatsResponse
	s.CPUStats.CPUUsage.TotalUsage = 200
	s.PreCPUStats.CPUUsage.TotalUsage = 100
	s.CPUStats.SystemUsage = 2000
	s.PreCPUStats.SystemUsage = 1000
	s.CPUStats.OnlineCPUs = 1
	s.MemoryStats.Usage = 100
	s.MemoryStats.Limit = 200
	s.Networks = map[string]container.NetworkStats{
		"eth0": {RxBytes: 10, TxBytes: 5},
		"eth1": {RxBytes: 1, TxBytes: 2},
	}
	s.BlkioStats.IoServiceBytesRecursive = []container.BlkioStatEntry{
		{Op: "Read", Value: 100},
		{Op: "Write", Value: 40},
		{Op: "Read", Value: 50},
		{Op: "Async", Value: 999}, // ignored
	}
	got := statsFromResponse("abc", s)
	if got.ID != "abc" {
		t.Errorf("ID = %q, want abc", got.ID)
	}
	if got.CPUPerc != 10 {
		t.Errorf("CPUPerc = %v, want 10", got.CPUPerc)
	}
	if got.MemUsage != 100 || got.MemLimit != 200 || got.MemPerc != 50 {
		t.Errorf("mem = (%d,%d,%v), want (100,200,50)", got.MemUsage, got.MemLimit, got.MemPerc)
	}
	if got.NetRx != 11 || got.NetTx != 7 {
		t.Errorf("net = (%d,%d), want (11,7)", got.NetRx, got.NetTx)
	}
	if got.BlockRead != 150 || got.BlockWrite != 40 {
		t.Errorf("block = (%d,%d), want (150,40)", got.BlockRead, got.BlockWrite)
	}
}

func TestBlkioReadWrite(t *testing.T) {
	var s container.StatsResponse
	s.BlkioStats.IoServiceBytesRecursive = []container.BlkioStatEntry{
		{Op: "read", Value: 10},
		{Op: "WRITE", Value: 3},
		{Op: "Read", Value: 5},
		{Op: "sync", Value: 100},
	}
	r, w := blkioReadWrite(s)
	if r != 15 || w != 3 {
		t.Errorf("blkioReadWrite = (%d,%d), want (15,3)", r, w)
	}
}

func TestContainerStatsStrings(t *testing.T) {
	s := ContainerStats{
		CPUPerc: 12.34, MemUsage: 48 * 1024 * 1024, MemPerc: 9.42,
		NetRx: 1024 * 1024, NetTx: 512 * 1024,
		BlockRead: 8 * 1024 * 1024, BlockWrite: 2 * 1024 * 1024,
	}
	if got := s.CPUString(); got != "12.3%" {
		t.Errorf("CPUString = %q, want 12.3%%", got)
	}
	if got := s.MemString(); got != "48.0 MB" {
		t.Errorf("MemString = %q, want 48.0 MB", got)
	}
	if got := s.MemPercString(); got != "9.4%" {
		t.Errorf("MemPercString = %q, want 9.4%%", got)
	}
	if got := s.NetString(); got != "1.0 MB / 512.0 KB" {
		t.Errorf("NetString = %q, want 1.0 MB / 512.0 KB", got)
	}
	if got := s.BlockString(); got != "8.0 MB / 2.0 MB" {
		t.Errorf("BlockString = %q, want 8.0 MB / 2.0 MB", got)
	}
}
