package daemon

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDebouncerFires(t *testing.T) {
	var count atomic.Int32
	d := NewDebouncer(50*time.Millisecond, func() { count.Add(1) })
	d.Trigger()
	time.Sleep(100 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatalf("expected 1 fire, got %d", count.Load())
	}
}

func TestDebouncerResets(t *testing.T) {
	var count atomic.Int32
	d := NewDebouncer(80*time.Millisecond, func() { count.Add(1) })
	d.Trigger()
	time.Sleep(40 * time.Millisecond)
	d.Trigger() // reset
	time.Sleep(40 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("expected 0 fires during debounce, got %d", count.Load())
	}
	time.Sleep(60 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatalf("expected 1 fire after debounce, got %d", count.Load())
	}
}

func TestDebouncerCancel(t *testing.T) {
	var count atomic.Int32
	d := NewDebouncer(50*time.Millisecond, func() { count.Add(1) })
	d.Trigger()
	d.Cancel()
	time.Sleep(100 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("expected 0 fires after cancel, got %d", count.Load())
	}
}

func TestDebouncerNoConcurrentCallbacks(t *testing.T) {
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	d := NewDebouncer(10*time.Millisecond, func() {
		n := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if n <= old || maxConcurrent.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)
	})

	// Rapid-fire triggers to try to get overlapping callbacks.
	for i := 0; i < 5; i++ {
		d.Trigger()
		time.Sleep(15 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	if maxConcurrent.Load() > 1 {
		t.Fatalf("concurrent callbacks detected: max=%d", maxConcurrent.Load())
	}
}

func TestDebouncerFlush(t *testing.T) {
	var count atomic.Int32
	d := NewDebouncer(1*time.Hour, func() { count.Add(1) })
	d.Trigger()

	// Timer is set for 1 hour. Flush should run the callback immediately.
	d.Flush()
	if count.Load() != 1 {
		t.Fatalf("expected 1 fire after flush, got %d", count.Load())
	}
}
