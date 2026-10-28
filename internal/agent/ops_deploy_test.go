package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

// cannedStream returns a closed channel holding lines and a stop that counts
// its calls.
func cannedStream(lines []string, stops *int) (<-chan string, func()) {
	ch := make(chan string, len(lines))
	for _, l := range lines {
		ch <- l
	}
	close(ch)
	return ch, func() { *stops++ }
}

func TestCollectProgress(t *testing.T) {
	over := numbered(maxProgressLines + 3)
	tests := []struct {
		name        string
		in          []string
		wantLines   []string
		wantDropped int
		wantFailure string
		wantContext []string
	}{
		{"success", []string{"a", "b"}, []string{"a", "b"}, 0, "", nil},
		{"blank lines skipped, trailing space trimmed", []string{"a  ", "", "\r", "b\r"}, []string{"a", "b"}, 0, "", nil},
		{
			"failure keeps the lines before it",
			[]string{"1", "2", "3", "4", "5", "6", "error: Process exited with status 1", "error: second"},
			[]string{"1", "2", "3", "4", "5", "6", "error: Process exited with status 1", "error: second"},
			0, "Process exited with status 1", []string{"2", "3", "4", "5", "6"},
		},
		{"over the cap drops the oldest", over, over[3:], 3, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stops := 0
			ch, stop := cannedStream(tt.in, &stops)
			p, err := collectProgress(context.Background(), ch, stop, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p.lines, tt.wantLines) || p.dropped != tt.wantDropped ||
				p.failure != tt.wantFailure || !reflect.DeepEqual(p.context, tt.wantContext) {
				t.Errorf("got %+v", p)
			}
			if stops != 1 {
				t.Errorf("stop called %d times, want 1", stops)
			}
		})
	}
}

// A cancelled context aborts the operation through stop instead of waiting
// for a stream that may never end.
func TestCollectProgressCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := false
	_, err := collectProgress(ctx, make(chan string), func() { stopped = true }, func(string) {})
	if !errors.Is(err, context.Canceled) || !stopped {
		t.Errorf("err = %v, stopped = %v", err, stopped)
	}
}

func TestProgressResult(t *testing.T) {
	t.Run("failure quotes the output", func(t *testing.T) {
		p := progress{failure: "Process exited with status 1", context: []string{"pulling web", "manifest unknown"}}
		_, err := p.result("compose up", "webapp")
		e := Classify(err)
		want := "compose up webapp failed: Process exited with status 1; last output: pulling web | manifest unknown"
		if e.Code != CodeInternal || e.Message != want {
			t.Errorf("err = %+v", e)
		}
	})
	t.Run("connection failure keeps its code", func(t *testing.T) {
		p := progress{failure: "dial tcp 10.0.0.1:22: connect: connection refused"}
		_, err := p.result("compose pull", "webapp")
		if e := Classify(err); e.Code != CodeConnection {
			t.Errorf("err = %+v, want connection", e)
		}
	})
	t.Run("dropped lines mark the result truncated", func(t *testing.T) {
		r, err := progress{lines: []string{"x"}, dropped: 7}.result("compose up", "webapp")
		if err != nil || !r.Truncated || !strings.Contains(r.Hint, "first 7 output lines") {
			t.Errorf("result = %+v (%v)", r, err)
		}
		if d := r.Data.(ComposeActionData); d.Action != "compose up" || d.Project != "webapp" || len(d.Output) != 1 {
			t.Errorf("data = %+v", d)
		}
	})
}

// pullSpy records pulled refs and fails with err when set.
type pullSpy struct {
	*docker.FakeBackend
	refs []string
	err  error
}

func (p *pullSpy) PullImage(ref string) error {
	p.refs = append(p.refs, ref)
	return p.err
}

func TestPull(t *testing.T) {
	spy := &pullSpy{FakeBackend: docker.NewFakeBackend()}
	r, err := invokeWrite(spy, "pull", Args{"image": " nginx:1.27 "})
	if err != nil || r.Data != (PullData{Image: "nginx:1.27"}) || !reflect.DeepEqual(spy.refs, []string{"nginx:1.27"}) {
		t.Errorf("pull = %+v (%v), refs %v", r, err, spy.refs)
	}
	if _, err := invokeWrite(spy, "pull", Args{"image": "  "}); Classify(err).Code != CodeUsage {
		t.Errorf("empty ref: %v, want usage", err)
	}
	spy.err = errors.New("Error response from daemon: pull access denied for ghost, repository does not exist")
	if _, err := invokeWrite(spy, "pull", Args{"image": "ghost"}); err == nil || !strings.Contains(Classify(err).Message, "pull access denied") {
		t.Errorf("failed pull: %v", err)
	}
}

// streamSpy serves canned compose progress and records which deployment each
// operation got.
type streamSpy struct {
	*docker.FakeBackend
	lines []string
	calls []string
	stops int
}

func (s *streamSpy) stream(call string) (<-chan string, func(), error) {
	s.calls = append(s.calls, call)
	ch, stop := cannedStream(s.lines, &s.stops)
	return ch, stop, nil
}

func (s *streamSpy) ComposeUp(p string) (<-chan string, func(), error) { return s.stream("up " + p) }
func (s *streamSpy) ComposeDown(p string) (<-chan string, func(), error) {
	return s.stream("down " + p)
}
func (s *streamSpy) ComposePull(p string) (<-chan string, func(), error) {
	return s.stream("pull " + p)
}
func (s *streamSpy) RestoreComposeProject(p, file string) (<-chan string, func(), error) {
	return s.stream("restore " + p + " " + file)
}

