// crilivetest exercises every docker.Backend operation against a live CRI-O /
// generic CRI host and prints PASS/FAIL/XFAIL per operation. Diagnostic tool;
// not part of the app. Fixtures (pod sandbox, containers) are managed over a
// raw SSH connection with crictl, since the CRI backend itself cannot create
// containers by design.
//
// Usage:
//
//	set D9C_PW=...   (optional; key auth is tried first)
//	go run ./cmd/crilivetest [-host crio+ssh://root@host]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"

	"golang.org/x/crypto/ssh"
)

const (
	imgMain  = "docker.io/library/busybox:1.36"
	imgSpare = "docker.io/library/alpine:3.20"
	podName  = "d9c-lt-pod"
	ctrName  = "d9c-lt-ctr"
	fixDir   = "/root/d9c-lt"
)

var (
	passN, failN, xfailN int
	failures             []string
)

func step(name string, fn func() error) {
	start := time.Now()
	err := fn()
	d := time.Since(start).Round(time.Millisecond)
	if err != nil {
		failN++
		failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		fmt.Printf("FAIL  %-46s %8s  %v\n", name, d, err)
		return
	}
	passN++
	fmt.Printf("pass  %-46s %8s\n", name, d)
}

// xfail runs an operation expected to return an error (unsupported on this
// backend); it fails when the operation unexpectedly succeeds.
func xfail(name string, fn func() error) {
	start := time.Now()
	err := fn()
	d := time.Since(start).Round(time.Millisecond)
	if err == nil {
		failN++
		failures = append(failures, name+": expected error, got success")
		fmt.Printf("FAIL  %-46s %8s  expected error, got success\n", name, d)
		return
	}
	xfailN++
	fmt.Printf("xfail %-46s %8s  %v\n", name, d, err)
}

// info runs an operation whose outcome is runtime-dependent (e.g. starting an
// exited container); the result is reported but never counted as a failure.
func info(name string, fn func() error) bool {
	start := time.Now()
	err := fn()
	d := time.Since(start).Round(time.Millisecond)
	if err != nil {
		fmt.Printf("info  %-46s %8s  %v\n", name, d, err)
		return false
	}
	fmt.Printf("info  %-46s %8s  ok\n", name, d)
	return true
}

