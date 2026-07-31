//go:build !windows

package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Plugins usually run through a shell wrapper, so stop must kill the whole
// process group: killing only the shell leaves grandchildren running (and
// holding the output pipes open).
func TestStopKillsGrandchild(t *testing.T) {
	beat := filepath.Join(t.TempDir(), "beat")
	script := "while true; do echo x >> " + beat + "; sleep 0.05; done & wait"
	ch, stop, err := streamLocalProcess("sh", []string{"-c", script})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	size := func() int64 {
		fi, err := os.Stat(beat)
		if err != nil {
			return -1
		}
		return fi.Size()
	}
	deadline := time.Now().Add(5 * time.Second)
	for size() <= 0 {
		if time.Now().After(deadline) {
			stop()
			t.Fatal("grandchild never started writing")
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop()

	// The channel must close even though a grandchild held the pipes.
	closed := make(chan struct{})
	go func() {
		for range ch {
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not close after stop")
	}

	// The grandchild must be gone: its heartbeat file stops growing.
	time.Sleep(300 * time.Millisecond)
	before := size()
	time.Sleep(300 * time.Millisecond)
	if after := size(); after != before {
		t.Errorf("grandchild still alive: file grew %d -> %d after stop", before, after)
	}
}