func TestComposeStreamOps(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "webapp.tar.gz")
	if err := os.WriteFile(archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		op       string
		args     Args
		wantCall string
	}{
		{"compose up", Args{"project": "webapp"}, "up /srv/webapp"},
		{"compose down", Args{"project": "/srv/monitoring"}, "down /srv/monitoring"},
		{"compose pull", Args{"project": "legacy"}, "pull /opt/legacy"},
		{"compose restore", Args{"project": "webapp", "file": archive}, "restore /srv/webapp " + archive},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			spy := &streamSpy{FakeBackend: docker.NewFakeBackend(), lines: []string{"[+] Running 1/1", "done"}}
			r, err := invokeWrite(spy, tt.op, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			d := r.Data.(ComposeActionData)
			if d.Action != tt.op || !reflect.DeepEqual(d.Output, spy.lines) || r.Truncated {
				t.Errorf("result = %+v", r)
			}
			if !reflect.DeepEqual(spy.calls, []string{tt.wantCall}) || spy.stops != 1 {
				t.Errorf("calls = %v, stops = %d", spy.calls, spy.stops)
			}

			spy.lines = []string{"Error: no such image", "error: Process exited with status 1"}
			if _, err := invokeWrite(spy, tt.op, tt.args); !strings.Contains(Classify(err).Message, "last output: Error: no such image") {
				t.Errorf("failed run: %v", err)
			}

			if e := Classify(func() error { _, err := invokeWrite(spy, tt.op, withProject(tt.args, "ghost")); return err }()); e.Code != CodeNotFound {
				t.Errorf("unknown project: %+v", e)
			}

			spy.NoHostCompose = true
			if e := Classify(func() error { _, err := invokeWrite(spy, tt.op, tt.args); return err }()); e.Code != CodeUsage || !strings.Contains(e.Hint, "ssh://") {
				t.Errorf("tcp connection: %+v", e)
			}
		})
	}
}

func withProject(a Args, project string) Args {
	out := maps.Clone(a)
	out["project"] = project
	return out
}

func TestComposeRestoreMissingArchive(t *testing.T) {
	spy := &streamSpy{FakeBackend: docker.NewFakeBackend()}
	_, err := invokeWrite(spy, "compose restore", Args{"project": "webapp", "file": filepath.Join(t.TempDir(), "nope.tar.gz")})
	if e := Classify(err); e.Code != CodeNotFound || !strings.Contains(e.Hint, "compose backup") || len(spy.calls) != 0 {
		t.Errorf("err = %+v, calls %v", e, spy.calls)
	}
}

// The fake backend's own compose ops change the project state end to end.
func TestComposeWriteOnFake(t *testing.T) {
	t.Chdir(t.TempDir()) // the fake writes the backup archive into the working directory
	fb := docker.NewFakeBackend()

	if _, err := invokeWrite(fb, "compose down", Args{"project": "webapp"}); err != nil {
		t.Fatal(err)
	}
	if st := composeStatus(t, fb, "webapp"); st != "stopped" {
		t.Errorf("after down: %s", st)
	}
	r, err := invokeWrite(fb, "compose restart", Args{"project": "webapp"})
	if err != nil || r.Data.(ComposeActionData).Output == nil {
		t.Fatalf("restart = %+v (%v)", r, err)
	}
	if st := composeStatus(t, fb, "webapp"); st != "running" {
		t.Errorf("after restart: %s", st)
	}

	r, err = invokeWrite(fb, "compose backup", Args{"project": "webapp"})
	if err != nil {
		t.Fatal(err)
	}
	b := r.Data.(BackupData)
	if b.Project != "webapp" || !filepath.IsAbs(b.Path) || b.Size == 0 || !strings.HasPrefix(filepath.Base(b.Path), "webapp-") {
		t.Errorf("backup = %+v", b)
	}
	if _, err := invokeWrite(fb, "compose restore", Args{"project": "webapp", "file": b.Path}); err != nil {
		t.Errorf("restore: %v", err)
	}

	// restart works over tcp:// (container API); backup needs SSH.
	fb.NoHostCompose = true
	if _, err := invokeWrite(fb, "compose restart", Args{"project": "webapp"}); err != nil {
		t.Errorf("restart over tcp: %v", err)
	}
	if _, err := invokeWrite(fb, "compose backup", Args{"project": "webapp"}); Classify(err).Code != CodeUsage {
		t.Errorf("backup over tcp: %v", err)
	}
}

func composeStatus(t *testing.T, b docker.Backend, project string) string {
	t.Helper()
	list, err := b.ListComposeProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Project == project {
			return p.Status
		}
	}
	return fmt.Sprintf("<no %s>", project)
}

// A destructive compose command is refused under a safe ceiling.
func TestComposeDownNeedsDestructive(t *testing.T) {
	_, err := Builtin().Invoke(context.Background(), &Env{Backend: docker.NewFakeBackend()}, "compose down", Args{"project": "webapp"}, Call{Ceiling: TierSafe})
	if Classify(err).Code != CodeForbidden {
		t.Errorf("compose down under safe: %v, want forbidden", err)
	}
}
