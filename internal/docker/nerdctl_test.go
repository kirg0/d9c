package docker

import (
	"reflect"
	"strings"
	"testing"
)

// fakeRunner is an in-memory nerdctlRunner: fn produces canned output for a
// command's argv (and records every call), so the backend's argv assembly and
// output parsing are testable without a real nerdctl.
type fakeRunner struct {
	fn    func(args []string) (string, error)
	calls [][]string
}

func (r *fakeRunner) output(args []string) (string, error) {
	r.calls = append(r.calls, args)
	if r.fn != nil {
		return r.fn(args)
	}
	return "", nil
}

func (r *fakeRunner) stream(args []string) (<-chan string, func(), error) {
	r.calls = append(r.calls, args)
	ch := make(chan string)
	close(ch)
	return ch, func() {}, nil
}

func (r *fakeRunner) interactive(args []string) (ExecSession, error) {
	r.calls = append(r.calls, args)
	return nil, nil
}

func (r *fakeRunner) close() {}

func newTestBackend(fr *fakeRunner) *nerdctlBackend {
	return &nerdctlBackend{runner: fr, namespace: defaultNamespace}
}

func TestIsNerdctlHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"nerdctl://", true},
		{"nerdctl:", true},
		{"nerdctl", true},
		{"nerdctl+ssh://user@host", true},
		{"nerdctl+ssh://user@host:2222", true},
		{"ssh://user@host", false},
		{"tcp://host:2375", false},
		{"unix:///run/containerd/containerd.sock", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isNerdctlHost(c.host); got != c.want {
			t.Errorf("isNerdctlHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestArgsNamespaceInjection(t *testing.T) {
	b := newTestBackend(&fakeRunner{})
	got := b.args("ps", "-a")
	want := []string{"--namespace", "default", "ps", "-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args() = %v, want %v", got, want)
	}
	b.SetNamespace("k8s.io")
	got = b.args("ps")
	want = []string{"--namespace", "k8s.io", "ps"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after SetNamespace: args() = %v, want %v", got, want)
	}
}

func TestBuildLogsArgs(t *testing.T) {
	cases := []struct {
		name string
		opts LogOptions
		want []string
	}{
		{"defaults", LogOptions{}, []string{"logs", "-f", "--timestamps", "abc"}},
		{"tail", LogOptions{Tail: 100}, []string{"logs", "-f", "--timestamps", "--tail", "100", "abc"}},
		{"since+until", LogOptions{Since: "1h", Until: "10m"}, []string{"logs", "-f", "--timestamps", "--since", "1h", "--until", "10m", "abc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildLogsArgs("abc", c.opts); !reflect.DeepEqual(got, c.want) {
				t.Errorf("buildLogsArgs = %v, want %v", got, c.want)
			}
		})
	}
}

