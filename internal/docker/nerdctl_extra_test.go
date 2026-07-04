package docker

import (
	"errors"
	"strings"
	"testing"
)

// byArgs builds a fakeRunner fn dispatching on the joined argv (namespace
// prefix included) via substring match, first hit wins.
func byArgs(rules map[string]string, errRules map[string]error) func([]string) (string, error) {
	return func(args []string) (string, error) {
		joined := strings.Join(args, " ")
		for sub, err := range errRules {
			if strings.Contains(joined, sub) {
				return "", err
			}
		}
		for sub, out := range rules {
			if strings.Contains(joined, sub) {
				return out, nil
			}
		}
		return "", nil
	}
}

const nerdctlPSJSON = `{"ID":"aaa111","Names":"web-1","Image":"nginx:1.25","Status":"Up 2 hours","Ports":"8080->80/tcp","CreatedAt":"2026-01-01 10:00:00 +0000 UTC","Labels":"com.docker.compose.project=web,com.docker.compose.service=web"}
{"ID":"bbb222","Names":"db-1","Image":"postgres:16","Status":"Exited (0) 1 hour ago","Ports":"","CreatedAt":"2026-01-01 09:00:00 +0000 UTC","Labels":"com.docker.compose.project=web,com.docker.compose.service=db"}`

func TestNerdctlImagesOps(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"inspect img1": `[{"Id":"img1","RepoTags":["nginx:1.25"]}]`,
		"image prune":  "sha256:aaa\nsha256:bbb\nTotal reclaimed space: 5MB\n",
		"history":      "LAYER OUTPUT",
		"images":       `{"ID":"sha256:aaa111","Repository":"nginx","Tag":"1.25","Size":"187MB","CreatedAt":"2026-01-01 10:00:00 +0000 UTC"}` + "\n" + `{"ID":"bbb222","Repository":"<none>","Tag":"<none>","Size":"5MB","CreatedAt":""}`,
	}, map[string]error{
		"rmi bad": errors.New("image has dependent child"),
	})}
	b := newTestBackend(fr)

	imgs, err := b.ListImages()
	if err != nil || len(imgs) != 2 {
		t.Fatalf("images = %d (%v)", len(imgs), err)
	}
	if imgs[0].Tags != "nginx:1.25" || imgs[1].Tags != "<none>" {
		t.Errorf("tags = %q / %q", imgs[0].Tags, imgs[1].Tags)
	}

	if res, err := b.InspectImage("img1"); err != nil || !strings.Contains(res.RawYAML, "nginx:1.25") {
		t.Errorf("inspect = %+v / %v", res, err)
	}
	if err := b.RemoveImage("ok", true); err != nil {
		t.Errorf("rmi -f: %v", err)
	}
	if err := b.RemoveImage("bad", false); err == nil {
		t.Error("dependent-child rmi should fail (friendly)")
	}
	if err := b.PullImage("nginx:1.25"); err != nil {
		t.Errorf("pull: %v", err)
	}
	if n, err := b.PruneImages(); err != nil || n != 2 {
		t.Errorf("prune = %d / %v, want 2", n, err)
	}
	if err := b.TagImage("a", "b"); err != nil {
		t.Errorf("tag: %v", err)
	}
	if res, err := b.ImageHistory("img1"); err != nil || res.RawYAML != "LAYER OUTPUT" {
		t.Errorf("history = %+v / %v", res, err)
	}
}

func TestNerdctlPushBuild(t *testing.T) {
	fr := &fakeRunner{}
	b := newTestBackend(fr)

	// Anonymous push: no login call, straight to stream.
	if _, _, err := b.PushImage("reg.local/app:v1", RegistryAuth{}); err != nil {
		t.Fatalf("anonymous push: %v", err)
	}
	// Authed push: login first (registry inferred from the ref).
	if _, _, err := b.PushImage("reg.local/app:v1", RegistryAuth{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("authed push: %v", err)
	}
	sawLogin := false
	for _, call := range fr.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "login reg.local -u u -p p") {
			sawLogin = true
		}
	}
	if !sawLogin {
		t.Errorf("expected a login call, calls: %v", fr.calls)
	}

	// Failed login aborts the push.
	frFail := &fakeRunner{fn: byArgs(nil, map[string]error{"login": errors.New("denied")})}
	bFail := newTestBackend(frFail)
	if _, _, err := bFail.PushImage("app:v1", RegistryAuth{Username: "u"}); err == nil {
		t.Error("push after failed login should fail")
	}

	// Build with and without tag.
	if _, _, err := b.BuildImage("/ctx", "app:v1"); err != nil {
		t.Errorf("build: %v", err)
	}
	if _, _, err := b.BuildImage("/ctx", ""); err != nil {
		t.Errorf("untagged build: %v", err)
	}
}