func main() {
	host := flag.String("host", "crio+ssh://root@192.168.0.1", "backend host URL")
	flag.Parse()

	pw := os.Getenv("D9C_PW")
	sshURL := "ssh://" + strings.TrimPrefix(strings.TrimPrefix(*host, "crio+ssh://"), "cri+ssh://")
	if i := strings.IndexByte(strings.TrimPrefix(sshURL, "ssh://"), '/'); i >= 0 {
		sshURL = sshURL[:len("ssh://")+i] // strip socket path from the SSH target
	}

	raw, err := docker.SSHClient(sshURL, "", pw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "raw ssh: %v\n", err)
		os.Exit(1)
	}
	defer raw.Close()

	cfg := &config.Config{Host: *host, SSHPassword: pw}
	b, err := docker.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend connect: %v\n", err)
		os.Exit(1)
	}
	defer b.Close()

	cleanup(raw)

	// ── identity ────────────────────────────────────────────────────────────
	step("Runtime", func() error {
		if rt := b.Runtime(); rt != docker.RuntimeCRIO && rt != docker.RuntimeCRI {
			return fmt.Errorf("want cri-o/cri, got %v", rt)
		}
		fmt.Printf("      runtime: %v\n", b.Runtime())
		return nil
	})
	step("Ping", b.Ping)
	step("Info", func() error {
		s, err := b.Info()
		if err != nil {
			return err
		}
		if s.Version == "" {
			return fmt.Errorf("empty runtime version: %+v", s)
		}
		fmt.Printf("      info: name=%s version=%s ncpu=%d mem=%d containers=%d images=%d\n",
			s.Name, s.Version, s.NCPU, s.MemTotal, s.Containers, s.Images)
		return nil
	})

	// ── images ──────────────────────────────────────────────────────────────
	step("PullImage(busybox)", func() error { return b.PullImage(imgMain) })
	step("PullImage(alpine)", func() error { return b.PullImage(imgSpare) })
	step("ListImages", func() error {
		imgs, err := b.ListImages()
		if err != nil {
			return err
		}
		for _, im := range imgs {
			if strings.Contains(im.Tags, "busybox") {
				fmt.Printf("      image: id=%q tags=%q size=%q created=%s\n", im.ID, im.Tags, im.Size, im.Created)
				return nil
			}
		}
		return fmt.Errorf("busybox not in list (%d images)", len(imgs))
	})
	step("InspectImage", func() error {
		r, err := b.InspectImage(imgMain)
		if err != nil {
			return err
		}
		if r.RawYAML == "" {
			return fmt.Errorf("empty yaml")
		}
		return nil
	})
	xfail("ImageHistory", func() error { _, err := b.ImageHistory(imgMain); return err })
	xfail("TagImage", func() error { return b.TagImage(imgMain, "d9c-lt-tag:tmp") })
	xfail("BuildImage", func() error { _, _, err := b.BuildImage("/tmp", "x:1"); return err })
	xfail("PushImage", func() error { _, _, err := b.PushImage("127.0.0.1:9999/x:1", docker.RegistryAuth{}); return err })
	step("RemoveImage(alpine)", func() error { return b.RemoveImage(imgSpare, false) })
	step("RemoveImage(bogus) err", func() error {
		err := b.RemoveImage("no-such-image:zzz", false)
		if err == nil {
			return fmt.Errorf("expected error")
		}
		fmt.Printf("      err text: %v\n", err)
		return nil
	})

	// ── containers (fixtures via raw ssh + crictl) ──────────────────────────
	step("fixture: pod sandbox + container", func() error {
		return sshRun(raw, fixtureScript)
	})
	var ctrID string
	fullName := podName + "/" + ctrName
	step("ListContainers", func() error {
		cs, err := b.ListContainers(true)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if c.Name == fullName {
				ctrID = c.ID
				fmt.Printf("      ctr: id=%s state=%q status=%q image=%q\n", c.ID, c.State, c.Status, c.Image)
				return nil
			}
		}
		return fmt.Errorf("%s not found among %d", fullName, len(cs))
	})
	if ctrID == "" {
		fmt.Println("FATAL: fixture container not found; aborting container section")
		report()
		return
	}
	step("InspectContainer", func() error {
		r, err := b.InspectContainer(ctrID)
		if err != nil {
			return err
		}
		if r.RawYAML == "" {
			return fmt.Errorf("empty yaml")
		}
		return nil
	})
	step("ContainerStats(two ticks)", func() error {
		if _, err := b.ContainerStats([]string{ctrID}); err != nil {
			return err
		}
		time.Sleep(2 * time.Second)
		m, err := b.ContainerStats([]string{ctrID})
		if err != nil {
			return err
		}
		s, ok := m[ctrID]
		if !ok {
			return fmt.Errorf("no stats for %s (map keys: %v)", ctrID, keys(m))
		}
		fmt.Printf("      stats: cpu=%.4f%% mem=%d\n", s.CPUPerc, s.MemUsage)
		return nil
	})
	step("ContainerLogs", func() error {
		lines, stop, err := b.ContainerLogs(ctrID, docker.LogOptions{Tail: 50})
		if err != nil {
			return err
		}
		defer stop()
		return expectLine(lines, 15*time.Second, "tick")
	})
	step("ContainerLogs(--since 1h)", func() error {
		lines, stop, err := b.ContainerLogs(ctrID, docker.LogOptions{Tail: 10, Since: "1h"})
		if err != nil {
			return err
		}
		defer stop()
		return expectLine(lines, 15*time.Second, "")
	})
	step("ListPath(/)", func() error {
		entries, err := b.ListPath(ctrID, "/")
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("empty root listing")
		}
		return nil
	})
	step("ListPath(bogus dir) err", func() error {
		_, err := b.ListPath(ctrID, "/no/such/dir")
		if err == nil {
			return fmt.Errorf("expected error")
		}
		fmt.Printf("      err text: %v\n", err)
		return nil
	})
	step("ExecInteractive", func() error {
		sess, err := b.ExecInteractive(ctrID, []string{"/bin/sh"})
		if err != nil {
			return err
		}
		return driveSession(sess, "echo LT_MARK_$((40+2))\nexit\n", "LT_MARK_42")
	})
	xfail("CopyFromContainer", func() error { return b.CopyFromContainer(ctrID, "/etc/hostname", os.TempDir()) })
	xfail("CopyToContainer", func() error { return b.CopyToContainer(ctrID, os.Args[0], "/tmp") })
	xfail("KillContainer(SIGTERM)", func() error { return b.KillContainer(ctrID, "SIGTERM") })
	step("KillContainer(SIGKILL)", func() error { return b.KillContainer(ctrID, "SIGKILL") })
	// Whether an exited CRI container can be started again is runtime-specific
	// (kubelet recreates instead); report, don't judge.
	restarted := info("StartContainer(after kill)", func() error { return b.StartContainer(ctrID) })
	if restarted {
		info("RestartContainer", func() error { return b.RestartContainer(ctrID) })
		info("StopContainer", func() error { return b.StopContainer(ctrID) })
	}
	step("RemoveContainer(force)", func() error { return b.RemoveContainer(ctrID, true) })
	// crictl stop exits 0 for an unknown ID on CRI-O (idempotent by design),
	// unlike docker/nerdctl; runtime-specific, so report without judging.
	info("StopContainer(bogus)", func() error { return b.StopContainer("no-such-ctr") })
	xfail("RunContainer", func() error {
		return b.RunContainer(docker.RunOptions{Image: imgMain, Name: "x"})
	})
	xfail("RunInteractive", func() error {
		_, err := b.RunInteractive(docker.ExecRunOptions{Image: imgMain, Cmd: []string{"/bin/sh"}})
		return err
	})

	// ── networks / volumes: soft degradation ────────────────────────────────
	step("ListNetworks(empty)", func() error { return wantEmpty(b.ListNetworks) })
	step("ListVolumes(empty)", func() error {
		vs, err := b.ListVolumes()
		if err != nil {
			return err
		}
		if len(vs) != 0 {
			return fmt.Errorf("want empty, got %d", len(vs))
		}
		return nil
	})
	xfail("CreateNetwork", func() error { return b.CreateNetwork(docker.NetworkCreateOptions{Name: "x"}) })
	xfail("InspectNetwork", func() error { _, err := b.InspectNetwork("x"); return err })
	xfail("RemoveNetwork", func() error { return b.RemoveNetwork("x") })
	xfail("CreateVolume", func() error { return b.CreateVolume(docker.VolumeCreateOptions{Name: "x"}) })
	xfail("InspectVolume", func() error { _, err := b.InspectVolume("x"); return err })
	xfail("RemoveVolume", func() error { return b.RemoveVolume("x") })
	xfail("PruneVolumes", func() error { _, err := b.PruneVolumes(); return err })

	// ── compose: soft degradation ───────────────────────────────────────────
	step("ListComposeProjects(empty)", func() error {
		ps, err := b.ListComposeProjects()
		if err != nil {
			return err
		}
		if len(ps) != 0 {
			return fmt.Errorf("want empty, got %d", len(ps))
		}
		return nil
	})
	step("SupportsHostCompose(false)", func() error {
		if b.SupportsHostCompose() {
			return fmt.Errorf("want false")
		}
		return nil
	})
	xfail("ComposeStop", func() error { return b.ComposeStop("x") })
	xfail("ComposeUp", func() error { _, _, err := b.ComposeUp("x"); return err })
	xfail("ComposeLogs", func() error { _, _, err := b.ComposeLogs("x", docker.LogOptions{}); return err })
	xfail("ReadComposeFile", func() error { _, _, err := b.ReadComposeFile("x"); return err })

	// ── events ──────────────────────────────────────────────────────────────
	step("Events", func() error {
		lines, stop, err := b.Events()
		if err != nil {
			return err
		}
		defer stop()
		go func() {
			_ = sshRun(raw, "sh "+fixDir+"/mkctr.sh d9c-lt-ev >/dev/null 2>&1; "+
				"crictl ps -a --name d9c-lt-ev -q | xargs -r crictl rm -f >/dev/null 2>&1")
		}()
		return expectLine(lines, 30*time.Second, "")
	})

	// ── system ──────────────────────────────────────────────────────────────
	step("SystemDF", func() error {
		r, err := b.SystemDF()
		if err != nil {
			return err
		}
		if r.RawYAML == "" {
			return fmt.Errorf("empty df")
		}
		return nil
	})
	step("SystemPrune", func() error {
		out, err := b.SystemPrune()
		if err != nil {
			return err
		}
		fmt.Printf("      prune: %s\n", firstLine(out))
		return nil
	})
	step("PruneImages", func() error {
		n, err := b.PruneImages()
		if err != nil {
			return err
		}
		fmt.Printf("      pruned images: %d\n", n)
		return nil
	})

	cleanup(raw)
	report()
}

