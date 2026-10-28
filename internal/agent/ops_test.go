package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kirg0/d9c/internal/docker"
)

// invoke runs a built-in op on b and fails the test on error.
func invoke(t *testing.T, b docker.Backend, name string, args Args) Result {
	t.Helper()
	res, err := Builtin().Invoke(context.Background(), &Env{Backend: b}, name, args, TierRead)
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return res
}

// invokeErr runs a built-in op on b and returns its classified error code.
func invokeErr(t *testing.T, b docker.Backend, name string, args Args) *Error {
	t.Helper()
	_, err := Builtin().Invoke(context.Background(), &Env{Backend: b}, name, args, TierRead)
	if err == nil {
		t.Fatalf("%s %v: want an error", name, args)
	}
	return Classify(err)
}

// Every connected op reports a missing connection as internal, not a panic.
func TestOpsWithoutBackend(t *testing.T) {
	r := Builtin()
	args := map[string]Args{
		"inspect": {"target": "x"}, "logs": {"container": "x"},
		"compose ps": {"project": "x"}, "compose config": {"project": "x"}, "compose logs": {"project": "x"},
		"start": {"targets": []string{"x"}}, "stop": {"targets": []string{"x"}}, "restart": {"targets": []string{"x"}},
		"kill": {"targets": []string{"x"}}, "rm": {"targets": []string{"x"}},
		"pull": {"image": "x"}, "compose up": {"project": "x"}, "compose down": {"project": "x"},
		"compose pull": {"project": "x"}, "compose restart": {"project": "x"}, "compose backup": {"project": "x"},
		"compose restore": {"project": "x", "file": "x"},
		"network create":  {"name": "x"}, "volume create": {"name": "x"},
	}
	for _, op := range r.Ops() {
		if op.Local {
			continue
		}
		_, err := r.Invoke(context.Background(), &Env{}, op.Name, args[op.Name], TierDestructive)
		if e := Classify(err); e.Code != CodeInternal {
			t.Errorf("%s without backend: err = %v, want internal", op.Name, err)
		}
	}
}

// The data of every op must marshal: it is the JSON contract.
func TestOpsMarshal(t *testing.T) {
	statsWindow = 0
	t.Cleanup(func() { statsWindow = time.Second })
	fb := docker.NewFakeBackend()
	calls := map[string]Args{
		"ps": nil, "inspect": {"target": "web"}, "logs": {"container": "web"}, "stats": nil,
		"images": nil, "networks": nil, "volumes": nil, "system df": nil, "events": nil,
		"compose ls": nil, "compose ps": {"project": "webapp"}, "compose config": {"project": "webapp"},
		"compose logs": {"project": "webapp"}, "info": nil,
	}
	for name, args := range calls {
		res := invoke(t, fb, name, args)
		if _, err := json.Marshal(NewEnvelope("demo", docker.RuntimeDocker, res)); err != nil {
			t.Errorf("%s: marshal: %v", name, err)
		}
	}
}

func TestPs(t *testing.T) {
	fb := docker.NewFakeBackend()
	running := invoke(t, fb, "ps", nil).Data.([]ContainerEntry)
	all := invoke(t, fb, "ps", Args{"all": true}).Data.([]ContainerEntry)
	if len(running) != 2 || len(all) != 3 {
		t.Fatalf("ps = %d running / %d all, want 2 / 3", len(running), len(all))
	}
	web := all[0]
	if web.ID != "9ae942fd8fbc" || web.Name != "web" || web.Health != "healthy" ||
		web.Ports != "0.0.0.0:8080->80/tcp" || !reflect.DeepEqual(web.Networks, []string{"bridge", "frontend"}) {
		t.Errorf("web = %+v", web)
	}
	if _, err := time.Parse(time.RFC3339, web.Created); err != nil || !strings.HasSuffix(web.Created, "Z") {
		t.Errorf("created = %q, want RFC3339 UTC", web.Created)
	}
}

func TestContainerEntriesEmpty(t *testing.T) {
	got := containerEntries(nil)
	if got == nil || len(got) != 0 {
		t.Errorf("containerEntries(nil) = %#v, want empty non-nil", got)
	}
	if timeString(time.Time{}) != "" {
		t.Error("zero time must render empty")
	}
}

