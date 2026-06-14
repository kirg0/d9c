package docker

import (
	"context"
	"time"

	"d9c/internal/config"
)

// HostSummary is a one-shot snapshot of a Docker daemon used by the multi-host
// dashboard: aggregate object counts plus version/resource facts. Host and
// Reachable identify which saved host the snapshot belongs to and whether it
// answered; Err carries the reason when it did not.
type HostSummary struct {
	Host      string // saved-host URL the snapshot belongs to
	Reachable bool   // daemon answered Info within the budget
	Err       string // failure reason when unreachable

	Containers int    // total containers
	Running    int    // running containers
	Paused     int    // paused containers
	Stopped    int    // stopped containers
	Images     int    // images
	Version    string // daemon server version
	Name       string // daemon hostname
	NCPU       int    // logical CPUs
	MemTotal   int64  // total memory (bytes)
}

// Info returns a one-shot daemon summary (`docker info`) for the multi-host
// dashboard. Reachable/Host are left for the caller (ProbeHostSummary) to set.
func (b *dockerBackend) Info() (HostSummary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := b.cli.Info(ctx)
	if err != nil {
		return HostSummary{}, err
	}
	return HostSummary{
		Containers: info.Containers,
		Running:    info.ContainersRunning,
		Paused:     info.ContainersPaused,
		Stopped:    info.ContainersStopped,
		Images:     info.Images,
		Version:    info.ServerVersion,
		Name:       info.Name,
		NCPU:       info.NCPU,
		MemTotal:   info.MemTotal,
	}, nil
}

// ProbeHostSummary connects to host with the auth settings from cfg, fetches a
// daemon summary and tears the connection down — the per-host data behind the
// dashboard. The whole probe is bounded by timeout: a probe that can't finish
// in time reports the host unreachable (the dial keeps running in the
// background and its result is discarded when it eventually returns). It never
// returns an error — failures land in HostSummary.Err with Reachable=false.
func ProbeHostSummary(cfg *config.Config, host string, timeout time.Duration) HostSummary {
	done := make(chan HostSummary, 1)
	go func() {
		c := *cfg // copy auth fields, pin the probed host
		c.Host = host
		b, err := New(&c)
		if err != nil {
			done <- HostSummary{Host: host, Err: err.Error()}
			return
		}
		s, err := b.Info()
		b.Close()
		if err != nil {
			done <- HostSummary{Host: host, Err: err.Error()}
			return
		}
		s.Host = host
		s.Reachable = true
		done <- s
	}()
	select {
	case s := <-done:
		return s
	case <-time.After(timeout):
		return HostSummary{Host: host, Err: "no response within " + timeout.String()}
	}
}
