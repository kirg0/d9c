package docker

import (
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Compose support for containerd reuses nerdctl's compose subcommand and the
// same container-label discovery as the docker backend: nerdctl stamps the
// standard com.docker.compose.* labels, so the pure grouping logic in compose.go
// (groupComposeProjects / composeFilter / composeStatus) applies unchanged.

// composeMembers reads all containers and projects their compose labels.
func (b *nerdctlBackend) composeMembers() ([]composeMember, error) {
	rows, err := b.psRows(true)
	if err != nil {
		return nil, err
	}
	members := make([]composeMember, 0, len(rows))
	for _, r := range rows {
		labels := parseLabels(r.Labels)
		members = append(members, composeMember{
			project: labels[composeProjectLabel],
			workdir: labels[composeWorkdirLabel],
			config:  labels[composeConfigLabel],
			state:   stateFromStatus(r.Status),
		})
	}
	return members, nil
}

func (b *nerdctlBackend) ListComposeProjects() ([]ComposeProject, error) {
	members, err := b.composeMembers()
	if err != nil {
		return nil, err
	}
	return groupComposeProjects(members), nil
}

// projectContainers returns the raw ps rows belonging to a deployment identity
// (working_dir, else project name).
func (b *nerdctlBackend) projectContainers(identity string) ([]nerdctlPS, error) {
	rows, err := b.psRows(true)
	if err != nil {
		return nil, err
	}
	var out []nerdctlPS
	for _, r := range rows {
		labels := parseLabels(r.Labels)
		if composeIdentity(labels[composeProjectLabel], labels[composeWorkdirLabel]) == identity {
			out = append(out, r)
		}
	}
	return out, nil
}

func (b *nerdctlBackend) ListComposeContainers(identity string) ([]Container, error) {
	rows, err := b.projectContainers(identity)
	if err != nil {
		return nil, err
	}
	result := make([]Container, 0, len(rows))
	for _, r := range rows {
		result = append(result, r.toContainer())
	}
	return result, nil
}

// composeInspectDetail is the lightweight project overview rendered for the
// detail view (built from ps rows, without a per-container inspect round-trip).
type composeInspectDetail struct {
	Project  string   `yaml:"project"`
	Status   string   `yaml:"status"`
	Services []string `yaml:"services"`
	Images   []string `yaml:"images,omitempty"`
}

func (b *nerdctlBackend) InspectComposeProject(identity string) (*InspectResult, error) {
	rows, err := b.projectContainers(identity)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no containers found for compose deployment %q", identity)
	}
	detail := composeInspectDetail{}
	imgSet := map[string]bool{}
	var states []string
	for _, r := range rows {
		labels := parseLabels(r.Labels)
		if detail.Project == "" {
			detail.Project = labels[composeProjectLabel]
		}
		if svc := labels[composeServiceLabel]; svc != "" {
			detail.Services = append(detail.Services, svc)
		}
		imgSet[r.Image] = true
		states = append(states, stateFromStatus(r.Status))
	}
	detail.Status = composeStatus(states)
	detail.Images = sortedKeys(imgSet)
	y, err := yaml.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal compose detail: %w", err)
	}
	return &InspectResult{Name: detail.Project, RawYAML: string(y)}, nil
}

// ComposeLogs fans the per-container follow-streams into one channel, prefixing
// each line with its service name — the same shape as the docker backend.
func (b *nerdctlBackend) ComposeLogs(identity string, opts LogOptions) (<-chan string, func(), error) {
	rows, err := b.projectContainers(identity)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("no containers found for compose deployment %q", identity)
	}
	out := make(chan string, 256)
	done := make(chan struct{})
	var stops []func()
	var wg sync.WaitGroup
	for _, r := range rows {
		svc := parseLabels(r.Labels)[composeServiceLabel]
		if svc == "" {
			svc = r.toContainer().Name
		}
		ch, stopOne, err := b.ContainerLogs(r.ID, opts)
		if err != nil {
			continue
		}
		stops = append(stops, stopOne)
		wg.Add(1)
		go func(svc string, ch <-chan string) {
			defer wg.Done()
			for line := range ch {
				select {
				case out <- svc + " | " + line:
				case <-done:
					return
				}
			}
		}(svc, ch)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			for _, s := range stops {
				s()
			}
		})
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out, stop, nil
}

// composeForEach applies fn (a nerdctl subcommand builder) to each container of
// a deployment, stopping at the first failure.
func (b *nerdctlBackend) composeForEach(identity string, sub func(id string) []string) error {
	rows, err := b.projectContainers(identity)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no containers found for compose deployment %q", identity)
	}
	for _, r := range rows {
		if _, err := b.run(sub(r.ID)...); err != nil {
			return err
		}
	}
	return nil
}

