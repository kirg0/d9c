package docker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"gopkg.in/yaml.v3"
)

// Docker Compose V2 labels stamped on every container of a project.
const (
	composeProjectLabel = "com.docker.compose.project"
	composeWorkdirLabel = "com.docker.compose.project.working_dir"
	composeConfigLabel  = "com.docker.compose.project.config_files"
	composeServiceLabel = "com.docker.compose.service"
)

// ComposeProject is a single Docker Compose deployment aggregated from container
// labels. A "deployment" is identified by its working_dir, not just its project
// name: several independent compose files (each in its own directory) can share
// one project name, and they must NOT be lumped together — see WorkingDir.
type ComposeProject struct {
	Project     string // com.docker.compose.project (e.g. "mcmc"); used for `-p`
	Name        string // display name of the deployment (working_dir basename)
	WorkingDir  string // identity: distinguishes deployments sharing a project
	ConfigFiles string
	Status      string // running | stopped | paused | partial | error
	Command     string
	Running     int
	Total       int
}

// composeIdentity is the stable key distinguishing one deployment from another:
// the working_dir when present (so deployments sharing a project name but living
// in different directories stay separate), else the project name (older compose
// without the working_dir label).
func composeIdentity(project, workdir string) string {
	if workdir != "" {
		return workdir
	}
	return project
}

// composeDisplayName is the human name of a deployment: the working_dir's base
// (e.g. "core.licensing"), or the project name when there's no working_dir.
func composeDisplayName(project, workdir string) string {
	if workdir == "" {
		return project
	}
	return path.Base(strings.ReplaceAll(workdir, "\\", "/"))
}

// composeFilter builds the container filter selecting exactly one deployment.
// A path-like identity (an absolute working_dir) filters by the working_dir
// label; anything else is a project name, which can never contain a path
// separator — so the two cases are unambiguous.
func composeFilter(identity string) filters.Args {
	if strings.ContainsAny(identity, "/\\") {
		return filters.NewArgs(filters.Arg("label", composeWorkdirLabel+"="+identity))
	}
	return filters.NewArgs(filters.Arg("label", composeProjectLabel+"="+identity))
}

// composeMember is one container's compose-relevant labels plus its state, the
// minimal input to the pure grouping logic.
type composeMember struct {
	project, workdir, config, state string
}

// groupComposeProjects aggregates containers into deployments keyed by
// composeIdentity (working_dir, fallback project). Containers without a project
// label are ignored. Order is project, then deployment name.
func groupComposeProjects(members []composeMember) []ComposeProject {
	type agg struct {
		project, workdir, config string
		states                   []string
	}
	groups := map[string]*agg{}
	order := make([]string, 0)
	for _, m := range members {
		if m.project == "" {
			continue
		}
		key := composeIdentity(m.project, m.workdir)
		g := groups[key]
		if g == nil {
			g = &agg{project: m.project, workdir: m.workdir, config: m.config}
			groups[key] = g
			order = append(order, key)
		}
		g.states = append(g.states, m.state)
	}

	projects := make([]ComposeProject, 0, len(groups))
	for _, key := range order {
		g := groups[key]
		running := 0
		for _, s := range g.states {
			if s == "running" {
				running++
			}
		}
		projects = append(projects, ComposeProject{
			Project:     g.project,
			Name:        composeDisplayName(g.project, g.workdir),
			WorkingDir:  g.workdir,
			ConfigFiles: g.config,
			Status:      composeStatus(g.states),
			Command:     composeCommand(g.config),
			Running:     running,
			Total:       len(g.states),
		})
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Project != projects[j].Project {
			return projects[i].Project < projects[j].Project
		}
		return projects[i].Name < projects[j].Name
	})
	return projects
}