func TestNerdctlNetworkVolumeOps(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"network inspect": `[{"Name":"bridge"}]`,
		"network ls":      `{"ID":"0123456789abcdef","Name":"bridge","Driver":"bridge","Scope":"local"}`,
		"volume inspect":  `[{"Name":"pgdata"}]`,
		"volume ls":       `{"Name":"pgdata","Driver":"local","Mountpoint":"/x"}`,
		"volume prune":    "vol1\nTotal reclaimed space: 1MB\n",
	}, nil)}
	b := newTestBackend(fr)

	nets, err := b.ListNetworks()
	if err != nil || len(nets) != 1 || nets[0].ID != "0123456789ab" {
		t.Fatalf("networks = %+v / %v", nets, err)
	}
	if res, err := b.InspectNetwork("bridge"); err != nil || !strings.Contains(res.RawYAML, "bridge") {
		t.Errorf("inspect net = %+v / %v", res, err)
	}
	if err := b.RemoveNetwork("bridge"); err != nil {
		t.Errorf("rm net: %v", err)
	}
	if err := b.CreateNetwork(NetworkCreateOptions{}); err == nil {
		t.Error("empty net name should fail")
	}
	if err := b.CreateNetwork(NetworkCreateOptions{Name: "n1", Driver: "bridge", Subnet: "10.0.0.0/24", Gateway: "10.0.0.1"}); err != nil {
		t.Errorf("create net: %v", err)
	}

	vols, err := b.ListVolumes()
	if err != nil || len(vols) != 1 || vols[0].Name != "pgdata" {
		t.Fatalf("volumes = %+v / %v", vols, err)
	}
	if res, err := b.InspectVolume("pgdata"); err != nil || !strings.Contains(res.RawYAML, "pgdata") {
		t.Errorf("inspect vol = %+v / %v", res, err)
	}
	if err := b.RemoveVolume("pgdata"); err != nil {
		t.Errorf("rm vol: %v", err)
	}
	if err := b.CreateVolume(VolumeCreateOptions{}); err == nil {
		t.Error("empty vol name should fail")
	}
	if err := b.CreateVolume(VolumeCreateOptions{Name: "v1", Driver: "local"}); err != nil {
		t.Errorf("create vol: %v", err)
	}
	if n, err := b.PruneVolumes(); err != nil || n != 1 {
		t.Errorf("prune vols = %d / %v, want 1", n, err)
	}
}

