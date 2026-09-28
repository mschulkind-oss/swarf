package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

// TestRelinkConflictLoggedOnce is the regression guard for the journal-spam
// audit finding: a permanently-stuck swept link must be logged once, not
// re-announced on every relink cycle. A divergent regular file is no longer
// stuck — content-aware heal resolves it — so the enduring conflict is a
// directory sitting where the swept symlink belongs, which heal cannot read.
func TestRelinkConflictLoggedOnce(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	if err := config.RegisterDrawer(slug, repo); err != nil {
		t.Fatal(err)
	}

	// Swept file in .links/ whose target is occupied by a directory → relink
	// can't read it to heal and warns every cycle.
	os.WriteFile(filepath.Join(paths.LinksDir(repo), "AGENTS.md"), []byte("swept\n"), 0o644)
	os.Mkdir(filepath.Join(repo, "AGENTS.md"), 0o755)

	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	// Reset the dedup state so the test is independent of other tests.
	loggedRelinkWarnings = map[string]bool{}

	relinkAllProjects() // cycle 1: should log the conflict
	relinkAllProjects() // cycle 2: same conflict, should be suppressed

	n := strings.Count(buf.String(), "re-link skipped")
	if n != 1 {
		t.Fatalf("expected conflict logged exactly once across two cycles, got %d\nlog:\n%s", n, buf.String())
	}
}

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
	go func() { done <- watchProjects(ctx, d, nil) }()

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

// An excluded directory is outside sync entirely, so writes inside it must
// not wake a sync cycle. Excluded trees are the churning ones — a jail's
// state directory, build output — and before the watcher consulted the
// excludes, every write inside one scheduled a full remote sync with
// nothing to copy.
func TestWatchProjectsIgnoresExcludedWrites(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	if err := config.RegisterDrawer(slug, repo); err != nil {
		t.Fatal(err)
	}
	swarfDir := paths.SwarfDir(repo)
	excluded := filepath.Join(swarfDir, ".yolo", "home")
	if err := os.MkdirAll(excluded, 0o755); err != nil {
		t.Fatal(err)
	}

	var cycles atomic.Int32
	d := NewDebouncer(50*time.Millisecond, func() { cycles.Add(1) })
	defer d.Cancel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchProjects(ctx, d, mirror.SkipPatterns([]string{".yolo/"}))

	// watchProjects triggers one cycle at startup no matter what. Wait it
	// out so later counts are attributable to the writes below.
	waitForCycles(t, &cycles, 1)

	os.WriteFile(filepath.Join(excluded, "bash_history"), []byte("churn"), 0o644)
	time.Sleep(400 * time.Millisecond)
	if got := cycles.Load(); got != 1 {
		t.Fatalf("a write under an excluded directory must not schedule a sync, got %d cycles", got)
	}

	os.WriteFile(filepath.Join(swarfDir, "notes.md"), []byte("real work"), 0o644)
	waitForCycles(t, &cycles, 2)
}

func waitForCycles(t *testing.T, cycles *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cycles.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least %d sync cycle(s), got %d", want, cycles.Load())
}

// A delete hold persists across cycles until the user acts; the daemon
// must announce it once, not on every debounce.
func TestDeleteHoldLoggedOnce(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	if err := config.RegisterDrawer(slug, repo); err != nil {
		t.Fatal(err)
	}
	swarfDir := paths.SwarfDir(repo)
	for _, f := range []string{"a.md", "b.md", "c.md"} {
		os.WriteFile(filepath.Join(swarfDir, f), []byte(f), 0o644)
	}
	mirrorAllProjects(nil) // records all three in the manifest

	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	for _, f := range []string{"a.md", "b.md", "c.md"} {
		os.Remove(filepath.Join(swarfDir, f))
	}
	mirrorAllProjects(nil) // cycle 1: the hold appears
	mirrorAllProjects(nil) // cycle 2: same hold, stay quiet

	if n := strings.Count(buf.String(), "delete hold"); n != 1 {
		t.Fatalf("expected the hold logged exactly once across two cycles, got %d\nlog:\n%s", n, buf.String())
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, slug, "a.md")); err != nil {
		t.Error("the daemon's held pass must not delete from the store")
	}
}
