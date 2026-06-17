package docker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FakeBackend is an in-memory Backend implementation used for the --demo mode
// and for headless UI tests. It serves sample data and reproduces the same
// daemon-style errors (and their friendly translations) that the real backend
// returns, so the UI can be exercised without a live Docker host.
type FakeBackend struct {
	Containers []Container
	Images     []Image
	Networks   []Network
	Volumes    []Volume
	Composes   []ComposeProject

	// ComposeFiles maps project name -> compose file content (for edit demo).
	ComposeFiles map[string]string

	// NoHostCompose simulates a tcp:// connection, where host-side compose
	// operations (up/down/pull/config/edit/create/backup/restore) are
	// unavailable. The default (false) mirrors an ssh:// connection so the demo
	// exercises every feature.
	NoHostCompose bool

	// LogLines is the canned log stream returned by ContainerLogs.
	LogLines []string
}

// NewFakeBackend returns a FakeBackend pre-populated with representative data.
func NewFakeBackend() *FakeBackend {
	now := time.Now()
	return &FakeBackend{
		Containers: []Container{
			{ID: "9ae942fd8fbc", Name: "web", Image: "nginx:1.25", Status: "Up 2 hours (healthy)", State: "running", Health: "healthy", Ports: "0.0.0.0:8080->80/tcp", Created: now.Add(-2 * time.Hour), Labels: map[string]string{"env": "prod", "tier": "frontend"}, Networks: []string{"bridge", "frontend"}},
			{ID: "d2c94e258dcb", Name: "api", Image: "hello-world:latest", Status: "Up 5 minutes (health: starting)", State: "running", Health: "starting", Ports: "", Created: now.Add(-5 * time.Minute), Labels: map[string]string{"env": "prod", "tier": "backend"}, Networks: []string{"backend"}},
			{ID: "3f1ab77c9012", Name: "db", Image: "postgres:16", Status: "Exited (0) 1 hour ago", State: "exited", Ports: "5432/tcp", Created: now.Add(-3 * time.Hour), Labels: map[string]string{"env": "staging", "tier": "backend"}, Networks: []string{"backend"}},
		},
		Images: []Image{
			{ID: "a08c488a9779", Tags: "nginx:1.25", Size: "187 MB", Created: now.Add(-72 * time.Hour)},
			{ID: "d2c94e258dcb", Tags: "hello-world:latest", Size: "13 kB", Created: now.Add(-240 * time.Hour)},
			{ID: "b1d3f9e7c4a2", Tags: "<none>:<none>", Size: "5 MB", Created: now.Add(-12 * time.Hour)},
			{ID: "c7e8a2b4d6f1", Tags: "postgres:16", Size: "431 MB", Created: now.Add(-500 * time.Hour)},
		},
		Networks: []Network{
			{ID: "f0a1b2c3d4e5", Name: "bridge", Driver: "bridge", Scope: "local", Subnet: "172.17.0.0/16"},
			{ID: "1122334455aa", Name: "host", Driver: "host", Scope: "local", Subnet: ""},
			{ID: "aabbccddeeff", Name: "app-net", Driver: "bridge", Scope: "local", Subnet: "172.20.0.0/16"},
		},
		Volumes: []Volume{
			{Name: "pgdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/pgdata/_data", Created: now.Add(-100 * time.Hour).Format(time.RFC3339)},
			{Name: "cache", Driver: "local", Mountpoint: "/var/lib/docker/volumes/cache/_data", Created: now.Add(-20 * time.Hour).Format(time.RFC3339)},
		},
		Composes: []ComposeProject{
			{Project: "webapp", Name: "webapp", WorkingDir: "/srv/webapp", ConfigFiles: "/srv/webapp/docker-compose.yml", Status: "running", Command: "docker compose up -d", Running: 3, Total: 3},
			{Project: "monitoring", Name: "monitoring", WorkingDir: "/srv/monitoring", ConfigFiles: "/srv/monitoring/compose.yaml", Status: "partial", Command: "docker compose up -d", Running: 1, Total: 2},
			{Project: "legacy", Name: "legacy", WorkingDir: "/opt/legacy", ConfigFiles: "/opt/legacy/docker-compose.yaml", Status: "stopped", Command: "docker compose up -d", Running: 0, Total: 2},
		},
		ComposeFiles: map[string]string{
			// Keyed by deployment identity (working_dir), matching ReadComposeFile.
			"/srv/webapp": "services:\n  web:\n    image: nginx:1.25\n    ports:\n      - 8080:80\n",
		},
		LogLines: []string{
			"2026-06-01T10:00:00Z INFO  server started on :8080",
			"2026-06-01T10:00:01Z DEBUG config loaded from /etc/app.yaml",
			"2026-06-01T10:00:05Z WARN  cache miss for key=session:abc",
			"2026-06-01T10:00:09Z ERROR failed to connect to upstream: timeout",
			"2026-06-01T10:00:12Z INFO  retrying connection (attempt 2)",
		},
	}
}