func (b *dockerBackend) ListComposeProjects() ([]ComposeProject, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := b.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	members := make([]composeMember, 0, len(list))
	for _, c := range list {
		members = append(members, composeMember{
			project: c.Labels[composeProjectLabel],
			workdir: c.Labels[composeWorkdirLabel],
			config:  c.Labels[composeConfigLabel],
			state:   c.State,
		})
	}
	return groupComposeProjects(members), nil
}

// composeStatus aggregates per-container states into a project-level status.
func composeStatus(states []string) string {
	if len(states) == 0 {
		return "unknown"
	}
	var running, paused, dead, restarting int
	for _, s := range states {
		switch s {
		case "running":
			running++
		case "paused":
			paused++
		case "dead":
			dead++
		case "restarting":
			restarting++
		}
	}
	switch {
	case dead > 0 || restarting > 0:
		return "error"
	case paused > 0 && running == 0:
		return "paused"
	case running == 0:
		return "stopped"
	case running == len(states):
		return "running"
	default:
		return "partial"
	}
}

// composeCommand synthesises the command that would (re)start the project,
// adding -f only when a non-default compose file name is used.
func composeCommand(configFiles string) string {
	first := strings.TrimSpace(strings.Split(configFiles, ",")[0])
	if first == "" {
		return "docker compose up -d"
	}
	base := path.Base(strings.ReplaceAll(first, "\\", "/"))
	switch base {
	case "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
		return "docker compose up -d"
	default:
		return "docker compose -f " + base + " up -d"
	}
}

// ListComposeContainers returns all containers (any state) of a deployment.
func (b *dockerBackend) ListComposeContainers(identity string) ([]Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := b.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: composeFilter(identity)})
	if err != nil {
		return nil, fmt.Errorf("list project containers: %w", err)
	}
	result := make([]Container, 0, len(list))
	for _, c := range list {
		result = append(result, toContainer(c))
	}
	return result, nil
}

// ── detail ─────────────────────────────────────────────────────────────────

type composeServiceDetail struct {
	Name     string   `yaml:"name"`
	Image    string   `yaml:"image"`
	State    string   `yaml:"state"`
	Created  string   `yaml:"created,omitempty"`
	Started  string   `yaml:"started,omitempty"`
	Ports    []string `yaml:"ports,omitempty"`
	Networks []string `yaml:"networks,omitempty"`
	Mounts   []string `yaml:"mounts,omitempty"`
	Env      []string `yaml:"env,omitempty"`
}

type composeDetail struct {
	Project    string                 `yaml:"project"`
	WorkingDir string                 `yaml:"working_dir,omitempty"`
	Status     string                 `yaml:"status"`
	Images     []string               `yaml:"images,omitempty"`
	Networks   []string               `yaml:"networks,omitempty"`
	Volumes    []string               `yaml:"volumes,omitempty"`
	Services   []composeServiceDetail `yaml:"services"`
}

