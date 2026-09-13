package docker

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/kirg0/d9c/internal/i18n"
)

// muxFrame writes one multiplexed exec/log frame (1 = stdout, 2 = stderr).
func muxFrame(w io.Writer, stream byte, payload string) {
	if payload == "" {
		return
	}
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = w.Write(header)
	_, _ = io.WriteString(w, payload)
}

// hijackWithFrames answers an exec attach the way the daemon does: it upgrades
// the connection, writes the multiplexed output and ends it. The request body is
// drained and the write side half-closed first, waiting for the client to hang
// up: closing a socket with unread bytes makes Windows reset the connection
// before the client has read the output.
func hijackWithFrames(t *testing.T, w http.ResponseWriter, r *http.Request, stdout, stderr string) {
	_, _ = io.Copy(io.Discard, r.Body)
	hj, ok := w.(http.Hijacker)
	if !ok {
		t.Error("response writer cannot hijack")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\n" +
		"Content-Type: application/vnd.docker.multiplexed-stream\r\n" +
		"Connection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	muxFrame(buf, 1, stdout)
	muxFrame(buf, 2, stderr)
	_ = buf.Flush()
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
		_ = tc.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.Copy(io.Discard, tc)
	}
}

func TestMockListPath(t *testing.T) {
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "POST" && p == "/containers/web/exec":
			var body struct{ Cmd []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := "ok"
			if len(body.Cmd) > 0 && body.Cmd[len(body.Cmd)-1] == "/nope" {
				id = "nope"
			}
			jsonOK(w, `{"Id":"`+id+`"}`)
		case m == "POST" && p == "/exec/ok/start":
			hijackWithFrames(t, w, r, "bin/\netc/\n.env\n", "")
		case m == "POST" && p == "/exec/nope/start":
			hijackWithFrames(t, w, r, "", "ls: /nope: No such file or directory\n")
		case m == "POST" && p == "/containers/gone/exec":
			jsonErr(w, http.StatusNotFound, "No such container: gone")
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	entries, err := b.ListPath("web", " ")
	if err != nil {
		t.Fatalf("ListPath: %v", err)
	}
	want := []FileEntry{{Name: "bin", IsDir: true}, {Name: "etc", IsDir: true}, {Name: ".env"}}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}

	_, err = b.ListPath("web", "/nope")
	if wantErr := friendlyListErr("/nope", "ls: /nope: No such file or directory\n"); err == nil || err.Error() != wantErr.Error() {
		t.Errorf("missing dir error = %v, want %v", err, wantErr)
	}

	if _, err := b.ListPath("gone", "/"); err == nil || !strings.Contains(err.Error(), "create exec") {
		t.Errorf("unknown container error = %v", err)
	}
}

func TestMockCopyToContainer(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "app.conf")
	if err := os.WriteFile(local, []byte("key=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var gotPath string
	var gotNames []string
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "PUT" && p == "/containers/web/archive":
			var names []string
			tr := tar.NewReader(r.Body)
			for {
				hdr, err := tr.Next()
				if err != nil {
					break
				}
				names = append(names, hdr.Name)
			}
			mu.Lock()
			gotPath, gotNames = r.URL.Query().Get("path"), names
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case m == "PUT" && p == "/containers/gone/archive":
			jsonErr(w, http.StatusNotFound, "No such container: gone")
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	if err := b.CopyToContainer("web", local, ""); err != nil {
		t.Fatalf("CopyToContainer: %v", err)
	}
	mu.Lock()
	if gotPath != "/" || len(gotNames) != 1 || gotNames[0] != "app.conf" {
		t.Errorf("uploaded to %q entries %q, want / and [app.conf]", gotPath, gotNames)
	}
	mu.Unlock()

	if err := b.CopyToContainer("gone", local, "/etc"); err == nil ||
		err.Error() != i18n.T("контейнер не найден", "container not found") {
		t.Errorf("unknown container error = %v", err)
	}
	if err := b.CopyToContainer("web", filepath.Join(dir, "missing"), "/"); err == nil {
		t.Error("a missing local path must fail before contacting the daemon")
	}
}

func TestMockCopyFromContainer(t *testing.T) {
	stat, _ := json.Marshal(container.PathStat{Name: "hello.txt", Size: 6, Mode: 0o644})
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	_ = tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: 6, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hello\n"))
	_ = tw.Close()

	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		m, p := route(r)
		if m != "GET" || p != "/containers/web/archive" {
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
			return
		}
		if r.URL.Query().Get("path") == "/nope" {
			jsonErr(w, http.StatusNotFound, "Could not find the file /nope in container web")
			return
		}
		w.Header().Set("X-Docker-Container-Path-Stat", base64.StdEncoding.EncodeToString(stat))
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(archive.Bytes())
	})

	dest := filepath.Join(t.TempDir(), "out")
	if err := b.CopyFromContainer("web", "/etc/hello.txt", dest); err != nil {
		t.Fatalf("CopyFromContainer: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "hello.txt")); err != nil || string(data) != "hello\n" {
		t.Errorf("downloaded = %q, %v", data, err)
	}

	if err := b.CopyFromContainer("web", "/nope", dest); err == nil ||
		err.Error() != i18n.T("путь в контейнере не найден", "path in the container not found") {
		t.Errorf("missing path error = %v", err)
	}
}

func TestFriendlyCopyErr(t *testing.T) {
	if friendlyCopyErr(nil) != nil {
		t.Error("nil error must stay nil")
	}
	notFound := i18n.T("путь в контейнере не найден", "path in the container not found")
	tests := []struct{ in, want string }{
		{"Error response from daemon: No such container: web", i18n.T("контейнер не найден", "container not found")},
		{"Could not find the file /x in container web", notFound},
		{"lstat /x: no such file or directory", notFound},
		{"extraction point is not a directory", i18n.T("целевой путь в контейнере — не каталог", "target path in the container is not a directory")},
		{"open /root/secret: permission denied", i18n.T("нет доступа к пути", "no access to the path")},
	}
	for _, tt := range tests {
		if got := friendlyCopyErr(errors.New(tt.in)); got == nil || got.Error() != tt.want {
			t.Errorf("friendlyCopyErr(%q) = %v, want %q", tt.in, got, tt.want)
		}
	}
	orig := errors.New("boom")
	if got := friendlyCopyErr(orig); !errors.Is(got, orig) || !strings.HasPrefix(got.Error(), "docker cp:") {
		t.Errorf("unknown error should be wrapped with context, got %v", got)
	}
}