// ── Containers ──────────────────────────────────────────────────────────────

func (f *FakeBackend) ListContainers(showAll bool) ([]Container, error) {
	if showAll {
		return f.Containers, nil
	}
	out := make([]Container, 0, len(f.Containers))
	for _, c := range f.Containers {
		if c.State == "running" {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *FakeBackend) InspectContainer(id string) (*InspectResult, error) {
	for _, c := range f.Containers {
		if c.ID == id {
			return &InspectResult{Name: c.Name, RawYAML: fmt.Sprintf("Id: %s\nName: %s\nImage: %s\nState: %s\nStatus: %s\n", c.ID, c.Name, c.Image, c.State, c.Status)}, nil
		}
	}
	return nil, fmt.Errorf("no such container: %s", id)
}

// setContainerState mutates a demo container's state so lifecycle actions are
// observable in tests and the demo.
func (f *FakeBackend) setContainerState(id, state, status string) error {
	for i := range f.Containers {
		if f.Containers[i].ID == id {
			f.Containers[i].State = state
			f.Containers[i].Status = status
			return nil
		}
	}
	return fmt.Errorf("no such container: %s", id)
}

func (f *FakeBackend) StartContainer(id string) error {
	return f.setContainerState(id, "running", "Up 1 second")
}
func (f *FakeBackend) StopContainer(id string) error {
	return f.setContainerState(id, "exited", "Exited (0) 1 second ago")
}
func (f *FakeBackend) RestartContainer(id string) error {
	return f.setContainerState(id, "running", "Up 1 second")
}

func (f *FakeBackend) RemoveContainer(id string, force bool) error {
	for i, c := range f.Containers {
		if c.ID == id {
			if c.State == "running" && !force {
				return fmt.Errorf("контейнер запущен — выполните rm -f или сначала остановите его")
			}
			f.Containers = append(f.Containers[:i], f.Containers[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no such container: %s", id)
}

func (f *FakeBackend) KillContainer(id, signal string) error {
	return f.setContainerState(id, "exited", "Exited (137) 1 second ago")
}

// ContainerStats returns canned resource samples for the requested running
// containers (stopped ones are omitted, like the real daemon).
func (f *FakeBackend) ContainerStats(ids []string) (map[string]ContainerStats, error) {
	// Deterministic sample figures keyed by container ID so the demo and tests
	// show stable, recognisable values.
	samples := map[string]ContainerStats{
		"9ae942fd8fbc": {ID: "9ae942fd8fbc", CPUPerc: 2.5, MemUsage: 48 * 1024 * 1024, MemLimit: 512 * 1024 * 1024, MemPerc: 9.4, NetRx: 1024 * 1024, NetTx: 512 * 1024, BlockRead: 8 * 1024 * 1024, BlockWrite: 2 * 1024 * 1024},
		"d2c94e258dcb": {ID: "d2c94e258dcb", CPUPerc: 0.1, MemUsage: 6 * 1024 * 1024, MemLimit: 512 * 1024 * 1024, MemPerc: 1.2, NetRx: 2048, NetTx: 1024, BlockRead: 512 * 1024, BlockWrite: 0},
	}
	out := make(map[string]ContainerStats, len(ids))
	for _, id := range ids {
		if s, ok := samples[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

// RunContainer mimics `docker run -d`: the image must exist among the demo
// images, the name must be free; the new container appears as running.
func (f *FakeBackend) RunContainer(opts RunOptions) error {
	if strings.TrimSpace(opts.Image) == "" {
		return fmt.Errorf("image is required")
	}
	found := false
	for _, img := range f.Images {
		if img.ID == opts.Image || strings.Contains(img.Tags, opts.Image) {
			found = true
			break
		}
	}
	if !found {
		return friendlyRunErr(errors.New("Error response from daemon: No such image: " + opts.Image))
	}
	name := opts.Name
	if name == "" {
		name = "demo-" + fmt.Sprintf("%04x", len(f.Containers)+1)
	}
	for _, c := range f.Containers {
		if c.Name == name {
			return friendlyRunErr(fmt.Errorf("Error response from daemon: Conflict. The container name %q is already in use", name))
		}
	}
	f.Containers = append(f.Containers, Container{
		ID:      fmt.Sprintf("run%09x", len(f.Containers)+1),
		Name:    name,
		Image:   opts.Image,
		Status:  "Up 1 second",
		State:   "running",
		Ports:   strings.Join(opts.Ports, ", "),
		Created: time.Now(),
	})
	return nil
}

// fakeExecSession is a no-op ExecSession for demo mode and tests: it can't open
// a real TTY, so it prints a banner and echoes typed input back, letting the
// embedded terminal be exercised without a daemon.
type fakeExecSession struct {
	ch     chan []byte
	closed chan struct{}
	once   sync.Once
	rem    []byte // bytes left over from a Read that didn't fit the caller's buffer
}

func (f *FakeBackend) ExecInteractive(containerID string, cmd []string) (ExecSession, error) {
	cmd = execArgv(cmd)
	s := &fakeExecSession{ch: make(chan []byte, 64), closed: make(chan struct{})}
	banner := "demo shell — exec [" + strings.Join(cmd, " ") + "] in " + containerID + "\r\n" +
		"fake session: typed input is echoed, type 'exit' is not handled.\r\n/ # "
	s.ch <- []byte(banner) // buffered: never blocks
	return s, nil
}

// RunInteractive mimics `docker run --rm -it`: the image must exist among the
// demo images; the session is the same echoing fake terminal as exec.
func (f *FakeBackend) RunInteractive(opts ExecRunOptions) (ExecSession, error) {
	if strings.TrimSpace(opts.Image) == "" {
		return nil, fmt.Errorf("image is required")
	}
	found := false
	for _, img := range f.Images {
		if img.ID == opts.Image || strings.Contains(img.Tags, opts.Image) {
			found = true
			break
		}
	}
	if !found {
		return nil, friendlyRunErr(errors.New("Error response from daemon: No such image: " + opts.Image))
	}
	cmd := execArgv(opts.Cmd)
	s := &fakeExecSession{ch: make(chan []byte, 64), closed: make(chan struct{})}
	banner := "demo one-off — run [" + strings.Join(cmd, " ") + "] from " + opts.Image
	if len(opts.Volumes) > 0 {
		banner += " volumes [" + strings.Join(opts.Volumes, " ") + "]"
	}
	banner += "\r\nfake session: container is removed when the panel closes.\r\n/ # "
	s.ch <- []byte(banner) // buffered: never blocks
	return s, nil
}

func (s *fakeExecSession) Read(p []byte) (int, error) {
	if len(s.rem) > 0 {
		n := copy(p, s.rem)
		s.rem = s.rem[n:]
		return n, nil
	}
	select {
	case b := <-s.ch:
		n := copy(p, b)
		if n < len(b) {
			s.rem = append(s.rem, b[n:]...)
		}
		return n, nil
	case <-s.closed:
		return 0, io.EOF
	}
}

func (s *fakeExecSession) Write(p []byte) (int, error) {
	// Echo input back, expanding CR into a CRLF + fresh prompt so the demo line
	// advances visibly.
	out := make([]byte, 0, len(p))
	for _, b := range p {
		if b == '\r' {
			out = append(out, '\r', '\n', '/', ' ', '#', ' ')
			continue
		}
		out = append(out, b)
	}
	select {
	case s.ch <- out:
	case <-s.closed:
		return 0, io.ErrClosedPipe
	default: // best-effort: drop the echo if the buffer is full
	}
	return len(p), nil
}

func (s *fakeExecSession) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *fakeExecSession) Resize(int, int) error { return nil }

// fakeFS is the canned container filesystem served by the demo backend, keyed
// by clean POSIX path. It lets the file browser be exercised (and tested)
// without a daemon.
var fakeFS = map[string][]FileEntry{
	"/":         {{Name: "app", IsDir: true}, {Name: "bin", IsDir: true}, {Name: "etc", IsDir: true}, {Name: "var", IsDir: true}, {Name: "hello.txt", IsDir: false}},
	"/app":      {{Name: "data", IsDir: true}, {Name: "config.yaml", IsDir: false}, {Name: "main.go", IsDir: false}},
	"/app/data": {{Name: "seed.sql", IsDir: false}},
	"/bin":      {{Name: "sh", IsDir: false}, {Name: "ls", IsDir: false}},
	"/etc":      {{Name: "hostname", IsDir: false}, {Name: "hosts", IsDir: false}, {Name: "passwd", IsDir: false}},
	"/var":      {{Name: "log", IsDir: true}},
	"/var/log":  {{Name: "app.log", IsDir: false}},
}

// ListPath serves the canned fakeFS, rejecting an unknown path like a real `ls`.
func (f *FakeBackend) ListPath(containerID, dir string) ([]FileEntry, error) {
	if dir == "" {
		dir = "/"
	}
	if dir != "/" {
		dir = strings.TrimRight(dir, "/")
	}
	entries, ok := fakeFS[dir]
	if !ok {
		return nil, fmt.Errorf("путь %s не найден", dir)
	}
	return entries, nil
}

// CopyFromContainer mimics `docker cp` download by writing a placeholder file
// named after srcPath's base into destDir, so the saved path actually exists.
func (f *FakeBackend) CopyFromContainer(containerID, srcPath, destDir string) error {
	if destDir == "" {
		destDir = "."
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	base := srcPath
	if i := strings.LastIndex(strings.TrimRight(srcPath, "/"), "/"); i >= 0 {
		base = strings.TrimRight(srcPath, "/")[i+1:]
	}
	if base == "" {
		base = "download"
	}
	return os.WriteFile(filepath.Join(destDir, base), []byte("demo copy of "+srcPath+"\n"), 0o644)
}

// CopyToContainer mimics `docker cp` upload: it only validates the local path
// exists (nothing is stored), so the success/error paths are observable.
func (f *FakeBackend) CopyToContainer(containerID, localPath, destDir string) error {
	if _, err := os.Stat(localPath); err != nil {
		return fmt.Errorf("открыть %s: %w", localPath, err)
	}
	return nil
}

func (f *FakeBackend) ContainerLogs(id string, opts LogOptions) (<-chan string, func(), error) {
	ch := make(chan string, len(f.LogLines))
	for _, l := range f.LogLines {
		ch <- l
	}
	close(ch)
	return ch, func() {}, nil
}

// ── Images ──────────────────────────────────────────────────────────────────

func (f *FakeBackend) ListImages() ([]Image, error) { return f.Images, nil }

func (f *FakeBackend) InspectImage(id string) (*InspectResult, error) {
	for _, img := range f.Images {
		if img.ID == id {
			return &InspectResult{Name: img.Tags, RawYAML: fmt.Sprintf("Id: %s\nRepoTags: %s\nSize: %s\n", img.ID, img.Tags, img.Size)}, nil
		}
	}
	return nil, fmt.Errorf("no such image: %s", id)
}

func (f *FakeBackend) RemoveImage(id string, force bool) error {
	for i, img := range f.Images {
		if img.ID == id || strings.Contains(img.Tags, id) {
			// Reproduce real daemon conflicts verbatim so the UI exercises the
			// same error-translation path; the capitalised "Error response from
			// daemon" prefix mirrors Docker's actual output.
			if strings.Contains(img.Tags, "hello-world") && !force {
				//lint:ignore ST1005 mimics verbatim Docker daemon output
				return friendlyImageRemoveErr(errors.New(`Error response from daemon: conflict: unable to remove repository reference "hello-world:latest" (must force) - container 9ae942fd8fbc is using its referenced image d2c94e258dcb`))
			}
			if img.Tags == "nginx:1.25" && !force {
				//lint:ignore ST1005 mimics verbatim Docker daemon output
				return friendlyImageRemoveErr(errors.New("Error response from daemon: conflict: unable to delete a08c488a9779 (cannot be forced) - image has dependent child"))
			}
			f.Images = append(f.Images[:i], f.Images[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no such image: %s", id)
}

func (f *FakeBackend) PullImage(ref string) error { return nil }

// TagImage records a new tag pointing at the source image, so it shows up as a
// separate row in the image list (like the real `docker tag`).
func (f *FakeBackend) TagImage(source, target string) error {
	for _, img := range f.Images {
		if img.ID == source || strings.Contains(img.Tags, source) {
			f.Images = append(f.Images, Image{ID: img.ID, Tags: target, Size: img.Size, Created: img.Created})
			return nil
		}
	}
	return fmt.Errorf("no such image: %s", source)
}

// PushImage streams canned push progress for the demo. It echoes the registry
// and whether credentials were supplied so the auth path is observable.
func (f *FakeBackend) PushImage(ref string, auth RegistryAuth) (<-chan string, func(), error) {
	who := "anonymous"
	if auth.Username != "" {
		who = auth.Username
	}
	reg := auth.Registry
	if reg == "" {
		reg = "docker.io"
	}
	ch, stop := fakeProgress([]string{
		"The push refers to repository [" + ref + "]",
		"auth: " + who + "@" + reg,
		"5f70bf18a086: Pushed",
		"a08c488a9779: Pushed",
		"latest: digest: sha256:demo size: 1234",
	})
	return ch, stop, nil
}

// BuildImage appends a freshly "built" image and streams canned build output.
func (f *FakeBackend) BuildImage(contextDir, tag string) (<-chan string, func(), error) {
	if tag == "" {
		tag = "<none>:<none>"
	}
	f.Images = append(f.Images, Image{ID: "b00b1e5fa11d", Tags: tag, Size: "42 MB", Created: time.Now()})
	ch, stop := fakeProgress([]string{
		"Step 1/3 : FROM alpine:3.20",
		" ---> deadbeef0001",
		"Step 2/3 : COPY . /app",
		" ---> beefdead0002",
		"Step 3/3 : CMD [\"/app/run\"]",
		" ---> Running in cafe12345678",
		"Successfully built b00b1e5fa11d",
		"Successfully tagged " + tag,
	})
	return ch, stop, nil
}

// ImageHistory returns a canned layer history for the demo.
func (f *FakeBackend) ImageHistory(id string) (*InspectResult, error) {
	for _, img := range f.Images {
		if img.ID == id || strings.Contains(img.Tags, id) {
			content := "2026-06-01 10:00       42 MB  CMD [\"/app/run\"]\n" +
				"2026-06-01 10:00      5.0 MB  COPY . /app\n" +
				"2026-05-20 08:30      7.8 MB  FROM alpine:3.20\n"
			return &InspectResult{Name: img.Tags + " · history", RawYAML: content}, nil
		}
	}
	return nil, fmt.Errorf("no such image: %s", id)
}

func (f *FakeBackend) PruneImages() (int, error) {
	removed := 0
	kept := f.Images[:0]
	for _, img := range f.Images {
		if strings.Contains(img.Tags, "<none>") {
			removed++
			continue
		}
		kept = append(kept, img)
	}
	f.Images = kept
	return removed, nil
}

// ── Networks ──────────────────────────────────────────────────────────────

func (f *FakeBackend) ListNetworks() ([]Network, error) { return f.Networks, nil }

func (f *FakeBackend) InspectNetwork(id string) (*InspectResult, error) {
	for _, n := range f.Networks {
		if n.ID == id {
			return &InspectResult{Name: n.Name, RawYAML: fmt.Sprintf("Id: %s\nName: %s\nDriver: %s\nScope: %s\n", n.ID, n.Name, n.Driver, n.Scope)}, nil
		}
	}
	return nil, fmt.Errorf("no such network: %s", id)
}

func (f *FakeBackend) RemoveNetwork(id string) error {
	for i, n := range f.Networks {
		if n.ID == id {
			if n.Name == "bridge" || n.Name == "host" {
				return fmt.Errorf("встроенную сеть %q удалить нельзя", n.Name)
			}
			f.Networks = append(f.Networks[:i], f.Networks[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no such network: %s", id)
}

// CreateNetwork appends a new user-defined network so it shows up in the list,
// rejecting a duplicate name like the real daemon. A blank driver defaults to
// "bridge".
func (f *FakeBackend) CreateNetwork(opts NetworkCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("network name is required")
	}
	for _, n := range f.Networks {
		if n.Name == opts.Name {
			return fmt.Errorf("network with name %s already exists", opts.Name)
		}
	}
	driver := opts.Driver
	if driver == "" {
		driver = "bridge"
	}
	f.Networks = append(f.Networks, Network{
		ID:     fmt.Sprintf("fake%08x", len(f.Networks)+1),
		Name:   opts.Name,
		Driver: driver,
		Scope:  "local",
		Subnet: opts.Subnet,
	})
	return nil
}

// ── Volumes ───────────────────────────────────────────────────────────────

func (f *FakeBackend) ListVolumes() ([]Volume, error) { return f.Volumes, nil }

func (f *FakeBackend) InspectVolume(name string) (*InspectResult, error) {
	for _, v := range f.Volumes {
		if v.Name == name {
			return &InspectResult{Name: v.Name, RawYAML: fmt.Sprintf("Name: %s\nDriver: %s\nMountpoint: %s\n", v.Name, v.Driver, v.Mountpoint)}, nil
		}
	}
	return nil, fmt.Errorf("no such volume: %s", name)
}

func (f *FakeBackend) RemoveVolume(name string) error {
	for i, v := range f.Volumes {
		if v.Name == name {
			f.Volumes = append(f.Volumes[:i], f.Volumes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no such volume: %s", name)
}

// CreateVolume appends a new named volume, rejecting a duplicate name like the
// real daemon. A blank driver defaults to "local".
func (f *FakeBackend) CreateVolume(opts VolumeCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("volume name is required")
	}
	for _, v := range f.Volumes {
		if v.Name == opts.Name {
			return fmt.Errorf("volume with name %s already exists", opts.Name)
		}
	}
	driver := opts.Driver
	if driver == "" {
		driver = "local"
	}
	f.Volumes = append(f.Volumes, Volume{
		Name:       opts.Name,
		Driver:     driver,
		Mountpoint: "/var/lib/docker/volumes/" + opts.Name + "/_data",
		Created:    time.Now().Format(time.RFC3339),
	})
	return nil
}

func (f *FakeBackend) PruneVolumes() (int, error) {
	n := len(f.Volumes)
	f.Volumes = nil
	return n, nil
}

// ── Compose ───────────────────────────────────────────────────────────────

func (f *FakeBackend) ListComposeProjects() ([]ComposeProject, error) { return f.Composes, nil }

// ListComposeContainers returns the demo containers as the project's containers.
func (f *FakeBackend) ListComposeContainers(project string) ([]Container, error) {
	for _, c := range f.Composes {
		if composeIdentity(c.Project, c.WorkingDir) == project {
			return f.Containers, nil
		}
	}
	return nil, fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) InspectComposeProject(project string) (*InspectResult, error) {
	for _, p := range f.Composes {
		if composeIdentity(p.Project, p.WorkingDir) == project {
			var b strings.Builder
			fmt.Fprintf(&b, "project: %s\nworking_dir: %s\nstatus: %s\nservices:\n", p.Name, p.WorkingDir, p.Status)
			for _, c := range f.Containers {
				fmt.Fprintf(&b, "  - name: %s\n    image: %s\n    state: %s\n    ports: %s\n", c.Name, c.Image, c.State, c.Ports)
			}
			return &InspectResult{Name: project, RawYAML: b.String()}, nil
		}
	}
	return nil, fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) ComposeLogs(project string, opts LogOptions) (<-chan string, func(), error) {
	for _, p := range f.Composes {
		if composeIdentity(p.Project, p.WorkingDir) == project {
			ch := make(chan string, len(f.LogLines))
			for _, l := range f.LogLines {
				ch <- "web | " + l
			}
			close(ch)
			return ch, func() {}, nil
		}
	}
	return nil, nil, fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) setComposeState(project, status string, running int) error {
	for i := range f.Composes {
		if composeIdentity(f.Composes[i].Project, f.Composes[i].WorkingDir) == project {
			f.Composes[i].Status = status
			f.Composes[i].Running = running
			return nil
		}
	}
	return fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) ComposeStart(p string) error {
	return f.setComposeState(p, "running", f.composeTotal(p))
}
func (f *FakeBackend) ComposeStop(p string) error { return f.setComposeState(p, "stopped", 0) }
func (f *FakeBackend) ComposeRestart(p string) error {
	return f.setComposeState(p, "running", f.composeTotal(p))
}
func (f *FakeBackend) ComposePause(p string) error { return f.setComposeState(p, "paused", 0) }
func (f *FakeBackend) ComposeUnpause(p string) error {
	return f.setComposeState(p, "running", f.composeTotal(p))
}

func (f *FakeBackend) ComposeRemove(project string) error {
	for i := range f.Composes {
		if composeIdentity(f.Composes[i].Project, f.Composes[i].WorkingDir) == project {
			f.Composes = append(f.Composes[:i], f.Composes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no such compose project: %s", project)
}

// fakeProgress returns a channel pre-loaded with the given lines and closed, so
// the UI's streaming consumer reads them all and then sees completion. The
// channel needs no producer goroutine, so the returned stop is a no-op — it
// exists only to honour the streaming contract shared with the real backend.
func fakeProgress(lines []string) (<-chan string, func()) {
	ch := make(chan string, len(lines))
	for _, l := range lines {
		ch <- l
	}
	close(ch)
	return ch, func() {}
}

// SupportsHostCompose mirrors the real backend: true unless NoHostCompose is set
// to simulate a tcp:// connection.
func (f *FakeBackend) SupportsHostCompose() bool { return !f.NoHostCompose }

func (f *FakeBackend) ComposeUp(project string) (<-chan string, func(), error) {
	if err := f.setComposeState(project, "running", f.composeTotal(project)); err != nil {
		return nil, nil, err
	}
	ch, stop := fakeProgress([]string{
		"[+] Running 2/2",
		" ⠿ Container " + project + "-web-1  Created",
		" ⠿ Container " + project + "-web-1  Started",
	})
	return ch, stop, nil
}

func (f *FakeBackend) ComposePull(project string) (<-chan string, func(), error) {
	for _, p := range f.Composes {
		if composeIdentity(p.Project, p.WorkingDir) == project {
			ch, stop := fakeProgress([]string{
				"[+] Pulling 1/1",
				" ⠿ web Pulling",
				" ⠿ nginx:1.25 Pull complete",
				" ⠿ web Pulled",
			})
			return ch, stop, nil
		}
	}
	return nil, nil, fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) ComposeDown(project string) (<-chan string, func(), error) {
	if err := f.setComposeState(project, "stopped", 0); err != nil {
		return nil, nil, err
	}
	ch, stop := fakeProgress([]string{
		"[+] Running 2/2",
		" ⠿ Container " + project + "-web-1  Stopping",
		" ⠿ Container " + project + "-web-1  Removed",
		" ⠿ Network " + project + "_default  Removed",
	})
	return ch, stop, nil
}

func (f *FakeBackend) ComposeConfig(project string) (string, error) {
	for _, p := range f.Composes {
		if composeIdentity(p.Project, p.WorkingDir) == project {
			return fmt.Sprintf("name: %s\nservices:\n  web:\n    image: nginx:1.25\n    ports:\n      - 8080:80\n", project), nil
		}
	}
	return "", fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) ReadComposeFile(project string) (string, string, error) {
	for _, p := range f.Composes {
		if composeIdentity(p.Project, p.WorkingDir) == project {
			content := f.ComposeFiles[project]
			if content == "" {
				content = "services: {}\n"
			}
			return p.ConfigFiles, content, nil
		}
	}
	return "", "", fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) WriteComposeFile(project, content string) error {
	for _, p := range f.Composes {
		if composeIdentity(p.Project, p.WorkingDir) == project {
			if f.ComposeFiles == nil {
				f.ComposeFiles = map[string]string{}
			}
			f.ComposeFiles[project] = content
			return nil
		}
	}
	return fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) CreateComposeFile(dir, content string) (<-chan string, func(), error) {
	dir = strings.TrimRight(strings.ReplaceAll(dir, "\\", "/"), "/")
	if dir == "" {
		return nil, nil, fmt.Errorf("a target directory is required")
	}
	name := dir
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		name = dir[i+1:]
	}
	if name == "" {
		name = "project"
	}
	f.Composes = append(f.Composes, ComposeProject{
		Project: name, Name: name, WorkingDir: dir, ConfigFiles: dir + "/docker-compose.yaml",
		Status: "running", Command: "docker compose up -d", Running: 1, Total: 1,
	})
	if f.ComposeFiles == nil {
		f.ComposeFiles = map[string]string{}
	}
	f.ComposeFiles[dir] = content
	ch, stop := fakeProgress([]string{
		"[+] Running 1/1",
		" ⠿ Network " + name + "_default  Created",
		" ⠿ Container " + name + "-app-1  Started",
	})
	return ch, stop, nil
}

func (f *FakeBackend) BackupComposeProject(project string) (string, error) {
	for _, p := range f.Composes {
		if composeIdentity(p.Project, p.WorkingDir) == project {
			path := backupFileName(composeDisplayName(p.Project, p.WorkingDir))
			// Demo: write a small placeholder so the saved path actually exists.
			if err := os.WriteFile(path, []byte("demo backup of "+p.Name+"\n"), 0o644); err != nil {
				return "", err
			}
			return path, nil
		}
	}
	return "", fmt.Errorf("no such compose project: %s", project)
}

func (f *FakeBackend) RestoreComposeProject(project, backupPath string) (<-chan string, func(), error) {
	if _, err := os.Stat(backupPath); err != nil {
		return nil, nil, fmt.Errorf("open backup: %w", err)
	}
	if err := f.setComposeState(project, "running", f.composeTotal(project)); err != nil {
		return nil, nil, err
	}
	ch, stop := fakeProgress([]string{
		"extracting " + backupPath + " into /srv/" + project,
		"starting project…",
		"[+] Running 1/1",
		" ⠿ Container " + project + "-web-1  Started",
	})
	return ch, stop, nil
}

func (f *FakeBackend) composeTotal(project string) int {
	for _, c := range f.Composes {
		if composeIdentity(c.Project, c.WorkingDir) == project {
			return c.Total
		}
	}
	return 0
}

// SystemDF returns a canned disk-usage report derived from the demo data.
func (f *FakeBackend) SystemDF() (*InspectResult, error) {
	running := 0
	for _, c := range f.Containers {
		if c.State == "running" {
			running++
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-15s %7s %8s %12s %14s\n", "TYPE", "TOTAL", "ACTIVE", "SIZE", "RECLAIMABLE")
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Images", len(f.Images), 2, "631.0 MB", "5.0 MB")
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Containers", len(f.Containers), running, "12.0 MB", "4.0 MB")
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Local Volumes", len(f.Volumes), 1, "256.0 MB", "32.0 MB")
	fmt.Fprintf(&sb, "%-15s %7d %8d %12s %14s\n", "Build Cache", 3, 0, "48.0 MB", "48.0 MB")
	return &InspectResult{Name: "system df", RawYAML: sb.String()}, nil
}

// SystemPrune mimics a full prune on the demo data: stopped containers and
// dangling images disappear, and a summary is reported.
func (f *FakeBackend) SystemPrune() (string, error) {
	var ctrs int
	kept := f.Containers[:0]
	for _, c := range f.Containers {
		if c.State == "running" {
			kept = append(kept, c)
		} else {
			ctrs++
		}
	}
	f.Containers = kept
	imgs, _ := f.PruneImages()
	summary := fmt.Sprintf("prune: контейнеров %d, сетей 0, образов %d, кэш 3 — освобождено 53.0 MB", ctrs, imgs)
	return summary, nil
}

// Ping always succeeds for the in-memory fake backend.
func (f *FakeBackend) Ping() error { return nil }

// Info returns a daemon summary derived from the in-memory demo data, so the
// multi-host dashboard can be exercised without a live host.
func (f *FakeBackend) Info() (HostSummary, error) {
	var running, paused, stopped int
	for _, c := range f.Containers {
		switch c.State {
		case "running":
			running++
		case "paused":
			paused++
		default:
			stopped++
		}
	}
	return HostSummary{
		Reachable:  true,
		Containers: len(f.Containers),
		Running:    running,
		Paused:     paused,
		Stopped:    stopped,
		Images:     len(f.Images),
		Version:    "27.4.0",
		Name:       "demo",
		NCPU:       4,
		MemTotal:   8 * 1024 * 1024 * 1024,
	}, nil
}

func (f *FakeBackend) Close() {}

// Events returns a fake event stream that emits a small set of deterministic
// demo events so the UI can exercise the events view without a daemon. The
// channel stays open (mimicking a live subscription) until stop closes it.
func (f *FakeBackend) Events() (<-chan string, func(), error) {
	lines := []string{
		"container create 9ae942fd8fbc (local)",
		"container start 9ae942fd8fbc (local)",
		"container die d2c94e258dcb (local)",
		"container health_status: healthy 9ae942fd8fbc (local)",
		"container oom 3f1ab77c9012 (local)",
	}
	ch := make(chan string, len(lines))
	for _, l := range lines {
		ch <- l
	}
	var once sync.Once
	stop := func() { once.Do(func() { close(ch) }) }
	return ch, stop, nil
}
