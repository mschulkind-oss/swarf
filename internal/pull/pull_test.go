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

func TestPullNoStore(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	err := Run()
	if err != ErrNoStore {
		t.Fatalf("expected ErrNoStore, got %v", err)
	}
}

func TestPullNotGitRepo(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	// Create store dir that is NOT a git repo
	paths.StoreDir = t.TempDir()
	err := Run()
	if err != ErrNotGitRepo {
		t.Fatalf("expected ErrNotGitRepo, got %v", err)
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
	runGit(t, staging, "push", "origin", "HEAD:refs/heads/master")

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

	// Create a fake rclone that succeeds.
	fakeDir := t.TempDir()
	fakeRclone := filepath.Join(fakeDir, "rclone")
	os.WriteFile(fakeRclone, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeDir)
	defer os.Setenv("PATH", origPath)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s"})

	err := Run()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPullRcloneFailure(t *testing.T) {
	testutil.InitializedSwarf(t)

	// Create a fake rclone that fails.
	fakeDir := t.TempDir()
	fakeRclone := filepath.Join(fakeDir, "rclone")
	os.WriteFile(fakeRclone, []byte("#!/bin/sh\necho 'rclone error: bad remote'\nexit 1\n"), 0o755)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeDir)
	defer os.Setenv("PATH", origPath)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s"})

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
