// livetest exercises every docker.Backend operation against a live host and
// prints PASS/FAIL/XFAIL per operation. Diagnostic tool; not part of the app.
//
// Usage:
//
//	set D9C_PW=...
//	go run ./cmd/livetest [-install-key] [-host nerdctl+ssh://user@host]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirg0/d9c/internal/config"
	"github.com/kirg0/d9c/internal/docker"

	"golang.org/x/crypto/ssh"
)

const (
	imgSmall = "docker.io/library/alpine:3.20"
	imgLong  = "docker.io/library/nginx:alpine"
	ctrName  = "d9c-lt-ctr"
	netName  = "d9c-lt-net"
	volName  = "d9c-lt-vol"
	tagName  = "d9c-lt-tag:tmp"
	composeP = "d9clt"
	buildDir = "/home/cont/d9c-lt-build"
	compDir  = "/home/cont/d9c-lt-compose"
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
		fmt.Printf("FAIL  %-42s %8s  %v\n", name, d, err)
		return
	}
	passN++
	fmt.Printf("pass  %-42s %8s\n", name, d)
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
		fmt.Printf("FAIL  %-42s %8s  expected error, got success\n", name, d)
		return
	}
	xfailN++
	fmt.Printf("xfail %-42s %8s  %v\n", name, d, err)
}

