package status

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestCountNonEmpty(t *testing.T) {
	tests := []struct {
		input []string
		want  int
	}{
		{[]string{}, 0},
		{[]string{"", "  "}, 0},
		{[]string{"a", "b"}, 2},
		{[]string{"a", "", "b"}, 2},
	}
	for _, tt := range tests {
		if got := countNonEmpty(tt.input); got != tt.want {
			t.Errorf("countNonEmpty(%v) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestReadPID(t *testing.T) {
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "pid")
	os.WriteFile(pidFile, []byte("12345"), 0o644)
	pid, ok := readPID(pidFile)
	if !ok || pid != 12345 {
		t.Fatalf("expected 12345, got %d (ok=%v)", pid, ok)
	}

	os.WriteFile(pidFile, []byte("notanumber"), 0o644)
	_, ok = readPID(pidFile)
	if ok {
		t.Fatal("expected not ok for invalid PID")
	}

	_, ok = readPID(filepath.Join(tmp, "nonexistent"))
	if ok {
		t.Fatal("expected not ok for missing file")
	}
}

func TestRunNoConfig(t *testing.T) {
	testutil.GitRepo(t)
	Run()
}

func TestRunWithStore(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	Run()
}

func TestRunWithStoreAndDrawers(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	config.RegisterDrawer("testproject", repo)
	// Make a commit so git log works
	exec.Command("git", "-C", paths.StoreDir, "add", "-A").Run()
	exec.Command("git", "-C", paths.StoreDir, "commit", "-m", "test").Run()
	Run()
}

func TestPrintDaemonStatusNotRunning(t *testing.T) {
	tmp := t.TempDir()
	paths.PIDFile = filepath.Join(tmp, "nonexistent.pid")
	printDaemonStatus()
}

func TestPrintDaemonStatusStalePid(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "daemon.pid")
	os.WriteFile(pidFile, []byte("999999999"), 0o644)
	paths.PIDFile = pidFile
	printDaemonStatus()
}

func TestPrintStoreInfoNoStore(t *testing.T) {
	testutil.GitRepo(t)
	gc := &config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"}
	printStoreInfo(gc)
}

func TestPrintStoreInfoNoRemote(t *testing.T) {
	testutil.InitializedSwarf(t)
	gc := &config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"}
	printStoreInfo(gc)
}

func TestPrintProjectsEmpty(t *testing.T) {
	testutil.GitRepo(t)
	printProjects()
}

func TestGitLogNonRepo(t *testing.T) {
	tmp := t.TempDir()
	got := gitLog(tmp)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestGitLogRepo(t *testing.T) {
	testutil.InitializedSwarf(t)
	// Create a file so git add has something to commit
	os.WriteFile(filepath.Join(paths.StoreDir, "dummy.txt"), []byte("x"), 0o644)
	if out, err := exec.Command("git", "-C", paths.StoreDir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", paths.StoreDir, "commit", "-m", "test commit").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s %v", out, err)
	}
	got := gitLog(paths.StoreDir)
	if got == "" {
		t.Fatal("expected non-empty git log")
	}
}

// requireRclone skips a test that shells out to rclone when the binary
// isn't installed. rclone treats a plain filesystem path as a remote, so
// these fixtures need no cloud account and no rclone.conf.
func requireRclone(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}
}

// rcloneSync pushes src into dst the way RcloneBackend.Sync does, so the
// fixture remote holds exactly what a real push would have left there.
func rcloneSync(t *testing.T, src, dst string) {
	t.Helper()
	if out, err := exec.Command("rclone", "sync", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("rclone sync %s %s: %v\n%s", src, dst, err, out)
	}
}

func rowsText(rows [][]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, " ") + "\n")
	}
	return b.String()
}

// TestVerifyRcloneRemoteScopedToThisMachine pins the multi-machine false
// alarm: rclone only ever syncs the store into <remote>/machines/<id>, so
// sizing <remote> itself sums every machine's subtree and the row claimed
// "mismatch — the daemon may still be syncing" forever on any host whose
// remote had a second machine in it.
func TestVerifyRcloneRemoteScopedToThisMachine(t *testing.T) {
	requireRclone(t)
	testutil.InitializedSwarf(t)
	os.WriteFile(filepath.Join(paths.StoreDir, "one.txt"), []byte("hello"), 0o644)

	remote := t.TempDir()
	gc := &config.GlobalConfig{Backend: "rclone", Remote: remote, MachineID: "thisbox"}
	mine := filepath.Join(remote, "machines", "thisbox")
	rcloneSync(t, paths.StoreDir, mine)

	// A second machine's subtree. It shares the bucket but is not ours,
	// and must not count toward what we pushed.
	peer := filepath.Join(remote, "machines", "otherbox")
	os.MkdirAll(peer, 0o755)
	for _, name := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(peer, name), []byte("peer data"), 0o644)
	}

	got := rowsText(remoteVerifyRows(gc))
	if !strings.Contains(got, "verified") {
		t.Fatalf("expected the machine subtree to verify, got:\n%s", got)
	}
	if !strings.Contains(got, mine) {
		t.Errorf("expected the compared remote path %q in the output, got:\n%s", mine, got)
	}
}

