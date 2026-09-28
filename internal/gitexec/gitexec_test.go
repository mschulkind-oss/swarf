package gitexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	os.MkdirAll(repo, 0o755)
	execCmd(t, repo, "git", "init")
	execCmd(t, repo, "git", "config", "user.email", "test@test.com")
	execCmd(t, repo, "git", "config", "user.name", "Test")
	return repo
}

func execCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %s\n%s", name, args, err, out)
	}
}

func TestInitAndIsRepo(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "new")
	os.MkdirAll(repo, 0o755)
	if err := Init(repo); err != nil {
		t.Fatal(err)
	}
	if !IsRepo(repo) {
		t.Fatal("expected IsRepo true after Init")
	}
}

func TestIsRepoFalse(t *testing.T) {
	tmp := t.TempDir()
	if IsRepo(tmp) {
		t.Fatal("expected IsRepo false for non-repo")
	}
}

func TestConfigSetGet(t *testing.T) {
	repo := makeRepo(t)
	ConfigSet(repo, "user.name", "TestUser")
	got := ConfigGet(repo, "user.name")
	if got != "TestUser" {
		t.Fatalf("got %q, want TestUser", got)
	}
}

func TestConfigGetMissing(t *testing.T) {
	repo := makeRepo(t)
	got := ConfigGet(repo, "nonexistent.key")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestAddAllCommitStatusPorcelain(t *testing.T) {
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello"), 0o644)

	AddAll(repo)
	status := StatusPorcelain(repo)
	if status == "" {
		t.Fatal("expected non-empty status after add")
	}

	if err := Commit(repo, "initial"); err != nil {
		t.Fatal(err)
	}
	status = StatusPorcelain(repo)
	if status != "" {
		t.Fatalf("expected clean status after commit, got %q", status)
	}
}

func TestRemoteURL(t *testing.T) {
	repo := makeRepo(t)
	if url := RemoteURL(repo, ""); url != "" {
		t.Fatalf("expected empty, got %q", url)
	}
	AddRemote(repo, "origin", "https://example.com/repo.git")
	url := RemoteURL(repo, "origin")
	if url != "https://example.com/repo.git" {
		t.Fatalf("got %q", url)
	}
}

func TestGetRepoRoot(t *testing.T) {
	repo := makeRepo(t)
	sub := filepath.Join(repo, "sub", "dir")
	os.MkdirAll(sub, 0o755)
	root := GetRepoRoot(sub)
	if root != repo {
		// Resolve symlinks for comparison
		resolvedRepo, _ := filepath.EvalSymlinks(repo)
		if root != resolvedRepo {
			t.Fatalf("got %q, want %q", root, repo)
		}
	}
}

func TestGetRepoRootNone(t *testing.T) {
	tmp := t.TempDir()
	root := GetRepoRoot(tmp)
	if root != "" {
		t.Fatalf("expected empty, got %q", root)
	}
}

func TestIsInsideWorkTree(t *testing.T) {
	repo := makeRepo(t)
	if !IsInsideWorkTree(repo) {
		t.Fatal("expected true")
	}
	if IsInsideWorkTree(t.TempDir()) {
		t.Fatal("expected false")
	}
}

func TestCheckIgnore(t *testing.T) {
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.log\n"), 0o644)
	if !CheckIgnore("test.log", repo) {
		t.Fatal("expected *.log to be ignored")
	}
	if CheckIgnore("test.txt", repo) {
		t.Fatal("expected .txt NOT ignored")
	}
}

func TestClone(t *testing.T) {
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello"), 0o644)
	AddAll(repo)
	Commit(repo, "initial")

	dest := filepath.Join(t.TempDir(), "clone")
	if err := Clone(repo, dest); err != nil {
		t.Fatal(err)
	}
	if !IsRepo(dest) {
		t.Fatal("clone should be a repo")
	}
}

func TestMergeBase(t *testing.T) {
	// Two branches off a common ancestor: merge-base should return that ancestor.
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	AddAll(repo)
	Commit(repo, "seed")
	base := RevParseHEAD(repo)

	execCmd(t, repo, "git", "checkout", "-b", "branch1")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b"), 0o644)
	AddAll(repo)
	Commit(repo, "b1")
	b1 := RevParseHEAD(repo)

	execCmd(t, repo, "git", "checkout", base)
	execCmd(t, repo, "git", "checkout", "-b", "branch2")
	os.WriteFile(filepath.Join(repo, "c.txt"), []byte("c"), 0o644)
	AddAll(repo)
	Commit(repo, "b2")
	b2 := RevParseHEAD(repo)

	got, err := MergeBase(repo, b1, b2)
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if got != base {
		t.Fatalf("MergeBase: got %s, want %s", got, base)
	}

	// Unrelated histories: fresh repo with no shared ancestor, merge-base
	// returns "" with no error.
	other := makeRepo(t)
	os.WriteFile(filepath.Join(other, "x.txt"), []byte("x"), 0o644)
	AddAll(other)
	Commit(other, "seed2")

	execCmd(t, repo, "git", "fetch", other)
	fetchHead, _ := runStdout(repo, "rev-parse", "FETCH_HEAD")
	fetchHead = strings.TrimSpace(fetchHead)

	got, err = MergeBase(repo, b1, fetchHead)
	if err != nil {
		t.Fatalf("MergeBase (unrelated): %v", err)
	}
	if got != "" {
		t.Fatalf("MergeBase unrelated: expected \"\", got %q", got)
	}
}

func TestReadRefUpdateRef(t *testing.T) {
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	AddAll(repo)
	Commit(repo, "seed")
	head := RevParseHEAD(repo)

	// Missing ref returns empty.
	if got := ReadRef(repo, "refs/swarf-peers/nobody"); got != "" {
		t.Fatalf("expected empty for missing ref, got %q", got)
	}

	// Write and read back.
	if err := UpdateRef(repo, "refs/swarf-peers/peer1", head); err != nil {
		t.Fatalf("UpdateRef: %v", err)
	}
	if got := ReadRef(repo, "refs/swarf-peers/peer1"); got != head {
		t.Fatalf("ReadRef: got %q, want %q", got, head)
	}
}

func TestDiffNameStatus(t *testing.T) {
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("k"), 0o644)
	os.WriteFile(filepath.Join(repo, "gone.txt"), []byte("g"), 0o644)
	AddAll(repo)
	Commit(repo, "seed")
	base := RevParseHEAD(repo)

	os.WriteFile(filepath.Join(repo, "added.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("k2"), 0o644)
	os.Remove(filepath.Join(repo, "gone.txt"))
	AddAll(repo)
	Commit(repo, "change")
	head := RevParseHEAD(repo)

	entries, err := DiffNameStatus(repo, base, head)
	if err != nil {
		t.Fatalf("DiffNameStatus: %v", err)
	}
	byPath := make(map[string]string)
	for _, e := range entries {
		byPath[e.Path] = e.Status
	}
	if byPath["added.txt"] != "A" {
		t.Fatalf("added.txt: got %q, want A (entries: %+v)", byPath["added.txt"], entries)
	}
	if byPath["keep.txt"] != "M" {
		t.Fatalf("keep.txt: got %q, want M", byPath["keep.txt"])
	}
	if byPath["gone.txt"] != "D" {
		t.Fatalf("gone.txt: got %q, want D", byPath["gone.txt"])
	}
}

func TestDiffNameStatusEmptyOld(t *testing.T) {
	// An empty oldRef diffs from the empty tree: every file in newRef is "A".
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b"), 0o644)
	AddAll(repo)
	Commit(repo, "seed")
	head := RevParseHEAD(repo)

	entries, err := DiffNameStatus(repo, "", head)
	if err != nil {
		t.Fatalf("DiffNameStatus: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %+v", entries)
	}
	for _, e := range entries {
		if e.Status != "A" {
			t.Fatalf("from empty tree, expected A; got %+v", e)
		}
	}
}

func TestShowFile(t *testing.T) {
	repo := makeRepo(t)
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hi there"), 0o644)
	AddAll(repo)
	Commit(repo, "seed")
	head := RevParseHEAD(repo)

	data, err := ShowFile(repo, head, "hello.txt")
	if err != nil {
		t.Fatalf("ShowFile: %v", err)
	}
	if string(data) != "hi there" {
		t.Fatalf("got %q, want %q", string(data), "hi there")
	}

	if _, err := ShowFile(repo, head, "missing.txt"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestPush(t *testing.T) {
	// Push to a bare remote
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "bare.git")
	exec.Command("git", "init", "--bare", bare).Run()

	repo := makeRepo(t)
	// Ensure we're on a named branch
	exec.Command("git", "-C", repo, "checkout", "-b", "main").Run()
	os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello"), 0o644)
	AddAll(repo)
	Commit(repo, "initial")
	AddRemote(repo, "origin", bare)
	ConfigSet(repo, "push.autoSetupRemote", "true")

	if err := Push(repo, "origin"); err != nil {
		t.Fatal(err)
	}
}

// PushRef pushes HEAD to the named ref only, reports a non-fast-forward
// refusal as ErrPushRejected, and replaces the ref when forced.
func TestPushRef(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "bare.git")
	execCmd(t, "", "git", "init", "--bare", bare)
	ctx := context.Background()
	const ref = "refs/heads/machines/box"

	repo := makeRepo(t)
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "mine")
	AddRemote(repo, "origin", bare)
	if err := PushRef(ctx, repo, "origin", ref, false); err != nil {
		t.Fatalf("PushRef: %v", err)
	}
	if sha, ok, err := LsRemoteRef(repo, "origin", ref); err != nil || !ok || sha != RevParseHEAD(repo) {
		t.Fatalf("LsRemoteRef = %q, %v, %v; want HEAD %q", sha, ok, err, RevParseHEAD(repo))
	}
	if _, ok, err := LsRemoteRef(repo, "origin", "refs/heads/nope"); ok || err != nil {
		t.Errorf("a missing ref should be ok=false with no error, got ok=%v err=%v", ok, err)
	}
	if _, _, err := LsRemoteRef(repo, filepath.Join(t.TempDir(), "missing.git"), ref); err == nil {
		t.Error("an unreadable remote should be an error")
	}

	other := makeRepo(t)
	execCmd(t, other, "git", "commit", "--allow-empty", "-m", "theirs")
	AddRemote(other, "origin", bare)
	err := PushRef(ctx, other, "origin", ref, false)
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("pushing unrelated history should be ErrPushRejected, got %v", err)
	}
	if err := PushRef(ctx, other, "origin", ref, true); err != nil {
		t.Fatalf("forced PushRef: %v", err)
	}
	if sha, _, _ := LsRemoteRef(other, "origin", ref); sha != RevParseHEAD(other) {
		t.Errorf("after a forced push the ref = %q, want %q", sha, RevParseHEAD(other))
	}

	if err := PushRef(ctx, repo, filepath.Join(t.TempDir(), "missing.git"), ref, false); err == nil || errors.Is(err, ErrPushRejected) {
		t.Errorf("an unreachable remote should be a plain error, got %v", err)
	}
}