func TestInspect(t *testing.T) {
	fb := docker.NewFakeBackend()
	tests := []struct {
		args     Args
		wantName string
		wantKey  string
	}{
		{Args{"target": "web"}, "web", "Image"},
		{Args{"target": "a08c488a9779", "type": "image"}, "nginx:1.25", "RepoTags"},
		{Args{"target": "webapp", "type": "compose"}, "/srv/webapp", "services"},
	}
	for _, tt := range tests {
		d := invoke(t, fb, "inspect", tt.args).Data.(InspectData)
		obj, ok := d.Object.(map[string]any)
		if d.Name != tt.wantName || !ok || obj[tt.wantKey] == nil {
			t.Errorf("inspect %v = %+v, want name %q and key %q", tt.args, d, tt.wantName, tt.wantKey)
		}
		if want := tt.args.String("type"); want != "" && d.Type != want || want == "" && d.Type != kindContainer {
			t.Errorf("inspect %v type = %q", tt.args, d.Type)
		}
	}
	for _, kind := range []string{"network", "volume"} {
		target := map[string]string{"network": "f0a1b2c3d4e5", "volume": "pgdata"}[kind]
		if _, err := Builtin().Invoke(context.Background(), &Env{Backend: fb}, "inspect", Args{"target": target, "type": kind}, TierRead); err != nil {
			t.Errorf("inspect %s: %v", kind, err)
		}
	}

	if e := invokeErr(t, fb, "inspect", Args{"target": "ghost"}); e.Code != CodeNotFound {
		t.Errorf("missing container: code = %s, want not_found", e.Code)
	}
	if e := invokeErr(t, fb, "inspect", Args{"target": "web", "type": "pod"}); e.Code != CodeUsage {
		t.Errorf("bad type: code = %s, want usage", e.Code)
	}
}

func TestDecodeYAML(t *testing.T) {
	tests := []struct {
		raw  string
		want any
	}{
		{"Id: abc\nState:\n  Running: true\n", map[string]any{"Id": "abc", "State": map[string]any{"Running": true}}},
		{"1: one\n2: [a, {3: x}]\n", map[string]any{"1": "one", "2": []any{"a", map[string]any{"3": "x"}}}},
		{"TYPE  TOTAL\n", "TYPE  TOTAL"}, // a scalar document stays a value
		{"", ""},
		{"a: [unclosed\n", "a: [unclosed\n"}, // not YAML: verbatim
	}
	for _, tt := range tests {
		got := decodeYAML(tt.raw)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("decodeYAML(%q) = %#v, want %#v", tt.raw, got, tt.want)
		}
		if _, err := json.Marshal(got); err != nil {
			t.Errorf("decodeYAML(%q) does not marshal: %v", tt.raw, err)
		}
	}
}

// linesBackend serves canned log lines and records the options it got.
type linesBackend struct {
	*docker.FakeBackend
	lines []string
	opts  []docker.LogOptions
	err   error
}

func (b *linesBackend) stream(opts docker.LogOptions, prefix string) (<-chan string, func(), error) {
	b.opts = append(b.opts, opts)
	if b.err != nil {
		return nil, nil, b.err
	}
	ch := make(chan string, len(b.lines))
	for _, l := range b.lines {
		ch <- prefix + l
	}
	close(ch)
	return ch, func() {}, nil
}

func (b *linesBackend) ContainerLogs(_ string, opts docker.LogOptions) (<-chan string, func(), error) {
	return b.stream(opts, "")
}

func (b *linesBackend) ComposeLogs(_ string, opts docker.LogOptions) (<-chan string, func(), error) {
	return b.stream(opts, "")
}

func numbered(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %d", i)
	}
	return out
}

