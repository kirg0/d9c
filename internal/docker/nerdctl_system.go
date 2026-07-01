package docker

import (
	"fmt"
	"strings"
)

// SystemDF reports containerd disk usage (`nerdctl system df`) as a detail
// payload.
func (b *nerdctlBackend) SystemDF() (*InspectResult, error) {
	out, err := b.run("system", "df")
	if err != nil {
		return nil, fmt.Errorf("system df: %w", err)
	}
	return &InspectResult{Name: "system df", RawYAML: out}, nil
}

// SystemPrune removes stopped containers, unused networks, dangling images and
// build cache (`nerdctl system prune -f`) and returns its human-readable report.
func (b *nerdctlBackend) SystemPrune() (string, error) {
	out, err := b.run("system", "prune", "-f")
	if err != nil {
		return "", fmt.Errorf("system prune: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// Events streams containerd daemon events (`nerdctl events`).
func (b *nerdctlBackend) Events() (<-chan string, func(), error) {
	return b.runner.stream(b.args("events"))
}

// Info returns a one-shot summary for the multi-host dashboard. containerd's
// info is sparser than docker's, so counts are derived from ps/images and the
// server version is read from `nerdctl version`.
func (b *nerdctlBackend) Info() (HostSummary, error) {
	rows, err := b.psRows(true)
	if err != nil {
		return HostSummary{}, err
	}
	var running, paused, stopped int
	for _, r := range rows {
		switch stateFromStatus(r.Status) {
		case "running":
			running++
		case "paused":
			paused++
		default:
			stopped++
		}
	}
	images, _ := b.ListImages()
	version := ""
	if v, err := b.run("version", "--format", "{{.Server.Version}}"); err == nil {
		version = strings.TrimSpace(v)
	}
	return HostSummary{
		Containers: len(rows),
		Running:    running,
		Paused:     paused,
		Stopped:    stopped,
		Images:     len(images),
		Version:    version,
	}, nil
}
