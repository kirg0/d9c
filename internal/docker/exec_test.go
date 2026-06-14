package docker

import (
	"io"
	"strings"
	"testing"
)

func TestDefaultShell(t *testing.T) {
	got := defaultShell()
	if len(got) != 1 || got[0] != "/bin/sh" {
		t.Errorf("defaultShell() = %v, want [/bin/sh]", got)
	}
}

// TestExecArgv checks an empty command defaults to a shell while an explicit
// argv is preserved.
func TestExecArgv(t *testing.T) {
	if got := strings.Join(execArgv(nil), " "); got != "/bin/sh" {
		t.Errorf("execArgv(nil) = %q, want /bin/sh", got)
	}
	if got := strings.Join(execArgv([]string{"bash", "-l"}), " "); got != "bash -l" {
		t.Errorf("execArgv(bash -l) = %q, want 'bash -l'", got)
	}
}

// TestFakeExecSession verifies the demo session emits a banner, echoes typed
// input back and reports EOF once closed — all without a real TTY or daemon.
func TestFakeExecSession(t *testing.T) {
	fb := NewFakeBackend()
	s, err := fb.ExecInteractive("9ae942fd8fbc", nil)
	if err != nil {
		t.Fatalf("ExecInteractive: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := s.Read(buf)
	if err != nil {
		t.Fatalf("Read banner: %v", err)
	}
	banner := string(buf[:n])
	if !strings.Contains(banner, "/bin/sh") || !strings.Contains(banner, "9ae942fd8fbc") {
		t.Errorf("banner = %q, want it to mention the default shell and container", banner)
	}

	if _, err := s.Write([]byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	n, _ = s.Read(buf)
	if echo := string(buf[:n]); echo != "hi" {
		t.Errorf("echo = %q, want %q", echo, "hi")
	}

	if err := s.Resize(24, 80); err != nil {
		t.Errorf("Resize: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.Read(buf); err != io.EOF {
		t.Errorf("Read after close = %v, want io.EOF", err)
	}
}

func TestFakeRunInteractive(t *testing.T) {
	t.Run("opens a session from a local image", func(t *testing.T) {
		f := NewFakeBackend()
		s, err := f.RunInteractive(ExecRunOptions{
			Image:   "nginx:1.25",
			Volumes: []string{"/srv:/data"},
			Cmd:     []string{"sh"},
		})
		if err != nil {
			t.Fatalf("run interactive: %v", err)
		}
		defer func() { _ = s.Close() }()
		buf := make([]byte, 256)
		n, err := s.Read(buf)
		if err != nil || n == 0 {
			t.Fatalf("read banner: n=%d err=%v", n, err)
		}
		banner := string(buf[:n])
		if !strings.Contains(banner, "nginx:1.25") || !strings.Contains(banner, "/srv:/data") {
			t.Errorf("banner = %q, want image and volumes echoed", banner)
		}
	})

	t.Run("missing image gets a pull hint", func(t *testing.T) {
		f := NewFakeBackend()
		_, err := f.RunInteractive(ExecRunOptions{Image: "ghost:0.0"})
		if err == nil || !strings.Contains(err.Error(), "pull") {
			t.Errorf("err = %v, want pull hint", err)
		}
	})

	t.Run("empty image rejected", func(t *testing.T) {
		f := NewFakeBackend()
		if _, err := f.RunInteractive(ExecRunOptions{}); err == nil {
			t.Error("expected error for empty image")
		}
	})
}