func TestStateFromStatus(t *testing.T) {
	cases := map[string]string{
		"Up 3 minutes":          "running",
		"Up 2 hours (healthy)":  "running",
		"Up 5 minutes (Paused)": "paused",
		"Exited (0) 1 hour ago": "exited",
		"Created":               "created",
		"Restarting (1) 2s ago": "restarting",
		"Dead":                  "dead",
	}
	for status, want := range cases {
		if got := stateFromStatus(status); got != want {
			t.Errorf("stateFromStatus(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestParseLabels(t *testing.T) {
	got := parseLabels("com.docker.compose.project=web,com.docker.compose.service=api")
	if got["com.docker.compose.project"] != "web" || got["com.docker.compose.service"] != "api" {
		t.Errorf("parseLabels = %v", got)
	}
	if parseLabels("") != nil {
		t.Errorf("parseLabels(\"\") should be nil")
	}
}

func TestImageTags(t *testing.T) {
	cases := []struct{ repo, tag, want string }{
		{"nginx", "1.25", "nginx:1.25"},
		{"<none>", "<none>", "<none>"},
		{"repo", "<none>", "<none>"},
		{"", "latest", "<none>"},
	}
	for _, c := range cases {
		if got := imageTags(c.repo, c.tag); got != c.want {
			t.Errorf("imageTags(%q,%q) = %q, want %q", c.repo, c.tag, got, c.want)
		}
	}
}

func TestParsePercentAndSize(t *testing.T) {
	if got := parsePercent("5.00%"); got != 5.0 {
		t.Errorf("parsePercent = %v", got)
	}
	if got := parsePercent("--"); got != 0 {
		t.Errorf("parsePercent(--) = %v, want 0", got)
	}
	if got := parseSize("10MiB"); got != 10*1024*1024 {
		t.Errorf("parseSize(10MiB) = %d", got)
	}
	if got := parseSize("--"); got != 0 {
		t.Errorf("parseSize(--) = %d, want 0", got)
	}
}

func TestSplitSlash(t *testing.T) {
	l, r := splitSlash("10MiB / 2GiB")
	if l != "10MiB" || r != "2GiB" {
		t.Errorf("splitSlash = %q,%q", l, r)
	}
	l, r = splitSlash("solo")
	if l != "solo" || r != "" {
		t.Errorf("splitSlash(solo) = %q,%q", l, r)
	}
}

func TestCountDeleted(t *testing.T) {
	out := "sha256:aaa\nsha256:bbb\nTotal reclaimed space: 12MB\n"
	if got := countDeleted(out); got != 2 {
		t.Errorf("countDeleted = %d, want 2", got)
	}
}

func TestListContainersParsesPS(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		return `{"ID":"abc123","Names":"web","Image":"nginx:1.25","Status":"Up 3 minutes","Ports":"80/tcp","Labels":"com.docker.compose.project=site"}
{"ID":"def456","Names":"db","Image":"postgres:16","Status":"Exited (0) 1 hour ago"}`, nil
	}}
	b := newTestBackend(fr)
	list, err := b.ListContainers(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d containers, want 2", len(list))
	}
	if list[0].Name != "web" || list[0].State != "running" || list[0].Image != "nginx:1.25" {
		t.Errorf("container[0] = %+v", list[0])
	}
	if list[1].State != "exited" {
		t.Errorf("container[1].State = %q, want exited", list[1].State)
	}
	// The command must be namespace-scoped and request JSON.
	last := strings.Join(fr.calls[len(fr.calls)-1], " ")
	if !strings.Contains(last, "--namespace default") || !strings.Contains(last, jsonFormat) {
		t.Errorf("ps call = %q", last)
	}
}

func TestComposeDiscovery(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		return `{"ID":"1","Names":"site-web","Image":"nginx","Status":"Up 1 minute","Labels":"com.docker.compose.project=site,com.docker.compose.service=web"}
{"ID":"2","Names":"site-db","Image":"postgres","Status":"Up 1 minute","Labels":"com.docker.compose.project=site,com.docker.compose.service=db"}`, nil
	}}
	b := newTestBackend(fr)
	projects, err := b.ListComposeProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Project != "site" || projects[0].Total != 2 {
		t.Fatalf("projects = %+v", projects)
	}
}

func TestNamespacesParsing(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		return "default\nk8s.io\nmoby\n", nil
	}}
	b := newTestBackend(fr)
	names, err := b.Namespaces()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"default", "k8s.io", "moby"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Namespaces = %v, want %v", names, want)
	}
}

func TestRuntimeContainerd(t *testing.T) {
	b := newTestBackend(&fakeRunner{})
	if b.Runtime() != RuntimeContainerd {
		t.Errorf("Runtime = %q, want containerd", b.Runtime())
	}
	if RuntimeContainerd.Label() != "containerd" {
		t.Errorf("Label = %q", RuntimeContainerd.Label())
	}
}

func TestJSONToYAML(t *testing.T) {
	// A single-element array (nerdctl inspect's shape) is unwrapped.
	y, err := jsonToYAML(`[{"Name":"web","Id":"abc"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(y, "Name: web") {
		t.Errorf("yaml = %q", y)
	}
}

func TestSSHRunnerCmdPrependsSbinPath(t *testing.T) {
	r := sshRunner{bin: "nerdctl"}
	got := r.cmd([]string{"run", "-d", "-p", "8080:80", "nginx"})
	// iptables lives in /usr/sbin, absent from a non-interactive SSH PATH.
	if !strings.HasPrefix(got, `PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin" nerdctl `) {
		t.Errorf("cmd() = %q, want PATH augmentation prefix", got)
	}
	if !strings.Contains(got, "'run' '-d' '-p' '8080:80' 'nginx'") {
		t.Errorf("cmd() = %q, want quoted args", got)
	}
}