// A server-side refusal (here a pre-receive hook) is a plain error, not
// ErrPushRejected, forced or not: only a non-fast-forward means another
// repository wrote the branch.
func TestPushRefHookRefusalIsNotRejected(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "bare.git")
	execCmd(t, "", "git", "init", "--bare", bare)
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := makeRepo(t)
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "mine")
	AddRemote(repo, "origin", bare)
	for _, force := range []bool{false, true} {
		err := PushRef(context.Background(), repo, "origin", "refs/heads/machines/box", force)
		if err == nil || errors.Is(err, ErrPushRejected) {
			t.Errorf("force=%v: a hook's refusal should be a plain error, got %v", force, err)
		}
	}
}

func TestIsAncestor(t *testing.T) {
	repo := makeRepo(t)
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "one")
	first := RevParseHEAD(repo)
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "two")
	second := RevParseHEAD(repo)
	if !IsAncestor(repo, first, second) || !IsAncestor(repo, second, second) {
		t.Error("a commit and its parent should both be ancestors of it")
	}
	if IsAncestor(repo, second, first) {
		t.Error("a child is not an ancestor of its parent")
	}
	if IsAncestor(repo, "0123456789abcdef0123456789abcdef01234567", second) {
		t.Error("a commit this repository does not have is not an ancestor")
	}
}

