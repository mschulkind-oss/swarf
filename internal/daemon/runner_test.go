package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/synclock"
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
	mirrorAllProjects(nil, nil) // records all three in the manifest

	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	for _, f := range []string{"a.md", "b.md", "c.md"} {
		os.Remove(filepath.Join(swarfDir, f))
	}
	mirrorAllProjects(nil, nil) // cycle 1: the hold appears
	mirrorAllProjects(nil, nil) // cycle 2: same hold, stay quiet

	if n := strings.Count(buf.String(), "delete hold"); n != 1 {
		t.Fatalf("expected the hold logged exactly once across two cycles, got %d\nlog:\n%s", n, buf.String())
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, slug, "a.md")); err != nil {
		t.Error("the daemon's held pass must not delete from the store")
	}
}

// recordingBackend is a SyncBackend that only records that Sync ran.
type recordingBackend struct{ synced chan struct{} }

func (b *recordingBackend) Sync(context.Context, string) backends.SyncResult {
	b.synced <- struct{}{}
	return backends.SyncResult{Success: true}
}
func (b *recordingBackend) HasChanges(string) bool { return false }

// A daemon cycle that finds the sync lock held — a 'swarf pull' or
// 'swarf push' is running — waits for it instead of syncing alongside.
func TestSyncCycleWaitsForSyncLock(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	if err := config.RegisterDrawer(filepath.Base(repo), repo); err != nil {
		t.Fatal(err)
	}
	release, err := synclock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	b := &recordingBackend{synced: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() { syncCycle(context.Background(), b, nil); close(done) }()

	select {
	case <-b.synced:
		t.Fatal("the cycle synced while another holder had the sync lock")
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the cycle should run once the sync lock is free")
	}
	if len(b.synced) != 1 {
		t.Fatal("the cycle should sync once it has the sync lock")
	}
}

// A cycle still waiting for the lock at shutdown gives up without
// syncing.
func TestSyncCycleGivesUpOnShutdown(t *testing.T) {
	testutil.InitializedSwarf(t)
	release, err := synclock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	b := &recordingBackend{synced: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() { syncCycle(ctx, b, nil); close(done) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a waiting cycle should return promptly on shutdown")
	}
	if len(b.synced) != 0 {
		t.Fatal("a cycle that never got the lock must not sync")
	}
}

// gatedBackend is a SyncBackend whose Sync reports that it started and
// then blocks until release is closed, so a test can act mid-cycle.
type gatedBackend struct{ started, release chan struct{} }

func (b *gatedBackend) Sync(context.Context, string) backends.SyncResult {
	close(b.started)
	<-b.release
	return backends.SyncResult{Success: true}
}
func (b *gatedBackend) HasChanges(string) bool { return false }

// A daemon cycle holds the sync lock until it is done, backend sync
// included, so a 'swarf push' or 'swarf pull' started mid-cycle waits.
func TestSyncCycleHoldsSyncLockUntilDone(t *testing.T) {
	testutil.InitializedSwarf(t)
	b := &gatedBackend{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { syncCycle(context.Background(), b, nil); close(done) }()
	select {
	case <-b.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the cycle never reached the backend sync")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if release, err := synclock.Acquire(ctx, nil); err == nil {
		release()
		close(b.release)
		t.Fatal("the sync lock was free while the cycle was still syncing")
	}

	close(b.release)
	<-done
	release, err := synclock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("the sync lock should be free once the cycle is done: %v", err)
	}
	release()
}

// A daemon whose machine subtree another store owns is refused on every
// cycle until the user acts, so the refusal is logged when it first
// appears and again only after it has cleared and come back.
func TestSyncCycleLogsOwnerRefusalOnce(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}
	testutil.InitializedSwarf(t)
	remote := t.TempDir()
	tokenPath := filepath.Join(remote, backends.MachineSuffix, "box", ".git", backends.OwnerTokenFile)
	claim := func(tok string) {
		os.MkdirAll(filepath.Dir(tokenPath), 0o755)
		os.WriteFile(tokenPath, []byte(tok+"\n"), 0o644)
	}
	claim("someone-else")

	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	b := &backends.RcloneBackend{Remote: remote, MachineID: "box"}
	count := func() int { return strings.Count(buf.String(), "different store") }

	syncCycle(context.Background(), b, nil) // refusal appears: logged
	syncCycle(context.Background(), b, nil) // same refusal: quiet
	if n := count(); n != 1 {
		t.Fatalf("expected the refusal logged once across two cycles, got %d\nlog:\n%s", n, buf.String())
	}

	// The user resolves it; the next cycle uploads.
	tok, err := backends.EnsureOwnerToken(paths.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	claim(tok)
	syncCycle(context.Background(), b, nil)
	if n := count(); n != 1 {
		t.Fatalf("a cycle that uploads should not log the refusal, got %d\nlog:\n%s", n, buf.String())
	}

	// It comes back: logged afresh.
	claim("someone-else")
	syncCycle(context.Background(), b, nil)
	if n := count(); n != 2 {
		t.Fatalf("a refusal that returns should be logged again, got %d\nlog:\n%s", n, buf.String())
	}
}

// The git backend's refusal of a machine branch another store wrote
// repeats on every cycle until the user acts, so, like the rclone owner
// check's refusal, it is logged when it first appears and not again
// until it has cleared.
func TestSyncCycleLogsGitMachineBranchRefusalOnce(t *testing.T) {
	testutil.InitializedSwarf(t)
	bare := testutil.BareRemote(t)
	other := t.TempDir()
	for _, args := range [][]string{
		{"-C", paths.StoreDir, "remote", "add", "origin", bare},
		{"-C", other, "init"},
		{"-C", other, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "--allow-empty", "-m", "other store"},
		{"-C", other, "push", bare, "HEAD:refs/heads/machines/box"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("mine"), 0o644)

	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	b := &backends.GitBackend{MachineID: "box"}
	syncCycle(context.Background(), b, nil) // refusal appears: logged
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("edited"), 0o644)
	syncCycle(context.Background(), b, nil) // same refusal: quiet
	if n := strings.Count(buf.String(), "different store"); n != 1 {
		t.Fatalf("expected the refusal logged once across two cycles, got %d\nlog:\n%s", n, buf.String())
	}

	// It clears: the other store's branch is gone, so a push succeeds.
	if out, err := exec.Command("git", "--git-dir", bare, "update-ref", "-d", "refs/heads/machines/box").CombinedOutput(); err != nil {
		t.Fatalf("delete branch: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("edited again"), 0o644)
	syncCycle(context.Background(), b, nil)

	// It comes back: logged afresh.
	if out, err := exec.Command("git", "-C", other, "push", "--force", bare, "HEAD:refs/heads/machines/box").CombinedOutput(); err != nil {
		t.Fatalf("reclaim: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("edited once more"), 0o644)
	syncCycle(context.Background(), b, nil)
	if n := strings.Count(buf.String(), "different store"); n != 2 {
		t.Fatalf("a refusal that returns should be logged again, got %d\nlog:\n%s", n, buf.String())
	}
}

// The git backend, like rclone, persists the hostname default machine id
// when none is set: the machine branch is named after it, so a later
// hostname change must not move this machine to a new branch.
func TestMakeBackendGitPersistsMachineID(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	b, ok := makeBackend(config.ReadGlobalConfig()).(*backends.GitBackend)
	if !ok {
		t.Fatal("expected the git backend")
	}
	gc := config.ReadGlobalConfig()
	if gc.MachineID == "" || gc.MachineID != b.MachineID {
		t.Errorf("config machine id = %q, want the backend's %q persisted", gc.MachineID, b.MachineID)
	}
}

// A git-backend daemon cycle is a push, so it migrates too: it folds the
// remote's legacy branch into the store, pushes the machine branch, and
// retires the legacy branch.
func TestSyncCycleFoldsAndRetiresLegacyBranch(t *testing.T) {
	testutil.InitializedSwarf(t)
	bare := testutil.BareRemote(t)
	staging := t.TempDir()
	os.WriteFile(filepath.Join(staging, "legacy.md"), []byte("from before"), 0o644)
	for _, args := range [][]string{
		{"-C", paths.StoreDir, "remote", "add", "origin", bare},
		{"-C", staging, "init"},
		{"-C", staging, "add", "-A"},
		{"-C", staging, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "-m", "older swarf"},
		{"-C", staging, "push", bare, "HEAD:refs/heads/main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	syncCycle(context.Background(), &backends.GitBackend{MachineID: "box"}, nil)
	if got, err := os.ReadFile(filepath.Join(paths.StoreDir, "legacy.md")); err != nil || string(got) != "from before" {
		t.Errorf("the cycle should fold main's legacy.md into the store, got %q, %v", got, err)
	}
	if out, _ := exec.Command("git", "--git-dir", bare, "for-each-ref", "--format=%(refname)", "refs/heads/").Output(); strings.TrimSpace(string(out)) != "refs/heads/machines/box" {
		t.Errorf("main should be retired, leaving only the machine branch:\n%s", out)
	}
}

// A git-backend daemon cycle that folds in a legacy branch while a
// delete hold is active leaves the hold for the user: it neither copies
// the held files back into the project nor clears the hold, which only
// a 'swarf pull' or 'swarf push --allow-deletes' the user chose does.
// The hold is still logged when it appears.
func TestSyncCycleFoldKeepsDeleteHold(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	if err := config.RegisterDrawer(slug, repo); err != nil {
		t.Fatal(err)
	}
	bare := testutil.BareRemote(t)
	if out, err := exec.Command("git", "-C", paths.StoreDir, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	b := &backends.GitBackend{MachineID: "box"}
	swarfDir := paths.SwarfDir(repo)
	for _, f := range []string{"a.md", "b.md", "c.md"} {
		os.WriteFile(filepath.Join(swarfDir, f), []byte(f), 0o644)
	}
	syncCycle(context.Background(), b, nil)

	staging := t.TempDir()
	os.MkdirAll(filepath.Join(staging, slug), 0o755)
	os.WriteFile(filepath.Join(staging, slug, "legacy.md"), []byte("from before"), 0o644)
	for _, args := range [][]string{
		{"-C", staging, "init"},
		{"-C", staging, "add", "-A"},
		{"-C", staging, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "-m", "older swarf"},
		{"-C", staging, "push", bare, "HEAD:refs/heads/main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	for _, f := range []string{"a.md", "b.md", "c.md"} {
		os.Remove(filepath.Join(swarfDir, f))
	}
	syncCycle(context.Background(), b, nil)

	if _, err := os.Stat(filepath.Join(swarfDir, "a.md")); err == nil {
		t.Error("the cycle restored held a.md into the project")
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, slug, "a.md")); err != nil {
		t.Errorf("the cycle deleted held a.md from the store: %v", err)
	}
	if _, err := os.Stat(paths.DeleteHoldMarker(slug)); err != nil {
		t.Errorf("the cycle should leave the hold for the user: %v", err)
	}
	if n := strings.Count(buf.String(), "delete hold:"); n != 1 {
		t.Errorf("expected the new hold logged once, got %d\nlog:\n%s", n, buf.String())
	}
	if got, err := os.ReadFile(filepath.Join(swarfDir, "legacy.md")); err != nil || string(got) != "from before" {
		t.Errorf("the cycle should fold main's legacy.md into the project, got %q, %v", got, err)
	}
}
