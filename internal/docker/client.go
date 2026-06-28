package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"d9c/internal/config"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"golang.org/x/crypto/ssh"
)

// Backend abstracts all Docker operations for easy mocking and future Podman support.
type Backend interface {
	// Containers
	ListContainers(showAll bool) ([]Container, error)
	InspectContainer(id string) (*InspectResult, error)
	StartContainer(id string) error
	StopContainer(id string) error
	RestartContainer(id string) error
	RemoveContainer(id string, force bool) error
	KillContainer(id string, signal string) error
	// ContainerLogs streams log lines until stop is called or the container
	// stops producing. The caller MUST call stop when it abandons the channel,
	// otherwise the Follow connection and producer goroutine leak.
	ContainerLogs(id string, opts LogOptions) (lines <-chan string, stop func(), err error)
	ContainerStats(ids []string) (map[string]ContainerStats, error)
	ExecInteractive(containerID string, cmd []string) (ExecSession, error)
	RunContainer(opts RunOptions) error
	// RunInteractive starts a disposable interactive container from an image
	// (`docker run --rm -it` analogue); closing the session removes it.
	RunInteractive(opts ExecRunOptions) (ExecSession, error)
	// Container filesystem (`docker cp` / browse). ListPath lists a directory
	// inside the container; CopyFromContainer downloads a path into a local
	// directory; CopyToContainer uploads a local path into a container directory.
	ListPath(containerID, dir string) ([]FileEntry, error)
	CopyFromContainer(containerID, srcPath, destDir string) error
	CopyToContainer(containerID, localPath, destDir string) error
	// Images
	ListImages() ([]Image, error)
	InspectImage(id string) (*InspectResult, error)
	RemoveImage(id string, force bool) error
	// Networks
	ListNetworks() ([]Network, error)
	InspectNetwork(id string) (*InspectResult, error)
	RemoveNetwork(id string) error
	CreateNetwork(opts NetworkCreateOptions) error
	// Volumes
	ListVolumes() ([]Volume, error)
	InspectVolume(name string) (*InspectResult, error)
	RemoveVolume(name string) error
	CreateVolume(opts VolumeCreateOptions) error
	PruneVolumes() (int, error)
	// Extra image ops
	PullImage(ref string) error
	PruneImages() (int, error)
	TagImage(source, target string) error
	// PushImage / BuildImage stream daemon progress lines. The returned stop
	// aborts the operation and releases the request/connection; the caller MUST
	// call it when it abandons the channel, otherwise the producer leaks.
	PushImage(ref string, auth RegistryAuth) (lines <-chan string, stop func(), err error)
	BuildImage(contextDir, tag string) (lines <-chan string, stop func(), err error)
	ImageHistory(id string) (*InspectResult, error)
	// Docker Compose projects (discovered via container labels)
	ListComposeProjects() ([]ComposeProject, error)
	ListComposeContainers(project string) ([]Container, error)
	InspectComposeProject(project string) (*InspectResult, error)
	// ComposeLogs streams the aggregated project logs; same stop contract as
	// ContainerLogs.
	ComposeLogs(project string, opts LogOptions) (lines <-chan string, stop func(), err error)
	ComposeStart(project string) error
	ComposeStop(project string) error
	ComposeRestart(project string) error
	ComposePause(project string) error
	ComposeUnpause(project string) error
	ComposeRemove(project string) error
	// ComposeUp/Pull/Down, CreateComposeFile and RestoreComposeProject stream
	// progress lines from the compose engine. Each returns a stop the caller MUST
	// call when it abandons the channel, otherwise the SSH session/producer leak.
	ComposeUp(project string) (lines <-chan string, stop func(), err error)
	ComposePull(project string) (lines <-chan string, stop func(), err error)
	ComposeDown(project string) (lines <-chan string, stop func(), err error)
	ComposeConfig(project string) (string, error)
	ReadComposeFile(project string) (path, content string, err error)
	WriteComposeFile(project, content string) error
	CreateComposeFile(dir, content string) (lines <-chan string, stop func(), err error)
	BackupComposeProject(project string) (string, error)
	RestoreComposeProject(project, backupPath string) (lines <-chan string, stop func(), err error)
	// SupportsHostCompose reports whether the backend can run the compose
	// operations that need shell/filesystem access to the host —
	// up/down/pull/config/edit/create/backup/restore. These require an SSH
	// connection; a tcp:// connection returns false. Discovery and the
	// container-level lifecycle ops (start/stop/restart/…) work regardless.
	SupportsHostCompose() bool
	// System-wide operations
	// SystemDF reports the daemon's disk usage (`docker system df`).
	SystemDF() (*InspectResult, error)
	// SystemPrune removes stopped containers, unused networks, dangling images
	// and the build cache; it returns a human-readable summary.
	SystemPrune() (string, error)
	// Events returns a live stream of Docker daemon events as formatted strings.
	// stop ends the subscription and closes the channel; the caller MUST call it
	// when it abandons the stream (closing the view, refreshing).
	Events() (lines <-chan string, stop func(), err error)
	// Ping checks the connection to the daemon is alive (used by auto-reconnect).
	Ping() error
	// Info returns a one-shot daemon summary (container/image counts, version) —
	// the data behind the multi-host dashboard. Reachable is left to the caller.
	Info() (HostSummary, error)
	Close()
}

