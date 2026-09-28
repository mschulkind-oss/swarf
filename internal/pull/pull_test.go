package pull

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestPullNoConfig(t *testing.T) {
	testutil.GitRepo(t)
	err := Run(context.Background(), Options{})
	if err != ErrNoConfig {
		t.Fatalf("expected ErrNoConfig, got %v", err)
	}
}

func TestPullGitNoStoreNoRemote(t *testing.T) {
	testutil.GitRepo(t)
	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	if err := Run(context.Background(), Options{}); err != ErrNotGitRepo {
		t.Fatalf("expected ErrNotGitRepo when store missing and no remote, got %v", err)
	}
}

// A fresh machine with no store bootstraps from the other machines'
// machine branches: pull creates the store, adds the remote, fetches,
// and applies every peer file as an add. It no longer clones.
func TestPullGitBootstrapsFromPeerMachineBranch(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "machines/peer", map[string]string{"proj/seed.txt": "seed"})

	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})

	res, err := RunWithResult(context.Background(), Options{})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(paths.StoreDir, "proj", "seed.txt")); err != nil || string(got) != "seed" {
		t.Fatalf("expected the peer's proj/seed.txt in the store, got %q, %v", got, err)
	}
	if res.PeersMerged != 1 {
		t.Errorf("PeersMerged = %d, want 1", res.PeersMerged)
	}
	if got := gitexec.RemoteURL(paths.StoreDir, "origin"); got != bare {
		t.Errorf("the bootstrapped store's origin = %q, want %q", got, bare)
	}
	if gitexec.ReadRef(paths.StoreDir, peerRefName("peer")) == "" {
		t.Error("the pull should record the peer's last-seen ref")
	}
	if out := gitOut(t, paths.StoreDir, "for-each-ref", "refs/remotes/"); out != "" {
		t.Errorf("pull must not create remote-tracking branches:\n%s", out)
	}
}

// A machine branch that disappears from the remote is pruned from the
// store's refs/swarf-machines/ copy, so pull stops reading it.
func TestPullGitPrunesRemovedMachineBranch(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "machines/gone", map[string]string{"proj/a.txt": "a"})

	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if gitexec.ReadRef(paths.StoreDir, machineRefName("gone")) == "" {
		t.Fatal("setup: the first pull should fetch machines/gone")
	}
	runGit(t, "", "--git-dir", bare, "update-ref", "-d", "refs/heads/machines/gone")
	res, err := RunWithResult(context.Background(), Options{})
	if err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if gitexec.ReadRef(paths.StoreDir, machineRefName("gone")) != "" {
		t.Error("the removed machine branch should be pruned from refs/swarf-machines/")
	}
	if res.PeersSeen != 0 {
		t.Errorf("PeersSeen = %d, want 0", res.PeersSeen)
	}
}

// pushBranch pushes one commit holding files to branch on the bare
// repository, on top of whatever the branch already has.
func pushBranch(t *testing.T, bare, branch string, files map[string]string) {
	t.Helper()
	staging := filepath.Join(t.TempDir(), "staging")
	runGit(t, "", "init", "-q", staging)
	runGit(t, staging, "config", "user.email", "t@t")
	runGit(t, staging, "config", "user.name", "t")
	if _, ok, _ := gitexec.LsRemoteRef(staging, bare, "refs/heads/"+branch); ok {
		runGit(t, staging, "fetch", "-q", bare, branch)
		runGit(t, staging, "reset", "-q", "--hard", "FETCH_HEAD")
	}
	for rel, content := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(staging, rel)), 0o755)
		os.WriteFile(filepath.Join(staging, rel), []byte(content), 0o644)
	}
	runGit(t, staging, "add", "-A")
	runGit(t, staging, "commit", "-q", "-m", "peer commit")
	runGit(t, staging, "push", "-q", bare, "HEAD:refs/heads/"+branch)
}

// gitOut runs git in dir and returns its trimmed output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestPullUnknownBackend(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "unknown", Remote: "", Debounce: "5s"})
	err := Run(context.Background(), Options{})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !errors.Is(err, ErrUnknownBackend) {
		t.Fatalf("expected ErrUnknownBackend, got %v", err)
	}
}

