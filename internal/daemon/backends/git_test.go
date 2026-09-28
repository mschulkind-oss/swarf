package backends

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

// gitFixture is a store with one uncommitted file whose origin is an
// empty local bare repository.
type gitFixture struct {
	bare string
}

func newGitFixture(t *testing.T) gitFixture {
	t.Helper()
	testutil.InitializedSwarf(t)
	bare := testutil.BareRemote(t)
	if err := gitexec.AddRemote(paths.StoreDir, "origin", bare); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("mine"), 0o644)
	return gitFixture{bare: bare}
}

// ref returns the SHA ref points at in the bare remote, or "".
func (f gitFixture) ref(t *testing.T, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", f.bare, "rev-parse", "--verify", "--quiet", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// branches lists the bare remote's branches.
func (f gitFixture) branches(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", f.bare, "for-each-ref", "--format=%(refname)", "refs/heads/").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// claimBranch pushes a commit that is not in this store to branch on the
// remote, the way a different store with the same machine_id would.
func (f gitFixture) claimBranch(t *testing.T, branch string) string {
	t.Helper()
	other := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "o@o"},
		{"config", "user.name", "o"},
		{"commit", "--allow-empty", "-m", "the other store"},
		{"push", f.bare, "HEAD:refs/heads/" + branch},
	} {
		if out, err := exec.Command("git", append([]string{"-C", other}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return f.ref(t, "refs/heads/"+branch)
}

// The git backend pushes HEAD to this machine's branch, machines/<id>,
// and to no other branch.
func TestGitSyncPushesMachineBranch(t *testing.T) {
	f := newGitFixture(t)
	b := &GitBackend{MachineID: "box"}
	res := b.Sync(context.Background(), paths.StoreDir)
	if !res.Success {
		t.Fatalf("sync failed: %s", res.Message)
	}
	head := gitexec.RevParseHEAD(paths.StoreDir)
	if got := f.ref(t, "refs/heads/machines/box"); got == "" || got != head {
		t.Fatalf("remote machines/box = %q, want the store's HEAD %q", got, head)
	}
	if got := f.branches(t); got != "refs/heads/machines/box" {
		t.Errorf("the push should create only the machine branch, remote has:\n%s", got)
	}
}

// A commit whose push failed is pushed by the next sync, even when that
// sync has nothing new to commit.
func TestGitSyncRetriesUnpushedCommit(t *testing.T) {
	f := newGitFixture(t)
	gitexec.SetRemoteURL(paths.StoreDir, "origin", filepath.Join(t.TempDir(), "missing.git"))
	b := &GitBackend{MachineID: "box"}
	b.Sync(context.Background(), paths.StoreDir)
	head := gitexec.RevParseHEAD(paths.StoreDir)
	if head == "" {
		t.Fatal("the sync should commit locally even when the push fails")
	}

	gitexec.SetRemoteURL(paths.StoreDir, "origin", f.bare)
	if res := b.Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("sync failed: %s", res.Message)
	}
	if got := f.ref(t, "refs/heads/machines/box"); got != head {
		t.Errorf("remote machines/box = %q, want the earlier unpushed commit %q", got, head)
	}
}

// A machine branch holding commits this store does not have was written
// by a different store with the same machine_id: the push is refused
// (never forced), the store commits locally only, and the refusal names
// the ways out.
func TestGitSyncRefusesMachineBranchOfAnotherStore(t *testing.T) {
	f := newGitFixture(t)
	theirs := f.claimBranch(t, "machines/box")
	b := &GitBackend{MachineID: "box"}
	res := b.Sync(context.Background(), paths.StoreDir)
	if res.Success {
		t.Fatal("sync must refuse a machine branch another store wrote")
	}
	if !res.Logged {
		t.Error("the backend logs the refusal itself, so the result should say so")
	}
	for _, want := range []string{"box", "machine_id", "--take-over", "discards", "committed locally only"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal should mention %q:\n%s", want, res.Message)
		}
	}
	if got := f.ref(t, "refs/heads/machines/box"); got != theirs {
		t.Errorf("remote machines/box = %q; the refused push must leave it at %q", got, theirs)
	}
	if gitexec.RevParseHEAD(paths.StoreDir) == "" || strings.TrimSpace(gitexec.StatusPorcelain(paths.StoreDir)) != "" {
		t.Error("a refused sync should still commit the store locally")
	}
}

// --take-over force-pushes the machine branch, even with nothing new to
// commit.
func TestGitSyncTakeOverForcePushes(t *testing.T) {
	f := newGitFixture(t)
	f.claimBranch(t, "machines/box")
	(&GitBackend{MachineID: "box"}).Sync(context.Background(), paths.StoreDir) // refused

	b := &GitBackend{MachineID: "box", TakeOver: true}
	if res := b.Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("take-over sync failed: %s", res.Message)
	}
	if got, head := f.ref(t, "refs/heads/machines/box"), gitexec.RevParseHEAD(paths.StoreDir); got != head {
		t.Errorf("after take-over remote machines/box = %q, want the store's HEAD %q", got, head)
	}
	if res := (&GitBackend{MachineID: "box"}).Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Errorf("after taking over, a plain sync succeeds: %s", res.Message)
	}
}

