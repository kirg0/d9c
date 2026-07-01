package docker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/docker/go-units"
	"gopkg.in/yaml.v3"
)

// nerdctlPS is the subset of `nerdctl ps --format '{{json .}}'` fields we use.
// nerdctl emits one JSON object per line (NDJSON), mirroring docker's ps format.
type nerdctlPS struct {
	ID        string `json:"ID"`
	Names     string `json:"Names"`
	Image     string `json:"Image"`
	Status    string `json:"Status"`
	Ports     string `json:"Ports"`
	CreatedAt string `json:"CreatedAt"`
	Labels    string `json:"Labels"`
}

// ListContainers lists containers in the active namespace. showAll includes
// stopped containers (nerdctl ps -a), matching the docker backend.
func (b *nerdctlBackend) ListContainers(showAll bool) ([]Container, error) {
	rows, err := b.psRows(showAll)
	if err != nil {
		return nil, err
	}
	result := make([]Container, 0, len(rows))
	for _, r := range rows {
		result = append(result, r.toContainer())
	}
	return result, nil
}

// psRows runs `nerdctl ps [-a]` and decodes the NDJSON rows; shared by
// ListContainers and the compose discovery.
func (b *nerdctlBackend) psRows(showAll bool) ([]nerdctlPS, error) {
	args := []string{"ps", "--format", jsonFormat}
	if showAll {
		args = append(args, "-a")
	}
	out, err := b.run(args...)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	return parsePS(out)
}

// jsonFormat is the Go-template that makes nerdctl emit one JSON object per line.
const jsonFormat = "{{json .}}"

// parsePS decodes NDJSON ps output into rows, tolerating blank lines.
func parsePS(out string) ([]nerdctlPS, error) {
	var rows []nerdctlPS
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r nerdctlPS
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("parse ps line: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func (r nerdctlPS) toContainer() Container {
	name := r.Names
	if i := strings.IndexByte(name, ','); i >= 0 {
		name = name[:i]
	}
	return Container{
		ID:      shortID(r.ID),
		Name:    name,
		Image:   r.Image,
		Status:  r.Status,
		State:   stateFromStatus(r.Status),
		Health:  parseHealth(r.Status),
		Ports:   r.Ports,
		Created: parseNerdctlTime(r.CreatedAt),
		Labels:  parseLabels(r.Labels),
	}
}

// stateFromStatus derives the coarse container state the UI colors by from a
// human status line ("Up 3 minutes", "Exited (0) 1 hour ago", "Created").
// nerdctl's ps JSON has no dedicated State field, unlike docker's.
func stateFromStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.HasPrefix(s, "up") && strings.Contains(s, "paused"):
		return "paused"
	case strings.HasPrefix(s, "up"):
		return "running"
	case strings.HasPrefix(s, "exited"), strings.HasPrefix(s, "stopped"):
		return "exited"
	case strings.HasPrefix(s, "created"):
		return "created"
	case strings.HasPrefix(s, "restarting"):
		return "restarting"
	case strings.HasPrefix(s, "dead"):
		return "dead"
	case strings.HasPrefix(s, "paused"):
		return "paused"
	default:
		return s
	}
}

// parseLabels turns nerdctl's comma-separated "k=v,k2=v2" label string into a
// map (the same shape the compose discovery and filter predicates expect).
func parseLabels(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(pair, "="); ok {
			m[strings.TrimSpace(k)] = v
		}
	}
	return m
}

// nerdctlTimeLayouts are the timestamp formats nerdctl has used for CreatedAt
// across versions; the first that parses wins, else the zero time.
var nerdctlTimeLayouts = []string{
	"2006-01-02 15:04:05 -0700 MST",
	"2006-01-02 15:04:05 -0700 -0700",
	time.RFC3339Nano,
	time.RFC3339,
}

func parseNerdctlTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range nerdctlTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// InspectContainer returns the container's `nerdctl inspect` output rendered as
// YAML for the detail view.
func (b *nerdctlBackend) InspectContainer(id string) (*InspectResult, error) {
	return b.inspectAsYAML("container", id, id)
}