type dockerBackend struct {
	cli     *client.Client
	closeFn func()
	// sshClient is non-nil for ssh:// connections; used to exec `docker compose`
	// on the host for compose-engine operations (up/pull). nil for tcp://.
	sshClient *ssh.Client

	// statsPrev caches the last CPU counters per container so the one-shot
	// stats endpoint (which carries no precpu data) can report CPU% as the
	// delta between refresh ticks. Guarded by statsMu; see ContainerStats.
	statsMu   sync.Mutex
	statsPrev map[string]cpuSample
}

// New creates a Backend from the provided config.
// Supports tcp://, unix:// and ssh:// schemes in cfg.Host.
func New(cfg *config.Config) (Backend, error) {
	host := cfg.Host

	if strings.HasPrefix(host, "ssh://") {
		return newSSHBackend(cfg)
	}
	if path, ok := unixSocketPath(host); ok {
		// Validate the socket path up front: the Docker client builds lazily and
		// never dials here, so an obviously wrong unix://… would otherwise surface
		// only as an opaque dial failure (or a pointless auto-reconnect loop).
		if err := validateUnixSocket(path); err != nil {
			return nil, err
		}
	}
	return newTCPBackend(cfg)
}

// ErrSocketPath is the sentinel wrapped by every unix:// socket-path validation
// failure; IsSocketError detects it to show a dedicated dialog.
var ErrSocketPath = errors.New("invalid docker socket")

// unixSocketPath extracts the filesystem path from a unix:// Docker host,
// reporting false for any other scheme.
func unixSocketPath(host string) (string, bool) {
	if !strings.HasPrefix(host, "unix://") {
		return "", false
	}
	return strings.TrimPrefix(host, "unix://"), true
}

// validateUnixSocket checks that path points to an existing unix domain socket
// before the Docker client is built, so a mistyped or missing socket yields a
// clear, actionable error instead of a confusing dial failure. The socket-type
// check is skipped on Windows, where os.Stat does not report AF_UNIX files as
// sockets. Every failure wraps ErrSocketPath (see IsSocketError).
func validateUnixSocket(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: путь до сокета пуст", ErrSocketPath)
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: файл сокета не найден: %s", ErrSocketPath, path)
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrSocketPath, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%w: %s — это каталог, а не сокет", ErrSocketPath, path)
	}
	if runtime.GOOS != "windows" && info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%w: %s — не unix-сокет", ErrSocketPath, path)
	}
	return nil
}

// IsSocketError reports whether err is a unix:// socket-path validation failure
// (missing file, a directory, not a socket, or an empty path). The UI uses it to
// show a dedicated dialog instead of dumping the raw error into the footer.
func IsSocketError(err error) bool {
	return errors.Is(err, ErrSocketPath)
}

