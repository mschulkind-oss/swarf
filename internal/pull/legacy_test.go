package pull

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

// legacyStore makes the store a clone of the bare remote's main branch,
// the way a store looked before the git backend used machine branches,
// and configures the git backend as machine "self".
func legacyStore(t *testing.T, bare string) {
	t.Helper()
	paths.StoreDir = filepath.Join(t.TempDir(), "store")
	runGit(t, "", "clone", "-q", "--branch", "main", bare, paths.StoreDir)
	runGit(t, paths.StoreDir, "config", "user.email", "t@t")
	runGit(t, paths.StoreDir, "config", "user.name", "t")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})
}

func readStore(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(paths.StoreDir, rel))
	if err != nil {
		t.Errorf("read store %s: %v", rel, err)
	}
	return string(data)
}

func bareTip(t *testing.T, bare, branch string) string {
	t.Helper()
	sha, _, err := gitexec.LsRemoteRef("", bare, "refs/heads/"+branch)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// A store with no commits folds in a legacy branch by fast-forwarding to
// it: the store starts from the branch's tip. The branch stays on the
// remote until this machine's machine branch has been pushed.
func TestPullGitFoldsLegacyIntoEmptyStore(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "main", map[string]string{"proj/old.txt": "old"})
	tip := bareTip(t, bare, "main")

	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := gitexec.RevParseHEAD(paths.StoreDir); got != tip {
		t.Errorf("store HEAD = %q, want the legacy tip %q (fast-forward)", got, tip)
	}
	if got := readStore(t, "proj/old.txt"); got != "old" {
		t.Errorf("proj/old.txt = %q", got)
	}
	if got := bareTip(t, bare, "main"); got != tip {
		t.Errorf("main must stay on the remote until the machine branch is pushed, got %q", got)
	}
}

// A store behind the legacy branch fast-forwards to it, and stops
// tracking it: no upstream, no remote-tracking branches.
func TestPullGitFastForwardsToLegacyTip(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "main", map[string]string{"proj/a.txt": "a1"})
	legacyStore(t, bare)
	pushBranch(t, bare, "main", map[string]string{"proj/a.txt": "a2", "proj/b.txt": "b"})
	tip := bareTip(t, bare, "main")

	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := gitexec.RevParseHEAD(paths.StoreDir); got != tip {
		t.Errorf("store HEAD = %q, want the legacy tip %q (fast-forward)", got, tip)
	}
	if got := readStore(t, "proj/a.txt"); got != "a2" {
		t.Errorf("proj/a.txt = %q, want the legacy branch's a2", got)
	}
	if out := gitOut(t, paths.StoreDir, "for-each-ref", "--format=%(upstream)", "refs/heads/"); out != "" {
		t.Errorf("the store should stop tracking the legacy branch, upstreams:\n%s", out)
	}
	if out := gitOut(t, paths.StoreDir, "for-each-ref", "refs/remotes/"); out != "" {
		t.Errorf("the store should drop its remote-tracking branches:\n%s", out)
	}
}

