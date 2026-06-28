package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"d9c/internal/i18n"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	dockervolume "github.com/docker/docker/api/types/volume"
	"gopkg.in/yaml.v3"
)

// ── Image ─────────────────────────────────────────────────────────────────────

type Image struct {
	ID      string
	Tags    string
	Size    string
	Created time.Time
}

func (b *dockerBackend) ListImages() ([]Image, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := b.cli.ImageList(ctx, dockerimage.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}

	result := make([]Image, 0, len(list))
	for _, img := range list {
		tags := "<none>"
		if len(img.RepoTags) > 0 {
			tags = strings.Join(img.RepoTags, ", ")
		}
		id := img.ID
		if strings.HasPrefix(id, "sha256:") && len(id) > 19 {
			id = id[7:19]
		}
		result = append(result, Image{
			ID:      id,
			Tags:    tags,
			Size:    formatBytes(img.Size),
			Created: time.Unix(img.Created, 0),
		})
	}
	return result, nil
}

func (b *dockerBackend) InspectImage(id string) (*InspectResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, _, err := b.cli.ImageInspectWithRaw(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}
	rawYAML := ""
	if y, err := yaml.Marshal(info); err == nil {
		rawYAML = string(y)
	}
	name := id
	if len(info.RepoTags) > 0 {
		name = info.RepoTags[0]
	}
	return &InspectResult{Name: name, RawYAML: rawYAML}, nil
}

func (b *dockerBackend) RemoveImage(id string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := b.cli.ImageRemove(ctx, id, dockerimage.RemoveOptions{Force: force})
	return friendlyImageRemoveErr(err)
}

// friendlyImageRemoveErr rewrites Docker's terse "image has dependent child"
// conflict into an actionable hint pointing at prune. Other errors pass through.
func friendlyImageRemoveErr(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "dependent child"):
		return errors.New(i18n.T("у образа есть зависимые образы — удалите их сначала или выполните prune", "the image has dependent images — remove them first or run prune"))
	case strings.Contains(msg, "must force"):
		return errors.New(i18n.T("образ используется контейнером — выполните rm -f или сначала удалите контейнер", "the image is used by a container — run rm -f or remove the container first"))
	}
	return err
}

func (b *dockerBackend) PullImage(ref string) error {
	reader, err := b.cli.ImagePull(context.Background(), ref, dockerimage.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image: %w", err)
	}
	defer func() { _ = reader.Close() }()
	_, err = io.Copy(io.Discard, reader) // drain progress stream
	return err
}

func (b *dockerBackend) PruneImages() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := b.cli.ImagesPrune(ctx, filters.Args{})
	if err != nil {
		return 0, fmt.Errorf("prune images: %w", err)
	}
	return len(report.ImagesDeleted), nil
}

// TagImage adds the target reference to an existing image (like `docker tag`).
func (b *dockerBackend) TagImage(source, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.cli.ImageTag(ctx, source, target); err != nil {
		return fmt.Errorf("tag image: %w", err)
	}
	return nil
}

// RegistryAuth holds optional credentials for pushing to a private registry.
// A zero value (empty Username) means an anonymous push, which works for local
// or insecure registries that don't require a login.
type RegistryAuth struct {
	Registry string // server address, e.g. "myregistry:5000"; empty = default
	Username string
	Password string
}

// RegistryFromRef extracts the registry host from an image reference, or ""
// when the reference targets Docker Hub (no explicit registry). The first
// path segment is a registry only when it looks like a host (contains "." or
// ":", or is "localhost").
func RegistryFromRef(ref string) string {
	first, _, ok := strings.Cut(ref, "/")
	if !ok {
		return ""
	}
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return ""
}

