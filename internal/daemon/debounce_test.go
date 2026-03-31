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