// FetchRefspec writes only the refspec's destination refs: no
// remote-tracking branches, no tags. It prunes a destination ref whose
// source branch is gone, and ListRefs reads the namespace back.
func TestFetchRefspecAndListRefs(t *testing.T) {
	src := makeRepo(t)
	os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644)
	execCmd(t, src, "git", "add", "f")
	execCmd(t, src, "git", "commit", "-m", "one")
	execCmd(t, src, "git", "tag", "v1")
	execCmd(t, src, "git", "branch", "machines/a")
	execCmd(t, src, "git", "branch", "machines/b")
	sha := RevParseHEAD(src)

	dst := makeRepo(t)
	execCmd(t, dst, "git", "remote", "add", "origin", src)
	spec := "+refs/heads/machines/*:refs/swarf-machines/*"
	if err := FetchRefspec(context.Background(), dst, "origin", spec); err != nil {
		t.Fatal(err)
	}
	refs, err := ListRefs(dst, "refs/swarf-machines/")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs["a"] != sha || refs["b"] != sha {
		t.Fatalf("ListRefs = %v, want a and b at %s", refs, sha)
	}
	if others, _ := ListRefs(dst, "refs/remotes/"); len(others) != 0 {
		t.Errorf("fetch must not write remote-tracking branches: %v", others)
	}
	if tags, _ := ListRefs(dst, "refs/tags/"); len(tags) != 0 {
		t.Errorf("fetch must not write tags: %v", tags)
	}

	execCmd(t, src, "git", "branch", "-D", "machines/b")
	if err := FetchRefspec(context.Background(), dst, "origin", spec); err != nil {
		t.Fatal(err)
	}
	if refs, _ := ListRefs(dst, "refs/swarf-machines/"); len(refs) != 1 || refs["a"] != sha {
		t.Errorf("a removed branch should be pruned, got %v", refs)
	}
}