func TestPullGitSuccess(t *testing.T) {
	// Set up an isolated swarf environment with a store that has a remote.
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)

	// Seed the bare remote with at least one commit so pull works.
	staging := filepath.Join(t.TempDir(), "staging")
	runGit(t, "", "clone", bare, staging)
	runGit(t, staging, "config", "user.email", "test@test.com")
	runGit(t, staging, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(staging, "init.txt"), []byte("seed"), 0o644)
	runGit(t, staging, "add", "-A")
	runGit(t, staging, "commit", "-m", "seed")
	runGit(t, staging, "push", "origin", "HEAD")

	// Clone the bare remote as the store.
	storeDir := filepath.Join(t.TempDir(), "store")
	cmd := exec.Command("git", "clone", bare, storeDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone for store setup failed: %s\n%s", err, out)
	}
	paths.StoreDir = storeDir

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s"})

	err := Run(context.Background(), Options{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

// A store with no origin and no [sync] remote has nowhere to pull
// from, and pull says so.
func TestPullGitNoRemote(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})

	err := Run(context.Background(), Options{})
	if !errors.Is(err, ErrNoGitRemote) {
		t.Fatalf("expected ErrNoGitRemote, got %v", err)
	}
}

func TestPullRcloneNotInstalled(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s"})

	// Override PATH so rclone can't be found.
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	err := Run(context.Background(), Options{})
	if err == nil {
		t.Fatal("expected error when rclone not installed")
	}
	if err.Error() != "rclone is not installed" {
		t.Fatalf("expected 'rclone is not installed', got %v", err)
	}
}

func TestPullRcloneSuccess(t *testing.T) {
	testutil.InitializedSwarf(t)

	// Fake rclone: "lsf --dirs-only <remote>/machines" lists a single peer,
	// "sync" copies an empty tree. The rest falls through to git merge.
	fakeDir := t.TempDir()
	fakeRclone := filepath.Join(fakeDir, "rclone")
	// Respond to "lsf --dirs-only" by printing "peer-one/" if the path looks
	// like .../machines, else empty. Treat sync/mkdir/copy as no-ops.
	script := `#!/bin/sh
case "$1" in
  lsf)
    for a in "$@"; do
      case "$a" in
        */machines) echo "peer-one/"; exit 0 ;;
      esac
    done
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`
	os.WriteFile(fakeRclone, []byte(script), 0o755)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeDir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s", MachineID: "self"})

	// Seed the store with an initial commit so git has a HEAD; pullFromPeer's
	// rclone sync is a no-op (fake), so the peer cache is effectively empty.
	// The fetch attempt will fail, and pullRclone treats that as a per-peer
	// warning and continues — so Run(Options{}) should return nil overall.
	runGit(t, paths.StoreDir, "config", "user.email", "t@t")
	runGit(t, paths.StoreDir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(paths.StoreDir, "seed.txt"), []byte("x"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "seed")

	err := Run(context.Background(), Options{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPullRcloneFailure(t *testing.T) {
	testutil.InitializedSwarf(t)

	// Fake rclone that fails on every invocation. The first failure is on
	// `rclone lsf` against the machines/ path, which surfaces as an error
	// from listPeers → Run(Options{}).
	fakeDir := t.TempDir()
	fakeRclone := filepath.Join(fakeDir, "rclone")
	os.WriteFile(fakeRclone, []byte("#!/bin/sh\necho 'rclone error: bad remote' >&2\nexit 1\n"), 0o755)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeDir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s", MachineID: "self"})

	// Must have HEAD or pullRclone takes the "not a git repo" path.
	runGit(t, paths.StoreDir, "config", "user.email", "t@t")
	runGit(t, paths.StoreDir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(paths.StoreDir, "seed.txt"), []byte("x"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "seed")

	err := Run(context.Background(), Options{})
	if err == nil {
		t.Fatal("expected error from failing rclone")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s\n%s", args, err, out)
	}
}

// 'swarf pull --resync' works on the git backend: a file this store
// lost after pulling it comes back from the unchanged peer, which a
// plain pull does not do.
func TestPullGitResync(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "machines/peer", map[string]string{"proj/a.txt": "a"})
	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	runGit(t, paths.StoreDir, "rm", "-q", "proj/a.txt")
	runGit(t, paths.StoreDir, "commit", "-q", "-m", "lost it")

	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("plain pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "proj", "a.txt")); err == nil {
		t.Fatal("setup: a plain pull should not restore a file the peer did not change")
	}
	if err := Run(context.Background(), Options{Resync: true}); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "proj", "a.txt")); err != nil {
		t.Fatalf("--resync should restore proj/a.txt: %v", err)
	}
}

// A reinstalled machine starts its store from its own machine branch,
// not from the remote's default branch, which the git backend no longer
// pushes: bootstrapping from the stale default branch would make this
// machine's next push look like another store's (non-fast-forward).
func TestPullGitBootstrapsFromOwnMachineBranch(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)

	staging := filepath.Join(t.TempDir(), "staging")
	runGit(t, "", "clone", bare, staging)
	runGit(t, staging, "config", "user.email", "t@t")
	runGit(t, staging, "config", "user.name", "t")
	os.WriteFile(filepath.Join(staging, "seed.txt"), []byte("seed"), 0o644)
	runGit(t, staging, "add", "-A")
	runGit(t, staging, "commit", "-m", "seed")
	runGit(t, staging, "push", "origin", "HEAD")
	os.WriteFile(filepath.Join(staging, "later.txt"), []byte("later"), 0o644)
	runGit(t, staging, "add", "-A")
	runGit(t, staging, "commit", "-m", "after the upgrade")
	runGit(t, staging, "push", "origin", "HEAD:refs/heads/machines/box")

	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "box"})

	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "later.txt")); err != nil {
		t.Fatalf("expected the machine branch's later.txt in the store, got: %v", err)
	}
	// The recovered tip is already on the branch, so it counts as pushed:
	// 'swarf status' does not report the recovered commits as unpushed.
	if got, want := gitexec.ReadRef(paths.StoreDir, backends.PushedRef("box")), gitexec.ReadRef(paths.StoreDir, machineRefName("box")); got == "" || got != want {
		t.Errorf("%s = %q, want the recovered tip %q", backends.PushedRef("box"), got, want)
	}
	os.WriteFile(filepath.Join(paths.StoreDir, "new.txt"), []byte("new"), 0o644)
	if res := (&backends.GitBackend{MachineID: "box"}).Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("the reinstalled machine's push should fast-forward its machine branch: %s", res.Message)
	}
}

