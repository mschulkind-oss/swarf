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
)

// legacyFixture is a gitFixture whose remote also has a legacy branch,
// main, that this store has folded in: the store's HEAD is main's tip.
// It returns that tip.
func (f gitFixture) legacyMain(t *testing.T) string {
	t.Helper()
	git(t, paths.StoreDir, "add", "-A")
	git(t, paths.StoreDir, "commit", "-q", "-m", "old swarf")
	git(t, paths.StoreDir, "push", "-q", "origin", "HEAD:refs/heads/main")
	tip := f.ref(t, "refs/heads/main")
	if err := gitexec.UpdateRef(paths.StoreDir, LegacyRef("main"), tip); err != nil {
		t.Fatal(err)
	}
	return tip
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// refuseMainHook installs a pre-receive hook on the bare remote that
// refuses deleting main, as GitHub refuses deleting the default branch.
// With everything set, it refuses every update to main.
func (f gitFixture) refuseMainHook(t *testing.T, everything bool) {
	t.Helper()
	cond := `[ "$new" = 0000000000000000000000000000000000000000 ]`
	if everything {
		cond = "true"
	}
	hook := "#!/bin/sh\nwhile read old new ref; do\n  if [ \"$ref\" = refs/heads/main ] && " + cond + "; then\n    echo \"refusing to change main\" >&2\n    exit 1\n  fi\ndone\nexit 0\n"
	path := filepath.Join(f.bare, "hooks", "pre-receive")
	if err := os.WriteFile(path, []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A folded-in legacy branch is not retired before the machine branch
// has been pushed: until then the remote's only copy of its content is
// the legacy branch itself.
func TestRetireLegacyWaitsForMachineBranch(t *testing.T) {
	f := newGitFixture(t)
	tip := f.legacyMain(t)
	if got := RetireLegacyBranches(context.Background(), paths.StoreDir, "box"); len(got) != 0 {
		t.Errorf("nothing should be retired before the machine branch is pushed, got %+v", got)
	}
	if got := f.ref(t, "refs/heads/main"); got != tip {
		t.Errorf("remote main = %q, want it untouched at %q", got, tip)
	}
}

// Once the machine branch holds the legacy tip, the legacy branch is
// deleted from the remote and the store forgets it.
func TestRetireLegacyDeletesBranch(t *testing.T) {
	f := newGitFixture(t)
	f.legacyMain(t)
	if res := (&GitBackend{MachineID: "box"}).Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("sync: %s", res.Message)
	}
	got := RetireLegacyBranches(context.Background(), paths.StoreDir, "box")
	if len(got) != 1 || got[0].Branch != "main" || got[0].Err != nil || got[0].Tombstoned {
		t.Fatalf("main should be retired by deleting it, got %+v", got)
	}
	if got := f.ref(t, "refs/heads/main"); got != "" {
		t.Errorf("remote main should be deleted, still at %q", got)
	}
	if gitexec.ReadRef(paths.StoreDir, LegacyRef("main")) != "" {
		t.Error("the store should forget a retired legacy branch")
	}
}

// When the remote refuses deleting the legacy branch (GitHub refuses
// deleting the default branch), it is replaced with a tombstone: a
// commit with no parents holding only SWARF-BRANCHES.md, with the
// Swarf-Tombstone trailer.
func TestRetireLegacyTombstonesWhenDeleteRefused(t *testing.T) {
	f := newGitFixture(t)
	f.legacyMain(t)
	f.refuseMainHook(t, false)
	if res := (&GitBackend{MachineID: "box"}).Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("sync: %s", res.Message)
	}
	got := RetireLegacyBranches(context.Background(), paths.StoreDir, "box")
	if len(got) != 1 || got[0].Err != nil || !got[0].Tombstoned {
		t.Fatalf("main should be retired with a tombstone, got %+v", got)
	}
	tomb := f.ref(t, "refs/heads/main")
	if tomb == "" {
		t.Fatal("remote main should hold the tombstone")
	}
	if files := strings.TrimSpace(gitBare(t, f.bare, "ls-tree", "--name-only", tomb)); files != TombstoneFile {
		t.Errorf("the tombstone should hold only %s, has:\n%s", TombstoneFile, files)
	}
	if parents := gitBare(t, f.bare, "rev-list", "--parents", "-n", "1", tomb); parents != tomb {
		t.Errorf("the tombstone should have no parents: %s", parents)
	}
	if msg := gitBare(t, f.bare, "log", "-1", "--format=%B", tomb); !strings.Contains(msg, "Swarf-Tombstone: true") {
		t.Errorf("the tombstone's message should carry the trailer:\n%s", msg)
	}
	if !IsTombstone(paths.StoreDir, tomb) {
		t.Error("IsTombstone should recognize the tombstone")
	}
	if IsTombstone(paths.StoreDir, gitexec.RevParseHEAD(paths.StoreDir)) {
		t.Error("an ordinary commit is not a tombstone")
	}
	if RetireRefusal(paths.StoreDir, "main") != "" {
		t.Error("a tombstoned branch has no retirement refusal")
	}
}

// If the legacy branch moved after the store folded it in (a machine
// still on an older swarf pushed to it), retiring does nothing, so the
// new commits stay on the remote for the next pass to fold in.
func TestRetireLegacySkipsBranchThatMoved(t *testing.T) {
	f := newGitFixture(t)
	f.legacyMain(t)
	if res := (&GitBackend{MachineID: "box"}).Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("sync: %s", res.Message)
	}
	moved := f.pushOnto(t, "main")
	got := RetireLegacyBranches(context.Background(), paths.StoreDir, "box")
	if len(got) != 1 || !got[0].Moved || got[0].Err != nil {
		t.Fatalf("main should be left alone as moved, got %+v", got)
	}
	if got := f.ref(t, "refs/heads/main"); got != moved {
		t.Errorf("remote main = %q, want the new commit %q kept", got, moved)
	}
}

// When the remote refuses both deleting the legacy branch and replacing
// it, it stays, and the refusal is recorded for 'swarf doctor'.
func TestRetireLegacyRecordsRefusal(t *testing.T) {
	f := newGitFixture(t)
	tip := f.legacyMain(t)
	f.refuseMainHook(t, true)
	if res := (&GitBackend{MachineID: "box"}).Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("sync: %s", res.Message)
	}
	got := RetireLegacyBranches(context.Background(), paths.StoreDir, "box")
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("retiring main should fail, got %+v", got)
	}
	if got := f.ref(t, "refs/heads/main"); got != tip {
		t.Errorf("remote main = %q, want it untouched at %q", got, tip)
	}
	if reason := RetireRefusal(paths.StoreDir, "main"); !strings.Contains(reason, "refusing to change main") {
		t.Errorf("the refusal should be recorded with the remote's reason, got %q", reason)
	}
}

// pushOnto pushes a new commit on top of branch on the remote, the way
// a machine still running an older swarf would, and returns it.
func (f gitFixture) pushOnto(t *testing.T, branch string) string {
	t.Helper()
	other := t.TempDir()
	git(t, other, "init", "-q")
	git(t, other, "fetch", "-q", f.bare, branch)
	git(t, other, "reset", "-q", "--hard", "FETCH_HEAD")
	os.WriteFile(filepath.Join(other, "older.md"), []byte("older swarf"), 0o644)
	git(t, other, "add", "-A")
	git(t, other, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "-q", "-m", "older swarf")
	git(t, other, "push", "-q", f.bare, "HEAD:refs/heads/"+branch)
	return f.ref(t, "refs/heads/"+branch)
}

func gitBare(t *testing.T, bare string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"--git-dir", bare}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
