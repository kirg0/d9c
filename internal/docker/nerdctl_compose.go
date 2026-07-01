package docker

import (
	"fmt"
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

// nerdctl compose (unlike docker compose) does not stamp the working_dir /
// config_files labels, and `nerdctl compose ls` is absent in 2.x — so the path
// of a *discovered* project's compose file is unrecoverable, and the engine ops
// cannot drive `nerdctl compose -f …`. They are instead reconstructed from the
// labels that ARE present (com.docker.compose.project on both containers and
// networks): up starts the project's containers, pull pulls their images, and
// down stops+removes the containers and the project's networks. Recreation from
// the file and `config` remain unavailable (errComposeFilesUnsupported).

// composeTask is one step of a reconstructed compose engine op: a banner line
// plus either a simple action (start/rm) or a streamed sub-command (pull).
type composeTask struct {
	banner string
	action func() error // simple action; nil when stream is set
	stream []string     // full (namespace-scoped) argv for runner.stream
}

// runComposeTasks streams the tasks' progress into a channel, running them in
// order off the event loop. The returned stop aborts the sequence (and the
// active streamed sub-command); the caller MUST call it when abandoning the
// channel, per the stream contract.
func (b *nerdctlBackend) runComposeTasks(tasks []composeTask) (<-chan string, func(), error) {
	out := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var activeStop func()
	stop := func() {
		once.Do(func() {
			close(done)
			mu.Lock()
			s := activeStop
			mu.Unlock()
			if s != nil {
				s()
			}
		})
	}
	send := func(line string) bool {
		select {
		case out <- line:
			return true
		case <-done:
			return false
		}
	}
	go func() {
		defer close(out)
		defer stop()
		for _, t := range tasks {
			select {
			case <-done:
				return
			default:
			}
			if t.banner != "" && !send(t.banner) {
				return
			}
			switch {
			case t.stream != nil:
				ch, s, err := b.runner.stream(t.stream)
				if err != nil {
					if !send("  error: " + err.Error()) {
						return
					}
					continue
				}
				mu.Lock()
				activeStop = s
				mu.Unlock()
				for line := range ch {
					if !send("  " + line) {
						s()
						return
					}
				}
				mu.Lock()
				activeStop = nil
				mu.Unlock()
			case t.action != nil:
				if err := t.action(); err != nil {
					if !send("  error: " + err.Error()) {
						return
					}
				} else if !send("  done") {
					return
				}
			}
		}
		send("done")
	}()
	return out, stop, nil
}

// ComposeUp starts every container of the project — it brings a discovered or
// stopped deployment up. nerdctl cannot recreate from the compose file without
// its (unrecorded) path, so this is a start, not a recreate.
func (b *nerdctlBackend) ComposeUp(project string) (<-chan string, func(), error) {
	rows, err := b.projectContainers(project)
	if err != nil {
		return nil, nil, err
	}
	tasks := make([]composeTask, 0, len(rows))
	for _, r := range rows {
		id, name := r.ID, r.toContainer().Name
		tasks = append(tasks, composeTask{
			banner: "Starting " + name,
			action: func() error { _, e := b.run("start", id); return e },
		})
	}
	return b.runComposeTasks(tasks)
}

// ComposePull pulls the image of every distinct service in the project,
// streaming each pull's progress.
func (b *nerdctlBackend) ComposePull(project string) (<-chan string, func(), error) {
	rows, err := b.projectContainers(project)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	var tasks []composeTask
	for _, r := range rows {
		if r.Image == "" || seen[r.Image] {
			continue
		}
		seen[r.Image] = true
		tasks = append(tasks, composeTask{
			banner: "Pulling " + r.Image,
			stream: b.args("pull", r.Image),
		})
	}
	return b.runComposeTasks(tasks)
}

// ComposeDown stops and removes the project's containers, then removes its
// networks (identified by the com.docker.compose.project label). Named volumes
// are left intact, matching `docker compose down` defaults.
func (b *nerdctlBackend) ComposeDown(project string) (<-chan string, func(), error) {
	rows, err := b.projectContainers(project)
	if err != nil {
		return nil, nil, err
	}
	name := project
	if len(rows) > 0 {
		if p := parseLabels(rows[0].Labels)[composeProjectLabel]; p != "" {
			name = p
		}
	}
	nets, _ := b.projectNetworks(name)
	tasks := make([]composeTask, 0, len(rows)+len(nets))
	for _, r := range rows {
		id, cname := r.ID, r.toContainer().Name
		tasks = append(tasks, composeTask{
			banner: "Removing " + cname,
			action: func() error { _, e := b.run("rm", "-f", id); return e },
		})
	}
	for _, n := range nets {
		net := n
		tasks = append(tasks, composeTask{
			banner: "Removing network " + net,
			action: func() error { _, e := b.run("network", "rm", net); return e },
		})
	}
	return b.runComposeTasks(tasks)
}

// projectNetworks returns the networks that belong to a compose project, matched
// by the com.docker.compose.project label nerdctl stamps on them.
func (b *nerdctlBackend) projectNetworks(project string) ([]string, error) {
	out, err := b.run("network", "ls", "--format", jsonFormat)
	if err != nil {
		return nil, err
	}
	rows, err := parseJSONLines[nerdctlNetwork](out)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, r := range rows {
		if r.Name == "" {
			continue
		}
		if parseLabels(r.Labels)[composeProjectLabel] == project {
			names = append(names, r.Name)
		}
	}
	return names, nil
}

// ComposeConfig is unavailable on the nerdctl backend: rendering the merged
// config needs the compose file, whose path nerdctl does not record.
func (b *nerdctlBackend) ComposeConfig(string) (string, error) {
	return "", errComposeFilesUnsupported
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