// ResetHard starts a repository with no commits at a fetched commit,
// working tree included.
func TestResetHardFromNoCommits(t *testing.T) {
	src := makeRepo(t)
	os.WriteFile(filepath.Join(src, "f"), []byte("content"), 0o644)
	execCmd(t, src, "git", "add", "f")
	execCmd(t, src, "git", "commit", "-m", "one")
	sha := RevParseHEAD(src)

	dst := makeRepo(t)
	execCmd(t, dst, "git", "fetch", src, "HEAD")
	if err := ResetHard(dst, sha); err != nil {
		t.Fatal(err)
	}
	if got := RevParseHEAD(dst); got != sha {
		t.Errorf("HEAD = %q, want %q", got, sha)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "f")); string(got) != "content" {
		t.Errorf("working tree f = %q", got)
	}
	if err := ResetHard(dst, "0000000000000000000000000000000000000000"); err == nil {
		t.Error("resetting to a commit the repository lacks should fail")
	}
}

// PushLease updates or deletes a remote branch only while it points at
// the expected commit: a branch that moved is left alone and reported as
// ErrStaleLease, and a hook's refusal is a different error.
func TestPushLease(t *testing.T) {
	repo := makeRepo(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	execCmd(t, "", "git", "init", "--bare", bare)
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "one")
	one := RevParseHEAD(repo)
	execCmd(t, repo, "git", "push", bare, "HEAD:refs/heads/main")
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "two")
	two := RevParseHEAD(repo)

	if err := PushLease(context.Background(), repo, bare, two, "refs/heads/main", two); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("a lease on a commit the branch is not at should be stale, got %v", err)
	}
	if err := PushLease(context.Background(), repo, bare, two, "refs/heads/main", one); err != nil {
		t.Fatalf("push with a lease that holds: %v", err)
	}
	if sha, _, _ := LsRemoteRef(repo, bare, "refs/heads/main"); sha != two {
		t.Fatalf("main = %q, want %q", sha, two)
	}

	hook := "#!/bin/sh\necho no >&2\nexit 1\n"
	os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte(hook), 0o755)
	err := PushLease(context.Background(), repo, bare, "", "refs/heads/main", two)
	if err == nil || errors.Is(err, ErrStaleLease) {
		t.Fatalf("a hook's refusal should be an error other than ErrStaleLease, got %v", err)
	}
	os.Remove(filepath.Join(bare, "hooks", "pre-receive"))
	if err := PushLease(context.Background(), repo, bare, "", "refs/heads/main", two); err != nil {
		t.Fatalf("delete with a lease that holds: %v", err)
	}
	if _, ok, _ := LsRemoteRef(repo, bare, "refs/heads/main"); ok {
		t.Error("main should be deleted")
	}
}

