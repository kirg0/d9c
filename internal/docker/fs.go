package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// FileEntry is one entry of a container directory listing (a file or a
// subdirectory). It carries just enough to drive the filesystem browser; the
// daemon has no readdir API, so listings come from `ls` run inside the
// container (see ListPath).
type FileEntry struct {
	Name  string
	IsDir bool
}

// ListPath lists the entries of dir inside the container. It runs `ls` in the
// container (the daemon exposes no directory-listing API) and parses the
// output; `-p` marks directories with a trailing slash, `-A` includes dotfiles
// but not "."/"..". A container without `ls` (scratch/distroless) yields an
// error explaining the listing could not be produced.
func (b *dockerBackend) ListPath(containerID, dir string) ([]FileEntry, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stdout, stderr, err := b.execCapture(ctx, containerID, []string{"ls", "-1Ap", "--", dir})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	if strings.TrimSpace(stdout) == "" && strings.TrimSpace(stderr) != "" {
		return nil, friendlyListErr(dir, stderr)
	}
	return parseLsEntries(stdout), nil
}

// friendlyListErr turns `ls` stderr into an actionable message: a missing `ls`
// binary (minimal images) and permission/not-found errors are the common cases.
func friendlyListErr(dir, stderr string) error {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "executable file not found"), strings.Contains(low, "no such file or directory") && strings.Contains(low, "ls"):
		return fmt.Errorf("в контейнере нет `ls` — обзор файловой системы недоступен")
	case strings.Contains(low, "permission denied"):
		return fmt.Errorf("нет доступа к %s", dir)
	case strings.Contains(low, "no such file"):
		return fmt.Errorf("путь %s не найден", dir)
	}
	return fmt.Errorf("ls: %s", strings.TrimSpace(stderr))
}

// parseLsEntries parses the output of `ls -1Ap` (one entry per line, a trailing
// "/" on directories) into FileEntry values. The trailing slash is stripped and
// recorded as IsDir; blank lines are skipped.
func parseLsEntries(out string) []FileEntry {
	var entries []FileEntry
	for line := range strings.SplitSeq(out, "\n") {
		name := strings.TrimRight(line, "\r")
		if name == "" {
			continue
		}
		isDir := strings.HasSuffix(name, "/")
		name = strings.TrimSuffix(name, "/")
		if name == "" {
			continue
		}
		entries = append(entries, FileEntry{Name: name, IsDir: isDir})
	}
	return entries
}

// execCapture runs cmd inside the container without a TTY and returns its
// captured stdout and stderr. The hijacked stream is demultiplexed with
// stdcopy (no TTY means the 8-byte stream headers are present).
func (b *dockerBackend) execCapture(ctx context.Context, containerID string, cmd []string) (string, string, error) {
	created, err := b.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		return "", "", fmt.Errorf("create exec: %w", err)
	}
	att, err := b.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", "", fmt.Errorf("attach exec: %w", err)
	}
	defer att.Close()

	var outBuf, errBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, att.Reader); err != nil {
		return "", "", fmt.Errorf("read exec output: %w", err)
	}
	return outBuf.String(), errBuf.String(), nil
}

// CopyFromContainer downloads srcPath from the container into the local
// directory destDir (`docker cp <ctr>:srcPath destDir`). The daemon streams a
// TAR archive whose top-level member is named after srcPath's base; it is
// extracted verbatim into destDir, so a file lands at destDir/<base> and a
// directory at destDir/<base>/….
func (b *dockerBackend) CopyFromContainer(containerID, srcPath, destDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rc, _, err := b.cli.CopyFromContainer(ctx, containerID, srcPath)
	if err != nil {
		return friendlyCopyErr(err)
	}
	defer func() { _ = rc.Close() }()

	if destDir == "" {
		destDir = "."
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}
	return extractTar(rc, destDir)
}

// CopyToContainer uploads a local file or directory into destDir inside the
// container (`docker cp localPath <ctr>:destDir`). destDir must be an existing
// directory in the container; the uploaded entry keeps its base name.
func (b *dockerBackend) CopyToContainer(containerID, localPath, destDir string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("открыть %s: %w", localPath, err)
	}
	buf, err := tarLocal(localPath, info)
	if err != nil {
		return fmt.Errorf("упаковать %s: %w", localPath, err)
	}
	if strings.TrimSpace(destDir) == "" {
		destDir = "/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := b.cli.CopyToContainer(ctx, containerID, destDir, buf, container.CopyToContainerOptions{}); err != nil {
		return friendlyCopyErr(err)
	}
	return nil
}

// friendlyCopyErr rewrites the daemon's terse copy errors into actionable hints.
func friendlyCopyErr(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no such container"):
		return fmt.Errorf("контейнер не найден")
	case strings.Contains(msg, "could not find the file"), strings.Contains(msg, "no such file"):
		return fmt.Errorf("путь в контейнере не найден")
	case strings.Contains(msg, "not a directory"):
		return fmt.Errorf("целевой путь в контейнере — не каталог")
	case strings.Contains(msg, "permission denied"):
		return fmt.Errorf("нет доступа к пути")
	}
	return fmt.Errorf("docker cp: %w", err)
}

// extractTar writes the members of a TAR stream into destDir, guarding against
// path-traversal ("zip slip") entries that would escape destDir.
func extractTar(r io.Reader, destDir string) error {
	root := filepath.Clean(destDir)
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		target := filepath.Join(root, filepath.Clean("/"+hdr.Name))
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", filepath.Dir(target), err)
			}
			if err := writeFile(target, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		default:
			// Symlinks, devices and the like are skipped: reproducing them on the
			// host is unsafe (a symlink could point outside destDir) and rarely
			// what a browse-and-download flow needs.
		}
	}
}

// writeFile creates target and copies the reader into it, applying mode (with a
// sane fallback when the archive carries none).
func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", target, err)
	}
	return nil
}

// tarLocal packs a local file or directory into an in-memory TAR archive whose
// member names are rooted at the path's base (so `docker cp ./x ctr:/dst`
// lands at /dst/x), mirroring the daemon's own archive layout.
func tarLocal(localPath string, info os.FileInfo) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	base := filepath.Base(localPath)

	if !info.IsDir() {
		if err := writeTarFile(tw, localPath, base, info); err != nil {
			return nil, err
		}
		if err := tw.Close(); err != nil {
			return nil, err
		}
		return buf, nil
	}

	err := filepath.Walk(localPath, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localPath, p)
		if err != nil {
			return err
		}
		name := path.Join(base, filepath.ToSlash(rel))
		if fi.IsDir() {
			hdr := &tar.Header{Name: name + "/", Mode: int64(fi.Mode().Perm()), Typeflag: tar.TypeDir}
			return tw.WriteHeader(hdr)
		}
		if !fi.Mode().IsRegular() {
			return nil // skip symlinks/devices, as on extraction
		}
		return writeTarFile(tw, p, name, fi)
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeTarFile appends a single regular file to the archive under member name.
func writeTarFile(tw *tar.Writer, srcPath, name string, fi os.FileInfo) error {
	hdr := &tar.Header{Name: name, Mode: int64(fi.Mode().Perm()), Size: fi.Size(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(tw, f)
	return err
}