// A push that fails for any reason but a non-fast-forward (here the
// remote cannot be reached) is a failed sync, not a success: 'swarf
// push' must not report it as done. The commit is still made locally,
// and the refusal wording is kept for a branch another store wrote.
func TestGitSyncReportsFailedPush(t *testing.T) {
	newGitFixture(t)
	gitexec.SetRemoteURL(paths.StoreDir, "origin", filepath.Join(t.TempDir(), "missing.git"))
	b := &GitBackend{MachineID: "box"}
	res := b.Sync(context.Background(), paths.StoreDir)
	if res.Success {
		t.Fatalf("a push to an unreachable remote must not succeed: %s", res.Message)
	}
	if gitexec.RevParseHEAD(paths.StoreDir) == "" {
		t.Error("the sync should still commit locally")
	}
	if strings.Contains(res.Message, "different store") {
		t.Errorf("an unreachable remote is not another store's branch:\n%s", res.Message)
	}
	// Nothing new to commit: the retry fails the same way.
	if res := b.Sync(context.Background(), paths.StoreDir); res.Success {
		t.Fatalf("the retry of a push to an unreachable remote must not succeed: %s", res.Message)
	}
}

// A server-side refusal of a --take-over force push (receive.denyNonFastForwards,
// or a hook) is a failed sync, and is not reported as another store's
// branch.
func TestGitSyncTakeOverRefusedByRemote(t *testing.T) {
	f := newGitFixture(t)
	theirs := f.claimBranch(t, "machines/box")
	if out, err := exec.Command("git", "--git-dir", f.bare, "config", "receive.denyNonFastForwards", "true").CombinedOutput(); err != nil {
		t.Fatalf("config: %v\n%s", err, out)
	}
	res := (&GitBackend{MachineID: "box", TakeOver: true}).Sync(context.Background(), paths.StoreDir)
	if res.Success {
		t.Fatalf("a take-over the remote refused must not succeed: %s", res.Message)
	}
	if strings.Contains(res.Message, "different store") {
		t.Errorf("the remote's refusal is not another store's branch:\n%s", res.Message)
	}
	if got := f.ref(t, "refs/heads/machines/box"); got != theirs {
		t.Errorf("remote machines/box = %q; the refused push must leave it at %q", got, theirs)
	}
}

// A sync pushes only what the remote does not have: with HEAD already
// pushed and nothing new, it does not contact the remote at all (here
// the remote is gone, and the sync still reports no changes).
func TestGitSyncSkipsPushWhenAlreadyPushed(t *testing.T) {
	newGitFixture(t)
	b := &GitBackend{MachineID: "box"}
	if res := b.Sync(context.Background(), paths.StoreDir); !res.Success || !res.Pushed {
		t.Fatalf("first sync should push: %+v", res)
	}
	gitexec.SetRemoteURL(paths.StoreDir, "origin", filepath.Join(t.TempDir(), "missing.git"))
	res := b.Sync(context.Background(), paths.StoreDir)
	if !res.Success || res.Pushed || res.Message != "No changes to sync" {
		t.Errorf("with HEAD already pushed, the sync should not push: %+v", res)
	}
}