func TestNerdctlContainerOps(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"inspect ctr1": `[{"Id":"ctr1","Name":"web"}]`,
	}, nil)}
	b := newTestBackend(fr)

	if res, err := b.InspectContainer("ctr1"); err != nil || !strings.Contains(res.RawYAML, "web") {
		t.Errorf("inspect = %+v / %v", res, err)
	}
	for name, op := range map[string]func(string) error{
		"start":   b.StartContainer,
		"stop":    b.StopContainer,
		"restart": b.RestartContainer,
	} {
		if err := op("ctr1"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := b.RemoveContainer("ctr1", true); err != nil {
		t.Errorf("rm -f: %v", err)
	}
	if err := b.KillContainer("ctr1", "TERM"); err != nil {
		t.Errorf("kill -s: %v", err)
	}
	if err := b.KillContainer("ctr1", ""); err != nil {
		t.Errorf("kill: %v", err)
	}

	if _, _, err := b.ContainerLogs("ctr1", LogOptions{Tail: 5, Since: "1h", Until: "now"}); err != nil {
		t.Errorf("logs: %v", err)
	}
	if _, err := b.ExecInteractive("ctr1", nil); err != nil {
		t.Errorf("exec: %v", err)
	}
}

func TestNerdctlRunOps(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(nil, map[string]error{"run -d --name taken": errors.New("is already in use")})}
	b := newTestBackend(fr)

	if err := b.RunContainer(RunOptions{}); err == nil {
		t.Error("empty image should fail")
	}
	if err := b.RunContainer(RunOptions{Image: "nginx", Name: "taken"}); err == nil {
		t.Error("name conflict should fail (friendly)")
	}
	if err := b.RunContainer(RunOptions{Image: "nginx", Name: "ok", Ports: []string{"80:80"}, Env: []string{"A=1"}, Volumes: []string{"/h:/c"}}); err != nil {
		t.Errorf("run: %v", err)
	}
	last := strings.Join(fr.calls[len(fr.calls)-1], " ")
	for _, want := range []string{"--name ok", "-p 80:80", "-e A=1", "-v /h:/c", "nginx"} {
		if !strings.Contains(last, want) {
			t.Errorf("run argv %q should contain %q", last, want)
		}
	}

	if _, err := b.RunInteractive(ExecRunOptions{}); err == nil {
		t.Error("empty image should fail")
	}
	if _, err := b.RunInteractive(ExecRunOptions{Image: "nginx", Volumes: []string{"/h:/c"}, Cmd: []string{"sh"}}); err != nil {
		t.Errorf("run -it: %v", err)
	}
}

func TestNerdctlStats(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"stats --no-stream": `{"ID":"aaa111bbb222ccc","CPUPerc":"5.00%","MemUsage":"10MiB / 2GiB","MemPerc":"0.49%","NetIO":"1.2kB / 600B","BlockIO":"4MiB / 1MiB"}
not-json`,
	}, nil)}
	b := newTestBackend(fr)

	if out, err := b.ContainerStats(nil); err != nil || len(out) != 0 {
		t.Errorf("empty ids = %v / %v", out, err)
	}
	out, err := b.ContainerStats([]string{"aaa111bbb222ccc"})
	if err != nil || len(out) != 1 {
		t.Fatalf("stats = %v / %v", out, err)
	}
	s := out["aaa111bbb222"]
	if s.CPUPerc != 5 || s.MemUsage != 10*1024*1024 || s.MemLimit != 2*1024*1024*1024 {
		t.Errorf("stats = %+v", s)
	}
	if s.NetRx != 1228 || s.BlockRead != 4*1024*1024 { // RAMInBytes: 1.2kB = 1.2*1024
		t.Errorf("io = %+v", s)
	}
}

func TestNerdctlListPathAndCopy(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"ls -1Ap": "app/\nfile.txt\n",
	}, nil)}
	b := newTestBackend(fr)

	entries, err := b.ListPath("ctr1", "")
	if err != nil || len(entries) != 2 || !entries[0].IsDir || entries[1].IsDir {
		t.Errorf("entries = %+v / %v", entries, err)
	}
	frErr := &fakeRunner{fn: byArgs(nil, map[string]error{"ls -1Ap": errors.New("No such file or directory")})}
	bErr := newTestBackend(frErr)
	if _, err := bErr.ListPath("ctr1", "/ghost"); err == nil {
		t.Error("missing dir should fail")
	}

	// Remote (ssh) transport: cp is unsupported.
	if err := b.CopyFromContainer("ctr1", "/a", "."); err == nil {
		t.Error("remote copy-from should be unsupported")
	}
	if err := b.CopyToContainer("ctr1", "x", "/"); err == nil {
		t.Error("remote copy-to should be unsupported")
	}
	// Local transport: cp shells out.
	b.local = true
	if err := b.CopyFromContainer("ctr1", "/a", ""); err != nil {
		t.Errorf("local copy-from: %v", err)
	}
	if err := b.CopyToContainer("ctr1", "x", ""); err != nil {
		t.Errorf("local copy-to: %v", err)
	}
}

