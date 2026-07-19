package docker

import (
	"strings"
	"testing"
	"time"
)

func newCRITestBackend(fr *fakeRunner) *criBackend {
	return &criBackend{runner: fr}
}

func TestIsCRIHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"crio://", true},
		{"crio", true},
		{"cri://", true},
		{"cri:///var/run/crio/crio.sock", true},
		{"crio+ssh://user@host", true},
		{"cri+ssh://user@host/run/containerd/containerd.sock", true},
		{"nerdctl://", false},
		{"ssh://user@host", false},
		{"tcp://host:2375", false},
		{"crioX://", false},
	}
	for _, tt := range tests {
		if got := isCRIHost(tt.host); got != tt.want {
			t.Errorf("isCRIHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestSplitCRIHost(t *testing.T) {
	tests := []struct {
		host         string
		wantSSH      string
		wantEndpoint string
	}{
		{"crio://", "", ""},
		{"crio:///var/run/crio/crio.sock", "", "unix:///var/run/crio/crio.sock"},
		{"cri:///run/containerd/containerd.sock", "", "unix:///run/containerd/containerd.sock"},
		{"crio+ssh://user@host", "user@host", ""},
		{"crio+ssh://user@host:2222", "user@host:2222", ""},
		{"crio+ssh://user@host/run/crio/crio.sock", "user@host", "unix:///run/crio/crio.sock"},
		{"cri+ssh://host/var/run/x.sock", "host", "unix:///var/run/x.sock"},
	}
	for _, tt := range tests {
		ssh, ep := splitCRIHost(tt.host)
		if ssh != tt.wantSSH || ep != tt.wantEndpoint {
			t.Errorf("splitCRIHost(%q) = (%q, %q), want (%q, %q)",
				tt.host, ssh, ep, tt.wantSSH, tt.wantEndpoint)
		}
	}
}

func TestCRIArgsEndpoint(t *testing.T) {
	b := newCRITestBackend(&fakeRunner{})
	if got := b.args("ps", "-a"); strings.Join(got, " ") != "ps -a" {
		t.Errorf("args without endpoint = %v", got)
	}
	b.endpoint = "unix:///run/crio/crio.sock"
	if got := b.args("ps"); strings.Join(got, " ") != "-r unix:///run/crio/crio.sock ps" {
		t.Errorf("args with endpoint = %v", got)
	}
}

const criPSJSON = `{
  "containers": [
    {
      "id": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
      "podSandboxId": "podsandbox1",
      "metadata": {"name": "nginx", "attempt": 0},
      "image": {"image": "docker.io/library/nginx:1.25"},
      "imageRef": "sha256:aaaa",
      "state": "CONTAINER_RUNNING",
      "createdAt": "1700000000000000000",
      "labels": {"io.kubernetes.pod.name": "web-abc", "app": "web"}
    },
    {
      "id": "1111111111111111111111111111111111111111111111111111111111111111",
      "podSandboxId": "podsandbox2",
      "metadata": {"name": "job"},
      "image": {"image": "sha256:bbbb0123456789"},
      "imageRef": "sha256:bbbb0123456789",
      "state": "CONTAINER_EXITED",
      "createdAt": "1700000000000000000",
      "labels": {}
    }
  ]
}`

const criPodsJSON = `{
  "items": [
    {"id": "podsandbox1", "metadata": {"name": "web-abc", "namespace": "default"}, "state": "SANDBOX_READY"}
  ]
}`

func TestParseCRIContainers(t *testing.T) {
	rows, err := parseCRIContainers(criPSJSON)
	if err != nil {
		t.Fatalf("parseCRIContainers: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	pods, err := parseCRIPods(criPodsJSON)
	if err != nil {
		t.Fatalf("parseCRIPods: %v", err)
	}

	c := rows[0].toContainer(pods)
	if c.ID != "abcdef012345" {
		t.Errorf("ID = %q", c.ID)
	}
	if c.Name != "web-abc/nginx" {
		t.Errorf("Name = %q, want pod/container", c.Name)
	}
	if c.Image != "docker.io/library/nginx:1.25" {
		t.Errorf("Image = %q", c.Image)
	}
	if c.State != "running" {
		t.Errorf("State = %q", c.State)
	}
	if !strings.HasPrefix(c.Status, "Up ") {
		t.Errorf("Status = %q, want Up …", c.Status)
	}
	if c.Created.Unix() != 1700000000 {
		t.Errorf("Created = %v", c.Created)
	}
	if c.Labels["app"] != "web" {
		t.Errorf("Labels = %v", c.Labels)
	}

	// Second row: pod not in the sandbox map and no kubelet label → bare name;
	// digest-only image falls back to the shortened digest.
	c2 := rows[1].toContainer(pods)
	if c2.Name != "job" {
		t.Errorf("Name = %q, want bare name", c2.Name)
	}
	if c2.State != "exited" {
		t.Errorf("State = %q", c2.State)
	}
	if c2.Image != "bbbb01234567" {
		t.Errorf("Image = %q, want shortened digest", c2.Image)
	}
}

func TestToContainerPodLabelFallback(t *testing.T) {
	rows, err := parseCRIContainers(criPSJSON)
	if err != nil {
		t.Fatalf("parseCRIContainers: %v", err)
	}
	// No pod map at all → kubelet label supplies the pod name.
	c := rows[0].toContainer(nil)
	if c.Name != "web-abc/nginx" {
		t.Errorf("Name = %q, want label fallback pod/container", c.Name)
	}
}

func TestCRIState(t *testing.T) {
	tests := []struct{ in, want string }{
		{"CONTAINER_RUNNING", "running"},
		{"CONTAINER_EXITED", "exited"},
		{"CONTAINER_CREATED", "created"},
		{"CONTAINER_UNKNOWN", "unknown"},
	}
	for _, tt := range tests {
		if got := criState(tt.in); got != tt.want {
			t.Errorf("criState(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCRIStatusLine(t *testing.T) {
	created := time.Now().Add(-2 * time.Hour)
	if got := criStatusLine("CONTAINER_RUNNING", created); !strings.HasPrefix(got, "Up 2 hours") {
		t.Errorf("running status = %q", got)
	}
	if got := criStatusLine("CONTAINER_EXITED", created); !strings.HasSuffix(got, "ago") {
		t.Errorf("exited status = %q", got)
	}
	if got := criStatusLine("CONTAINER_CREATED", created); got != "Created" {
		t.Errorf("created status = %q", got)
	}
	if got := criStatusLine("CONTAINER_RUNNING", time.Time{}); got != "Up" {
		t.Errorf("running status without time = %q", got)
	}
}

func TestCRIImageDisplay(t *testing.T) {
	tests := []struct{ image, ref, want string }{
		{"docker.io/nginx:1.25", "sha256:aaa", "docker.io/nginx:1.25"},
		{"sha256:bbbb0123456789", "docker.io/nginx:1.25", "docker.io/nginx:1.25"},
		{"sha256:bbbb0123456789", "sha256:bbbb0123456789", "bbbb01234567"},
		{"", "", ""},
	}
	for _, tt := range tests {
		if got := criImageDisplay(tt.image, tt.ref); got != tt.want {
			t.Errorf("criImageDisplay(%q, %q) = %q, want %q", tt.image, tt.ref, got, tt.want)
		}
	}
}

func TestBuildCRILogsArgs(t *testing.T) {
	got := buildCRILogsArgs("abc", LogOptions{Tail: 100, Since: "1h", Until: "30m"})
	want := "logs -f --timestamps --tail 100 --since 1h abc"
	if strings.Join(got, " ") != want {
		t.Errorf("buildCRILogsArgs = %v, want %q (Until must be ignored)", got, want)
	}
	got = buildCRILogsArgs("abc", LogOptions{})
	if strings.Join(got, " ") != "logs -f --timestamps abc" {
		t.Errorf("buildCRILogsArgs default = %v", got)
	}
}

func TestCRIKillContainer(t *testing.T) {
	fr := &fakeRunner{}
	b := newCRITestBackend(fr)
	if err := b.KillContainer("abc", ""); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := b.KillContainer("abc", "SIGKILL"); err != nil {
		t.Fatalf("kill SIGKILL: %v", err)
	}
	for _, call := range fr.calls {
		if strings.Join(call, " ") != "stop --timeout 0 abc" {
			t.Errorf("kill call = %v", call)
		}
	}
	if err := b.KillContainer("abc", "HUP"); err == nil {
		t.Error("kill HUP: want error, got nil")
	}
}

const criImagesJSON = `{
  "images": [
    {"id": "sha256:cccc0123456789cccc", "repoTags": ["nginx:1.25"], "repoDigests": ["nginx@sha256:x"], "size": "187553280"},
    {"id": "sha256:dddd0123456789dddd", "repoTags": [], "size": "0"}
  ]
}`

func TestCRIListImages(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		return criImagesJSON, nil
	}}
	b := newCRITestBackend(fr)
	images, err := b.ListImages()
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("got %d images, want 2", len(images))
	}
	if images[0].ID != "cccc01234567" {
		t.Errorf("ID = %q", images[0].ID)
	}
	if images[0].Tags != "nginx:1.25" {
		t.Errorf("Tags = %q", images[0].Tags)
	}
	if !strings.HasPrefix(images[0].Size, "187.6") {
		t.Errorf("Size = %q, want humanized ~187.6MB", images[0].Size)
	}
	if images[1].Tags != "<none>" || images[1].Size != "-" {
		t.Errorf("untagged image = %+v", images[1])
	}
}

const criStatsJSON = `{
  "stats": [
    {
      "attributes": {"id": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"},
      "cpu": {"timestamp": "2000000000", "usageCoreNanoSeconds": {"value": "1500000000"}},
      "memory": {"workingSetBytes": {"value": "104857600"}}
    }
  ]
}`

func TestCRIContainerStats(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		return criStatsJSON, nil
	}}
	b := newCRITestBackend(fr)

	// First sample: no previous point → CPU% 0, memory reported.
	stats, err := b.ContainerStats([]string{"abcdef012345"})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	s, ok := stats["abcdef012345"]
	if !ok {
		t.Fatalf("stats missing container: %v", stats)
	}
	if s.CPUPerc != 0 {
		t.Errorf("first-sample CPUPerc = %v, want 0", s.CPUPerc)
	}
	if s.MemUsage != 104857600 {
		t.Errorf("MemUsage = %d", s.MemUsage)
	}

	// Second sample 1s later with +0.5 core-seconds → 50%.
	fr.fn = func(args []string) (string, error) {
		return strings.NewReplacer(
			`"timestamp": "2000000000"`, `"timestamp": "3000000000"`,
			`"value": "1500000000"`, `"value": "2000000000"`,
		).Replace(criStatsJSON), nil
	}
	stats, err = b.ContainerStats([]string{"abcdef012345"})
	if err != nil {
		t.Fatalf("stats 2: %v", err)
	}
	if got := stats["abcdef012345"].CPUPerc; got != 50 {
		t.Errorf("CPUPerc = %v, want 50", got)
	}
}

func TestCRICPUPercent(t *testing.T) {
	tests := []struct {
		name      string
		prev, cur criCPUSample
		want      float64
	}{
		{"first sample", criCPUSample{}, criCPUSample{usageNano: 100, timestamp: 200}, 0},
		{"counter reset", criCPUSample{usageNano: 500, timestamp: 100}, criCPUSample{usageNano: 100, timestamp: 200}, 0},
		{"same timestamp", criCPUSample{usageNano: 100, timestamp: 200}, criCPUSample{usageNano: 200, timestamp: 200}, 0},
		{"full core", criCPUSample{usageNano: 0, timestamp: 0}, criCPUSample{usageNano: 100, timestamp: 100}, 0},
		{"half core", criCPUSample{usageNano: 100, timestamp: 100}, criCPUSample{usageNano: 600, timestamp: 1100}, 50},
	}
	for _, tt := range tests {
		if got := criCPUPercent(tt.prev, tt.cur); got != tt.want {
			t.Errorf("%s: criCPUPercent = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestParseCRIVersion(t *testing.T) {
	out := "Version:  0.1.0\nRuntimeName:  cri-o\nRuntimeVersion:  1.28.1\nRuntimeApiVersion:  v1\n"
	v := parseCRIVersion(out)
	if v.RuntimeName != "cri-o" || v.RuntimeVersion != "1.28.1" {
		t.Errorf("parseCRIVersion = %+v", v)
	}
}

func TestCRIRuntime(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		return "RuntimeName: cri-o\nRuntimeVersion: 1.28.1", nil
	}}
	b := newCRITestBackend(fr)
	if got := b.Runtime(); got != RuntimeCRIO {
		t.Errorf("Runtime = %v, want cri-o", got)
	}
	// Cached: no second version call needed.
	calls := len(fr.calls)
	if got := b.Runtime(); got != RuntimeCRIO || len(fr.calls) != calls {
		t.Errorf("Runtime not cached: %v calls", len(fr.calls))
	}

	fr2 := &fakeRunner{fn: func(args []string) (string, error) {
		return "RuntimeName: containerd\nRuntimeVersion: 1.7.0", nil
	}}
	if got := newCRITestBackend(fr2).Runtime(); got != RuntimeCRI {
		t.Errorf("Runtime = %v, want generic cri", got)
	}
}

func TestCRIRuntimeLabels(t *testing.T) {
	if RuntimeCRIO.Label() != "cri-o" || RuntimeCRI.Label() != "cri" {
		t.Errorf("labels = %q / %q", RuntimeCRIO.Label(), RuntimeCRI.Label())
	}
}

func TestCRIDegradedSections(t *testing.T) {
	b := newCRITestBackend(&fakeRunner{})
	if nets, err := b.ListNetworks(); err != nil || len(nets) != 0 {
		t.Errorf("ListNetworks = %v, %v; want empty, nil", nets, err)
	}
	if vols, err := b.ListVolumes(); err != nil || len(vols) != 0 {
		t.Errorf("ListVolumes = %v, %v; want empty, nil", vols, err)
	}
	if projects, err := b.ListComposeProjects(); err != nil || len(projects) != 0 {
		t.Errorf("ListComposeProjects = %v, %v; want empty, nil", projects, err)
	}
	if b.SupportsHostCompose() {
		t.Error("SupportsHostCompose = true, want false")
	}
	for name, err := range map[string]error{
		"RunContainer":  b.RunContainer(RunOptions{Image: "x"}),
		"TagImage":      b.TagImage("a", "b"),
		"CreateNetwork": b.CreateNetwork(NetworkCreateOptions{Name: "n"}),
		"CreateVolume":  b.CreateVolume(VolumeCreateOptions{Name: "v"}),
		"ComposeStart":  b.ComposeStart("p"),
	} {
		if err == nil {
			t.Errorf("%s: want unsupported error, got nil", name)
		}
	}
}

func TestCRIInfo(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		switch args[0] {
		case "ps":
			return criPSJSON, nil
		case "images":
			return criImagesJSON, nil
		case "version":
			return "RuntimeName: cri-o\nRuntimeVersion: 1.28.1", nil
		}
		return "", nil
	}}
	b := newCRITestBackend(fr)
	info, err := b.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Containers != 2 || info.Running != 1 || info.Stopped != 1 {
		t.Errorf("counts = %+v", info)
	}
	if info.Images != 2 || info.Version != "1.28.1" || info.Name != "cri-o" {
		t.Errorf("summary = %+v", info)
	}
}

func TestCRISystemDF(t *testing.T) {
	fr := &fakeRunner{fn: func(args []string) (string, error) {
		switch args[0] {
		case "ps":
			return criPSJSON, nil
		case "images":
			return criImagesJSON, nil
		}
		return "", nil
	}}
	b := newCRITestBackend(fr)
	res, err := b.SystemDF()
	if err != nil {
		t.Fatalf("SystemDF: %v", err)
	}
	if !strings.Contains(res.RawYAML, "Images") || !strings.Contains(res.RawYAML, "Containers") {
		t.Errorf("df report = %q", res.RawYAML)
	}
}