// InspectComposeProject builds a detailed YAML overview of a project: its
// services with images, ports, networks, mounts and env, plus aggregated
// images/networks/volumes.
func (b *dockerBackend) InspectComposeProject(identity string) (*InspectResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	list, err := b.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: composeFilter(identity)})
	if err != nil {
		return nil, fmt.Errorf("list project containers: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no containers found for compose deployment %q", identity)
	}

	detail := composeDetail{Project: list[0].Labels[composeProjectLabel]}
	imgSet, netSet, volSet := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var states []string

	for _, c := range list {
		if detail.WorkingDir == "" {
			detail.WorkingDir = c.Labels[composeWorkdirLabel]
		}
		info, err := b.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}
		states = append(states, info.State.Status)

		sd := composeServiceDetail{
			Name:    strings.TrimPrefix(info.Name, "/"),
			Image:   info.Config.Image,
			State:   info.State.Status,
			Created: info.Created,
			Env:     info.Config.Env,
		}
		if info.State != nil {
			sd.Started = info.State.StartedAt
		}
		for port, bindings := range info.NetworkSettings.Ports {
			if len(bindings) == 0 {
				sd.Ports = append(sd.Ports, string(port))
				continue
			}
			for _, bnd := range bindings {
				sd.Ports = append(sd.Ports, fmt.Sprintf("%s:%s->%s", bnd.HostIP, bnd.HostPort, string(port)))
			}
		}
		sort.Strings(sd.Ports)
		for n := range info.NetworkSettings.Networks {
			sd.Networks = append(sd.Networks, n)
			netSet[n] = true
		}
		sort.Strings(sd.Networks)
		for _, mnt := range info.Mounts {
			src := mnt.Source
			if mnt.Type == "volume" && mnt.Name != "" {
				src = mnt.Name
			}
			s := fmt.Sprintf("%s -> %s (%s)", src, mnt.Destination, mnt.Type)
			sd.Mounts = append(sd.Mounts, s)
			volSet[s] = true
		}
		imgSet[info.Config.Image] = true
		detail.Services = append(detail.Services, sd)
	}

	detail.Status = composeStatus(states)
	detail.Images = sortedKeys(imgSet)
	detail.Networks = sortedKeys(netSet)
	detail.Volumes = sortedKeys(volSet)

	y, err := yaml.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal compose detail: %w", err)
	}
	return &InspectResult{Name: composeDisplayName(detail.Project, detail.WorkingDir), RawYAML: string(y)}, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── logs ───────────────────────────────────────────────────────────────────

// ComposeLogs fans the follow-streams of all project containers into a single
// channel, prefixing each line with its service name (like `docker compose logs`).
func (b *dockerBackend) ComposeLogs(identity string, opts LogOptions) (<-chan string, func(), error) {
	ctx := context.Background()
	list, err := b.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: composeFilter(identity)})
	if err != nil {
		return nil, nil, fmt.Errorf("list project containers: %w", err)
	}
	if len(list) == 0 {
		return nil, nil, fmt.Errorf("no containers found for compose deployment %q", identity)
	}

	out := make(chan string, 256)
	done := make(chan struct{})
	var stops []func()
	var wg sync.WaitGroup
	for _, c := range list {
		svc := c.Labels[composeServiceLabel]
		if svc == "" {
			svc = strings.TrimPrefix(firstName(c.Names), "/")
		}
		ch, stopOne, err := b.ContainerLogs(c.ID, opts)
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
	// stop tears down every per-container stream and unblocks forwarders stuck
	// on a send nobody reads.
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

func firstName(names []string) string {
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// ── lifecycle (operate on the project's containers via the Docker API) ─────────

func (b *dockerBackend) composeForEach(identity string, fn func(ctx context.Context, id string) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	list, err := b.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: composeFilter(identity)})
	if err != nil {
		return fmt.Errorf("list project containers: %w", err)
	}
	if len(list) == 0 {
		return fmt.Errorf("no containers found for compose deployment %q", identity)
	}
	for _, c := range list {
		if err := fn(ctx, c.ID); err != nil {
			return err
		}
	}
	return nil
}

func (b *dockerBackend) ComposeStart(project string) error {
	return b.composeForEach(project, func(ctx context.Context, id string) error {
		return b.cli.ContainerStart(ctx, id, container.StartOptions{})
	})
}

func (b *dockerBackend) ComposeStop(project string) error {
	return b.composeForEach(project, func(ctx context.Context, id string) error {
		return b.cli.ContainerStop(ctx, id, container.StopOptions{})
	})
}

func (b *dockerBackend) ComposeRestart(project string) error {
	return b.composeForEach(project, func(ctx context.Context, id string) error {
		return b.cli.ContainerRestart(ctx, id, container.StopOptions{})
	})
}

func (b *dockerBackend) ComposePause(project string) error {
	return b.composeForEach(project, func(ctx context.Context, id string) error {
		return b.cli.ContainerPause(ctx, id)
	})
}

func (b *dockerBackend) ComposeUnpause(project string) error {
	return b.composeForEach(project, func(ctx context.Context, id string) error {
		return b.cli.ContainerUnpause(ctx, id)
	})
}