func TestLogs(t *testing.T) {
	tests := []struct {
		name          string
		lines         []string
		args          Args
		wantLines     int
		wantTruncated bool
		wantHint      string
	}{
		{"default tail, short log", numbered(5), Args{"container": "web"}, 5, false, ""},
		{"full tail", numbered(3), Args{"container": "web", "tail": 3}, 3, true, "raise --tail"},
		{"all lines", numbered(300), Args{"container": "web", "tail": 0}, 300, false, ""},
		{"hard cap", numbered(maxLogLines + 5), Args{"container": "web", "tail": 0}, maxLogLines, true, "stopped at"},
		{"grep", []string{"a ok", "b ERROR x", "c error", "d ERROR y"}, Args{"container": "web", "grep": "ERROR"}, 2, false, ""},
		{"grep keeps truncation", []string{"x", "y"}, Args{"container": "web", "tail": 2, "grep": "z"}, 0, true, "raise --tail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &linesBackend{FakeBackend: docker.NewFakeBackend(), lines: tt.lines}
			res := invoke(t, b, "logs", tt.args)
			d := res.Data.(LogsData)
			if len(d.Lines) != tt.wantLines || d.Lines == nil || d.Target != "web" {
				t.Errorf("lines = %d (nil %v) target %q, want %d", len(d.Lines), d.Lines == nil, d.Target, tt.wantLines)
			}
			if res.Truncated != tt.wantTruncated || !strings.Contains(res.Hint, tt.wantHint) {
				t.Errorf("truncated = %v hint %q, want %v %q", res.Truncated, res.Hint, tt.wantTruncated, tt.wantHint)
			}
		})
	}

	// The window arguments reach the engine as a one-shot query.
	b := &linesBackend{FakeBackend: docker.NewFakeBackend()}
	invoke(t, b, "logs", Args{"container": "web", "since": "1h", "until": "5m"})
	want := docker.LogOptions{Tail: defaultLogTail, Since: "1h", Until: "5m", NoFollow: true}
	if len(b.opts) != 1 || b.opts[0] != want {
		t.Errorf("log options = %+v, want %+v", b.opts, want)
	}

	if e := invokeErr(t, b, "logs", Args{"container": "web", "grep": "("}); e.Code != CodeUsage || e.Hint == "" {
		t.Errorf("bad grep: %+v, want usage with hint", e)
	}
	if e := invokeErr(t, docker.NewFakeBackend(), "logs", Args{"container": "ghost"}); e.Code != CodeNotFound {
		t.Errorf("missing container: code = %s, want not_found", e.Code)
	}
	if e := invokeErr(t, b, "logs", Args{"container": "web", "tail": -1}); e.Code != CodeUsage {
		t.Errorf("negative tail: code = %s, want usage", e.Code)
	}
}

func TestDrain(t *testing.T) {
	stopped := 0
	stop := func() { stopped++ }

	ch := make(chan string, 3)
	ch <- "a"
	ch <- "b"
	ch <- "c"
	close(ch)
	lines, capped, err := drain(context.Background(), ch, stop, 2)
	if err != nil || !capped || !reflect.DeepEqual(lines, []string{"a", "b"}) {
		t.Errorf("capped drain = %q %v %v", lines, capped, err)
	}

	ch = make(chan string, 2)
	ch <- "a"
	ch <- "b"
	close(ch)
	if lines, capped, _ = drain(context.Background(), ch, stop, 2); capped || len(lines) != 2 {
		t.Errorf("exact max = %q capped %v, want 2 lines uncapped", lines, capped)
	}

	// A cancelled context ends a stream that never closes.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = drain(ctx, make(chan string), stop, 10); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled drain err = %v", err)
	}
	if stopped != 3 {
		t.Errorf("stop called %d times, want 3", stopped)
	}
}

// statsBackend counts ContainerStats calls and reports a CPU only from the
// second one on, like an engine deriving CPU% from counter deltas.
type statsBackend struct {
	*docker.FakeBackend
	calls int
	err   error
}

func (b *statsBackend) ContainerStats(ids []string) (map[string]docker.ContainerStats, error) {
	b.calls++
	if b.err != nil {
		return nil, b.err
	}
	out, _ := b.FakeBackend.ContainerStats(ids)
	for id, s := range out {
		if b.calls == 1 {
			s.CPUPerc = 0
		} else {
			s.CPUPerc = 12.3456
		}
		out[id] = s
	}
	return out, nil
}

