package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

// TestWatchProjectsShutdownSkipsFlush is the regression guard for the slow-
// shutdown bug: on SIGTERM (a cancelled context) the daemon must return
// promptly and must NOT run a final synchronous flush. The old code called
// debouncer.Flush() here, which ran the full relink→mirror→backend.Sync
// callback inline; backend.Sync shells out to rclone/git-push, an
// uncancellable network call that blocked reboot for ~66s. The fix calls
// debouncer.Cancel() instead, so the callback never fires on shutdown.
func TestWatchProjectsShutdownSkipsFlush(t *testing.T) {
	// Sandbox the store dir so watchProjects' os.MkdirAll is harmless.
	oldStore := paths.StoreDir
	paths.StoreDir = t.TempDir()
	defer func() { paths.StoreDir = oldStore }()

	var ran atomic.Bool
	// Long debounce: the startup Trigger() must never have time to fire on
	// its own, so if the callback runs at all it can only be via a flush.
	d := NewDebouncer(1*time.Hour, func() { ran.Store(true) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // shutdown is already requested before we enter the loop

	done := make(chan error, 1)
	go func() { done <- watchProjects(ctx, d) }()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchProjects did not return promptly on shutdown")
	}

	// Give a pending flush, if any, a moment to surface.
	time.Sleep(50 * time.Millisecond)
	if ran.Load() {
		t.Fatal("shutdown ran the sync callback; it must skip the final flush")
	}
}