func (b *dockerBackend) ComposeRemove(project string) error {
	return b.composeForEach(project, func(ctx context.Context, id string) error {
		return b.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
	})
}

// ── compose-engine ops over SSH (up / pull / down / config) ─────────────────

// ComposeUp/Pull/Down run the corresponding `docker compose` action on the host
// and stream its combined stdout/stderr line-by-line, so the UI can show live
// progress (layer pulls, container creation) for these long-running operations.
func (b *dockerBackend) ComposeUp(project string) (<-chan string, func(), error) {
	return b.runComposeSSHStream(project, "up -d")
}

func (b *dockerBackend) ComposePull(project string) (<-chan string, func(), error) {
	return b.runComposeSSHStream(project, "pull")
}

func (b *dockerBackend) ComposeDown(project string) (<-chan string, func(), error) {
	return b.runComposeSSHStream(project, "down")
}

// ComposeConfig returns the merged/validated configuration (`docker compose config`).
func (b *dockerBackend) ComposeConfig(project string) (string, error) {
	return b.runComposeSSHOutput(project, "config")
}

// ── create a brand-new project from scratch ─────────────────────────────────

// CreateComposeFile writes content to "<dir>/docker-compose.yaml" on the host
// (creating dir if needed) and brings the project up, streaming `up` output.
func (b *dockerBackend) CreateComposeFile(dir, content string) (<-chan string, func(), error) {
	if b.sshClient == nil {
		return nil, nil, fmt.Errorf("creating a compose project requires an SSH connection (use -H ssh://...)")
	}
	dir = strings.TrimRight(strings.ReplaceAll(dir, "\\", "/"), "/")
	if dir == "" {
		return nil, nil, fmt.Errorf("a target directory is required")
	}
	path := dir + "/docker-compose.yaml"
	if err := b.sshEnsureDirAndWrite(dir, path, content); err != nil {
		return nil, nil, err
	}
	base := "docker compose --project-directory " + shellQuote(dir) + " -f " + shellQuote(path) + " up -d"
	if b.sshNeedsSudo() {
		base = "sudo " + base
	}
	return b.sshExecStream(base)
}

// sshEnsureDirAndWrite makes dir and writes content to path, each with a sudo
// fallback for permission-restricted locations.
func (b *dockerBackend) sshEnsureDirAndWrite(dir, path, content string) error {
	if _, err := b.sshExecOutput("mkdir -p " + shellQuote(dir)); err != nil {
		if _, err2 := b.sshExecOutput("sudo mkdir -p " + shellQuote(dir)); err2 != nil {
			return err
		}
	}
	if err := b.sshWriteFile("tee "+shellQuote(path)+" >/dev/null", content); err == nil {
		return nil
	}
	return b.sshWriteFile("sudo tee "+shellQuote(path)+" >/dev/null", content)
}

// ── backup (download the project's working directory as a tar.gz) ────────────

// BackupComposeProject streams a gzip-compressed tar of the project's working
// directory over SSH into a local "<project>-<timestamp>.tar.gz" file and
// returns its path.
func (b *dockerBackend) BackupComposeProject(identity string) (string, error) {
	if b.sshClient == nil {
		return "", fmt.Errorf("backup requires an SSH connection (use -H ssh://...)")
	}
	project, workdir, _, err := b.composeProjectMeta(identity)
	if err != nil {
		return "", err
	}
	if workdir == "" {
		return "", fmt.Errorf("deployment %q has no working directory to back up", identity)
	}
	// Name the archive after the deployment (working_dir basename), not the raw
	// path identity, so it reads "core.licensing-<ts>.tar.gz" rather than a
	// slash-mangled path. The UI catalog matches by the same display name.
	local := backupFileName(composeDisplayName(project, workdir))
	tarCmd := "tar czf - -C " + shellQuote(workdir) + " ."
	if err := b.sshExecToFile(tarCmd, local); err == nil {
		return local, nil
	}
	// Some hosts need sudo to read root-owned project files.
	if err := b.sshExecToFile("sudo "+tarCmd, local); err != nil {
		return "", err
	}
	return local, nil
}

