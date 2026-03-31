package gitexec

import (
	"os"
	"os/exec"
	"path/filepath"
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