func main() {
	host := flag.String("host", "nerdctl+ssh://cont@192.168.1.249", "backend host URL")
	installKey := flag.Bool("install-key", false, "install local public key via password auth first")
	flag.Parse()

	pw := os.Getenv("D9C_PW")
	sshURL := "ssh://" + strings.TrimPrefix(*host, "nerdctl+ssh://")

	if *installKey {
		if err := installPubKey(sshURL, pw); err != nil {
			fmt.Fprintf(os.Stderr, "install key: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("public key installed")
	}

	// Raw SSH client for host-side fixtures (compose file, build context).
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

	cleanup(raw) // clear leftovers from previous runs

	// ── identity ────────────────────────────────────────────────────────────
	step("Runtime", func() error {
		if rt := b.Runtime(); rt != docker.RuntimeContainerd {
			return fmt.Errorf("want containerd, got %v", rt)
		}
		return nil
	})
	step("Ping", b.Ping)
	step("Info", func() error {
		s, err := b.Info()
		if err != nil {
			return err
		}
		if s.Version == "" {
			return fmt.Errorf("empty server version: %+v", s)
		}
		fmt.Printf("      info: name=%s version=%s ncpu=%d mem=%d containers=%d images=%d\n",
			s.Name, s.Version, s.NCPU, s.MemTotal, s.Containers, s.Images)
		return nil
	})

	nb, ok := b.(docker.NamespacedBackend)
	if !ok {
		fmt.Println("FATAL: backend does not implement NamespacedBackend")
		os.Exit(1)
	}
	step("Namespaces", func() error {
		ns, err := nb.Namespaces()
		if err != nil {
			return err
		}
		fmt.Printf("      namespaces: %v current=%s\n", ns, nb.CurrentNamespace())
		return nil
	})

	// ── images ──────────────────────────────────────────────────────────────
	step("PullImage(alpine)", func() error { return b.PullImage(imgSmall) })
	step("PullImage(nginx)", func() error { return b.PullImage(imgLong) })
	step("ListImages", func() error {
		imgs, err := b.ListImages()
		if err != nil {
			return err
		}
		for _, im := range imgs {
			if strings.Contains(im.Tags, "alpine") {
				fmt.Printf("      image: id=%q tags=%q size=%q created=%s\n", im.ID, im.Tags, im.Size, im.Created)
				return nil
			}
		}
		return fmt.Errorf("alpine not in list (%d images)", len(imgs))
	})
	step("InspectImage", func() error {
		r, err := b.InspectImage(imgSmall)
		if err != nil {
			return err
		}
		if r.RawYAML == "" {
			return fmt.Errorf("empty yaml")
		}
		return nil
	})
	step("ImageHistory", func() error {
		r, err := b.ImageHistory(imgSmall)
		if err != nil {
			return err
		}
		if r.RawYAML == "" {
			return fmt.Errorf("empty history")
		}
		return nil
	})
	step("TagImage", func() error { return b.TagImage(imgSmall, tagName) })
	step("RemoveImage(tag)", func() error { return b.RemoveImage(tagName, false) })
	step("RemoveImage(bogus) err", func() error {
		err := b.RemoveImage("no-such-image:zzz", false)
		if err == nil {
			return fmt.Errorf("expected error")
		}
		fmt.Printf("      err text: %v\n", err)
		return nil
	})

	// ── containers ──────────────────────────────────────────────────────────
	step("RunContainer", func() error {
		return b.RunContainer(docker.RunOptions{Image: imgLong, Name: ctrName, Ports: []string{"18080:80"}, Env: []string{"LT=1"}})
	})
	var ctrID string
	step("ListContainers", func() error {
		cs, err := b.ListContainers(true)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if c.Name == ctrName {
				ctrID = c.ID
				fmt.Printf("      ctr: id=%s state=%q status=%q image=%q ports=%q nets=%v\n",
					c.ID, c.State, c.Status, c.Image, c.Ports, c.Networks)
				return nil
			}
		}
		return fmt.Errorf("%s not found among %d", ctrName, len(cs))
	})
	if ctrID == "" {
		ctrID = ctrName
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
	step("ContainerStats", func() error {
		m, err := b.ContainerStats([]string{ctrID})
		if err != nil {
			return err
		}
		s, ok := m[ctrID]
		if !ok {
			return fmt.Errorf("no stats for %s (map keys: %v)", ctrID, keys(m))
		}
		fmt.Printf("      stats: cpu=%.2f%% mem=%d/%d net=%d/%d blk=%d/%d\n",
			s.CPUPerc, s.MemUsage, s.MemLimit, s.NetRx, s.NetTx, s.BlockRead, s.BlockWrite)
		return nil
	})
	step("ContainerLogs", func() error {
		lines, stop, err := b.ContainerLogs(ctrID, docker.LogOptions{Tail: 50})
		if err != nil {
			return err
		}
		defer stop()
		return expectLine(lines, 10*time.Second, "")
	})
	step("ContainerLogs(--since 1h)", func() error {
		lines, stop, err := b.ContainerLogs(ctrID, docker.LogOptions{Tail: 10, Since: "1h"})
		if err != nil {
			return err
		}
		defer stop()
		return expectLine(lines, 10*time.Second, "")
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
	xfail("CopyFromContainer (ssh)", func() error {
		return b.CopyFromContainer(ctrID, "/etc/hostname", os.TempDir())
	})
	xfail("CopyToContainer (ssh)", func() error {
		return b.CopyToContainer(ctrID, os.Args[0], "/tmp")
	})
	step("ExecInteractive", func() error {
		sess, err := b.ExecInteractive(ctrID, []string{"/bin/sh"})
		if err != nil {
			return err
		}
		return driveSession(sess, "echo LT_MARK_$((40+2))\nexit\n", "LT_MARK_42")
	})
	step("StopContainer", func() error { return b.StopContainer(ctrID) })
	step("StartContainer", func() error { return b.StartContainer(ctrID) })
	step("RestartContainer", func() error { return b.RestartContainer(ctrID) })
	step("KillContainer(SIGTERM)", func() error { return b.KillContainer(ctrID, "SIGTERM") })
	step("StartContainer(after kill)", func() error { return b.StartContainer(ctrID) })
	step("RemoveContainer(force)", func() error { return b.RemoveContainer(ctrID, true) })
	step("StopContainer(bogus) err", func() error {
		err := b.StopContainer("no-such-ctr")
		if err == nil {
			return fmt.Errorf("expected error")
		}
		fmt.Printf("      err text: %v\n", err)
		return nil
	})
	step("RunInteractive", func() error {
		sess, err := b.RunInteractive(docker.ExecRunOptions{Image: imgSmall, Cmd: []string{"/bin/sh"}})
		if err != nil {
			return err
		}
		return driveSession(sess, "echo RI_MARK_$((40+2))\nexit\n", "RI_MARK_42")
	})

	// ── networks ────────────────────────────────────────────────────────────
	step("CreateNetwork", func() error {
		return b.CreateNetwork(docker.NetworkCreateOptions{Name: netName, Driver: "bridge", Subnet: "172.29.0.0/24", Gateway: "172.29.0.1"})
	})
	step("ListNetworks", func() error {
		ns, err := b.ListNetworks()
		if err != nil {
			return err
		}
		for _, n := range ns {
			if n.Name == netName {
				return nil
			}
		}
		return fmt.Errorf("%s not found among %d", netName, len(ns))
	})
	step("InspectNetwork", func() error {
		r, err := b.InspectNetwork(netName)
		if err != nil {
			return err
		}
		if !strings.Contains(r.RawYAML, "172.29.0.0") {
			return fmt.Errorf("subnet missing in inspect yaml")
		}
		return nil
	})
	step("RemoveNetwork", func() error { return b.RemoveNetwork(netName) })

	// ── volumes ─────────────────────────────────────────────────────────────
	step("CreateVolume", func() error { return b.CreateVolume(docker.VolumeCreateOptions{Name: volName}) })
	step("ListVolumes", func() error {
		vs, err := b.ListVolumes()
		if err != nil {
			return err
		}
		for _, v := range vs {
			if v.Name == volName {
				return nil
			}
		}
		return fmt.Errorf("%s not found among %d", volName, len(vs))
	})
	step("InspectVolume", func() error {
		_, err := b.InspectVolume(volName)
		return err
	})
	step("Volume in container", func() error {
		return b.RunContainer(docker.RunOptions{Image: imgLong, Name: ctrName + "-vol", Volumes: []string{volName + ":/data"}})
	})
	step("RemoveContainer(vol user)", func() error { return b.RemoveContainer(ctrName+"-vol", true) })
	step("RemoveVolume", func() error { return b.RemoveVolume(volName) })
	step("PruneVolumes", func() error {
		n, err := b.PruneVolumes()
		if err != nil {
			return err
		}
		fmt.Printf("      pruned volumes: %d\n", n)
		return nil
	})

	// ── build / push ────────────────────────────────────────────────────────
	step("BuildImage(remote ctx)", func() error {
		if err := sshRun(raw, "mkdir -p "+buildDir+` && printf 'FROM docker.io/library/alpine:3.20\nRUN echo built-by-livetest > /built\n' > `+buildDir+"/Dockerfile"); err != nil {
			return fmt.Errorf("fixture: %w", err)
		}
		lines, stop, err := b.BuildImage(buildDir, "d9c-lt-built:1")
		if err != nil {
			return err
		}
		defer stop()
		return drainStream(lines, 120*time.Second, true)
	})
	step("RemoveImage(built)", func() error { return b.RemoveImage("d9c-lt-built:1", true) })
	step("PushImage(bogus registry)", func() error {
		lines, stop, err := b.PushImage("127.0.0.1:9999/d9c-lt:1", docker.RegistryAuth{})
		if err != nil {
			fmt.Printf("      immediate err: %v\n", err)
			return nil // an immediate error is acceptable — must not hang
		}
		defer stop()
		_ = drainStream(lines, 60*time.Second, false)
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

	// ── compose ─────────────────────────────────────────────────────────────
	step("compose fixture (host up -d)", func() error {
		yml := `services:
  web:
    image: docker.io/library/nginx:alpine
  sleeper:
    image: docker.io/library/alpine:3.20
    command: ["sleep", "3600"]
`
		cmd := "mkdir -p " + compDir + " && cat > " + compDir + "/compose.yml <<'EOF'\n" + yml + "EOF\n" +
			"cd " + compDir + " && nerdctl compose -p " + composeP + " up -d 2>&1 | tail -5"
		return sshRun(raw, cmd)
	})
	var composeID string
	step("ListComposeProjects", func() error {
		ps, err := b.ListComposeProjects()
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.Project == composeP {
				composeID = p.Identity()
				fmt.Printf("      project: %+v identity=%q\n", p, composeID)
				return nil
			}
		}
		return fmt.Errorf("%s not found among %d", composeP, len(ps))
	})
	if composeID == "" {
		composeID = composeP
	}
	step("SupportsHostCompose", func() error {
		fmt.Printf("      supports host compose: %v\n", b.SupportsHostCompose())
		return nil
	})
	step("ListComposeContainers", func() error {
		cs, err := b.ListComposeContainers(composeID)
		if err != nil {
			return err
		}
		if len(cs) != 2 {
			return fmt.Errorf("want 2 containers, got %d", len(cs))
		}
		return nil
	})
	step("InspectComposeProject", func() error {
		r, err := b.InspectComposeProject(composeID)
		if err != nil {
			return err
		}
		if r.RawYAML == "" {
			return fmt.Errorf("empty yaml")
		}
		return nil
	})
	step("ComposeLogs", func() error {
		lines, stop, err := b.ComposeLogs(composeID, docker.LogOptions{Tail: 20})
		if err != nil {
			return err
		}
		defer stop()
		return expectLine(lines, 15*time.Second, "")
	})
	step("ComposeStop", func() error { return b.ComposeStop(composeID) })
	step("ComposeStart", func() error { return b.ComposeStart(composeID) })
	step("ComposeRestart", func() error { return b.ComposeRestart(composeID) })
	step("ComposePause", func() error { return b.ComposePause(composeID) })
	step("ComposeUnpause", func() error { return b.ComposeUnpause(composeID) })
	step("ComposePull", func() error {
		lines, stop, err := b.ComposePull(composeID)
		if err != nil {
			return err
		}
		defer stop()
		return drainStream(lines, 120*time.Second, true)
	})
	step("ComposeUp", func() error {
		lines, stop, err := b.ComposeUp(composeID)
		if err != nil {
			return err
		}
		defer stop()
		return drainStream(lines, 120*time.Second, true)
	})
	xfail("ComposeConfig", func() error { _, err := b.ComposeConfig(composeID); return err })
	xfail("ReadComposeFile", func() error { _, _, err := b.ReadComposeFile(composeID); return err })
	xfail("WriteComposeFile", func() error { return b.WriteComposeFile(composeID, "x") })
	xfail("CreateComposeFile", func() error { _, _, err := b.CreateComposeFile("/tmp", "x"); return err })
	xfail("BackupComposeProject", func() error { _, err := b.BackupComposeProject(composeID); return err })
	xfail("RestoreComposeProject", func() error { _, _, err := b.RestoreComposeProject(composeID, "x"); return err })
	step("ComposeDown", func() error {
		lines, stop, err := b.ComposeDown(composeID)
		if err != nil {
			return err
		}
		defer stop()
		return drainStream(lines, 120*time.Second, true)
	})
	step("ComposeDown removed all", func() error {
		cs, err := b.ListComposeContainers(composeID)
		if err == nil && len(cs) > 0 {
			return fmt.Errorf("still %d containers", len(cs))
		}
		return nil
	})
	step("ComposeRemove(gone) err", func() error {
		err := b.ComposeRemove(composeID)
		if err == nil {
			return fmt.Errorf("expected error for removed project")
		}
		fmt.Printf("      err text: %v\n", err)
		return nil
	})

	// ── events ──────────────────────────────────────────────────────────────
	step("Events", func() error {
		lines, stop, err := b.Events()
		if err != nil {
			return err
		}
		defer stop()
		// generate an event
		go func() {
			_ = b.RunContainer(docker.RunOptions{Image: imgSmall, Name: ctrName + "-ev"})
			_ = b.RemoveContainer(ctrName+"-ev", true)
		}()
		return expectLine(lines, 30*time.Second, "")
	})

	// ── namespaces switch ───────────────────────────────────────────────────
	step("SetNamespace(d9c-lt-ns)", func() error {
		nb.SetNamespace("d9c-lt-ns")
		defer nb.SetNamespace("default")
		cs, err := b.ListContainers(true)
		if err != nil {
			return err
		}
		if len(cs) != 0 {
			return fmt.Errorf("fresh namespace has %d containers", len(cs))
		}
		return nil
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

	cleanup(raw)

	fmt.Printf("\n=== done: %d pass, %d xfail(expected), %d FAIL ===\n", passN, xfailN, failN)
	for _, f := range failures {
		fmt.Println("  FAIL:", f)
	}
	if failN > 0 {
		os.Exit(1)
	}
}

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

// drainStream reads all lines until close or timeout. When mustClose is true a
// timeout is an error (operation should finish); otherwise it just returns.
func drainStream(lines <-chan string, timeout time.Duration, mustClose bool) error {
	deadline := time.After(timeout)
	n := 0
	var last string
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				fmt.Printf("      stream: %d lines, last=%.120q\n", n, last)
				return nil
			}
			n++
			last = l
		case <-deadline:
			if mustClose {
				return fmt.Errorf("stream not closed within %s (%d lines, last=%.120q)", timeout, n, last)
			}
			fmt.Printf("      stream timeout tolerated: %d lines, last=%.120q\n", n, last)
			return nil
		}
	}
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

// cleanup removes livetest leftovers on the host (ignores errors).
func cleanup(client *ssh.Client) {
	cmds := []string{
		"nerdctl rm -f " + ctrName + " " + ctrName + "-vol " + ctrName + "-ev 2>/dev/null",
		"nerdctl compose -p " + composeP + " -f " + compDir + "/compose.yml down 2>/dev/null",
		"nerdctl network rm " + netName + " 2>/dev/null",
		"nerdctl volume rm " + volName + " 2>/dev/null",
		"nerdctl rmi d9c-lt-built:1 " + tagName + " 2>/dev/null",
		"rm -rf " + buildDir + " " + compDir,
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

func installPubKey(sshURL, pw string) error {
	home, _ := os.UserHomeDir()
	pub, err := os.ReadFile(filepath.Join(home, ".ssh", "id_ed25519.pub"))
	if err != nil {
		return err
	}
	client, err := docker.SSHClient(sshURL, "", pw)
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	cmd := fmt.Sprintf(`mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qF %q ~/.ssh/authorized_keys 2>/dev/null || echo %q >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`,
		strings.TrimSpace(string(pub)), strings.TrimSpace(string(pub)))
	return sess.Run(cmd)
}