// inspectAsYAML runs `nerdctl <kind> inspect <ref>` (kind: container/image/
// network/volume) and converts the returned JSON to YAML. name is the detail
// header. nerdctl inspect returns a JSON array; the first element is used.
func (b *nerdctlBackend) inspectAsYAML(kind, ref, name string) (*InspectResult, error) {
	sub := []string{"inspect", ref}
	if kind != "container" {
		sub = append([]string{kind, "inspect"}, ref)
	}
	out, err := b.run(sub...)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", kind, err)
	}
	y, err := jsonToYAML(out)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", kind, err)
	}
	return &InspectResult{Name: name, RawYAML: y}, nil
}

// jsonToYAML converts a JSON document (object or array) to indented YAML. A
// single-element array (nerdctl inspect's usual shape) is unwrapped.
func jsonToYAML(jsonStr string) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return "", fmt.Errorf("decode json: %w", err)
	}
	if arr, ok := v.([]any); ok && len(arr) == 1 {
		v = arr[0]
	}
	y, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode yaml: %w", err)
	}
	return string(y), nil
}

func (b *nerdctlBackend) StartContainer(id string) error {
	_, err := b.run("start", id)
	return err
}

func (b *nerdctlBackend) StopContainer(id string) error {
	_, err := b.run("stop", id)
	return err
}

func (b *nerdctlBackend) RestartContainer(id string) error {
	_, err := b.run("restart", id)
	return err
}

func (b *nerdctlBackend) RemoveContainer(id string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	_, err := b.run(append(args, id)...)
	return err
}

func (b *nerdctlBackend) KillContainer(id, signal string) error {
	args := []string{"kill"}
	if signal != "" {
		args = append(args, "-s", signal)
	}
	_, err := b.run(append(args, id)...)
	return err
}

// ContainerLogs streams a container's logs. nerdctl logs writes plain text (no
// docker multiplex header), so lines pass straight through the runner stream.
func (b *nerdctlBackend) ContainerLogs(id string, opts LogOptions) (<-chan string, func(), error) {
	return b.runner.stream(b.args(buildLogsArgs(id, opts)...))
}

// buildLogsArgs assembles `logs -f --timestamps [--tail N] [--since] [--until] id`.
func buildLogsArgs(id string, opts LogOptions) []string {
	args := []string{"logs", "-f", "--timestamps"}
	if opts.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(opts.Tail))
	}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	if opts.Until != "" {
		args = append(args, "--until", opts.Until)
	}
	return append(args, id)
}

// ── stats ───────────────────────────────────────────────────────────────────