func (b *nerdctlBackend) ComposeStart(p string) error {
	return b.composeForEach(p, func(id string) []string { return []string{"start", id} })
}
func (b *nerdctlBackend) ComposeStop(p string) error {
	return b.composeForEach(p, func(id string) []string { return []string{"stop", id} })
}
func (b *nerdctlBackend) ComposeRestart(p string) error {
	return b.composeForEach(p, func(id string) []string { return []string{"restart", id} })
}
func (b *nerdctlBackend) ComposePause(p string) error {
	return b.composeForEach(p, func(id string) []string { return []string{"pause", id} })
}
func (b *nerdctlBackend) ComposeUnpause(p string) error {
	return b.composeForEach(p, func(id string) []string { return []string{"unpause", id} })
}
func (b *nerdctlBackend) ComposeRemove(p string) error {
	return b.composeForEach(p, func(id string) []string { return []string{"rm", "-f", id} })
}

// SupportsHostCompose is always true for nerdctl: the compose subcommand works
// both locally and over SSH.
func (b *nerdctlBackend) SupportsHostCompose() bool { return true }

// composeArgs builds a `nerdctl compose` argv scoped to a deployment's project
// name, working directory and config files, ending with the action words.
func (b *nerdctlBackend) composeArgs(identity string, action ...string) ([]string, error) {
	project, workdir, configFiles, err := b.composeMeta(identity)
	if err != nil {
		return nil, err
	}
	args := []string{"compose", "--project-name", project}
	if workdir != "" {
		args = append(args, "--project-directory", workdir)
	}
	for _, cf := range splitCommaList(configFiles) {
		args = append(args, "-f", resolveComposePath(workdir, cf))
	}
	return append(args, action...), nil
}

// composeMeta reads a deployment's project/workdir/config from its containers'
// labels (selected by identity).
func (b *nerdctlBackend) composeMeta(identity string) (project, workdir, configFiles string, err error) {
	rows, err := b.projectContainers(identity)
	if err != nil {
		return "", "", "", err
	}
	if len(rows) == 0 {
		return "", "", "", fmt.Errorf("no containers found for compose deployment %q", identity)
	}
	l := parseLabels(rows[0].Labels)
	return l[composeProjectLabel], l[composeWorkdirLabel], l[composeConfigLabel], nil
}

func (b *nerdctlBackend) composeStream(identity string, action ...string) (<-chan string, func(), error) {
	args, err := b.composeArgs(identity, action...)
	if err != nil {
		return nil, nil, err
	}
	return b.runner.stream(b.args(args...))
}

func (b *nerdctlBackend) ComposeUp(p string) (<-chan string, func(), error) {
	return b.composeStream(p, "up", "-d")
}
func (b *nerdctlBackend) ComposePull(p string) (<-chan string, func(), error) {
	return b.composeStream(p, "pull")
}
func (b *nerdctlBackend) ComposeDown(p string) (<-chan string, func(), error) {
	return b.composeStream(p, "down")
}

func (b *nerdctlBackend) ComposeConfig(identity string) (string, error) {
	args, err := b.composeArgs(identity, "config")
	if err != nil {
		return "", err
	}
	return b.run(args...)
}

// ── compose file edit / create / backup (host filesystem) ───────────────────
//
// These operate on the compose project's files on the host filesystem. nerdctl
// exposes no file API, so — as with `cp` — they are only meaningful when nerdctl
// runs locally; over SSH the files live on the remote host. They are reported as
// unsupported rather than silently doing the wrong thing.

func (b *nerdctlBackend) ReadComposeFile(identity string) (path, content string, err error) {
	return "", "", errComposeFilesUnsupported
}
func (b *nerdctlBackend) WriteComposeFile(identity, content string) error {
	return errComposeFilesUnsupported
}
func (b *nerdctlBackend) CreateComposeFile(dir, content string) (<-chan string, func(), error) {
	return nil, nil, errComposeFilesUnsupported
}
func (b *nerdctlBackend) BackupComposeProject(identity string) (string, error) {
	return "", errComposeFilesUnsupported
}
func (b *nerdctlBackend) RestoreComposeProject(identity, backupPath string) (<-chan string, func(), error) {
	return nil, nil, errComposeFilesUnsupported
}

var errComposeFilesUnsupported = fmt.Errorf("editing/backup of compose files is not supported on the containerd (nerdctl) backend")

// splitCommaList splits a comma-separated list into trimmed, non-empty items.
func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