// PushImage pushes a tagged image to its registry, streaming progress lines.
// When auth carries a username the credentials are sent to the registry;
// otherwise the push is anonymous and a private registry will answer 401,
// surfaced as an error line in the stream.
func (b *dockerBackend) PushImage(ref string, auth RegistryAuth) (<-chan string, func(), error) {
	encoded, err := registry.EncodeAuthConfig(registry.AuthConfig{
		Username:      auth.Username,
		Password:      auth.Password,
		ServerAddress: auth.Registry,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("encode registry auth: %w", err)
	}
	reader, err := b.cli.ImagePush(context.Background(), ref, dockerimage.PushOptions{RegistryAuth: encoded})
	if err != nil {
		return nil, nil, fmt.Errorf("push image: %w", err)
	}
	ch, stop := streamDockerJSON(reader)
	return ch, stop, nil
}

// BuildImage builds an image from the Dockerfile in contextDir, streaming the
// build output. The directory is tarred locally and sent to the daemon, so it
// works over both TCP and SSH connections (context is relative to where d9c
// runs, matching `docker build` semantics).
func (b *dockerBackend) BuildImage(contextDir, tag string) (<-chan string, func(), error) {
	if fi, err := os.Stat(contextDir); err != nil || !fi.IsDir() {
		return nil, nil, fmt.Errorf("build context %q is not a directory", contextDir)
	}
	tarReader := tarDir(contextDir)
	opts := types.ImageBuildOptions{Dockerfile: "Dockerfile", Remove: true}
	if tag != "" {
		opts.Tags = []string{tag}
	}
	resp, err := b.cli.ImageBuild(context.Background(), tarReader, opts)
	if err != nil {
		_ = tarReader.Close()
		return nil, nil, fmt.Errorf("build image: %w", err)
	}
	// Keep the context tar open until the build stream drains.
	ch, stop := streamDockerJSON(resp.Body, tarReader)
	return ch, stop, nil
}

// tarDir streams the directory tree rooted at dir as a tar archive, with paths
// relative to dir (so the Dockerfile lands at the archive root). It runs the
// walk in a goroutine and reports any error by closing the pipe with it.
func tarDir(dir string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			_, err = io.Copy(tw, f)
			return err
		})
		if err == nil {
			err = tw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	return pr
}

// ImageHistory returns the layer history of an image as a readable detail view.
func (b *dockerBackend) ImageHistory(id string) (*InspectResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	layers, err := b.cli.ImageHistory(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("image history: %w", err)
	}
	var sb strings.Builder
	for _, l := range layers {
		created := time.Unix(l.Created, 0).Format("2006-01-02 15:04")
		fmt.Fprintf(&sb, "%s  %10s  %s\n", created, formatBytes(l.Size), cleanHistoryCmd(l.CreatedBy))
	}
	return &InspectResult{Name: id + " · history", RawYAML: sb.String()}, nil
}

// cleanHistoryCmd strips the noisy "/bin/sh -c #(nop) " and "/bin/sh -c "
// prefixes Docker prepends to layer-creating commands.
func cleanHistoryCmd(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/bin/sh -c #(nop) ")
	s = strings.TrimPrefix(s, "/bin/sh -c ")
	return strings.TrimSpace(s)
}

// jsonProgress is the subset of Docker's JSON build/push stream we render.
type jsonProgress struct {
	Stream   string `json:"stream"`
	Status   string `json:"status"`
	Progress string `json:"progress"`
	ID       string `json:"id"`
	Error    string `json:"error"`
}

// formatJSONProgress renders one Docker JSON progress message as a line, or ""
// to skip empty keep-alive frames.
func formatJSONProgress(m jsonProgress) string {
	switch {
	case m.Error != "":
		return "error: " + m.Error
	case m.Stream != "":
		return strings.TrimRight(m.Stream, "\n")
	case m.Status != "":
		s := m.Status
		if m.ID != "" {
			s = m.ID + ": " + s
		}
		if m.Progress != "" {
			s += " " + m.Progress
		}
		return s
	}
	return ""
}

// streamDockerJSON decodes a Docker JSON progress stream (build/push) into
// readable lines. Any extra closers (e.g. a build-context tar) are closed once
// the stream drains. The returned stop aborts the stream: it closes the reader
// (ending the daemon request) and unblocks a producer stuck on a send nobody
// reads; the caller MUST call it when it abandons the channel, otherwise the
// connection and producer goroutine leak. stop is also invoked on natural end.
func streamDockerJSON(r io.ReadCloser, extra ...io.Closer) (<-chan string, func()) {
	ch := make(chan string, 256)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			_ = r.Close()
			for _, c := range extra {
				_ = c.Close()
			}
		})
	}
	go func() {
		defer close(ch)
		defer stop()
		dec := json.NewDecoder(r)
		for {
			var m jsonProgress
			if err := dec.Decode(&m); err != nil {
				// A deliberate stop closes the reader, surfacing a read error
				// here; the done channel keeps it from being reported as one.
				if err != io.EOF {
					select {
					case ch <- "error: " + err.Error():
					case <-done:
					}
				}
				return
			}
			if line := formatJSONProgress(m); line != "" {
				select {
				case ch <- line:
				case <-done:
					return
				}
			}
		}
	}()
	return ch, stop
}

