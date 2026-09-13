package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kirg0/d9c/internal/i18n"

	"github.com/docker/go-units"
)

// crictl object listings (-o json) are protojson documents: int64 fields
// (createdAt, timestamps) serialize as decimal JSON *strings* and uint64
// counters are wrapped as {"value":"123"}. criNumber accepts both the string
// and the bare-number encoding, whichever the crictl version emits.

// criNumber is a protojson int64/uint64 that may arrive quoted or bare.
type criNumber string

// UnmarshalJSON accepts both `"123"` and `123`.
func (n *criNumber) UnmarshalJSON(data []byte) error {
	*n = criNumber(strings.Trim(string(data), `"`))
	return nil
}

func (n criNumber) int64() int64 {
	v, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func (n criNumber) uint64() uint64 {
	v, err := strconv.ParseUint(string(n), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// criUint64 is protojson's UInt64Value wrapper ({"value":"123"}).
type criUint64 struct {
	Value criNumber `json:"value"`
}

// criContainer is the subset of `crictl ps -o json` we use.
type criContainer struct {
	ID           string `json:"id"`
	PodSandboxID string `json:"podSandboxId"`
	Metadata     struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Image struct {
		Image string `json:"image"`
	} `json:"image"`
	ImageRef  string            `json:"imageRef"`
	State     string            `json:"state"`
	CreatedAt criNumber         `json:"createdAt"`
	Labels    map[string]string `json:"labels"`
}

// criPod is the subset of `crictl pods -o json` we use.
type criPod struct {
	ID       string `json:"id"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

// parseCRIContainers decodes the `crictl ps -o json` envelope.
func parseCRIContainers(out string) ([]criContainer, error) {
	var doc struct {
		Containers []criContainer `json:"containers"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("parse crictl ps: %w", err)
	}
	return doc.Containers, nil
}

// parseCRIPods decodes the `crictl pods -o json` envelope into an id→pod map.
func parseCRIPods(out string) (map[string]criPod, error) {
	var doc struct {
		Items []criPod `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("parse crictl pods: %w", err)
	}
	pods := make(map[string]criPod, len(doc.Items))
	for _, p := range doc.Items {
		pods[p.ID] = p
	}
	return pods, nil
}

// psRows lists CRI containers; shared by ListContainers, SystemDF and Info.
func (b *criBackend) psRows(showAll bool) ([]criContainer, error) {
	args := []string{"ps", "-o", "json"}
	if showAll {
		args = append(args, "-a")
	}
	out, err := b.run(args...)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	return parseCRIContainers(out)
}

// pods lists the pod sandboxes keyed by id (best effort: an error yields an
// empty map and container names simply lose their pod prefix).
func (b *criBackend) pods() map[string]criPod {
	out, err := b.run("pods", "-o", "json")
	if err != nil {
		return nil
	}
	pods, err := parseCRIPods(out)
	if err != nil {
		return nil
	}
	return pods
}

// ListContainers lists CRI containers. Names are rendered as "pod/container"
// (the pod is CRI's grouping unit; there is no dedicated pods section).
func (b *criBackend) ListContainers(showAll bool) ([]Container, error) {
	rows, err := b.psRows(showAll)
	if err != nil {
		return nil, err
	}
	pods := b.pods()
	result := make([]Container, 0, len(rows))
	for _, r := range rows {
		result = append(result, r.toContainer(pods))
	}
	return result, nil
}

// kubernetesPodLabel is the pod-name label kubelet stamps on every container it
// creates; used as a fallback when the sandbox listing is unavailable.
const kubernetesPodLabel = "io.kubernetes.pod.name"

func (c criContainer) toContainer(pods map[string]criPod) Container {
	name := c.Metadata.Name
	pod := ""
	if p, ok := pods[c.PodSandboxID]; ok {
		pod = p.Metadata.Name
	} else if n := c.Labels[kubernetesPodLabel]; n != "" {
		pod = n
	}
	if pod != "" {
		name = pod + "/" + name
	}
	created := time.Unix(0, c.CreatedAt.int64())
	return Container{
		ID:      shortID(c.ID),
		Name:    name,
		Image:   criImageDisplay(c.Image.Image, c.ImageRef),
		Status:  criStatusLine(c.State, created),
		State:   criState(c.State),
		Created: created,
		Labels:  c.Labels,
	}
}

// criImageDisplay picks the human image reference: the user-specified image
// unless it is a bare digest, then the runtime's imageRef, shortened.
func criImageDisplay(image, imageRef string) string {
	if image != "" && !strings.HasPrefix(image, "sha256:") {
		return image
	}
	if imageRef != "" && !strings.HasPrefix(imageRef, "sha256:") {
		return imageRef
	}
	if image == "" {
		image = imageRef
	}
	return shortID(strings.TrimPrefix(image, "sha256:"))
}

// criState maps a CRI ContainerState constant to the coarse state the UI
// colors by.
func criState(state string) string {
	switch state {
	case "CONTAINER_RUNNING":
		return "running"
	case "CONTAINER_EXITED":
		return "exited"
	case "CONTAINER_CREATED":
		return "created"
	default:
		return strings.ToLower(strings.TrimPrefix(state, "CONTAINER_"))
	}
}

// criStatusLine renders a docker-style status string ("Up 2 hours", "Exited
// 3 minutes ago") from the CRI state and the creation time. CRI listings carry
// no startedAt/exit code, so the age is measured from creation.
func criStatusLine(state string, created time.Time) string {
	age := ""
	if !created.IsZero() && created.Unix() != 0 {
		age = units.HumanDuration(time.Since(created))
	}
	switch criState(state) {
	case "running":
		if age == "" {
			return "Up"
		}
		return "Up " + age
	case "exited":
		if age == "" {
			return "Exited"
		}
		return "Exited " + age + " ago"
	case "created":
		return "Created"
	default:
		return state
	}
}

// InspectContainer renders `crictl inspect` (single JSON object) as YAML.
func (b *criBackend) InspectContainer(id string) (*InspectResult, error) {
	out, err := b.run("inspect", id)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	y, err := jsonToYAML(out)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	return &InspectResult{Name: id, RawYAML: y}, nil
}

func (b *criBackend) StartContainer(id string) error {
	_, err := b.run("start", id)
	return err
}

func (b *criBackend) StopContainer(id string) error {
	_, err := b.run("stop", id)
	return err
}

// RestartContainer stops then starts the container. Note: some CRI runtimes
// refuse to start an exited container (in Kubernetes the kubelet recreates
// instead); the runtime's error is surfaced as-is.
func (b *criBackend) RestartContainer(id string) error {
	if err := b.StopContainer(id); err != nil {
		return err
	}
	return b.StartContainer(id)
}

func (b *criBackend) RemoveContainer(id string, force bool) error {
	// CRI's RemoveContainer forcibly removes running containers by spec, so
	// there is no separate -f; crictl rm -f additionally stops first.
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	_, err := b.run(append(args, id)...)
	return err
}

// KillContainer maps to CRI's StopContainer with a zero timeout (immediate
// SIGKILL). CRI carries no arbitrary-signal RPC, so any signal other than KILL
// is rejected with an explanation.
func (b *criBackend) KillContainer(id, signal string) error {
	s := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(signal), "SIG"))
	if s != "" && s != "KILL" {
		return errors.New(i18n.T(
			"CRI не поддерживает произвольные сигналы — доступен только KILL (stop с таймаутом 0)",
			"CRI does not support arbitrary signals — only KILL (stop with timeout 0) is available"))
	}
	_, err := b.run("stop", "--timeout", "0", id)
	return err
}

// ContainerLogs streams `crictl logs -f` (crictl reads the CRI log files on
// the host). Until has no crictl equivalent and is ignored.
func (b *criBackend) ContainerLogs(id string, opts LogOptions) (<-chan string, func(), error) {
	return b.runner.stream(b.args(buildCRILogsArgs(id, opts)...))
}

// buildCRILogsArgs assembles `logs -f --timestamps [--tail N] [--since] id`.
func buildCRILogsArgs(id string, opts LogOptions) []string {
	args := []string{"logs", "-f", "--timestamps"}
	if opts.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(opts.Tail))
	}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	return append(args, id)
}

// ── stats ───────────────────────────────────────────────────────────────────

// criStat is the subset of `crictl stats -o json` we use.
type criStat struct {
	Attributes struct {
		ID string `json:"id"`
	} `json:"attributes"`
	CPU struct {
		Timestamp            criNumber `json:"timestamp"`
		UsageCoreNanoSeconds criUint64 `json:"usageCoreNanoSeconds"`
	} `json:"cpu"`
	Memory struct {
		WorkingSetBytes criUint64 `json:"workingSetBytes"`
	} `json:"memory"`
}

// parseCRIStats decodes the `crictl stats -o json` envelope.
func parseCRIStats(out string) ([]criStat, error) {
	var doc struct {
		Stats []criStat `json:"stats"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("parse crictl stats: %w", err)
	}
	return doc.Stats, nil
}

// criCPUSample is one point of a container's cumulative CPU counter, kept
// between refresh ticks to derive CPU%.
type criCPUSample struct {
	usageNano uint64
	timestamp int64 // unix nanos
}

// ContainerStats samples `crictl stats`. CRI reports only the cumulative CPU
// counter, so CPU% is computed as the delta against the previous tick's sample
// (first sight of a container yields 0%, exactly like the docker backend's
// one-shot stats). Memory limit is not part of CRI stats, so MemPerc stays 0.
func (b *criBackend) ContainerStats(ids []string) (map[string]ContainerStats, error) {
	if len(ids) == 0 {
		return map[string]ContainerStats{}, nil
	}
	out, err := b.run("stats", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	rows, err := parseCRIStats(out)
	if err != nil {
		return nil, err
	}

	b.statsMu.Lock()
	defer b.statsMu.Unlock()
	if b.cpuPrev == nil {
		b.cpuPrev = map[string]criCPUSample{}
	}
	result := make(map[string]ContainerStats, len(rows))
	for _, s := range rows {
		id := shortID(s.Attributes.ID)
		cur := criCPUSample{
			usageNano: s.CPU.UsageCoreNanoSeconds.Value.uint64(),
			timestamp: s.CPU.Timestamp.int64(),
		}
		cpu := criCPUPercent(b.cpuPrev[id], cur)
		b.cpuPrev[id] = cur
		result[id] = ContainerStats{
			ID:       id,
			CPUPerc:  cpu,
			MemUsage: s.Memory.WorkingSetBytes.Value.uint64(),
		}
	}
	return result, nil
}

// criCPUPercent derives CPU% from two cumulative samples; unusable pairs
// (first sample, clock skew, counter reset) yield 0.
func criCPUPercent(prev, cur criCPUSample) float64 {
	if prev.timestamp == 0 || cur.timestamp <= prev.timestamp || cur.usageNano < prev.usageNano {
		return 0
	}
	return float64(cur.usageNano-prev.usageNano) / float64(cur.timestamp-prev.timestamp) * 100
}

// ── exec / filesystem ───────────────────────────────────────────────────────

// ExecInteractive opens an interactive `crictl exec -i -t` session. An empty
// cmd defaults to a shell. Only available over the ssh transport (the local
// runner cannot bridge a PTY into the embedded terminal).
func (b *criBackend) ExecInteractive(containerID string, cmd []string) (ExecSession, error) {
	args := append([]string{"exec", "-i", "-t", containerID}, execArgv(cmd)...)
	return b.runner.interactive(b.args(args...))
}

// ListPath lists a directory inside the container by running `ls -1Ap` there
// via a non-interactive exec, reusing the docker backend's parser.
func (b *criBackend) ListPath(containerID, dir string) ([]FileEntry, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "/"
	}
	out, err := b.run("exec", containerID, "ls", "-1Ap", "--", dir)
	if err != nil {
		return nil, friendlyListErr(dir, err.Error())
	}
	return parseLsEntries(out), nil
}

// ── not modeled by CRI ──────────────────────────────────────────────────────

// RunContainer is unsupported: creating a CRI container requires a pod sandbox
// plus pod/container config documents — kubelet territory, not a TUI form.
func (b *criBackend) RunContainer(RunOptions) error {
	return errCRIUnsupported(i18n.T("запуск контейнера (:run)", "running a container (:run)"))
}

func (b *criBackend) RunInteractive(ExecRunOptions) (ExecSession, error) {
	return nil, errCRIUnsupported(i18n.T("одноразовый контейнер (:exec из образа)", "a disposable container (:exec from an image)"))
}

func (b *criBackend) CopyFromContainer(string, string, string) error {
	return errCRIUnsupported(i18n.T("копирование файлов (cp)", "file copy (cp)"))
}

func (b *criBackend) CopyToContainer(string, string, string) error {
	return errCRIUnsupported(i18n.T("копирование файлов (cp)", "file copy (cp)"))
}
