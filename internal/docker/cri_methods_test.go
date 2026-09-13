package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/i18n"
)

// criLastCall returns the most recent crictl argv, joined.
func criLastCall(fr *fakeRunner) string {
	if len(fr.calls) == 0 {
		return ""
	}
	return strings.Join(fr.calls[len(fr.calls)-1], " ")
}

func TestCRIListContainers(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{"ps -o json": criPSJSON, "pods -o json": criPodsJSON}, nil)}
	b := newCRITestBackend(fr)
	ctrs, err := b.ListContainers(true)
	if err != nil || len(ctrs) == 0 {
		t.Fatalf("ListContainers = %d, %v", len(ctrs), err)
	}
	if got := strings.Join(fr.calls[0], " "); got != "ps -o json -a" {
		t.Errorf("ps argv = %q, want -a for showAll", got)
	}

	// Without the sandbox listing, names fall back to the kubelet pod label.
	fr = &fakeRunner{fn: byArgs(map[string]string{"ps -o json": criPSJSON}, map[string]error{"pods": errors.New("rpc error")})}
	ctrs, err = newCRITestBackend(fr).ListContainers(false)
	if err != nil {
		t.Fatalf("ListContainers without pods: %v", err)
	}
	found := false
	for _, c := range ctrs {
		if c.Name == "web-abc/nginx" {
			found = true
		}
	}
	if !found {
		t.Errorf("containers = %+v, want web-abc/nginx via pod label", ctrs)
	}

	fr = &fakeRunner{fn: byArgs(map[string]string{"ps -o json": criPSJSON, "pods -o json": "not json"}, nil)}
	if ctrs, err := newCRITestBackend(fr).ListContainers(false); err != nil || len(ctrs) == 0 {
		t.Errorf("unparsable pods must not break the listing: %d, %v", len(ctrs), err)
	}

	fr = &fakeRunner{fn: byArgs(nil, map[string]error{"ps": errors.New("connection refused")})}
	if _, err := newCRITestBackend(fr).ListContainers(false); err == nil || !strings.Contains(err.Error(), "list containers") {
		t.Errorf("ps failure = %v", err)
	}
}

func TestCRIContainerOps(t *testing.T) {
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"inspect c1":  `{"status":{"id":"c1","state":"CONTAINER_RUNNING"}}`,
		"inspect bad": `not json`,
		"exec c1 ls":  "etc/\nhosts\n",
	}, map[string]error{
		"inspect gone": errors.New("container not found"),
		"stop c2":      errors.New("stop failed"),
		"exec c3":      errors.New("ls: /nope: No such file or directory"),
	})}
	b := newCRITestBackend(fr)

	res, err := b.InspectContainer("c1")
	if err != nil || !strings.Contains(res.RawYAML, "CONTAINER_RUNNING") {
		t.Errorf("inspect = %+v, %v", res, err)
	}
	if _, err := b.InspectContainer("gone"); err == nil || !strings.Contains(err.Error(), "inspect container") {
		t.Errorf("inspect failure = %v", err)
	}
	if _, err := b.InspectContainer("bad"); err == nil {
		t.Error("invalid inspect JSON must fail")
	}

	ops := []struct {
		op   func() error
		want string
	}{
		{func() error { return b.StartContainer("c1") }, "start c1"},
		{func() error { return b.StopContainer("c1") }, "stop c1"},
		{func() error { return b.RemoveContainer("c1", false) }, "rm c1"},
		{func() error { return b.RemoveContainer("c1", true) }, "rm -f c1"},
		{func() error { return b.RestartContainer("c1") }, "start c1"},
	}
	for _, tc := range ops {
		if err := tc.op(); err != nil {
			t.Errorf("%s: %v", tc.want, err)
		}
		if got := criLastCall(fr); got != tc.want {
			t.Errorf("last argv = %q, want %q", got, tc.want)
		}
	}
	calls := len(fr.calls)
	if err := b.RestartContainer("c2"); err == nil || err.Error() != "stop failed" {
		t.Errorf("restart with failing stop = %v", err)
	}
	if len(fr.calls) != calls+1 {
		t.Error("restart must not start the container after a failed stop")
	}

	entries, err := b.ListPath("c1", " ")
	if err != nil || len(entries) != 2 || !entries[0].IsDir || entries[1].Name != "hosts" {
		t.Errorf("ListPath = %+v, %v", entries, err)
	}
	if got := criLastCall(fr); got != "exec c1 ls -1Ap -- /" {
		t.Errorf("ls argv = %q, want default dir /", got)
	}
	want := friendlyListErr("/nope", "ls: /nope: No such file or directory")
	if _, err := b.ListPath("c3", "/nope"); err == nil || err.Error() != want.Error() {
		t.Errorf("ListPath error = %v, want %v", err, want)
	}

	if _, _, err := b.ContainerLogs("c1", LogOptions{Tail: 5}); err != nil || criLastCall(fr) != "logs -f --timestamps --tail 5 c1" {
		t.Errorf("logs argv = %q, %v", criLastCall(fr), err)
	}
	if _, _, err := b.Events(); err != nil || criLastCall(fr) != "events" {
		t.Errorf("events argv = %q, %v", criLastCall(fr), err)
	}
	if _, err := b.ExecInteractive("c1", []string{"bash"}); err != nil || !strings.HasPrefix(criLastCall(fr), "exec -i -t c1") {
		t.Errorf("exec argv = %q, %v", criLastCall(fr), err)
	}

	b.endpoint = "unix:///run/crio/crio.sock"
	if err := b.Ping(); err != nil || criLastCall(fr) != "-r unix:///run/crio/crio.sock version" {
		t.Errorf("ping argv = %q, %v", criLastCall(fr), err)
	}
	b.Close()
	failing := newCRITestBackend(&fakeRunner{fn: byArgs(nil, map[string]error{"version": errors.New("socket missing")})})
	if err := failing.Ping(); err == nil {
		t.Error("ping must surface a runtime failure")
	}
}