// Pull merges every machine branch except this machine's own. When a
// different store also writes this machine_id, merging machines/<self>
// as a peer would silently bring that store's files into this one.
func TestPullGitSkipsOwnMachineBranch(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "machines/self", map[string]string{"proj/other-store.txt": "not ours"})
	pushBranch(t, bare, "machines/peer", map[string]string{"proj/peer.txt": "peer"})

	// A store that already has commits, so reinstall recovery does not
	// start it from machines/self.
	paths.StoreDir = filepath.Join(t.TempDir(), "store")
	runGit(t, "", "init", "-q", paths.StoreDir)
	runGit(t, paths.StoreDir, "config", "user.email", "t@t")
	runGit(t, paths.StoreDir, "config", "user.name", "t")
	runGit(t, paths.StoreDir, "commit", "-q", "--allow-empty", "-m", "local")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})

	res, err := RunWithResult(context.Background(), Options{})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "proj", "peer.txt")); err != nil {
		t.Fatalf("setup: the peer's file should be pulled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "proj", "other-store.txt")); err == nil {
		t.Error("this machine's own machine branch was merged as a peer")
	}
	if gitexec.ReadRef(paths.StoreDir, peerRefName("self")) != "" {
		t.Error("this machine's own machine branch got a last-seen ref")
	}
	if res.PeersMerged != 1 {
		t.Errorf("PeersMerged = %d, want 1", res.PeersMerged)
	}
}

// Ending ctx before the fetch finishes (the daemon stopping during a
// scheduled pull) ends the git pull with ctx.Err() and leaves the store
// as it was: nothing fetched, nothing merged.
func TestPullGitCanceledLeavesStoreAlone(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "machines/peer", map[string]string{"proj/peer.txt": "peer"})

	paths.StoreDir = filepath.Join(t.TempDir(), "store")
	runGit(t, "", "init", "-q", paths.StoreDir)
	runGit(t, paths.StoreDir, "config", "user.email", "t@t")
	runGit(t, paths.StoreDir, "config", "user.name", "t")
	runGit(t, paths.StoreDir, "commit", "-q", "--allow-empty", "-m", "local")
	head := gitexec.RevParseHEAD(paths.StoreDir)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunWithResult(ctx, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled pull should return context.Canceled, got %v", err)
	}
	if got := gitexec.RevParseHEAD(paths.StoreDir); got != head {
		t.Errorf("a canceled pull moved HEAD from %s to %s", head, got)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "proj", "peer.txt")); err == nil {
		t.Error("a canceled pull applied the peer's file")
	}
	if gitexec.ReadRef(paths.StoreDir, machineRefName("peer")) != "" {
		t.Error("a canceled pull fetched the peer's machine branch")
	}
}