func report() {
	fmt.Printf("\n=== done: %d pass, %d xfail(expected), %d FAIL ===\n", passN, xfailN, failN)
	for _, f := range failures {
		fmt.Println("  FAIL:", f)
	}
	if failN > 0 {
		os.Exit(1)
	}
}

// fixtureScript prepares the pod sandbox, a helper that spawns containers in
// it, and the first ticking container used by most container steps.
const fixtureScript = `set -e
mkdir -p ` + fixDir + `/logs
cat > ` + fixDir + `/pod.json <<'EOF'
{"metadata":{"name":"` + podName + `","namespace":"d9clt","uid":"d9c-lt-uid-1","attempt":1},
 "log_directory":"` + fixDir + `/logs","linux":{}}
EOF
cat > ` + fixDir + `/ctr.json <<'EOF'
{"metadata":{"name":"` + ctrName + `","attempt":0},
 "image":{"image":"` + imgMain + `"},
 "command":["sh","-c","i=0; while true; do echo tick $i; i=$((i+1)); sleep 2; done"],
 "log_path":"` + ctrName + `.log","linux":{}}
EOF
cat > ` + fixDir + `/mkctr.sh <<'EOF'
#!/bin/sh
set -e
POD=$(crictl pods --name ` + podName + ` -q | head -1)
sed "s/` + ctrName + `/$1/g" ` + fixDir + `/ctr.json > /tmp/ctr-$1.json
CTR=$(crictl -t 60s create "$POD" /tmp/ctr-$1.json ` + fixDir + `/pod.json)
crictl -t 60s start "$CTR"
EOF
crictl -t 120s runp ` + fixDir + `/pod.json
sh ` + fixDir + `/mkctr.sh ` + ctrName