// ── restore (upload a backup tar.gz and bring the project back up) ────────────

// RestoreComposeProject uploads a local gzip-tar backup, extracts it into the
// project's working directory and brings the project up again, streaming the
// extract and `up` progress line-by-line into the returned channel.
func (b *dockerBackend) RestoreComposeProject(identity, backupPath string) (<-chan string, func(), error) {
	if b.sshClient == nil {
		return nil, nil, fmt.Errorf("restore requires an SSH connection (use -H ssh://...)")
	}
	project, workdir, configFiles, err := b.composeProjectMeta(identity)
	if err != nil {
		return nil, nil, err
	}
	if workdir == "" {
		return nil, nil, fmt.Errorf("deployment %q has no working directory to restore into", identity)
	}
	f, err := os.Open(backupPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open backup: %w", err)
	}
	// A streamed pipe can't be retried mid-flight, so decide on sudo up front.
	sudo := b.sshNeedsSudo()

	out := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	// upStop aborts the inner `up` stream once it exists; guarded because the
	// producer sets it after stop may already have fired.
	var mu sync.Mutex
	var upStop func()
	// stop aborts the restore: closing the backup file unblocks the extract pipe,
	// done unblocks producers, and the inner up-stream (if started) is torn down.
	stop := func() {
		once.Do(func() {
			close(done)
			_ = f.Close()
			mu.Lock()
			s := upStop
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

		if !send("extracting " + backupPath + " into " + workdir) {
			return
		}
		extract := "tar xzf - -C " + shellQuote(workdir)
		if sudo {
			extract = "sudo " + extract
		}
		if err := b.sshPipeReader(extract, f); err != nil {
			send("error: " + err.Error())
			return
		}

		if !send("starting project…") {
			return
		}
		upBase := buildComposeCmd(project, workdir, configFiles, "up -d")
		if sudo {
			upBase = "sudo " + upBase
		}
		ch, stopUp, err := b.sshExecStream(upBase)
		if err != nil {
			send("error: " + err.Error())
			return
		}
		mu.Lock()
		upStop = stopUp
		mu.Unlock()
		// Cover the window where stop fired before upStop was published.
		select {
		case <-done:
			stopUp()
		default:
		}
		for line := range ch {
			if !send(line) {
				stopUp()
				return
			}
		}
	}()
	return out, stop, nil
}

// backupFileName builds a safe "<project>-<timestamp>.tar.gz" name.
func backupFileName(project string) string {
	return BackupFilePrefix(project) + "-" + time.Now().Format("20060102-150405") + ".tar.gz"
}

// BackupFilePrefix returns the sanitized name prefix shared by all of a
// project's backup archives — everything before the "-<timestamp>.tar.gz".
// The UI uses it to find a project's existing backups in the catalog.
func BackupFilePrefix(project string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, project)
	safe = strings.Trim(safe, "-")
	if safe == "" {
		safe = "compose"
	}
	return safe
}

// sshExecToFile runs a command over SSH and streams its stdout into a local
// file. On a non-zero exit the partial file is removed and the remote stderr
// (trimmed) becomes the error.
func (b *dockerBackend) sshExecToFile(cmd, path string) error {
	session, err := b.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	session.Stdout = f
	session.Stderr = &stderr
	runErr := session.Run(cmd)
	closeErr := f.Close()
	if runErr != nil {
		_ = os.Remove(path)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return closeErr
}

// ── compose file edit over SSH (read / write) ──────────────────────────────

// composeConfigPath returns the first config file path recorded for the project,
// resolved to an absolute path on the remote host. Compose may record the config
// file relative to the working_dir (e.g. just "docker-compose.yml"), so a bare or
// relative path is joined onto working_dir — otherwise `cat`/`tee` would run in
// the SSH session's home directory and miss the file.
func (b *dockerBackend) composeConfigPath(identity string) (string, error) {
	_, workdir, configFiles, err := b.composeProjectMeta(identity)
	if err != nil {
		return "", err
	}
	first := strings.TrimSpace(strings.Split(configFiles, ",")[0])
	if first == "" {
		return "", fmt.Errorf("deployment %q has no recorded compose file", identity)
	}
	return resolveComposePath(workdir, first), nil
}

// resolveComposePath joins a (possibly relative) compose config path onto the
// project's working directory using POSIX semantics (the remote host is Linux).
func resolveComposePath(workdir, configFile string) string {
	cf := strings.ReplaceAll(configFile, "\\", "/")
	if path.IsAbs(cf) || workdir == "" {
		return cf
	}
	return path.Join(strings.ReplaceAll(workdir, "\\", "/"), cf)
}

// ReadComposeFile returns the path and contents of the project's compose file.
func (b *dockerBackend) ReadComposeFile(project string) (path, content string, err error) {
	if b.sshClient == nil {
		return "", "", fmt.Errorf("editing requires an SSH connection (use -H ssh://...)")
	}
	path, err = b.composeConfigPath(project)
	if err != nil {
		return "", "", err
	}
	out, err := b.sshExecOutput("cat " + shellQuote(path))
	if err != nil {
		if out2, err2 := b.sshExecOutput("sudo cat " + shellQuote(path)); err2 == nil {
			return path, out2, nil
		}
		return "", "", err
	}
	return path, out, nil
}

// WriteComposeFile writes content back to the project's compose file.
func (b *dockerBackend) WriteComposeFile(project, content string) error {
	if b.sshClient == nil {
		return fmt.Errorf("editing requires an SSH connection (use -H ssh://...)")
	}
	path, err := b.composeConfigPath(project)
	if err != nil {
		return err
	}
	if err := b.sshWriteFile("tee "+shellQuote(path)+" >/dev/null", content); err == nil {
		return nil
	}
	return b.sshWriteFile("sudo tee "+shellQuote(path)+" >/dev/null", content)
}

// sshWriteFile pipes content to a remote command's stdin (e.g. `tee <path>`).
func (b *dockerBackend) sshWriteFile(cmd, content string) error {
	return b.sshPipeReader(cmd, strings.NewReader(content))
}

// sshPipeReader streams r into a remote command's stdin (e.g. `tee <path>` or
// `tar xzf - -C <dir>`). On a non-zero exit the trimmed remote stderr becomes
// the error message.
func (b *dockerBackend) sshPipeReader(cmd string, r io.Reader) error {
	session, err := b.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Start(cmd); err != nil {
		return err
	}
	if _, err := io.Copy(stdin, r); err != nil {
		_ = stdin.Close()
		return err
	}
	_ = stdin.Close()
	if err := session.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// runComposeSSHStream builds the `docker compose <action>` command for the
// project (escalating to sudo if the host requires it) and streams its combined
// output line-by-line into the returned channel, which closes when it exits.
func (b *dockerBackend) runComposeSSHStream(identity, action string) (<-chan string, func(), error) {
	if b.sshClient == nil {
		return nil, nil, fmt.Errorf("compose %s requires an SSH connection (use -H ssh://...)", action)
	}
	project, workdir, configFiles, err := b.composeProjectMeta(identity)
	if err != nil {
		return nil, nil, err
	}
	base := buildComposeCmd(project, workdir, configFiles, action)
	if b.sshNeedsSudo() {
		base = "sudo " + base
	}
	return b.sshExecStream(base)
}

// sshNeedsSudo probes once whether docker on the host requires sudo, mirroring
// the sudo fallback used by sshExecOutput. A streamed command can't be retried
// mid-flight, so the privilege is decided before it starts.
func (b *dockerBackend) sshNeedsSudo() bool {
	_, err := b.sshExecOutput("docker version --format '{{.Server.Version}}'")
	return err != nil
}

// sshExecStream runs a command over SSH and streams its combined stdout/stderr
// line-by-line into the returned channel. The channel is closed when the command
// exits; on a non-zero exit a trailing line carries the error. The returned stop
// aborts the command early: closing the session kills the remote process and
// unblocks producers stuck on a send nobody reads. The caller MUST call stop
// when it abandons the channel, otherwise the SSH session and goroutines leak.
func (b *dockerBackend) sshExecStream(cmd string) (<-chan string, func(), error) {
	session, err := b.sshClient.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("ssh session: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	if err := session.Start(cmd); err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("start %q: %w", cmd, err)
	}

	out := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			_ = session.Close()
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
	var wg sync.WaitGroup
	scan := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if !send(sc.Text()) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			send("error: read output: " + err.Error())
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	go func() {
		wg.Wait()
		if err := session.Wait(); err != nil {
			send("error: " + err.Error())
		}
		stop() // release the session when the command ends naturally
		close(out)
	}()
	return out, stop, nil
}

// runComposeSSHOutput runs `docker compose <action>` over SSH using the
// working_dir and config files from the labels, returning stdout on success.
func (b *dockerBackend) runComposeSSHOutput(identity, action string) (string, error) {
	if b.sshClient == nil {
		return "", fmt.Errorf("compose %s requires an SSH connection (use -H ssh://...)", action)
	}
	project, workdir, configFiles, err := b.composeProjectMeta(identity)
	if err != nil {
		return "", err
	}
	base := buildComposeCmd(project, workdir, configFiles, action)
	out, err := b.sshExecOutput(base)
	if err == nil {
		return out, nil
	}
	// Some hosts require sudo for docker (mirrors the dial-stdio fallback).
	if out2, err2 := b.sshExecOutput("sudo " + base); err2 == nil {
		return out2, nil
	}
	return "", err
}

// composeProjectMeta reads a deployment's project name, working_dir and config
// files from the labels of one of its containers, selected by identity. The
// project name is needed for `docker compose -p`, the others for
// --project-directory / -f.
func (b *dockerBackend) composeProjectMeta(identity string) (project, workdir, configFiles string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := b.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: composeFilter(identity)})
	if err != nil {
		return "", "", "", fmt.Errorf("list project containers: %w", err)
	}
	if len(list) == 0 {
		return "", "", "", fmt.Errorf("no containers found for compose deployment %q", identity)
	}
	l := list[0].Labels
	return l[composeProjectLabel], l[composeWorkdirLabel], l[composeConfigLabel], nil
}

// buildComposeCmd assembles a `docker compose` command line, scoping it to the
// project name, working directory and config files.
func buildComposeCmd(project, workdir, configFiles, action string) string {
	var sb strings.Builder
	sb.WriteString("docker compose --project-name ")
	sb.WriteString(shellQuote(project))
	if workdir != "" {
		sb.WriteString(" --project-directory ")
		sb.WriteString(shellQuote(workdir))
	}
	for cf := range strings.SplitSeq(configFiles, ",") {
		cf = strings.TrimSpace(cf)
		if cf != "" {
			// docker resolves -f relative to the process cwd (the SSH session's
			// home dir), not --project-directory, so make the path absolute.
			sb.WriteString(" -f ")
			sb.WriteString(shellQuote(resolveComposePath(workdir, cf)))
		}
	}
	sb.WriteString(" ")
	sb.WriteString(action)
	return sb.String()
}

// shellQuote single-quotes a value for safe use in a remote shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshExecOutput runs a command over SSH, returning stdout+stderr. On non-zero
// exit the trimmed remote output becomes the error message.
func (b *dockerBackend) sshExecOutput(cmd string) (string, error) {
	session, err := b.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	out, err := session.CombinedOutput(cmd)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}
