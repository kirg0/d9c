package docker

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseJSONLines decodes NDJSON output (one JSON object per line, as nerdctl's
// `--format '{{json .}}'` emits) into a slice, skipping blank lines. A malformed
// line fails the whole parse — callers surface it as a list error.
func parseJSONLines[T any](out string) ([]T, error) {
	var rows []T
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r T
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("parse json line: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// ── images ──────────────────────────────────────────────────────────────────

type nerdctlImage struct {
	ID         string `json:"ID"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	Size       string `json:"Size"`
	CreatedAt  string `json:"CreatedAt"`
}

func (b *nerdctlBackend) ListImages() ([]Image, error) {
	out, err := b.run("images", "--format", jsonFormat)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	rows, err := parseJSONLines[nerdctlImage](out)
	if err != nil {
		return nil, err
	}
	result := make([]Image, 0, len(rows))
	for _, r := range rows {
		result = append(result, Image{
			ID:      shortID(strings.TrimPrefix(r.ID, "sha256:")),
			Tags:    imageTags(r.Repository, r.Tag),
			Size:    r.Size,
			Created: parseNerdctlTime(r.CreatedAt),
		})
	}
	return result, nil
}

// imageTags composes the "repo:tag" display, collapsing untagged images to
// "<none>".
func imageTags(repo, tag string) string {
	if repo == "" || repo == "<none>" || tag == "<none>" {
		return "<none>"
	}
	return repo + ":" + tag
}

func (b *nerdctlBackend) InspectImage(id string) (*InspectResult, error) {
	return b.inspectAsYAML("image", id, id)
}

func (b *nerdctlBackend) RemoveImage(id string, force bool) error {
	args := []string{"rmi"}
	if force {
		args = append(args, "-f")
	}
	_, err := b.run(append(args, id)...)
	return friendlyImageRemoveErr(err)
}

func (b *nerdctlBackend) PullImage(ref string) error {
	_, err := b.run("pull", ref)
	if err != nil {
		return fmt.Errorf("pull image: %w", err)
	}
	return nil
}

func (b *nerdctlBackend) PruneImages() (int, error) {
	out, err := b.run("image", "prune", "-f")
	if err != nil {
		return 0, fmt.Errorf("prune images: %w", err)
	}
	return countDeleted(out), nil
}

// countDeleted counts the deleted-object lines in a prune report (nerdctl prints
// one digest per removed object, then a "Total reclaimed space" summary).
func countDeleted(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "total") {
			continue
		}
		n++
	}
	return n
}

func (b *nerdctlBackend) TagImage(source, target string) error {
	if _, err := b.run("tag", source, target); err != nil {
		return fmt.Errorf("tag image: %w", err)
	}
	return nil
}

// PushImage streams `nerdctl push` progress. When credentials are supplied it
// runs `nerdctl login` first (nerdctl has no inline push auth); an anonymous
// push (empty username) skips login.
func (b *nerdctlBackend) PushImage(ref string, auth RegistryAuth) (<-chan string, func(), error) {
	if auth.Username != "" {
		reg := auth.Registry
		if reg == "" {
			reg = RegistryFromRef(ref)
		}
		login := []string{"login"}
		if reg != "" {
			login = append(login, reg)
		}
		login = append(login, "-u", auth.Username, "-p", auth.Password)
		if _, err := b.run(login...); err != nil {
			return nil, nil, fmt.Errorf("registry login: %w", err)
		}
	}
	return b.runner.stream(b.args("push", ref))
}

// BuildImage streams `nerdctl build -t <tag> <dir>` progress.
func (b *nerdctlBackend) BuildImage(contextDir, tag string) (<-chan string, func(), error) {
	args := []string{"build"}
	if tag != "" {
		args = append(args, "-t", tag)
	}
	args = append(args, contextDir)
	return b.runner.stream(b.args(args...))
}

// ImageHistory returns the image's layer history as a detail payload.
func (b *nerdctlBackend) ImageHistory(id string) (*InspectResult, error) {
	out, err := b.run("history", "--no-trunc", id)
	if err != nil {
		return nil, fmt.Errorf("image history: %w", err)
	}
	return &InspectResult{Name: id, RawYAML: out}, nil
}

// ── networks ────────────────────────────────────────────────────────────────

type nerdctlNetwork struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Scope  string `json:"Scope"`
	Labels string `json:"Labels"`
}

func (b *nerdctlBackend) ListNetworks() ([]Network, error) {
	out, err := b.run("network", "ls", "--format", jsonFormat)
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	rows, err := parseJSONLines[nerdctlNetwork](out)
	if err != nil {
		return nil, err
	}
	result := make([]Network, 0, len(rows))
	for _, r := range rows {
		result = append(result, Network{
			ID:     shortID(r.ID),
			Name:   r.Name,
			Driver: r.Driver,
			Scope:  r.Scope,
		})
	}
	return result, nil
}

func (b *nerdctlBackend) InspectNetwork(id string) (*InspectResult, error) {
	return b.inspectAsYAML("network", id, id)
}

func (b *nerdctlBackend) RemoveNetwork(id string) error {
	_, err := b.run("network", "rm", id)
	return err
}

func (b *nerdctlBackend) CreateNetwork(opts NetworkCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("network name is required")
	}
	args := []string{"network", "create"}
	if opts.Driver != "" {
		args = append(args, "-d", opts.Driver)
	}
	if opts.Subnet != "" {
		args = append(args, "--subnet", opts.Subnet)
	}
	if opts.Gateway != "" {
		args = append(args, "--gateway", opts.Gateway)
	}
	args = append(args, opts.Name)
	if _, err := b.run(args...); err != nil {
		return fmt.Errorf("create network: %w", err)
	}
	return nil
}

// ── volumes ─────────────────────────────────────────────────────────────────

type nerdctlVolume struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
}

func (b *nerdctlBackend) ListVolumes() ([]Volume, error) {
	out, err := b.run("volume", "ls", "--format", jsonFormat)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	rows, err := parseJSONLines[nerdctlVolume](out)
	if err != nil {
		return nil, err
	}
	result := make([]Volume, 0, len(rows))
	for _, r := range rows {
		result = append(result, Volume{
			Name:       r.Name,
			Driver:     r.Driver,
			Mountpoint: r.Mountpoint,
		})
	}
	return result, nil
}

func (b *nerdctlBackend) InspectVolume(name string) (*InspectResult, error) {
	return b.inspectAsYAML("volume", name, name)
}

func (b *nerdctlBackend) RemoveVolume(name string) error {
	_, err := b.run("volume", "rm", name)
	return err
}

func (b *nerdctlBackend) CreateVolume(opts VolumeCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("volume name is required")
	}
	args := []string{"volume", "create"}
	if opts.Driver != "" {
		args = append(args, "-d", opts.Driver)
	}
	args = append(args, opts.Name)
	if _, err := b.run(args...); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}
	return nil
}

func (b *nerdctlBackend) PruneVolumes() (int, error) {
	out, err := b.run("volume", "prune", "-f")
	if err != nil {
		return 0, fmt.Errorf("prune volumes: %w", err)
	}
	return countDeleted(out), nil
}