func TestCRIImageOps(t *testing.T) {
	const pruneOut = "Deleted: sha256:aaa\nDeleted: sha256:bbb\n"
	usedErr := errors.New("image is being used by running container")
	fr := &fakeRunner{fn: byArgs(map[string]string{
		"inspecti img1": `{"status":{"id":"img1","repoTags":["nginx:1.25"]}}`,
		"rmi --prune":   pruneOut,
	}, map[string]error{
		"inspecti gone": errors.New("no such image"),
		"pull bad":      errors.New("manifest unknown"),
		"rmi used":      usedErr,
	})}
	b := newCRITestBackend(fr)

	if res, err := b.InspectImage("img1"); err != nil || !strings.Contains(res.RawYAML, "nginx:1.25") {
		t.Errorf("inspect image = %+v, %v", res, err)
	}
	if _, err := b.InspectImage("gone"); err == nil || !strings.Contains(err.Error(), "inspect image") {
		t.Errorf("inspect image failure = %v", err)
	}
	if err := b.RemoveImage("img1", true); err != nil || criLastCall(fr) != "rmi img1" {
		t.Errorf("rmi argv = %q, %v (force has no CRI flag)", criLastCall(fr), err)
	}
	if err := b.RemoveImage("used", false); err == nil || err.Error() != friendlyImageRemoveErr(usedErr).Error() {
		t.Errorf("rmi in-use error = %v", err)
	}
	if err := b.PullImage("nginx:1.25"); err != nil || criLastCall(fr) != "pull nginx:1.25" {
		t.Errorf("pull argv = %q, %v", criLastCall(fr), err)
	}
	if err := b.PullImage("bad"); err == nil || !strings.Contains(err.Error(), "pull image") {
		t.Errorf("pull failure = %v", err)
	}
	if n, err := b.PruneImages(); err != nil || n != countDeleted(pruneOut) {
		t.Errorf("prune = %d, %v", n, err)
	}
	if report, err := b.SystemPrune(); err != nil || !strings.Contains(report, "sha256:aaa") {
		t.Errorf("system prune = %q, %v", report, err)
	}

	if report, err := newCRITestBackend(&fakeRunner{}).SystemPrune(); err != nil ||
		!strings.Contains(report, i18n.T("нечего удалять", "nothing to remove")) {
		t.Errorf("empty prune report = %q, %v", report, err)
	}
	failing := newCRITestBackend(&fakeRunner{fn: byArgs(nil, map[string]error{"rmi": errors.New("boom")})})
	if _, err := failing.PruneImages(); err == nil || !strings.Contains(err.Error(), "prune images") {
		t.Errorf("prune failure = %v", err)
	}
	if _, err := failing.SystemPrune(); err == nil || !strings.Contains(err.Error(), "system prune") {
		t.Errorf("system prune failure = %v", err)
	}
}