// ── Network ───────────────────────────────────────────────────────────────────

type Network struct {
	ID     string
	Name   string
	Driver string
	Scope  string
	Subnet string
}

func (b *dockerBackend) ListNetworks() ([]Network, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := b.cli.NetworkList(ctx, dockernetwork.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}

	result := make([]Network, 0, len(list))
	for _, n := range list {
		subnet := ""
		if len(n.IPAM.Config) > 0 {
			subnet = n.IPAM.Config[0].Subnet
		}
		result = append(result, Network{
			ID:     shortID(n.ID),
			Name:   n.Name,
			Driver: n.Driver,
			Scope:  n.Scope,
			Subnet: subnet,
		})
	}
	return result, nil
}

func (b *dockerBackend) InspectNetwork(id string) (*InspectResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := b.cli.NetworkInspect(ctx, id, dockernetwork.InspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspect network: %w", err)
	}
	rawYAML := ""
	if y, err := yaml.Marshal(info); err == nil {
		rawYAML = string(y)
	}
	return &InspectResult{Name: info.Name, RawYAML: rawYAML}, nil
}

func (b *dockerBackend) RemoveNetwork(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return b.cli.NetworkRemove(ctx, id)
}

// NetworkCreateOptions describes a network to create. Driver defaults to
// "bridge" when empty; Subnet/Gateway are optional and configure a single IPAM
// pool when Subnet is set.
type NetworkCreateOptions struct {
	Name    string
	Driver  string
	Subnet  string
	Gateway string
}

// CreateNetwork creates a user-defined network. A blank driver falls back to
// "bridge"; when a subnet is given it is registered as the network's IPAM pool
// (with an optional gateway).
func (b *dockerBackend) CreateNetwork(opts NetworkCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("network name is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	driver := opts.Driver
	if driver == "" {
		driver = "bridge"
	}
	createOpts := dockernetwork.CreateOptions{Driver: driver}
	if opts.Subnet != "" {
		ipamCfg := dockernetwork.IPAMConfig{Subnet: opts.Subnet, Gateway: opts.Gateway}
		createOpts.IPAM = &dockernetwork.IPAM{Config: []dockernetwork.IPAMConfig{ipamCfg}}
	}
	if _, err := b.cli.NetworkCreate(ctx, opts.Name, createOpts); err != nil {
		return fmt.Errorf("create network: %w", err)
	}
	return nil
}

// ── Volume ────────────────────────────────────────────────────────────────────

type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	Created    string
}

func (b *dockerBackend) ListVolumes() ([]Volume, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := b.cli.VolumeList(ctx, dockervolume.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}

	result := make([]Volume, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		created := v.CreatedAt
		if len(created) > 19 {
			created = created[:19] // trim nanoseconds
		}
		result = append(result, Volume{
			Name:       v.Name,
			Driver:     v.Driver,
			Mountpoint: v.Mountpoint,
			Created:    created,
		})
	}
	return result, nil
}

func (b *dockerBackend) InspectVolume(name string) (*InspectResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := b.cli.VolumeInspect(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("inspect volume: %w", err)
	}
	rawYAML := ""
	if y, err := yaml.Marshal(info); err == nil {
		rawYAML = string(y)
	}
	return &InspectResult{Name: info.Name, RawYAML: rawYAML}, nil
}

func (b *dockerBackend) RemoveVolume(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return b.cli.VolumeRemove(ctx, name, false)
}

// VolumeCreateOptions describes a volume to create. Driver defaults to "local"
// when empty.
type VolumeCreateOptions struct {
	Name   string
	Driver string
}

// CreateVolume creates a named volume. A blank driver falls back to "local".
func (b *dockerBackend) CreateVolume(opts VolumeCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("volume name is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	driver := opts.Driver
	if driver == "" {
		driver = "local"
	}
	if _, err := b.cli.VolumeCreate(ctx, dockervolume.CreateOptions{Name: opts.Name, Driver: driver}); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}
	return nil
}

func (b *dockerBackend) PruneVolumes() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := b.cli.VolumesPrune(ctx, filters.Args{})
	if err != nil {
		return 0, fmt.Errorf("prune volumes: %w", err)
	}
	return len(report.VolumesDeleted), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