func TestInfoServerVersion(t *testing.T) {
	// nerdctl reports the server engine version inside Server.Components, not as a
	// flat Server.Version field (real output shape from nerdctl 2.3.4).
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		if len(args) > 0 && args[len(args)-1] == jsonFormat && contains(args, "version") {
			return `{"Client":{"Version":"v2.3.4"},"Server":{"Components":[{"Name":"containerd","Version":"v2.3.2"},{"Name":"runc","Version":"1.5.0"}]}}`, nil
		}
		return "", nil // empty ps/images
	}}
	b := newTestBackend(fr)
	if got := b.serverVersion(); got != "v2.3.2" {
		t.Errorf("serverVersion = %q, want v2.3.2", got)
	}
	sum, err := b.Info()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Version != "v2.3.2" {
		t.Errorf("Info().Version = %q, want v2.3.2", sum.Version)
	}
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// composeFakeRunner serves a two-service project (web/cache) plus its network,
// so the reconstructed compose engine ops can be exercised without nerdctl.
func composeFakeRunner() *fakeRunner {
	return &fakeRunner{fn: func(args []string) (string, error) {
		switch {
		case contains(args, "ps"):
			return `{"ID":"c1","Names":"d9c-demo-web-1","Image":"nginx","Status":"Up","Labels":"com.docker.compose.project=d9c-demo,com.docker.compose.service=web"}
{"ID":"c2","Names":"d9c-demo-cache-1","Image":"redis","Status":"Up","Labels":"com.docker.compose.project=d9c-demo,com.docker.compose.service=cache"}`, nil
		case contains(args, "network") && contains(args, "ls"):
			return `{"ID":"n1","Name":"d9c-demo_default","Labels":"com.docker.compose.project=d9c-demo"}
{"ID":"n2","Name":"bridge","Labels":""}`, nil
		}
		return "", nil
	}}
}

func drain(ch <-chan string) string {
	var lines []string
	for l := range ch {
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

func TestComposeDownReconstructed(t *testing.T) {
	fr := composeFakeRunner()
	b := newTestBackend(fr)
	ch, stop, err := b.ComposeDown("d9c-demo")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	out := drain(ch)
	if !strings.Contains(out, "Removing d9c-demo-web-1") || !strings.Contains(out, "Removing network d9c-demo_default") {
		t.Errorf("down output:\n%s", out)
	}
	var sawRm, sawNetRm bool
	for _, c := range fr.calls {
		if contains(c, "rm") && contains(c, "-f") && contains(c, "c1") {
			sawRm = true
		}
		if contains(c, "network") && contains(c, "rm") && contains(c, "d9c-demo_default") {
			sawNetRm = true
		}
	}
	if !sawRm || !sawNetRm {
		t.Errorf("expected `rm -f` and `network rm`; calls=%v", fr.calls)
	}
}

func TestComposeUpStartsContainers(t *testing.T) {
	fr := composeFakeRunner()
	b := newTestBackend(fr)
	ch, stop, err := b.ComposeUp("d9c-demo")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	out := drain(ch)
	if !strings.Contains(out, "Starting d9c-demo-web-1") || !strings.Contains(out, "Starting d9c-demo-cache-1") {
		t.Errorf("up output:\n%s", out)
	}
	var starts int
	for _, c := range fr.calls {
		if contains(c, "start") && (contains(c, "c1") || contains(c, "c2")) {
			starts++
		}
	}
	if starts != 2 {
		t.Errorf("expected 2 start calls, got %d (calls=%v)", starts, fr.calls)
	}
}

func TestComposePullPullsDistinctImages(t *testing.T) {
	fr := composeFakeRunner()
	b := newTestBackend(fr)
	ch, stop, err := b.ComposePull("d9c-demo")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	out := drain(ch)
	if !strings.Contains(out, "Pulling nginx") || !strings.Contains(out, "Pulling redis") {
		t.Errorf("pull output:\n%s", out)
	}
	var pulls int
	for _, c := range fr.calls {
		if contains(c, "pull") {
			pulls++
		}
	}
	if pulls != 2 {
		t.Errorf("expected 2 pull calls, got %d", pulls)
	}
}

func TestComposeConfigUnsupported(t *testing.T) {
	b := newTestBackend(&fakeRunner{})
	if _, err := b.ComposeConfig("d9c-demo"); err == nil {
		t.Error("ComposeConfig should be unsupported on nerdctl")
	}
}

func TestCopyNeedsLocalOverSSH(t *testing.T) {
	b := &nerdctlBackend{runner: &fakeRunner{}, namespace: defaultNamespace, local: false}
	if err := b.CopyFromContainer("id", "/etc/hosts", "."); err == nil {
		t.Error("CopyFromContainer over ssh should error")
	}
	if err := b.CopyToContainer("id", "./x", "/tmp"); err == nil {
		t.Error("CopyToContainer over ssh should error")
	}
}