// Everything CRI does not model must fail with the shared explanation rather
// than a confusing crictl error.
func TestCRIUnsupportedOperations(t *testing.T) {
	b := newCRITestBackend(&fakeRunner{})
	errs := map[string]error{
		"run":             b.RunContainer(RunOptions{}),
		"tag":             b.TagImage("a", "b"),
		"cp from":         b.CopyFromContainer("c", "/x", "."),
		"cp to":           b.CopyToContainer("c", "x", "/"),
		"rm network":      b.RemoveNetwork("n"),
		"create network":  b.CreateNetwork(NetworkCreateOptions{}),
		"rm volume":       b.RemoveVolume("v"),
		"create volume":   b.CreateVolume(VolumeCreateOptions{}),
		"compose start":   b.ComposeStart("p"),
		"compose stop":    b.ComposeStop("p"),
		"compose restart": b.ComposeRestart("p"),
		"compose pause":   b.ComposePause("p"),
		"compose unpause": b.ComposeUnpause("p"),
		"compose remove":  b.ComposeRemove("p"),
		"write compose":   b.WriteComposeFile("p", "x"),
	}
	var err error
	_, errs["run interactive"] = b.RunInteractive(ExecRunOptions{})
	_, _, errs["push"] = b.PushImage("a", RegistryAuth{})
	_, _, errs["build"] = b.BuildImage(".", "t")
	_, errs["history"] = b.ImageHistory("i")
	_, errs["inspect network"] = b.InspectNetwork("n")
	_, errs["inspect volume"] = b.InspectVolume("v")
	_, errs["prune volumes"] = b.PruneVolumes()
	_, errs["compose containers"] = b.ListComposeContainers("p")
	_, errs["inspect compose"] = b.InspectComposeProject("p")
	_, _, errs["compose logs"] = b.ComposeLogs("p", LogOptions{})
	_, _, errs["compose up"] = b.ComposeUp("p")
	_, _, errs["compose pull"] = b.ComposePull("p")
	_, _, errs["compose down"] = b.ComposeDown("p")
	_, errs["compose config"] = b.ComposeConfig("p")
	_, _, errs["read compose"] = b.ReadComposeFile("p")
	_, _, errs["create compose"] = b.CreateComposeFile("d", "c")
	_, errs["backup"] = b.BackupComposeProject("p")
	_, _, errs["restore"] = b.RestoreComposeProject("p", "f")
	for name, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "CRI") {
			t.Errorf("%s: err = %v, want the CRI explanation", name, err)
		}
	}

	nets, err := b.ListNetworks()
	if err != nil || len(nets) != 0 {
		t.Errorf("networks = %v, %v; want empty", nets, err)
	}
	vols, err := b.ListVolumes()
	if err != nil || len(vols) != 0 {
		t.Errorf("volumes = %v, %v; want empty", vols, err)
	}
	projects, err := b.ListComposeProjects()
	if err != nil || len(projects) != 0 {
		t.Errorf("compose projects = %v, %v; want empty", projects, err)
	}
	if b.SupportsHostCompose() {
		t.Error("CRI has no host compose operations")
	}
}

func TestNewCRIBackendLocal(t *testing.T) {
	b, err := New(&config.Config{Host: "crio:///run/crio/crio.sock"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cb, ok := b.(*criBackend)
	if !ok {
		t.Fatalf("backend is %T, want *criBackend", b)
	}
	if !cb.local || cb.endpoint != "unix:///run/crio/crio.sock" {
		t.Errorf("local = %v endpoint = %q", cb.local, cb.endpoint)
	}
	if _, ok := cb.runner.(localRunner); !ok {
		t.Errorf("runner is %T, want localRunner", cb.runner)
	}
}