func newTCPBackend(cfg *config.Config) (Backend, error) {
	opts := []client.Opt{
		client.WithHost(cfg.Host),
		client.WithAPIVersionNegotiation(),
	}

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		opts = append(opts, client.WithTLSClientConfig(cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker TCP client: %w", err)
	}
	return &dockerBackend{cli: cli, closeFn: func() { _ = cli.Close() }}, nil
}

func newSSHBackend(cfg *config.Config) (Backend, error) {
	dialer, sshClient, closeTunnel, err := sshDialer(cfg.Host, cfg.SSHKeyFile, cfg.SSHPassword)
	if err != nil {
		return nil, fmt.Errorf("SSH tunnel: %w", err)
	}

	// Use tcp://localhost so the Docker client accepts the scheme on all OSes.
	// The actual connection is intercepted by our DialContext (dial-stdio over SSH).
	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://localhost"),
		client.WithDialContext(dialer),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		closeTunnel()
		return nil, fmt.Errorf("docker SSH client: %w", err)
	}

	return &dockerBackend{
		cli:       cli,
		sshClient: sshClient,
		closeFn:   func() { _ = cli.Close(); closeTunnel() },
	}, nil
}

func (b *dockerBackend) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := b.cli.Ping(ctx)
	return err
}

func (b *dockerBackend) Close() {
	b.closeFn()
}

// Events opens a stream of Docker daemon events and formats them into human-
// readable lines. The returned stop cancels the subscription and closes the
// channel; every send also watches the context so an abandoned stream can't
// block the producer forever.
func (b *dockerBackend) Events() (<-chan string, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	msgCh, errCh := b.cli.Events(ctx, events.ListOptions{})

	ch := make(chan string, 256)
	send := func(line string) bool {
		select {
		case ch <- line:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(ch)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgCh:
				if !ok || !send(formatEvent(msg)) {
					return
				}
			case err, ok := <-errCh:
				if !ok || !send("[error] "+err.Error()) {
					return
				}
			}
		}
	}()

	return ch, cancel, nil
}

// formatEvent renders a single Docker events.Message as a one-line string.
func formatEvent(msg events.Message) string {
	actor := shortID(msg.Actor.ID)
	attrs := msg.Actor.Attributes
	if name, ok := attrs["name"]; ok {
		actor = name
	}
	if container, ok := attrs["container"]; ok {
		actor = shortID(container)
	}
	return fmt.Sprintf("%s %s %s (%s)", msg.Type, msg.Action, actor, msg.Scope)
}

// shortID truncates a full Docker object ID to the familiar 12-character form;
// shorter strings (names, already-short IDs) pass through unchanged.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// IsHostKeyError reports whether err is the SSH known_hosts verification
// failure raised when the remote host key is missing from ~/.ssh/known_hosts or
// (more commonly) when the host's key has changed since it was recorded — the
// typical case after a host is re-provisioned. The UI uses it to show a
// dedicated dialog instead of dumping the raw "knownhosts: key" string into
// the footer.
func IsHostKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "knownhosts:") || strings.Contains(msg, "ssh: host key")
}

// IsHostNotFoundError reports whether err is a DNS resolution failure raised
// when the target host name cannot be resolved — the typical case when the
// host address is mistyped or the host simply does not exist. Go's net package
// renders this as "...: no such host". The UI uses it to show a dedicated
// dialog (the same modal as the host-key notice) instead of dumping the raw
// "lookup ...: no such host" string into the footer.
func IsHostNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no such host")
}

// IsConnectionError reports whether err looks like a lost/unreachable daemon
// connection (TCP socket down or SSH tunnel broken), as opposed to a normal
// operational error like "no such container". The auto-reconnect logic uses it
// to decide when to start retrying.
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if client.IsErrConnectionFailed(err) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	for _, s := range []string{
		"connection refused", "connection reset", "broken pipe",
		"no route to host", "i/o timeout", "EOF",
		"use of closed network connection", "ssh:", "tunnel",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
