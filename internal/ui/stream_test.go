package ui

import (
	"fmt"
	"testing"
)

// drainLines is the coalescing step behind streamLogs/streamEvents/streamOp:
// one message per drain instead of one per line. It must return whatever is
// buffered without blocking, honour the batch bound and survive a channel that
// closes mid-drain.
func TestDrainLines(t *testing.T) {
	t.Run("collects buffered lines", func(t *testing.T) {
		ch := make(chan string, 10)
		for i := 0; i < 4; i++ {
			ch <- fmt.Sprintf("l%d", i)
		}
		got := drainLines(ch, "first")
		want := []string{"first", "l0", "l1", "l2", "l3"}
		if len(got) != len(want) {
			t.Fatalf("drained %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("drained %v, want %v", got, want)
			}
		}
	})

	t.Run("empty channel yields single line without blocking", func(t *testing.T) {
		ch := make(chan string, 1)
		got := drainLines(ch, "solo")
		if len(got) != 1 || got[0] != "solo" {
			t.Fatalf("drained %v, want [solo]", got)
		}
	})

	t.Run("closed channel ends the batch", func(t *testing.T) {
		ch := make(chan string, 2)
		ch <- "a"
		close(ch)
		got := drainLines(ch, "first")
		if len(got) != 2 || got[0] != "first" || got[1] != "a" {
			t.Fatalf("drained %v, want [first a]", got)
		}
	})

	t.Run("bounded by streamBatchMax", func(t *testing.T) {
		ch := make(chan string, streamBatchMax+10)
		for i := 0; i < streamBatchMax+10; i++ {
			ch <- "x"
		}
		got := drainLines(ch, "first")
		if len(got) != streamBatchMax {
			t.Fatalf("batch size = %d, want %d", len(got), streamBatchMax)
		}
	})
}
