package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCapture(t *testing.T) {
	tests := []struct {
		max    int
		writes []string
		want   string
		total  int64
	}{
		{0, []string{"abc", "def"}, "abcdef", 6},
		{4, []string{"abc", "def"}, "abcd", 6},
		{3, []string{"abc", "def"}, "abc", 6},
		{10, []string{"abc"}, "abc", 3},
	}
	for _, tt := range tests {
		c := newCapture(tt.max)
		for _, w := range tt.writes {
			if n, err := c.Write([]byte(w)); n != len(w) || err != nil {
				t.Fatalf("Write = %d, %v", n, err)
			}
		}
		if c.buf.String() != tt.want || c.total != tt.total {
			t.Errorf("max %d %v: kept %q (total %d), want %q (%d)", tt.max, tt.writes, c.buf.String(), c.total, tt.want, tt.total)
		}
	}
}

func TestMockExec(t *testing.T) {
	var gotCmd []string
	b := newMockBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch m, p := route(r); {
		case m == "POST" && p == "/containers/web/exec":
			var body struct {
				Cmd []string
				Tty bool
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Tty {
				t.Error("non-interactive exec asked for a TTY")
			}
			gotCmd = body.Cmd
			jsonOK(w, `{"Id":"e1"}`)
		case m == "POST" && p == "/exec/e1/start":
			hijackWithFrames(t, w, r, "hello world\n", "warning: x\n")
		case m == "GET" && p == "/exec/e1/json":
			jsonOK(w, `{"ID":"e1","Running":false,"ExitCode":3}`)
		case m == "POST" && p == "/containers/gone/exec":
			jsonErr(w, http.StatusNotFound, "No such container: gone")
		default:
			jsonErr(w, http.StatusNotFound, "unexpected "+m+" "+p)
		}
	})

	res, err := b.Exec(context.Background(), "web", []string{"sh", "-c", "echo hello world"}, ExecOptions{MaxOutput: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotCmd, " ") != "sh -c echo hello world" {
		t.Errorf("cmd = %q", gotCmd)
	}
	if string(res.Stdout) != "hello" || res.StdoutBytes != 12 || string(res.Stderr) != "warni" || res.StderrBytes != 11 || res.ExitCode != 3 {
		t.Errorf("result = %+v", res)
	}

	if _, err := b.Exec(context.Background(), "gone", []string{"true"}, ExecOptions{}); !IsNotFound(err) {
		t.Errorf("missing container: err = %v, want not found", err)
	}
	if _, err := b.Exec(context.Background(), "web", nil, ExecOptions{}); !errors.Is(err, errEmptyExec) {
		t.Errorf("empty command: err = %v", err)
	}
}

func TestRunnerExec(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		stderr   string
		code     int
		wantErr  string
		wantCode int
	}{
		{"success", "ok\n", "", 0, "", 0},
		{"command exit status", "", "grep: no match\n", 1, "", 1},
		{"runtime failure", "", `time="2026-10-28T10:00:00Z" level=fatal msg="no such container: ghost"` + "\n", 1, "no such container: ghost", 1},
		{"fatal-looking output with stdout is the command's", "partial\n", `level=fatal msg="x"`, 2, "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{capFn: func([]string) (string, string, int, error) {
				return tt.stdout, tt.stderr, tt.code, nil
			}}
			nb := newTestBackend(fr)
			res, err := nb.Exec(context.Background(), "web", []string{"grep", "x", "/etc/hosts"}, ExecOptions{})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if res.ExitCode != tt.wantCode || string(res.Stdout) != tt.stdout {
				t.Errorf("result = %+v", res)
			}
			want := []string{"--namespace", defaultNamespace, "exec", "web", "grep", "x", "/etc/hosts"}
			if got := fr.calls[len(fr.calls)-1]; strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("argv = %q, want %q", got, want)
			}
		})
	}

	cb := &criBackend{runner: &fakeRunner{}, endpoint: "unix:///run/crio/crio.sock"}
	if _, err := cb.Exec(context.Background(), "abc", []string{"true"}, ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := cb.runner.(*fakeRunner).calls[0]; strings.Join(got, " ") != "-r unix:///run/crio/crio.sock exec abc true" {
		t.Errorf("crictl argv = %q", got)
	}
	if _, err := cb.Exec(context.Background(), "abc", nil, ExecOptions{}); !errors.Is(err, errEmptyExec) {
		t.Errorf("empty command: err = %v", err)
	}
}