func TestStats(t *testing.T) {
	statsWindow = 0
	t.Cleanup(func() { statsWindow = time.Second })

	b := &statsBackend{FakeBackend: docker.NewFakeBackend()}
	got := invoke(t, b, "stats", nil).Data.([]StatsEntry)
	if b.calls != 2 {
		t.Errorf("ContainerStats calls = %d, want 2 (baseline + sample)", b.calls)
	}
	if len(got) != 2 || got[0].Name != "web" || got[0].CPUPerc != 12.35 || got[0].MemUsage != 48*1024*1024 {
		t.Errorf("stats = %+v", got)
	}

	got = invoke(t, &statsBackend{FakeBackend: docker.NewFakeBackend()}, "stats", Args{"container": []string{"api"}}).Data.([]StatsEntry)
	if len(got) != 1 || got[0].ID != "d2c94e258dcb" {
		t.Errorf("filtered stats = %+v", got)
	}

	// No running containers: no sampling, an empty list.
	idle := &statsBackend{FakeBackend: docker.NewFakeBackend()}
	idle.Containers = nil
	if got := invoke(t, idle, "stats", nil).Data.([]StatsEntry); got == nil || len(got) != 0 || idle.calls != 0 {
		t.Errorf("idle stats = %#v after %d calls", got, idle.calls)
	}

	if e := invokeErr(t, b, "stats", Args{"once": false}); e.Code != CodeUsage {
		t.Errorf("--once=false: code = %s, want usage", e.Code)
	}
	if e := invokeErr(t, b, "stats", Args{"container": []string{"db"}}); e.Code != CodeNotFound {
		t.Errorf("stopped container: code = %s, want not_found", e.Code)
	}
	failing := &statsBackend{FakeBackend: docker.NewFakeBackend(), err: errors.New("dial tcp: connection refused")}
	if e := invokeErr(t, failing, "stats", nil); e.Code != CodeConnection {
		t.Errorf("engine failure: code = %s, want connection", e.Code)
	}

	// A cancelled context interrupts the sampling pause.
	statsWindow = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Builtin().Invoke(ctx, &Env{Backend: &statsBackend{FakeBackend: docker.NewFakeBackend()}}, "stats", nil, TierRead); err == nil {
		t.Error("cancelled stats should fail")
	}
}

func TestPickContainers(t *testing.T) {
	list := []docker.Container{{ID: "aaa111", Name: "web"}, {ID: "bbb222", Name: "/api"}, {ID: "aab333", Name: "db"}}
	tests := []struct {
		refs    []string
		wantIDs []string
		wantErr bool
	}{
		{nil, []string{"aaa111", "bbb222", "aab333"}, false},
		{[]string{"web"}, []string{"aaa111"}, false},
		{[]string{"api"}, []string{"bbb222"}, false},
		{[]string{"aa"}, []string{"aaa111", "aab333"}, false},  // ID prefix
		{[]string{"web", "aaa111"}, []string{"aaa111"}, false}, // deduplicated
		{[]string{"web", "nope"}, nil, true},
	}
	for _, tt := range tests {
		got, err := pickContainers(list, tt.refs)
		if tt.wantErr {
			if Classify(err).Code != CodeNotFound {
				t.Errorf("pick %q: err = %v, want not_found", tt.refs, err)
			}
			continue
		}
		var ids []string
		for _, c := range got {
			ids = append(ids, c.ID)
		}
		if err != nil || !reflect.DeepEqual(ids, tt.wantIDs) {
			t.Errorf("pick %q = %v (%v), want %v", tt.refs, ids, err, tt.wantIDs)
		}
	}
}

func TestListings(t *testing.T) {
	fb := docker.NewFakeBackend()
	images := invoke(t, fb, "images", nil).Data.([]ImageEntry)
	if len(images) != 4 || images[0] != (ImageEntry{ID: "a08c488a9779", Tags: "nginx:1.25", Size: "187 MB", Created: timeString(fb.Images[0].Created)}) {
		t.Errorf("images = %+v", images)
	}
	networks := invoke(t, fb, "networks", nil).Data.([]NetworkEntry)
	if len(networks) != 3 || networks[1] != (NetworkEntry{ID: "1122334455aa", Name: "host", Driver: "host", Scope: "local"}) {
		t.Errorf("networks = %+v", networks)
	}
	volumes := invoke(t, fb, "volumes", nil).Data.([]VolumeEntry)
	if len(volumes) != 2 || volumes[0].Name != "pgdata" || volumes[0].Mountpoint == "" {
		t.Errorf("volumes = %+v", volumes)
	}

	empty := docker.NewFakeBackend()
	empty.Images, empty.Networks, empty.Volumes = nil, nil, nil
	for _, name := range []string{"images", "networks", "volumes"} {
		b, _ := json.Marshal(invoke(t, empty, name, nil).Data)
		if string(b) != "[]" {
			t.Errorf("empty %s = %s, want []", name, b)
		}
	}
}

