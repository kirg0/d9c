package docker

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// Stopping a stream must close its channel even when the remote command never
// exits on its own (e.g. `logs -f`), otherwise the UI leaks the session.
func TestSSHStreamStopAbortsCommand(t *testing.T) {
	srv := newSSHTestServer(t, func(_ string, _ io.Reader, out, _ io.Writer) int {
		for i := 0; ; i++ {
			if _, err := fmt.Fprintf(out, "tick %d\n", i); err != nil {
				return 1
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	ch, stop, err := sshStream(srv.dial(t), "tail -f app.log")
	if err != nil {
		t.Fatalf("sshStream: %v", err)
	}
	select {
	case l := <-ch:
		if !strings.HasPrefix(l, "tick") {
			t.Errorf("first line = %q, want a tick", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no output from stream")
	}
	stop()
	stop() // idempotent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after stop")
		}
	}
}

func TestSSHOutputAndPipe(t *testing.T) {
	var got stdinCapture
	srv := newSSHTestServer(t, sshScript(map[string]sshExecHandler{
		"hostname":   sshReply("box\n", "", 0),
		"false":      sshReply("", "", 1),
		"denied":     sshReply("", "permission denied\n", 1),
		"tee /tmp/x": got.handler(0),
		"tee /root/x": func(_ string, in io.Reader, _, errw io.Writer) int {
			_, _ = io.Copy(io.Discard, in)
			_, _ = io.WriteString(errw, "tee: /root/x: Permission denied\n")
			return 1
		},
	}))
	client := srv.dial(t)

	if out, err := sshOutput(client, "hostname"); err != nil || out != "box\n" {
		t.Errorf("sshOutput = %q, %v", out, err)
	}
	if _, err := sshOutput(client, "denied"); err == nil || err.Error() != "permission denied" {
		t.Errorf("error should carry trimmed remote output, got %v", err)
	}
	if _, err := sshOutput(client, "false"); err == nil || !strings.Contains(err.Error(), "exited with status 1") {
		t.Errorf("silent failure should fall back to the exit error, got %v", err)
	}

	if err := sshPipe(client, "tee /tmp/x", strings.NewReader("payload")); err != nil {
		t.Fatalf("sshPipe: %v", err)
	}
	if got.get() != "payload" {
		t.Errorf("remote stdin = %q, want payload", got.get())
	}
	if err := sshPipe(client, "tee /root/x", strings.NewReader("payload")); err == nil ||
		err.Error() != "tee: /root/x: Permission denied" {
		t.Errorf("sshPipe error = %v, want remote stderr", err)
	}
}

func TestSSHInteractiveSession(t *testing.T) {
	srv := newSSHTestServer(t, func(_ string, in io.Reader, out, _ io.Writer) int {
		line, _ := bufio.NewReader(in).ReadString('\n')
		_, _ = io.WriteString(out, "echo:"+line)
		return 0
	})
	sess, err := sshInteractive(srv.dial(t), "sh")
	if err != nil {
		t.Fatalf("sshInteractive: %v", err)
	}
	if err := sess.Resize(0, 80); err != nil {
		t.Errorf("non-positive resize should be ignored, got %v", err)
	}
	if err := sess.Resize(40, 120); err != nil {
		t.Errorf("resize: %v", err)
	}
	if _, err := sess.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, _ := io.ReadAll(sess)
	if !strings.Contains(string(data), "echo:hello") {
		t.Errorf("output = %q, want echo:hello", data)
	}
	_ = sess.Close()
	_ = sess.Close() // second close is a no-op

	deadline := time.Now().Add(2 * time.Second)
	for {
		pty, window := srv.requestCounts()
		if pty == 1 && window == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pty requests = %d, window changes = %d; want 1 and 1", pty, window)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSSHRunner(t *testing.T) {
	const prefix = `PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin" nerdctl `
	srv := newSSHTestServer(t, sshScript(map[string]sshExecHandler{
		prefix + "'ps' '-a'":               sshReply("CONTAINER ID\n", "", 0),
		prefix + "'logs' '-f' 'web'":       sshReply("l1\nl2\n", "", 0),
		prefix + "'exec' '-it' 'web' 'sh'": sshReply("$ ", "", 0),
	}))
	closed := false
	r := sshRunner{client: srv.dial(t), bin: "nerdctl", closeFn: func() { closed = true }}

	if out, err := r.output([]string{"ps", "-a"}); err != nil || !strings.Contains(out, "CONTAINER ID") {
		t.Errorf("output = %q, %v", out, err)
	}
	ch, stop, err := r.stream([]string{"logs", "-f", "web"})
	if logs := drainStream(t, ch, stop, err); !strings.Contains(logs, "l1") || !strings.Contains(logs, "l2") {
		t.Errorf("stream = %q", logs)
	}
	sess, err := r.interactive([]string{"exec", "-it", "web", "sh"})
	if err != nil {
		t.Fatalf("interactive: %v", err)
	}
	if data, _ := io.ReadAll(sess); !strings.Contains(string(data), "$ ") {
		t.Errorf("interactive output = %q", data)
	}
	_ = sess.Close()

	if _, err := r.output([]string{"rm", "gone"}); err == nil || !strings.Contains(err.Error(), "unexpected command") {
		t.Errorf("failing command error = %v", err)
	}
	r.close()
	if !closed {
		t.Error("close did not call closeFn")
	}
	sshRunner{}.close() // nil closeFn is a no-op
}