// A store that diverged from the legacy branch folds it in as it would a
// peer — the same file rules and conflict sidecars — and the resulting
// commit has the legacy tip as a second parent, so the legacy tip is an
// ancestor of HEAD.
func TestPullGitFoldsDivergedLegacyAsPeer(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "main", map[string]string{"proj/shared.txt": "base"})
	legacyStore(t, bare)
	os.WriteFile(filepath.Join(paths.StoreDir, "proj", "shared.txt"), []byte("local"), 0o644)
	os.WriteFile(filepath.Join(paths.StoreDir, "proj", "mine.txt"), []byte("mine"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-q", "-m", "local change")
	local := gitexec.RevParseHEAD(paths.StoreDir)
	pushBranch(t, bare, "main", map[string]string{"proj/shared.txt": "legacy", "proj/theirs.txt": "theirs"})
	tip := bareTip(t, bare, "main")

	res, err := RunWithResult(context.Background(), Options{})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := readStore(t, "proj/shared.txt"); got != "local" {
		t.Errorf("proj/shared.txt = %q, want the store's own version kept", got)
	}
	if got := readStore(t, "proj/theirs.txt"); got != "theirs" {
		t.Errorf("proj/theirs.txt = %q, want the legacy branch's file", got)
	}
	if got := readStore(t, "proj/mine.txt"); got != "mine" {
		t.Errorf("proj/mine.txt = %q", got)
	}
	if len(res.ConflictFiles) != 1 || !strings.HasPrefix(res.ConflictFiles[0], "proj/shared.txt.conflict.main.") {
		t.Fatalf("want one proj/shared.txt.conflict.main.* sidecar, got %v", res.ConflictFiles)
	}
	if got := readStore(t, res.ConflictFiles[0]); got != "legacy" {
		t.Errorf("the sidecar should hold the legacy branch's version, got %q", got)
	}
	head := gitexec.RevParseHEAD(paths.StoreDir)
	if parents := strings.Fields(gitOut(t, paths.StoreDir, "show", "-s", "--format=%P", head)); len(parents) != 2 || parents[0] != local || parents[1] != tip {
		t.Errorf("the fold commit's parents = %v, want [%s %s]", parents, local, tip)
	}
	if strings.TrimSpace(gitexec.StatusPorcelain(paths.StoreDir)) != "" {
		t.Error("the fold should leave the store committed")
	}

	// Folded in once, it is not folded in again.
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if got := gitexec.RevParseHEAD(paths.StoreDir); got != head {
		t.Errorf("a second pull folded the same legacy tip again: HEAD %s -> %s", head, got)
	}
}

// A legacy branch this machine's machine branch already holds is
// retired by the pull: deleted from the remote.
func TestPullGitRetiresLegacyAlreadyOnMachineBranch(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushBranch(t, bare, "main", map[string]string{"proj/a.txt": "a"})
	legacyStore(t, bare)
	if res := (&backends.GitBackend{MachineID: "self"}).Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("push the machine branch: %s", res.Message)
	}

	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := bareTip(t, bare, "main"); got != "" {
		t.Errorf("main should be retired, still at %q", got)
	}
	if got := bareTip(t, bare, "machines/self"); got == "" {
		t.Error("the machine branch should stay")
	}
}

// Pull skips a tombstone: nothing of it reaches the store.
func TestPullGitSkipsTombstone(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	pushTombstone(t, bare, "main")
	pushBranch(t, bare, "machines/peer", map[string]string{"proj/peer.txt": "peer"})

	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "self"})
	res, err := RunWithResult(context.Background(), Options{})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, backends.TombstoneFile)); err == nil {
		t.Errorf("the tombstone's %s reached the store", backends.TombstoneFile)
	}
	if got := readStore(t, "proj/peer.txt"); got != "peer" {
		t.Errorf("the peer should still be pulled, proj/peer.txt = %q", got)
	}
	if gitexec.ReadRef(paths.StoreDir, backends.LegacyRef("main")) != "" {
		t.Error("a tombstone is not a legacy branch waiting to be retired")
	}
	if res.PeersMerged != 1 || len(res.ConflictFiles) != 0 {
		t.Errorf("PeersMerged = %d, ConflictFiles = %v", res.PeersMerged, res.ConflictFiles)
	}
}

// pushTombstone pushes a tombstone, as swarf writes one, to branch.
func pushTombstone(t *testing.T, bare, branch string) {
	t.Helper()
	staging := filepath.Join(t.TempDir(), "staging")
	runGit(t, "", "init", "-q", staging)
	runGit(t, staging, "config", "user.email", "t@t")
	runGit(t, staging, "config", "user.name", "t")
	os.WriteFile(filepath.Join(staging, backends.TombstoneFile), []byte("empty\n"), 0o644)
	runGit(t, staging, "add", "-A")
	runGit(t, staging, "commit", "-q", "-m", "swarf: retire this branch\n\n"+backends.TombstoneTrailer)
	runGit(t, staging, "push", "-q", "--force", bare, "HEAD:refs/heads/"+branch)
}