func TestParseDF(t *testing.T) {
	dockerDF := "TYPE              TOTAL   ACTIVE         SIZE    RECLAIMABLE\n" +
		"Images                4        2     631.0 MB         5.0 MB\n" +
		"Local Volumes         2        1     256.0 MB        32.0 MB\n"
	nerdctlDF := "TYPE            TOTAL    ACTIVE   SIZE\n" +
		"Images          3        -        631MB\n" +
		"Containers      2        1        -\n"
	tests := []struct {
		name string
		text string
		want SystemDFData
	}{
		{"docker", dockerDF, SystemDFData{Rows: []map[string]string{
			{"type": "Images", "total": "4", "active": "2", "size": "631.0 MB", "reclaimable": "5.0 MB"},
			{"type": "Local Volumes", "total": "2", "active": "1", "size": "256.0 MB", "reclaimable": "32.0 MB"},
		}, columns: []string{"type", "total", "active", "size", "reclaimable"}}},
		{"nerdctl", nerdctlDF, SystemDFData{Rows: []map[string]string{
			{"type": "Images", "total": "3", "active": "-", "size": "631MB"},
			{"type": "Containers", "total": "2", "active": "1", "size": "-"},
		}, columns: []string{"type", "total", "active", "size"}}},
		{"not a table", "nothing to see\nhere", SystemDFData{Rows: []map[string]string{}, Report: "nothing to see\nhere"}},
		{"ragged row", "TYPE  TOTAL\nImages  1  2\n", SystemDFData{Rows: []map[string]string{}, Report: "TYPE  TOTAL\nImages  1  2"}},
		{"empty", "", SystemDFData{Rows: []map[string]string{}}},
	}
	for _, tt := range tests {
		if got := parseDF(tt.text); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parseDF = %#v, want %#v", tt.name, got, tt.want)
		}
	}

	d := invoke(t, docker.NewFakeBackend(), "system df", nil).Data.(SystemDFData)
	if len(d.Rows) != 4 || d.Rows[3]["type"] != "Build Cache" || d.Report != "" {
		t.Errorf("fake system df = %+v", d)
	}
}

// eventsBackend records the window it was asked for and serves n lines,
// then tail when set (an in-band stream error).
type eventsBackend struct {
	*docker.FakeBackend
	got  docker.EventOptions
	n    int
	tail string
	err  error
}

func (b *eventsBackend) Events(opts docker.EventOptions) (<-chan string, func(), error) {
	b.got = opts
	if b.err != nil {
		return nil, nil, b.err
	}
	ch := make(chan string, b.n+1)
	for i := 0; i < b.n; i++ {
		ch <- fmt.Sprintf("event %d", i)
	}
	if b.tail != "" {
		ch <- b.tail
	}
	close(ch)
	return ch, func() {}, nil
}

func TestEvents(t *testing.T) {
	b := &eventsBackend{FakeBackend: docker.NewFakeBackend(), n: 3}
	before := time.Now().Add(-time.Second)
	res := invoke(t, b, "events", nil)
	d := res.Data.(EventsData)
	until, err := time.Parse(time.RFC3339, b.got.Until)
	if err != nil || until.Before(before.Truncate(time.Second)) || b.got.Since != "10m" {
		t.Errorf("window = %+v, want since 10m until now", b.got)
	}
	if d.Since != "10m" || d.Until != b.got.Until || len(d.Events) != 3 || res.Truncated {
		t.Errorf("events = %+v truncated %v", d, res.Truncated)
	}

	invoke(t, b, "events", Args{"since": "2h", "until": "1h"})
	if b.got != (docker.EventOptions{Since: "2h", Until: "1h"}) {
		t.Errorf("explicit window = %+v", b.got)
	}

	b.n = maxEvents + 1
	if res := invoke(t, b, "events", nil); !res.Truncated || len(res.Data.(EventsData).Events) != maxEvents {
		t.Errorf("capped events: truncated %v, %d events", res.Truncated, len(res.Data.(EventsData).Events))
	}

	// The daemon fails the stream in band: never a window without events.
	b.n = 1
	b.tail = docker.EventErrorPrefix + `failed to parse value as time or duration: "bad"`
	if e := invokeErr(t, b, "events", Args{"until": "bad"}); e.Code != CodeUsage || e.Hint == "" || !strings.Contains(e.Message, `"bad"`) {
		t.Errorf("bad window: %+v", e)
	}
	b.tail = docker.EventErrorPrefix + "unexpected EOF"
	if e := invokeErr(t, b, "events", nil); e.Code != CodeConnection || !strings.Contains(e.Message, "unexpected EOF") {
		t.Errorf("broken stream: %+v", e)
	}
	b.tail = ""

	b.err = errors.New("event history (--since/--until) is not supported by containerd (nerdctl)")
	if e := invokeErr(t, b, "events", nil); e.Code != CodeInternal || !strings.Contains(e.Message, "not supported") {
		t.Errorf("unsupported window: %+v", e)
	}
}