// driveSession writes input to an interactive session, reads until EOF or
// timeout, and checks the output contains want.
func driveSession(sess docker.ExecSession, input, want string) error {
	defer sess.Close()
	_ = sess.Resize(40, 120)
	done := make(chan string, 1)
	go func() {
		buf, _ := io.ReadAll(sess)
		done <- string(buf)
	}()
	time.Sleep(1 * time.Second) // let the shell start
	if _, err := sess.Write([]byte(input)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	select {
	case out := <-done:
		if !strings.Contains(out, want) {
			return fmt.Errorf("output lacks %q: %.300s", want, out)
		}
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for session output")
	}
}

// expectLine waits for at least one line (containing substr when non-empty).
func expectLine(lines <-chan string, timeout time.Duration, substr string) error {
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return fmt.Errorf("stream closed before expected line")
			}
			if substr == "" || strings.Contains(l, substr) {
				fmt.Printf("      line: %.120s\n", l)
				return nil
			}
		case <-deadline:
			return fmt.Errorf("timeout: no line within %s", timeout)
		}
	}
}

func wantEmpty(fn func() ([]docker.Network, error)) error {
	ns, err := fn()
	if err != nil {
		return err
	}
	if len(ns) != 0 {
		return fmt.Errorf("want empty, got %d", len(ns))
	}
	return nil
}

func keys(m map[string]docker.ContainerStats) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sshRun(client *ssh.Client, cmd string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	if len(out) > 0 {
		fmt.Printf("      host: %s\n", strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", "\n      host: "))
	}
	return nil
}

// cleanup removes crilivetest leftovers on the host (ignores errors).
func cleanup(client *ssh.Client) {
	cmds := []string{
		"crictl ps -a -q --name d9c-lt | xargs -r crictl rm -f",
		"crictl pods --name " + podName + " -q | xargs -r crictl rmp -f",
		"rm -rf " + fixDir + " /tmp/ctr-d9c-lt*",
	}
	for _, c := range cmds {
		sess, err := client.NewSession()
		if err != nil {
			continue
		}
		_, _ = sess.CombinedOutput(c)
		sess.Close()
	}
}