// TestVerifyRcloneRemoteMismatchInOwnSubtree is the other half: a real
// shortfall inside our own subtree still has to report as one.
func TestVerifyRcloneRemoteMismatchInOwnSubtree(t *testing.T) {
	requireRclone(t)
	testutil.InitializedSwarf(t)
	remote := t.TempDir()
	gc := &config.GlobalConfig{Backend: "rclone", Remote: remote, MachineID: "thisbox"}
	rcloneSync(t, paths.StoreDir, filepath.Join(remote, "machines", "thisbox"))

	// Written after the push: the remote is genuinely behind now.
	os.WriteFile(filepath.Join(paths.StoreDir, "unpushed.txt"), []byte("new"), 0o644)

	got := rowsText(remoteVerifyRows(gc))
	if !strings.Contains(got, "mismatch") {
		t.Fatalf("expected a mismatch for an unpushed file, got:\n%s", got)
	}
}

// TestVerifyRcloneRemoteNotPushedYet distinguishes "this machine has not
// pushed" from "the remote is unreachable". Before scoping, our subtree
// was never the thing being sized, so this case could not arise; now a
// reachable remote with no subtree of ours must not read as an outage.
func TestVerifyRcloneRemoteNotPushedYet(t *testing.T) {
	requireRclone(t)
	testutil.InitializedSwarf(t)
	remote := t.TempDir()
	os.MkdirAll(filepath.Join(remote, "machines", "otherbox"), 0o755)
	os.WriteFile(filepath.Join(remote, "machines", "otherbox", "a"), []byte("x"), 0o644)

	gc := &config.GlobalConfig{Backend: "rclone", Remote: remote, MachineID: "thisbox"}
	got := rowsText(remoteVerifyRows(gc))
	if strings.Contains(got, "cannot reach remote") {
		t.Fatalf("a reachable remote must not read as unreachable, got:\n%s", got)
	}
	if !strings.Contains(got, "not on the remote yet") {
		t.Fatalf("expected a 'not pushed yet' report, got:\n%s", got)
	}
}

// TestVerifyRcloneRemoteUnreachable keeps the genuine outage reporting as one.
func TestVerifyRcloneRemoteUnreachable(t *testing.T) {
	requireRclone(t)
	testutil.InitializedSwarf(t)
	gc := &config.GlobalConfig{
		Backend:   "rclone",
		Remote:    filepath.Join(t.TempDir(), "no-such-remote"),
		MachineID: "thisbox",
	}
	got := rowsText(remoteVerifyRows(gc))
	if !strings.Contains(got, "cannot reach remote") {
		t.Fatalf("expected an unreachable report, got:\n%s", got)
	}
}

// TestCountPendingFilesAfterMirror is the reported symptom: "Pending 12
// file(s) not yet committed" against a store whose git status was empty.
// A drawer holding a symlink — the shape swarf's own sweep/link layout
// leaves everywhere — was counted as pending on every single run.
func TestCountPendingFilesAfterMirror(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	config.RegisterDrawer(slug, repo)

	swarfDir := paths.SwarfDir(repo)
	os.WriteFile(filepath.Join(swarfDir, "notes.md"), []byte("notes"), 0o644)

	// A swept file in .links/ and the symlink that projects it, which is
	// what a drawer actually looks like after `swarf sweep`.
	linked := filepath.Join(paths.LinksDir(repo), "config.jsonc")
	os.WriteFile(linked, []byte("{ // long enough to differ from the link's own size\n}\n"), 0o644)
	os.Symlink(linked, filepath.Join(swarfDir, "config.jsonc"))

	if _, err := mirror.TrackedDir(swarfDir, filepath.Join(paths.StoreDir, slug), paths.ProjectManifest(slug), nil, mirror.Guard{}); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	if got := countPendingFiles(&config.GlobalConfig{}); got != 0 {
		t.Fatalf("expected 0 pending right after a mirror pass, got %d", got)
	}

	os.WriteFile(filepath.Join(swarfDir, "fresh.md"), []byte("not mirrored yet"), 0o644)
	if got := countPendingFiles(&config.GlobalConfig{}); got != 1 {
		t.Fatalf("expected the unmirrored file to be pending, got %d", got)
	}
}

// A project in a delete hold is listed with its held paths and both
// ways out; a project without a hold marker is not listed at all.
func TestDeleteHoldLines(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	config.RegisterDrawer(slug, repo)
	gc := &config.GlobalConfig{Backend: "rclone"}

	if got := deleteHoldLines(gc); len(got) != 0 {
		t.Fatalf("no marker, no lines; got %v", got)
	}

	swarfDir := paths.SwarfDir(repo)
	store := filepath.Join(paths.StoreDir, slug)
	for _, f := range []string{"a.md", "b.md"} {
		os.WriteFile(filepath.Join(swarfDir, f), []byte(f), 0o644)
	}
	guard := mirror.Guard{Marker: paths.DeleteHoldMarker(slug)}
	mirror.TrackedDir(swarfDir, store, paths.ProjectManifest(slug), nil, guard)
	os.Remove(filepath.Join(swarfDir, "a.md"))
	os.Remove(filepath.Join(swarfDir, "b.md"))
	if h, _ := mirror.TrackedDir(swarfDir, store, paths.ProjectManifest(slug), nil, guard); h == nil {
		t.Fatal("setup: expected a delete hold")
	}

	got := strings.Join(deleteHoldLines(gc), "\n")
	for _, want := range []string{slug, "delete hold", "a.md", "b.md", "swarf pull", "swarf push --allow-deletes"} {
		if !strings.Contains(got, want) {
			t.Errorf("hold lines missing %q:\n%s", want, got)
		}
	}
}