func TestNerdctlSystemAndInfo(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"system prune": " reclaimed stuff \n",
		"ps --format":  nerdctlPSJSON,
		"images":       `{"ID":"aaa","Repository":"nginx","Tag":"1.25","Size":"187MB"}`,
		"info --format": `WARN[0000] some warning
{"Name":"host1","NCPU":4,"MemTotal":1024,"ServerVersion":"2.0.0"}`,
	}, nil)}
	b := newTestBackend(fr)

	if out, err := b.SystemPrune(); err != nil || out != "reclaimed stuff" {
		t.Errorf("prune = %q / %v", out, err)
	}
	if _, _, err := b.Events(); err != nil {
		t.Errorf("events: %v", err)
	}

	info, err := b.Info()
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Containers != 2 || info.Running != 1 || info.Stopped != 1 || info.Images != 1 {
		t.Errorf("info counts = %+v", info)
	}
	if info.Name != "host1" || info.Version != "2.0.0" || info.NCPU != 4 {
		t.Errorf("info identity = %+v", info)
	}
}

func TestNerdctlComposeOps(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"ps --format": nerdctlPSJSON,
	}, nil)}
	b := newTestBackend(fr)

	ctrs, err := b.ListComposeContainers("web")
	if err != nil || len(ctrs) != 2 {
		t.Fatalf("compose containers = %d / %v", len(ctrs), err)
	}

	res, err := b.InspectComposeProject("web")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, want := range []string{"project: web", "status: partial", "web", "db", "nginx:1.25"} {
		if !strings.Contains(res.RawYAML, want) {
			t.Errorf("inspect should contain %q:\n%s", want, res.RawYAML)
		}
	}
	if _, err := b.InspectComposeProject("ghost"); err == nil {
		t.Error("unknown project should fail")
	}

	ch, stop, err := b.ComposeLogs("web", LogOptions{})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	stop()
	for range ch {
	}
	if _, _, err := b.ComposeLogs("ghost", LogOptions{}); err == nil {
		t.Error("logs of unknown project should fail")
	}

	for name, op := range map[string]func(string) error{
		"start":   b.ComposeStart,
		"stop":    b.ComposeStop,
		"restart": b.ComposeRestart,
		"pause":   b.ComposePause,
		"unpause": b.ComposeUnpause,
		"remove":  b.ComposeRemove,
	} {
		if err := op("web"); err != nil {
			t.Errorf("compose %s: %v", name, err)
		}
		if err := op("ghost"); err == nil {
			t.Errorf("compose %s of unknown project should fail", name)
		}
	}

	if !b.SupportsHostCompose() {
		t.Error("nerdctl should support host compose")
	}

	// File-level compose ops are unsupported on containerd.
	if _, _, err := b.ReadComposeFile("web"); !errors.Is(err, errComposeFilesUnsupported) {
		t.Errorf("read = %v", err)
	}
	if err := b.WriteComposeFile("web", "x"); !errors.Is(err, errComposeFilesUnsupported) {
		t.Errorf("write = %v", err)
	}
	if _, _, err := b.CreateComposeFile("/d", "x"); !errors.Is(err, errComposeFilesUnsupported) {
		t.Errorf("create = %v", err)
	}
	if _, err := b.BackupComposeProject("web"); !errors.Is(err, errComposeFilesUnsupported) {
		t.Errorf("backup = %v", err)
	}
	if _, _, err := b.RestoreComposeProject("web", "f"); !errors.Is(err, errComposeFilesUnsupported) {
		t.Errorf("restore = %v", err)
	}
}

func TestNerdctlIdentityOps(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"namespace ls -q": "default\nk8s.io\n",
	}, nil)}
	b := newTestBackend(fr)

	if got := b.CurrentNamespace(); got != "default" {
		t.Errorf("namespace = %q", got)
	}
	b.SetNamespace("  ") // blank is ignored
	if got := b.CurrentNamespace(); got != "default" {
		t.Errorf("namespace after blank set = %q", got)
	}
	ns, err := b.Namespaces()
	if err != nil || len(ns) != 2 || ns[1] != "k8s.io" {
		t.Errorf("namespaces = %v / %v", ns, err)
	}
	if got := b.Runtime(); got != RuntimeContainerd {
		t.Errorf("runtime = %v", got)
	}
	if err := b.Ping(); err != nil {
		t.Errorf("ping: %v", err)
	}
	b.Close()
}
