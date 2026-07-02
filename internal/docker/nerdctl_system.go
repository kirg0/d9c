package docker

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/docker/go-units"
)

// SystemDF reports containerd disk usage as a detail payload. nerdctl (2.x)
// has no `system df` subcommand, so the report is assembled from the object
// lists instead: image count with their summed unpacked sizes, container and
// volume counts. Volume sizes are not reported (`volume ls --size` walks every
// volume and can be very slow on real hosts).
func (b *nerdctlBackend) SystemDF() (*InspectResult, error) {
	images, err := b.ListImages()
	if err != nil {
		return nil, fmt.Errorf("system df: %w", err)
	}
	rows, err := b.psRows(true)
	if err != nil {
		return nil, fmt.Errorf("system df: %w", err)
	}
	volumes, err := b.ListVolumes()
	if err != nil {
		return nil, fmt.Errorf("system df: %w", err)
	}
	running := 0
	for _, r := range rows {
		if stateFromStatus(r.Status) == "running" {
			running++
		}
	}
	return &InspectResult{
		Name:    "system df",
		RawYAML: buildDFReport(images, len(rows), running, len(volumes)),
	}, nil
}

// buildDFReport renders the emulated `system df` table from the gathered
// counts; split out of SystemDF so the formatting is unit-testable.
func buildDFReport(images []Image, ctrTotal, ctrRunning, volumes int) string {
	var imgBytes uint64
	for _, im := range images {
		imgBytes += parseSize(im.Size)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-15s %-8s %-8s %s\n", "TYPE", "TOTAL", "ACTIVE", "SIZE")
	fmt.Fprintf(&sb, "%-15s %-8d %-8s %s\n", "Images", len(images), "-", units.HumanSize(float64(imgBytes)))
	fmt.Fprintf(&sb, "%-15s %-8d %-8d %s\n", "Containers", ctrTotal, ctrRunning, "-")
	fmt.Fprintf(&sb, "%-15s %-8d %-8s %s\n", "Local Volumes", volumes, "-", "-")
	return sb.String()
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
	info := b.infoSummary()
	version := info.ServerVersion
	if version == "" {
		version = b.serverVersion()
	}
	return HostSummary{
		Containers: len(rows),
		Running:    running,
		Paused:     paused,
		Stopped:    stopped,
		Images:     len(images),
		Version:    version,
		Name:       info.Name,
		NCPU:       info.NCPU,
		MemTotal:   info.MemTotal,
	}, nil
}

// nerdctlInfo is the subset of `nerdctl info --format '{{json .}}'` the
// dashboard shows: the host name, CPU/memory capacity and the server version.
type nerdctlInfo struct {
	Name          string `json:"Name"`
	NCPU          int    `json:"NCPU"`
	MemTotal      int64  `json:"MemTotal"`
	ServerVersion string `json:"ServerVersion"`
}

// infoSummary fetches `nerdctl info` (best effort; zero value when it can't be
// read). The JSON object is picked out line-wise because rootless nerdctl may
// interleave warning lines with the payload on the combined output.
func (b *nerdctlBackend) infoSummary() nerdctlInfo {
	out, err := b.run("info", "--format", jsonFormat)
	if err != nil {
		return nerdctlInfo{}
	}
	return parseInfoJSON(out)
}

// parseInfoJSON extracts the first JSON object line of `nerdctl info` output.
func parseInfoJSON(out string) nerdctlInfo {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var info nerdctlInfo
		if err := json.Unmarshal([]byte(line), &info); err == nil {
			return info
		}
	}
	return nerdctlInfo{}
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
