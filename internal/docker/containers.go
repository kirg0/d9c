package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/go-connections/nat"
	"gopkg.in/yaml.v3"
)

type Container struct {
	ID      string
	Name    string
	Image   string
	Status  string
	State   string
	Health  string // healthy | unhealthy | starting | "" (no healthcheck)
	Ports   string
	CPU     string
	Memory  string
	Created time.Time
	// Labels and Networks back the label:/network: filter predicates; they are
	// not shown in the table. Networks holds the attached network names.
	Labels   map[string]string
	Networks []string
}

// InspectResult is the generic detail-view payload for any Docker resource.
type InspectResult struct {
	Name    string
	RawYAML string
}

// LogOptions controls how logs are fetched. Tail <= 0 means "all"; Since/Until
// accept Docker's filter syntax (a duration like "1h"/"10m" or an RFC3339
// timestamp); empty values disable that bound.
type LogOptions struct {
	Tail  int
	Since string
	Until string
}

func (b *dockerBackend) ListContainers(showAll bool) ([]Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := container.ListOptions{All: showAll}
	if !showAll {
		opts.Filters = filters.NewArgs(filters.Arg("status", "running"))
	}

	list, err := b.cli.ContainerList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	result := make([]Container, 0, len(list))
	for _, c := range list {
		result = append(result, toContainer(c))
	}
	return result, nil
}

// toContainer maps a Docker API container summary to our Container type.
func toContainer(c types.Container) Container {
	name := ""
	if len(c.Names) > 0 {
		name = strings.TrimPrefix(c.Names[0], "/")
	}
	var networks []string
	if c.NetworkSettings != nil {
		networks = make([]string, 0, len(c.NetworkSettings.Networks))
		for net := range c.NetworkSettings.Networks {
			networks = append(networks, net)
		}
	}
	return Container{
		ID:       shortID(c.ID),
		Name:     name,
		Image:    c.Image,
		Status:   c.Status,
		State:    c.State,
		Health:   parseHealth(c.Status),
		Ports:    formatPorts(c.Ports),
		Created:  time.Unix(c.Created, 0),
		Labels:   c.Labels,
		Networks: networks,
	}
}

// parseHealth extracts the healthcheck verdict the daemon embeds in a
// container's status line — "Up 2 hours (healthy)", "(unhealthy)" or
// "(health: starting)". Containers without a healthcheck yield "".
func parseHealth(status string) string {
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "(health: starting)"):
		return "starting"
	default:
		return ""
	}
}

func (b *dockerBackend) InspectContainer(id string) (*InspectResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := b.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	rawYAML := ""
	if y, err := yaml.Marshal(info); err == nil {
		rawYAML = string(y)
	}
	name := strings.TrimPrefix(info.Name, "/")
	return &InspectResult{Name: name, RawYAML: rawYAML}, nil
}

func (b *dockerBackend) StartContainer(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return b.cli.ContainerStart(ctx, id, container.StartOptions{})
}

func (b *dockerBackend) StopContainer(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	timeout := 10
	return b.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout})
}

func (b *dockerBackend) RestartContainer(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	timeout := 10
	return b.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeout})
}

func (b *dockerBackend) RemoveContainer(id string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return b.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: force})
}

func (b *dockerBackend) KillContainer(id string, signal string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if signal == "" {
		signal = "SIGKILL"
	}
	return b.cli.ContainerKill(ctx, id, signal)
}

// RunOptions describes a container to create and start (the `run` wizard).
// Image is required; everything else is optional. Ports take `docker run -p`
// specs ("8080:80", "127.0.0.1:9443:443/udp"), Env takes KEY=VALUE pairs and
// Volumes takes bind specs ("/host:/ctr", "named-vol:/data[:ro]").
type RunOptions struct {
	Image   string
	Name    string
	Ports   []string
	Env     []string
	Volumes []string
}

