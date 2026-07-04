package docker

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// collect reads a canned stream to completion and returns all lines.
func collect(t *testing.T, ch <-chan string, stop func(), err error) []string {
	t.Helper()
	if err != nil {
		t.Fatalf("stream err: %v", err)
	}
	defer stop()
	var out []string
	for l := range ch {
		out = append(out, l)
	}
	return out
}

const demoWebapp = "/srv/webapp" // compose identity of the demo webapp project

func TestFakeListContainers(t *testing.T) {
	f := NewFakeBackend()
	all, err := f.ListContainers(true)
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %d (%v), want 3", len(all), err)
	}
	running, err := f.ListContainers(false)
	if err != nil || len(running) != 2 {
		t.Fatalf("running = %d (%v), want 2", len(running), err)
	}
}

func TestFakeContainerLifecycle(t *testing.T) {
	f := NewFakeBackend()
	id := "3f1ab77c9012" // db, exited

	if err := f.StartContainer(id); err != nil {
		t.Fatalf("start: %v", err)
	}
	if res, err := f.InspectContainer(id); err != nil || !strings.Contains(res.RawYAML, "State: running") {
		t.Errorf("after start: %v / %v", res, err)
	}
	if err := f.StopContainer(id); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := f.RestartContainer(id); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := f.KillContainer(id, "KILL"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if res, _ := f.InspectContainer(id); !strings.Contains(res.RawYAML, "Exited (137)") {
		t.Error("kill should leave a 137 status")
	}

	if err := f.StartContainer("nope"); err == nil {
		t.Error("start of unknown id should fail")
	}
	if _, err := f.InspectContainer("nope"); err == nil {
		t.Error("inspect of unknown id should fail")
	}
}

func TestFakeRemoveContainer(t *testing.T) {
	f := NewFakeBackend()
	// Running container without force → friendly error.
	if err := f.RemoveContainer("9ae942fd8fbc", false); err == nil {
		t.Error("rm of a running container should fail without force")
	}
	if err := f.RemoveContainer("9ae942fd8fbc", true); err != nil {
		t.Errorf("rm -f: %v", err)
	}
	if err := f.RemoveContainer("3f1ab77c9012", false); err != nil {
		t.Errorf("rm of exited container: %v", err)
	}
	if err := f.RemoveContainer("nope", false); err == nil {
		t.Error("rm of unknown id should fail")
	}
}

func TestFakeContainerStats(t *testing.T) {
	f := NewFakeBackend()
	stats, err := f.ContainerStats([]string{"9ae942fd8fbc", "3f1ab77c9012"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stats["9ae942fd8fbc"]; !ok {
		t.Error("expected stats for the running container")
	}
	if _, ok := stats["3f1ab77c9012"]; ok {
		t.Error("stopped container should have no stats")
	}
}

func TestFakeRunContainerBranches(t *testing.T) {
	f := NewFakeBackend()
	if err := f.RunContainer(RunOptions{}); err == nil {
		t.Error("empty image should fail")
	}
	if err := f.RunContainer(RunOptions{Image: "nginx:1.25", Name: "web"}); err == nil {
		t.Error("duplicate name should fail")
	}
	if err := f.RunContainer(RunOptions{Image: "nginx:1.25", Name: "web2", Ports: []string{"80:80"}}); err != nil {
		t.Errorf("run: %v", err)
	}
	// Unknown image is auto-pulled and registered.
	before := len(f.Images)
	if err := f.RunContainer(RunOptions{Image: "redis:7"}); err != nil {
		t.Errorf("run with auto-pull: %v", err)
	}
	if len(f.Images) != before+1 {
		t.Error("auto-pull should register the image")
	}
}

func TestFakeExecSessionIO(t *testing.T) {
	f := NewFakeBackend()
	s, err := f.ExecInteractive("9ae942fd8fbc", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Banner arrives, split across small reads to hit the remainder path.
	buf := make([]byte, 8)
	n, err := s.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("read banner: n=%d err=%v", n, err)
	}
	if n, err = s.Read(buf); err != nil || n == 0 {
		t.Fatalf("read remainder: n=%d err=%v", n, err)
	}
	// Echo with CR expansion.
	if _, err := s.Write([]byte("ls\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := s.Resize(80, 24); err != nil {
		t.Errorf("resize: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	_ = s.Close() // idempotent
	// After close the echo buffer is never drained, so repeated writes fill it
	// and must eventually surface ErrClosedPipe (send blocked, closed wins).
	sawClosed := false
	for i := 0; i < 500 && !sawClosed; i++ {
		if _, err := s.Write([]byte("xxxxxxxx")); err == io.ErrClosedPipe {
			sawClosed = true
		}
	}
	if !sawClosed {
		t.Error("writes after close should eventually report ErrClosedPipe")
	}
	// Reads drain the leftovers, then EOF.
	for {
		if _, err := s.Read(buf); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
}

func TestFakeRunInteractiveBranches(t *testing.T) {
	f := NewFakeBackend()
	if _, err := f.RunInteractive(ExecRunOptions{}); err == nil {
		t.Error("empty image should fail")
	}
	if _, err := f.RunInteractive(ExecRunOptions{Image: "ghost:1"}); err == nil {
		t.Error("unknown image should fail")
	}
	s, err := f.RunInteractive(ExecRunOptions{Image: "nginx:1.25", Volumes: []string{"/data:/data"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestFakeListPathVariants(t *testing.T) {
	f := NewFakeBackend()
	tests := []struct {
		dir     string
		wantErr bool
	}{
		{"", false}, {"/", false}, {"/app/", false}, {"/var/log", false}, {"/ghost", true},
	}
	for _, tt := range tests {
		if _, err := f.ListPath("id", tt.dir); (err != nil) != tt.wantErr {
			t.Errorf("ListPath(%q) err=%v, wantErr=%v", tt.dir, err, tt.wantErr)
		}
	}
}

func TestFakeCopy(t *testing.T) {
	f := NewFakeBackend()
	dir := t.TempDir()
	if err := f.CopyFromContainer("id", "/app/config.yaml", dir); err != nil {
		t.Fatalf("copy from: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Errorf("downloaded file missing: %v", err)
	}
	// Trailing slash is stripped for the base name; empty source -> "download".
	if err := f.CopyFromContainer("id", "/app/data/", dir); err != nil {
		t.Fatalf("copy of dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data")); err != nil {
		t.Errorf("dir base name missing: %v", err)
	}
	t.Chdir(t.TempDir()) // empty destDir writes into the current directory
	if err := f.CopyFromContainer("id", "", ""); err != nil {
		t.Fatalf("copy of empty path: %v", err)
	}
	if _, err := os.Stat("download"); err != nil {
		t.Errorf("placeholder name missing: %v", err)
	}

	local := filepath.Join(dir, "config.yaml")
	if err := f.CopyToContainer("id", local, "/app"); err != nil {
		t.Errorf("copy to: %v", err)
	}
	if err := f.CopyToContainer("id", filepath.Join(dir, "ghost"), "/app"); err == nil {
		t.Error("copy of missing local file should fail")
	}
}

func TestFakeContainerLogs(t *testing.T) {
	f := NewFakeBackend()
	ch, stop, err := f.ContainerLogs("id", LogOptions{})
	lines := collect(t, ch, stop, err)
	if len(lines) != len(f.LogLines) {
		t.Errorf("logs = %d lines, want %d", len(lines), len(f.LogLines))
	}
}

func TestFakeImages(t *testing.T) {
	f := NewFakeBackend()
	imgs, err := f.ListImages()
	if err != nil || len(imgs) != 4 {
		t.Fatalf("images = %d (%v), want 4", len(imgs), err)
	}
	if _, err := f.InspectImage("a08c488a9779"); err != nil {
		t.Errorf("inspect: %v", err)
	}
	if _, err := f.InspectImage("nope"); err == nil {
		t.Error("inspect unknown should fail")
	}
	if err := f.PullImage("whatever"); err != nil {
		t.Errorf("pull: %v", err)
	}

	// Remove conflicts mirror the daemon.
	if err := f.RemoveImage("hello-world", false); err == nil {
		t.Error("hello-world without force should conflict")
	}
	if err := f.RemoveImage("nginx:1.25", false); err == nil {
		t.Error("nginx without force should conflict")
	}
	if err := f.RemoveImage("c7e8a2b4d6f1", false); err != nil {
		t.Errorf("remove postgres: %v", err)
	}
	if err := f.RemoveImage("nope", false); err == nil {
		t.Error("remove unknown should fail")
	}

	if err := f.TagImage("a08c488a9779", "nginx:mirror"); err != nil {
		t.Errorf("tag: %v", err)
	}
	if err := f.TagImage("nope", "x"); err == nil {
		t.Error("tag of unknown source should fail")
	}

	if _, err := f.ImageHistory("a08c488a9779"); err != nil {
		t.Errorf("history: %v", err)
	}
	if _, err := f.ImageHistory("nope"); err == nil {
		t.Error("history of unknown should fail")
	}

	removed, err := f.PruneImages()
	if err != nil || removed != 1 {
		t.Errorf("prune removed %d (%v), want 1 dangling", removed, err)
	}
}

func TestFakePushAndBuild(t *testing.T) {
	f := NewFakeBackend()
	ch, stop, err := f.PushImage("nginx:1.25", RegistryAuth{})
	if lines := collect(t, ch, stop, err); !containsSub(lines, "anonymous@docker.io") {
		t.Errorf("anonymous push should be labelled, got %v", lines)
	}
	ch, stop, err = f.PushImage("nginx:1.25", RegistryAuth{Registry: "ghcr.io", Username: "bob"})
	if lines := collect(t, ch, stop, err); !containsSub(lines, "bob@ghcr.io") {
		t.Errorf("authed push should echo user@registry, got %v", lines)
	}

	before := len(f.Images)
	ch, stop, err = f.BuildImage(".", "")
	if lines := collect(t, ch, stop, err); !containsSub(lines, "Successfully tagged <none>:<none>") {
		t.Errorf("untagged build output wrong: %v", lines)
	}
	ch, stop, err = f.BuildImage(".", "app:v1")
	if lines := collect(t, ch, stop, err); !containsSub(lines, "Successfully tagged app:v1") {
		t.Errorf("build output wrong: %v", lines)
	}
	if len(f.Images) != before+2 {
		t.Error("each build should register an image")
	}
}

func containsSub(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestFakeNetworks(t *testing.T) {
	f := NewFakeBackend()
	nets, err := f.ListNetworks()
	if err != nil || len(nets) != 3 {
		t.Fatalf("networks = %d (%v), want 3", len(nets), err)
	}
	if _, err := f.InspectNetwork("f0a1b2c3d4e5"); err != nil {
		t.Errorf("inspect: %v", err)
	}
	if _, err := f.InspectNetwork("nope"); err == nil {
		t.Error("inspect unknown should fail")
	}
	if err := f.RemoveNetwork("f0a1b2c3d4e5"); err == nil {
		t.Error("removing built-in bridge should fail")
	}
	if err := f.RemoveNetwork("aabbccddeeff"); err != nil {
		t.Errorf("remove app-net: %v", err)
	}
	if err := f.RemoveNetwork("nope"); err == nil {
		t.Error("remove unknown should fail")
	}

	if err := f.CreateNetwork(NetworkCreateOptions{}); err == nil {
		t.Error("empty name should fail")
	}
	if err := f.CreateNetwork(NetworkCreateOptions{Name: "bridge"}); err == nil {
		t.Error("duplicate name should fail")
	}
	if err := f.CreateNetwork(NetworkCreateOptions{Name: "mynet", Subnet: "10.0.0.0/24"}); err != nil {
		t.Errorf("create: %v", err)
	}
	nets, _ = f.ListNetworks()
	last := nets[len(nets)-1]
	if last.Driver != "bridge" || last.Subnet != "10.0.0.0/24" {
		t.Errorf("created net = %+v, want default bridge driver", last)
	}
}

func TestFakeVolumes(t *testing.T) {
	f := NewFakeBackend()
	vols, err := f.ListVolumes()
	if err != nil || len(vols) != 2 {
		t.Fatalf("volumes = %d (%v), want 2", len(vols), err)
	}
	if _, err := f.InspectVolume("pgdata"); err != nil {
		t.Errorf("inspect: %v", err)
	}
	if _, err := f.InspectVolume("nope"); err == nil {
		t.Error("inspect unknown should fail")
	}
	if err := f.CreateVolume(VolumeCreateOptions{}); err == nil {
		t.Error("empty name should fail")
	}
	if err := f.CreateVolume(VolumeCreateOptions{Name: "pgdata"}); err == nil {
		t.Error("duplicate name should fail")
	}
	if err := f.CreateVolume(VolumeCreateOptions{Name: "newvol"}); err != nil {
		t.Errorf("create: %v", err)
	}
	if err := f.RemoveVolume("newvol"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := f.RemoveVolume("nope"); err == nil {
		t.Error("remove unknown should fail")
	}
	n, err := f.PruneVolumes()
	if err != nil || n != 2 {
		t.Errorf("prune = %d (%v), want 2", n, err)
	}
}

func TestFakeComposeQueries(t *testing.T) {
	f := NewFakeBackend()
	projects, err := f.ListComposeProjects()
	if err != nil || len(projects) != 3 {
		t.Fatalf("projects = %d (%v), want 3", len(projects), err)
	}
	if _, err := f.ListComposeContainers(demoWebapp); err != nil {
		t.Errorf("containers: %v", err)
	}
	if _, err := f.ListComposeContainers("nope"); err == nil {
		t.Error("containers of unknown project should fail")
	}
	if res, err := f.InspectComposeProject(demoWebapp); err != nil || !strings.Contains(res.RawYAML, "project: webapp") {
		t.Errorf("inspect: %v / %v", res, err)
	}
	if _, err := f.InspectComposeProject("nope"); err == nil {
		t.Error("inspect unknown should fail")
	}
	ch, stop, err := f.ComposeLogs(demoWebapp, LogOptions{})
	lines := collect(t, ch, stop, err)
	if len(lines) != len(f.LogLines) {
		t.Errorf("compose logs = %d lines, want %d", len(lines), len(f.LogLines))
	}
	if _, _, err := f.ComposeLogs("nope", LogOptions{}); err == nil {
		t.Error("logs of unknown project should fail")
	}
	if cfg, err := f.ComposeConfig(demoWebapp); err != nil || !strings.Contains(cfg, "services:") {
		t.Errorf("config: %q / %v", cfg, err)
	}
	if _, err := f.ComposeConfig("nope"); err == nil {
		t.Error("config of unknown project should fail")
	}
}

func TestFakeComposeLifecycle(t *testing.T) {
	f := NewFakeBackend()
	const legacy = "/opt/legacy"

	steps := []struct {
		name       string
		op         func(string) error
		wantStatus string
	}{
		{"start", f.ComposeStart, "running"},
		{"pause", f.ComposePause, "paused"},
		{"unpause", f.ComposeUnpause, "running"},
		{"stop", f.ComposeStop, "stopped"},
		{"restart", f.ComposeRestart, "running"},
	}
	for _, s := range steps {
		if err := s.op(legacy); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		projects, _ := f.ListComposeProjects()
		for _, p := range projects {
			if p.WorkingDir == legacy && p.Status != s.wantStatus {
				t.Errorf("%s: status = %q, want %q", s.name, p.Status, s.wantStatus)
			}
		}
		if err := s.op("nope"); err == nil {
			t.Errorf("%s of unknown project should fail", s.name)
		}
	}

	if err := f.ComposeRemove(legacy); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := f.ComposeRemove(legacy); err == nil {
		t.Error("second remove should fail")
	}
}

func TestFakeComposeStreams(t *testing.T) {
	f := NewFakeBackend()

	ch, stop, err := f.ComposeUp(demoWebapp)
	if lines := collect(t, ch, stop, err); !containsSub(lines, "Started") {
		t.Errorf("up output: %v", lines)
	}
	if _, _, err := f.ComposeUp("nope"); err == nil {
		t.Error("up of unknown project should fail")
	}

	ch, stop, err = f.ComposePull(demoWebapp)
	if lines := collect(t, ch, stop, err); !containsSub(lines, "Pull complete") {
		t.Errorf("pull output: %v", lines)
	}
	if _, _, err := f.ComposePull("nope"); err == nil {
		t.Error("pull of unknown project should fail")
	}

	ch, stop, err = f.ComposeDown(demoWebapp)
	if lines := collect(t, ch, stop, err); !containsSub(lines, "Removed") {
		t.Errorf("down output: %v", lines)
	}
	if _, _, err := f.ComposeDown("nope"); err == nil {
		t.Error("down of unknown project should fail")
	}
}

func TestFakeComposeFiles(t *testing.T) {
	f := NewFakeBackend()

	path, content, err := f.ReadComposeFile(demoWebapp)
	if err != nil || path == "" || !strings.Contains(content, "nginx:1.25") {
		t.Errorf("read: %q / %q / %v", path, content, err)
	}
	// Project without stored content gets the default stub.
	_, content, err = f.ReadComposeFile("/srv/monitoring")
	if err != nil || content != "services: {}\n" {
		t.Errorf("read default = %q / %v", content, err)
	}
	if _, _, err := f.ReadComposeFile("nope"); err == nil {
		t.Error("read of unknown project should fail")
	}

	if err := f.WriteComposeFile(demoWebapp, "services:\n  web:\n    image: nginx:1.26\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, content, _ = f.ReadComposeFile(demoWebapp)
	if !strings.Contains(content, "nginx:1.26") {
		t.Error("write should persist the new content")
	}
	f.ComposeFiles = nil // lazily re-created map
	if err := f.WriteComposeFile(demoWebapp, "x: 1\n"); err != nil {
		t.Fatalf("write with nil map: %v", err)
	}
	if err := f.WriteComposeFile("nope", "x"); err == nil {
		t.Error("write to unknown project should fail")
	}
}

func TestFakeCreateComposeFile(t *testing.T) {
	f := NewFakeBackend()
	if _, _, err := f.CreateComposeFile("", "x"); err == nil {
		t.Error("empty dir should fail")
	}
	f.ComposeFiles = nil
	ch, stop, err := f.CreateComposeFile(`C:\srv\newapp\`, "services: {}\n")
	lines := collect(t, ch, stop, err)
	if !containsSub(lines, "newapp-app-1") {
		t.Errorf("create output: %v", lines)
	}
	projects, _ := f.ListComposeProjects()
	last := projects[len(projects)-1]
	if last.Name != "newapp" || last.WorkingDir != "C:/srv/newapp" {
		t.Errorf("created project = %+v", last)
	}
}

func TestFakeBackupRestore(t *testing.T) {
	f := NewFakeBackend()
	cwd, _ := os.Getwd()
	t.Chdir(t.TempDir())
	defer func() { _ = cwd }()

	path, err := f.BackupComposeProject(demoWebapp)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
	if _, err := f.BackupComposeProject("nope"); err == nil {
		t.Error("backup of unknown project should fail")
	}

	ch, stop, rerr := f.RestoreComposeProject(demoWebapp, path)
	lines := collect(t, ch, stop, rerr)
	if !containsSub(lines, "extracting") {
		t.Errorf("restore output: %v", lines)
	}
	if _, _, err := f.RestoreComposeProject(demoWebapp, "ghost.tar.gz"); err == nil {
		t.Error("restore from missing archive should fail")
	}
	if _, _, err := f.RestoreComposeProject("nope", path); err == nil {
		t.Error("restore of unknown project should fail")
	}
}

func TestFakeSystem(t *testing.T) {
	f := NewFakeBackend()
	df, err := f.SystemDF()
	if err != nil || !strings.Contains(df.RawYAML, "Images") {
		t.Errorf("df: %v / %v", df, err)
	}
	summary, err := f.SystemPrune()
	if err != nil || summary == "" {
		t.Errorf("prune: %q / %v", summary, err)
	}
	// Prune keeps only running containers and drops dangling images.
	all, _ := f.ListContainers(true)
	for _, c := range all {
		if c.State != "running" {
			t.Errorf("container %s survived prune in state %s", c.Name, c.State)
		}
	}
}

func TestFakeMisc(t *testing.T) {
	f := NewFakeBackend()
	if err := f.Ping(); err != nil {
		t.Errorf("ping: %v", err)
	}
	if got := f.Runtime(); got != RuntimeDocker {
		t.Errorf("runtime = %v, want docker by default", got)
	}
	f.RuntimeKind = RuntimePodman
	if got := f.Runtime(); got != RuntimePodman {
		t.Errorf("runtime = %v, want podman override", got)
	}
	if f.SupportsHostCompose() != true {
		t.Error("host compose should default to true")
	}
	f.NoHostCompose = true
	if f.SupportsHostCompose() {
		t.Error("NoHostCompose should disable host compose")
	}
	f.Close()

	info, err := f.Info()
	if err != nil || !info.Reachable || info.Containers != 3 || info.Running != 2 || info.Stopped != 1 {
		t.Errorf("info = %+v / %v", info, err)
	}

	ch, stop, err := f.Events()
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if l := <-ch; !strings.Contains(l, "container create") {
		t.Errorf("first event = %q", l)
	}
	stop()
	stop() // idempotent
	for range ch {
	} // channel must be closed after stop
}
