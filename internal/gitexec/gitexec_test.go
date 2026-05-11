package gitexec

import (
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