// RunContainer creates and starts a container from opts — the API-side
// equivalent of `docker run -d`. If the image is missing on the host it is
// pulled automatically (like `docker run`) and the create is retried once.
func (b *dockerBackend) RunContainer(opts RunOptions) error {
	if strings.TrimSpace(opts.Image) == "" {
		return fmt.Errorf("image is required")
	}
	exposed, bindings, err := nat.ParsePortSpecs(opts.Ports)
	if err != nil {
		return fmt.Errorf("invalid port spec: %w", err)
	}

	cfg := &container.Config{
		Image:        opts.Image,
		Env:          opts.Env,
		ExposedPorts: exposed,
	}
	hostCfg := &container.HostConfig{
		PortBindings: bindings,
		Binds:        opts.Volumes,
	}

	created, err := b.createContainer(cfg, hostCfg, opts.Name)
	if err != nil {
		// The image is not on the host: pull it (like `docker run` does) and
		// retry the create once before giving up.
		if isNoSuchImageErr(err) {
			if perr := b.PullImage(opts.Image); perr != nil {
				return fmt.Errorf("скачивание образа не удалось: %w", perr)
			}
			created, err = b.createContainer(cfg, hostCfg, opts.Name)
		}
		if err != nil {
			return friendlyRunErr(err)
		}
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.cli.ContainerStart(startCtx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// createContainer is the single create step shared by the initial attempt and
// the post-pull retry in RunContainer.
func (b *dockerBackend) createContainer(cfg *container.Config, hostCfg *container.HostConfig, name string) (container.CreateResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return b.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
}

// isNoSuchImageErr reports whether the daemon rejected a create because the
// image is absent on the host.
func isNoSuchImageErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such image")
}

// friendlyRunErr rewrites the daemon's terse create-conflicts into actionable
// hints. Other errors pass through wrapped.
func friendlyRunErr(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no such image"):
		return fmt.Errorf("образ не найден на хосте — скачайте его сначала (:pull в разделе Images)")
	case strings.Contains(msg, "is already in use"):
		return fmt.Errorf("имя контейнера занято — выберите другое или удалите старый контейнер")
	case strings.Contains(msg, "invalid containerport"), strings.Contains(msg, "invalid port"):
		return fmt.Errorf("неверный формат порта — используйте host:container, например 8080:80")
	}
	return fmt.Errorf("create container: %w", err)
}

func (b *dockerBackend) ContainerLogs(id string, opts LogOptions) (<-chan string, func(), error) {
	tailStr := "all"
	if opts.Tail > 0 {
		tailStr = fmt.Sprintf("%d", opts.Tail)
	}

	// TTY containers write a plain byte stream; only non-TTY logs carry the
	// 8-byte multiplex headers that stripDockerHeader removes. Stripping a TTY
	// stream would eat real log bytes, so check the container's mode first.
	tty := false
	inspectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if info, err := b.cli.ContainerInspect(inspectCtx, id); err == nil && info.Config != nil {
		tty = info.Config.Tty
	}
	cancel()

	reader, err := b.cli.ContainerLogs(context.Background(), id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       tailStr,
		Timestamps: true,
		Since:      opts.Since,
		Until:      opts.Until,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("container logs: %w", err)
	}

	var src io.Reader = reader
	if !tty {
		src = stripDockerHeader(reader)
	}

	ch := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	// stop aborts the stream: closing the reader ends the Follow connection and
	// the done channel unblocks a producer stuck on a send nobody reads.
	stop := func() {
		once.Do(func() {
			close(done)
			_ = reader.Close()
		})
	}
	go func() {
		defer close(ch)
		defer stop() // release the connection when the stream ends naturally
		sc := bufio.NewScanner(src)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case ch <- sc.Text():
			case <-done:
				return
			}
		}
		// Surface read errors (e.g. a line above the buffer cap) instead of
		// silently ending the stream; a deliberate stop lands in <-done.
		if err := sc.Err(); err != nil {
			select {
			case ch <- "error: " + err.Error():
			case <-done:
			}
		}
	}()

	return ch, stop, nil
}

func stripDockerHeader(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		header := make([]byte, 8)
		for {
			if _, err := io.ReadFull(r, header); err != nil {
				pw.CloseWithError(err)
				return
			}
			size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
			if _, err := io.CopyN(pw, r, int64(size)); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()
	return pr
}

func formatPorts(ports []types.Port) string {
	seen := map[string]bool{}
	parts := []string{}
	for _, p := range ports {
		var s string
		if p.PublicPort != 0 {
			s = fmt.Sprintf("%d->%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		} else {
			s = fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		}
		if !seen[s] {
			seen[s] = true
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}
