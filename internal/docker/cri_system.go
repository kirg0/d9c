package docker

import (
	"fmt"
	"strings"

	"d9c/internal/i18n"
)

// SystemDF reports CRI disk usage as a detail payload, assembled from the
// object lists (CRI has no df endpoint; image sizes are known, container and
// volume sizes are not).
func (b *criBackend) SystemDF() (*InspectResult, error) {
	images, err := b.ListImages()
	if err != nil {
		return nil, fmt.Errorf("system df: %w", err)
	}
	rows, err := b.psRows(true)
	if err != nil {
		return nil, fmt.Errorf("system df: %w", err)
	}
	running := 0
	for _, r := range rows {
		if criState(r.State) == "running" {
			running++
		}
	}
	return &InspectResult{
		Name:    "system df",
		RawYAML: buildDFReport(images, len(rows), running, 0),
	}, nil
}

// SystemPrune removes all images unused by containers (`crictl rmi --prune`) —
// the only prune CRI models; containers/networks/build cache belong to the
// orchestrator.
func (b *criBackend) SystemPrune() (string, error) {
	out, err := b.run("rmi", "--prune")
	if err != nil {
		return "", fmt.Errorf("system prune: %w", err)
	}
	report := strings.TrimSpace(out)
	if report == "" {
		report = i18n.T("нечего удалять", "nothing to remove")
	}
	return i18n.T("удалены неиспользуемые образы:\n", "removed unused images:\n") + report, nil
}

// Events streams CRI container events (`crictl events`, cri-tools ≥ 1.26; an
// older crictl surfaces its "unknown command" error in the viewer).
func (b *criBackend) Events() (<-chan string, func(), error) {
	return b.runner.stream(b.args("events"))
}

// Info returns a one-shot summary for the multi-host dashboard: counts from
// the listings and the runtime version from `crictl version`. CRI exposes no
// host name/CPU/memory, so those stay zero.
func (b *criBackend) Info() (HostSummary, error) {
	rows, err := b.psRows(true)
	if err != nil {
		return HostSummary{}, err
	}
	var running, stopped int
	for _, r := range rows {
		if criState(r.State) == "running" {
			running++
		} else {
			stopped++
		}
	}
	images, _ := b.ListImages()
	version := ""
	name := ""
	if v, err := b.versionInfo(); err == nil {
		version = v.RuntimeVersion
		name = v.RuntimeName
	}
	return HostSummary{
		Containers: len(rows),
		Running:    running,
		Stopped:    stopped,
		Images:     len(images),
		Version:    version,
		Name:       name,
	}, nil
}

// ── compose (not applicable to CRI) ─────────────────────────────────────────

// ListComposeProjects returns an empty list: compose is a Docker-family
// concept; CRI workloads are orchestrated as pods.
func (b *criBackend) ListComposeProjects() ([]ComposeProject, error) {
	return []ComposeProject{}, nil
}

func errCRICompose() error {
	return errCRIUnsupported("compose")
}

func (b *criBackend) ListComposeContainers(string) ([]Container, error) {
	return nil, errCRICompose()
}
func (b *criBackend) InspectComposeProject(string) (*InspectResult, error) {
	return nil, errCRICompose()
}
func (b *criBackend) ComposeLogs(string, LogOptions) (<-chan string, func(), error) {
	return nil, nil, errCRICompose()
}
func (b *criBackend) ComposeStart(string) error   { return errCRICompose() }
func (b *criBackend) ComposeStop(string) error    { return errCRICompose() }
func (b *criBackend) ComposeRestart(string) error { return errCRICompose() }
func (b *criBackend) ComposePause(string) error   { return errCRICompose() }
func (b *criBackend) ComposeUnpause(string) error { return errCRICompose() }
func (b *criBackend) ComposeRemove(string) error  { return errCRICompose() }
func (b *criBackend) ComposeUp(string) (<-chan string, func(), error) {
	return nil, nil, errCRICompose()
}
func (b *criBackend) ComposePull(string) (<-chan string, func(), error) {
	return nil, nil, errCRICompose()
}
func (b *criBackend) ComposeDown(string) (<-chan string, func(), error) {
	return nil, nil, errCRICompose()
}
func (b *criBackend) ComposeConfig(string) (string, error) { return "", errCRICompose() }
func (b *criBackend) ReadComposeFile(string) (string, string, error) {
	return "", "", errCRICompose()
}
func (b *criBackend) WriteComposeFile(string, string) error { return errCRICompose() }
func (b *criBackend) CreateComposeFile(string, string) (<-chan string, func(), error) {
	return nil, nil, errCRICompose()
}
func (b *criBackend) BackupComposeProject(string) (string, error) { return "", errCRICompose() }
func (b *criBackend) RestoreComposeProject(string, string) (<-chan string, func(), error) {
	return nil, nil, errCRICompose()
}
func (b *criBackend) SupportsHostCompose() bool { return false }