// The local runner reports a command's exit status as a result and a missing
// binary or a cancelled context as an error. `go` is on PATH wherever these
// tests run.
func TestLocalRunnerCapture(t *testing.T) {
	r := localRunner{bin: "go"}
	var out, errOut bytes.Buffer
	code, err := r.capture(context.Background(), []string{"env", "GOOS"}, &out, &errOut)
	if err != nil || code != 0 || strings.TrimSpace(out.String()) == "" {
		t.Errorf("go env GOOS = %d, %v, %q", code, err, out.String())
	}
	out.Reset()
	code, err = r.capture(context.Background(), []string{"no-such-subcommand"}, &out, &errOut)
	if err != nil || code == 0 || errOut.Len() == 0 {
		t.Errorf("go no-such-subcommand = %d, %v, stderr %q", code, err, errOut.String())
	}
	if _, err := (localRunner{bin: "d9c-no-such-binary"}).capture(context.Background(), nil, io.Discard, io.Discard); err == nil {
		t.Error("missing binary: want an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.capture(ctx, []string{"version"}, io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v", err)
	}
}

func TestSSHCapture(t *testing.T) {
	release := make(chan struct{})
	srv := newSSHTestServer(t, sshScript(map[string]sshExecHandler{
		"ok":   sshReply("out\n", "err\n", 0),
		"fail": sshReply("", "boom\n", 42),
		"hang": func(_ string, _ io.Reader, _, _ io.Writer) int {
			<-release
			return 0
		},
	}))
	defer close(release)
	client := srv.dial(t)

	var out, errOut bytes.Buffer
	code, err := sshCapture(context.Background(), client, "ok", &out, &errOut)
	if err != nil || code != 0 || out.String() != "out\n" || errOut.String() != "err\n" {
		t.Errorf("ok = %d, %v, %q, %q", code, err, out.String(), errOut.String())
	}
	errOut.Reset()
	code, err = sshCapture(context.Background(), client, "fail", io.Discard, &errOut)
	if err != nil || code != 42 || errOut.String() != "boom\n" {
		t.Errorf("fail = %d, %v, %q", code, err, errOut.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := sshCapture(ctx, client, "hang", io.Discard, io.Discard); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("hang: err = %v, want deadline exceeded", err)
	}
	if _, err := (sshRunner{client: client, bin: "nerdctl"}).capture(context.Background(), []string{"x"}, io.Discard, io.Discard); err != nil {
		t.Errorf("runner capture of an unscripted command = %v, want its status as a result", err)
	}
}

func TestFakeExec(t *testing.T) {
	fb := NewFakeBackend()
	ctx := context.Background()
	tests := []struct {
		id      string
		cmd     []string
		stdout  string
		code    int
		wantErr string
	}{
		{"web", []string{"echo", "hi", "there"}, "hi there\n", 0, ""},
		{"web", []string{"seq", "3"}, "1\n2\n3\n", 0, ""},
		{"9ae942fd8fbc", []string{"false"}, "", 1, ""},
		{"web", []string{"ls"}, "", 127, ""},
		{"db", []string{"echo"}, "", 0, "not running"},
		{"ghost", []string{"echo"}, "", 0, "no such container"},
	}
	for _, tt := range tests {
		res, err := fb.Exec(ctx, tt.id, tt.cmd, ExecOptions{})
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s %v: err = %v, want %q", tt.id, tt.cmd, err, tt.wantErr)
			}
			continue
		}
		if err != nil || string(res.Stdout) != tt.stdout || res.ExitCode != tt.code {
			t.Errorf("%s %v = %+v, %v", tt.id, tt.cmd, res, err)
		}
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, err := fb.Exec(tctx, "web", []string{"sleep", "60"}, ExecOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("sleep: err = %v", err)
	}
	if _, err := NewDisconnected(nil).Exec(ctx, "web", []string{"true"}, ExecOptions{}); err == nil {
		t.Error("disconnected exec: want an error")
	}
}
