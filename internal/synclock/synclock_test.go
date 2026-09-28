package synclock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

func sandbox(t *testing.T) {
	t.Helper()
	old := paths.CacheDir
	paths.CacheDir = t.TempDir() + "/swarf"
	t.Cleanup(func() { paths.CacheDir = old })
}

// An uncontended lock is taken at once and never reports a wait.
func TestAcquireFreeLockDoesNotWait(t *testing.T) {
	sandbox(t)
	waited := false
	release, err := Acquire(context.Background(), func() { waited = true })
	if err != nil {
		t.Fatal(err)
	}
	release()
	if waited {
		t.Error("a free lock must not report a wait")
	}
}

// A second holder waits, says so once, and gets the lock as soon as the
// first releases it.
func TestAcquireExcludesSecondHolder(t *testing.T) {
	sandbox(t)
	release1, err := Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	waits := make(chan struct{}, 10)
	acquired := make(chan func(), 1)
	go func() {
		release2, err := Acquire(context.Background(), func() { waits <- struct{}{} })
		if err != nil {
			t.Error(err)
			return
		}
		acquired <- release2
	}()

	select {
	case <-waits:
	case <-time.After(2 * time.Second):
		t.Fatal("the second holder should report that it is waiting")
	}
	select {
	case <-acquired:
		t.Fatal("the second holder got the lock while the first still held it")
	case <-time.After(300 * time.Millisecond):
	}

	release1()
	select {
	case release2 := <-acquired:
		release2()
	case <-time.After(2 * time.Second):
		t.Fatal("the second holder should get the lock once the first releases it")
	}
	if n := len(waits); n != 0 {
		t.Errorf("the wait should be reported once, got %d more", n)
	}
}

// A waiter whose context ends gives up with the context's error.
func TestAcquireGivesUpWhenContextEnds(t *testing.T) {
	sandbox(t)
	release, err := Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the context's error, got %v", err)
	}
}