// CommitInfo reads a root commit's empty parent list, not its message's
// first line, as the parents.
func TestCommitInfoRootCommit(t *testing.T) {
	repo := makeRepo(t)
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "root\n\nTrailer: yes")
	parents, msg, ok := CommitInfo(repo, RevParseHEAD(repo))
	if !ok || len(parents) != 0 || !strings.Contains(msg, "Trailer: yes") || !strings.HasPrefix(msg, "root") {
		t.Fatalf("CommitInfo(root) = %v, %q, %v", parents, msg, ok)
	}
	execCmd(t, repo, "git", "commit", "--allow-empty", "-m", "child")
	if parents, _, _ := CommitInfo(repo, "HEAD"); len(parents) != 1 {
		t.Errorf("a child commit has one parent, got %v", parents)
	}
	if _, _, ok := CommitInfo(repo, strings.Repeat("0", 40)); ok {
		t.Error("a commit the repository does not have is not ok")
	}
}

// UntrackUpstreams leaves a clone with no upstream and no
// remote-tracking branches, including the origin/HEAD symbolic ref.
func TestUntrackUpstreams(t *testing.T) {
	src := makeRepo(t)
	execCmd(t, src, "git", "commit", "--allow-empty", "-m", "one")
	clone := filepath.Join(t.TempDir(), "clone")
	if err := Clone(src, clone); err != nil {
		t.Fatal(err)
	}
	if err := UntrackUpstreams(clone, "origin"); err != nil {
		t.Fatal(err)
	}
	out, _ := runStdout(clone, "for-each-ref", "--format=%(refname) %(upstream)")
	if strings.Contains(out, "refs/remotes/") {
		t.Errorf("the clone still tracks origin:\n%s", out)
	}
	if RevParseHEAD(clone) == "" {
		t.Error("untracking must not touch the local branch")
	}
}

// After UntrackUpstreams, a push to or fetch from the remote does not
// recreate remote-tracking branches: the clone's fetch refspec, which
// git uses to update them opportunistically, is gone too.
func TestUntrackUpstreamsStaysUntracked(t *testing.T) {
	src := makeRepo(t)
	execCmd(t, src, "git", "commit", "--allow-empty", "-m", "one")
	clone := filepath.Join(t.TempDir(), "clone")
	if err := Clone(src, clone); err != nil {
		t.Fatal(err)
	}
	if err := UntrackUpstreams(clone, "origin"); err != nil {
		t.Fatal(err)
	}
	execCmd(t, clone, "git", "push", "-q", "origin", "HEAD:refs/heads/machines/box")
	if err := Fetch(clone, "origin"); err != nil {
		t.Fatal(err)
	}
	if out, _ := runStdout(clone, "for-each-ref", "--format=%(refname)", "refs/remotes/"); strings.TrimSpace(out) != "" {
		t.Errorf("a push or fetch recreated remote-tracking branches:\n%s", out)
	}
}
