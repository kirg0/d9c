package docker

import (
	"encoding/json"
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
	return HostSummary{
		Containers: len(rows),
		Running:    running,
		Paused:     paused,
		Stopped:    stopped,
		Images:     len(images),
		Version:    b.serverVersion(),
	}, nil
}

// nerdctlVersion is the subset of `nerdctl version --format '{{json .}}'` we use:
// the server engine version lives in Server.Components (there is no flat
// Server.Version field), keyed by component name ("containerd").
type nerdctlVersion struct {
	Server struct {
		Components []struct {
			Name    string `json:"Name"`
			Version string `json:"Version"`
		} `json:"Components"`
	} `json:"Server"`
}

// serverVersion reports the containerd server version behind nerdctl (best
// effort; empty string when it can't be read).
func (b *nerdctlBackend) serverVersion() string {
	out, err := b.run("version", "--format", jsonFormat)
	if err != nil {
		return ""
	}
	var v nerdctlVersion
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return ""
	}
	for _, c := range v.Server.Components {
		if strings.EqualFold(c.Name, "containerd") {
			return c.Version
		}
	}
	if len(v.Server.Components) > 0 {
		return v.Server.Components[0].Version
	}
	return ""
}
