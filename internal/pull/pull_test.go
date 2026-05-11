package pull

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestPullNoConfig(t *testing.T) {
	testutil.GitRepo(t)
	err := Run()
	if err != ErrNoConfig {
		t.Fatalf("expected ErrNoConfig, got %v", err)
	}
}

func TestPullGitNoStoreNoRemote(t *testing.T) {
	testutil.GitRepo(t)
	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	if err := Run(); err != ErrNotGitRepo {
		t.Fatalf("expected ErrNotGitRepo when store missing and no remote, got %v", err)
	}
}

func TestPullGitBootstrapsFromRemote(t *testing.T) {
	// A fresh machine with no store but a valid git remote should clone.
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)

	// Seed bare remote with a commit.
	staging := filepath.Join(t.TempDir(), "staging")
	runGit(t, "", "clone", bare, staging)
	runGit(t, staging, "config", "user.email", "t@t")
	runGit(t, staging, "config", "user.name", "t")
	os.WriteFile(filepath.Join(staging, "seed.txt"), []byte("seed"), 0o644)
	runGit(t, staging, "add", "-A")
	runGit(t, staging, "commit", "-m", "seed")
	runGit(t, staging, "push", "origin", "HEAD")

	paths.StoreDir = filepath.Join(t.TempDir(), "fresh-store")
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s"})

	if err := Run(); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "seed.txt")); err != nil {
		t.Fatalf("expected seed.txt in store, got: %v", err)
	}
}

func TestPullUnknownBackend(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "unknown", Remote: "", Debounce: "5s"})
	err := Run()
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

	err := Run()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPullGitPullFailure(t *testing.T) {
	// Store is a git repo but has no remote, so pull will fail.
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})

	err := Run()
	if err == nil {
		t.Fatal("expected error from git pull with no remote")
	}
	// The error should be wrapped with "git pull:" prefix.
	if err.Error()[:9] != "git pull:" {
		t.Fatalf("expected 'git pull:' prefix, got %v", err)
	}
}

func TestPullRcloneNotInstalled(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s"})

	// Override PATH so rclone can't be found.
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	err := Run()
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
	// warning and continues — so Run() should return nil overall.
	runGit(t, paths.StoreDir, "config", "user.email", "t@t")
	runGit(t, paths.StoreDir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(paths.StoreDir, "seed.txt"), []byte("x"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "seed")

	err := Run()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPullRcloneFailure(t *testing.T) {
	testutil.InitializedSwarf(t)

	// Fake rclone that fails on every invocation. The first failure is on
	// `rclone lsf` against the machines/ path, which surfaces as an error
	// from listPeers → Run().
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

	err := Run()
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