// nerdctlStat is the subset of `nerdctl stats --no-stream --format '{{json .}}'`
// fields we parse. Values are human strings ("5.00%", "10MiB / 2GiB").
type nerdctlStat struct {
	ID       string `json:"ID"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
}

// ContainerStats fetches a one-shot resource sample for the given containers via
// `nerdctl stats --no-stream`. Unlike the docker Stats API, nerdctl already
// reports CPU% directly, so no cross-tick delta bookkeeping is needed. ids are
// ignored beyond scoping the daemon call — nerdctl reports all running
// containers and we key the result by ID.
func (b *nerdctlBackend) ContainerStats(ids []string) (map[string]ContainerStats, error) {
	if len(ids) == 0 {
		return map[string]ContainerStats{}, nil
	}
	out, err := b.run("stats", "--no-stream", "--format", jsonFormat)
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	result := map[string]ContainerStats{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var s nerdctlStat
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue
		}
		cs := s.toStats()
		result[cs.ID] = cs
	}
	return result, nil
}

func (s nerdctlStat) toStats() ContainerStats {
	usage, limit := splitSlash(s.MemUsage)
	rx, tx := splitSlash(s.NetIO)
	rd, wr := splitSlash(s.BlockIO)
	return ContainerStats{
		ID:         shortID(s.ID),
		CPUPerc:    parsePercent(s.CPUPerc),
		MemUsage:   parseSize(usage),
		MemLimit:   parseSize(limit),
		MemPerc:    parsePercent(s.MemPerc),
		NetRx:      parseSize(rx),
		NetTx:      parseSize(tx),
		BlockRead:  parseSize(rd),
		BlockWrite: parseSize(wr),
	}
}

// splitSlash splits a "left / right" figure (as `nerdctl stats` prints memory,
// net and block I/O) into its two sides, trimmed.
func splitSlash(s string) (left, right string) {
	if l, r, ok := strings.Cut(s, "/"); ok {
		return strings.TrimSpace(l), strings.TrimSpace(r)
	}
	return strings.TrimSpace(s), ""
}

// parsePercent parses "5.00%" into 5.0; a malformed value yields 0.
func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseSize parses a human size ("10MiB", "1.2kB") into bytes; 0 on failure.
func parseSize(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "--" {
		return 0
	}
	n, err := units.RAMInBytes(s)
	if err != nil || n < 0 {
		return 0
	}
	return uint64(n)
}

// ── exec / run ──────────────────────────────────────────────────────────────

// ExecInteractive opens an interactive `nerdctl exec -it` session. An empty cmd
// defaults to a shell. Only available over the ssh transport (see localRunner).
func (b *nerdctlBackend) ExecInteractive(containerID string, cmd []string) (ExecSession, error) {
	args := append([]string{"exec", "-it", containerID}, execArgv(cmd)...)
	return b.runner.interactive(b.args(args...))
}

// RunContainer creates and starts a detached container (`nerdctl run -d`).
func (b *nerdctlBackend) RunContainer(opts RunOptions) error {
	if strings.TrimSpace(opts.Image) == "" {
		return fmt.Errorf("image is required")
	}
	args := []string{"run", "-d"}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	for _, p := range opts.Ports {
		args = append(args, "-p", p)
	}
	for _, e := range opts.Env {
		args = append(args, "-e", e)
	}
	for _, v := range opts.Volumes {
		args = append(args, "-v", v)
	}
	args = append(args, opts.Image)
	if _, err := b.run(args...); err != nil {
		return friendlyRunErr(err)
	}
	return nil
}

// RunInteractive starts a disposable interactive container (`nerdctl run --rm
// -it`). Only available over the ssh transport (see localRunner).
func (b *nerdctlBackend) RunInteractive(opts ExecRunOptions) (ExecSession, error) {
	if strings.TrimSpace(opts.Image) == "" {
		return nil, fmt.Errorf("image is required")
	}
	args := []string{"run", "--rm", "-it"}
	for _, v := range opts.Volumes {
		args = append(args, "-v", v)
	}
	args = append(args, opts.Image)
	args = append(args, opts.Cmd...)
	return b.runner.interactive(b.args(args...))
}

// ── filesystem browse / cp ──────────────────────────────────────────────────

// ListPath lists a directory inside the container by running `ls -1Ap` there
// (containerd exposes no readdir API), reusing the docker backend's parser.
func (b *nerdctlBackend) ListPath(containerID, dir string) ([]FileEntry, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "/"
	}
	out, err := b.run("exec", containerID, "ls", "-1Ap", "--", dir)
	if err != nil {
		return nil, friendlyListErr(dir, err.Error())
	}
	return parseLsEntries(out), nil
}

// CopyFromContainer downloads srcPath into the local directory destDir. Over the
// ssh transport `nerdctl cp` would land the files on the remote host, not the
// machine running d9c, so it is unsupported there.
func (b *nerdctlBackend) CopyFromContainer(containerID, srcPath, destDir string) error {
	if !b.local {
		return errCopyNeedsLocal
	}
	if destDir == "" {
		destDir = "."
	}
	_, err := b.run("cp", containerID+":"+srcPath, destDir)
	if err != nil {
		return friendlyCopyErr(err)
	}
	return nil
}

// CopyToContainer uploads a local path into destDir inside the container. Same
// ssh-transport limitation as CopyFromContainer.
func (b *nerdctlBackend) CopyToContainer(containerID, localPath, destDir string) error {
	if !b.local {
		return errCopyNeedsLocal
	}
	if strings.TrimSpace(destDir) == "" {
		destDir = "/"
	}
	_, err := b.run("cp", localPath, containerID+":"+destDir)
	if err != nil {
		return friendlyCopyErr(err)
	}
	return nil
}

// errCopyNeedsLocal explains why `cp` is unavailable over SSH.
var errCopyNeedsLocal = fmt.Errorf("cp is only available when nerdctl runs locally (over ssh it would copy on the remote host)")
