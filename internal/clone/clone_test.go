package clone

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

func TestCloneNoConfig(t *testing.T) {
	testutil.GitRepo(t)
	err := Run()
	if err != ErrNoConfig {
		t.Fatalf("expected ErrNoConfig, got %v", err)
	}
}

func TestCloneNoRemote(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	err := Run()
	if err != ErrNoRemote {
		t.Fatalf("expected ErrNoRemote, got %v", err)
	}
}

func TestCloneStoreExists(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"})
	err := Run()
	if err != ErrStoreExists {
		t.Fatalf("expected ErrStoreExists, got %v", err)
	}
}

func TestCloneUnknownBackend(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "unknown", Remote: "test", Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "nonexistent")
	err := Run()
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !errors.Is(err, ErrUnknownBackend) {
		t.Fatalf("expected ErrUnknownBackend, got %v", err)
	}
}

func TestCloneGitFromBare(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "store")
	err := Run()
	if err != nil {
		t.Fatal(err)
	}
}

func TestCloneGitWithProjects(t *testing.T) {
	// Create a bare remote with some project directories.
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)

	// Clone the bare, add project dirs, push back.
	staging := filepath.Join(t.TempDir(), "staging")
	runGit(t, "", "clone", bare, staging)
	runGit(t, staging, "config", "user.email", "test@test.com")
	runGit(t, staging, "config", "user.name", "Test")

	os.MkdirAll(filepath.Join(staging, "projectA"), 0o755)
	os.WriteFile(filepath.Join(staging, "projectA", ".keep"), []byte{}, 0o644)
	os.MkdirAll(filepath.Join(staging, "projectB"), 0o755)
	os.WriteFile(filepath.Join(staging, "projectB", ".keep"), []byte{}, 0o644)
	// Also create a regular file (not a dir) to make sure it's not counted.
	os.WriteFile(filepath.Join(staging, "README.md"), []byte("hi"), 0o644)

	runGit(t, staging, "add", "-A")
	runGit(t, staging, "commit", "-m", "init")
	runGit(t, staging, "push", "origin", "HEAD:refs/heads/master")

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "store")

	err := Run()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify project dirs exist in the store.
	for _, name := range []string{"projectA", "projectB"} {
		info, err := os.Stat(filepath.Join(paths.StoreDir, name))
		if err != nil || !info.IsDir() {
			t.Errorf("expected directory %s in store", name)
		}
	}
}

func TestCloneGitEmptyStore(t *testing.T) {
	// Clone an empty bare remote (no commits).
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "store")

	err := Run()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCloneGitRemovesExistingNonGitStore(t *testing.T) {
	// If StoreDir exists but is NOT a git repo, clone should succeed
	// (the StoreExists guard only triggers if it IS a git repo).
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)

	storeDir := filepath.Join(t.TempDir(), "store")
	os.MkdirAll(storeDir, 0o755)
	os.WriteFile(filepath.Join(storeDir, "leftover.txt"), []byte("old"), 0o644)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s"})
	paths.StoreDir = storeDir

	err := Run()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCloneGitBadRemote(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "/nonexistent/repo.git", Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "store")

	err := Run()
	if err == nil {
		t.Fatal("expected error from clone with bad remote")
	}
}

func TestCloneRcloneNotInstalled(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "nonexistent")

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

func TestCloneRcloneSuccess(t *testing.T) {
	testutil.GitRepo(t)

	// Create a fake rclone that succeeds.
	fakeDir := t.TempDir()
	fakeRclone := filepath.Join(fakeDir, "rclone")
	os.WriteFile(fakeRclone, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeDir)
	defer os.Setenv("PATH", origPath)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "store")

	err := Run()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCloneRcloneFailure(t *testing.T) {
	testutil.GitRepo(t)

	// Create a fake rclone that fails.
	fakeDir := t.TempDir()
	fakeRclone := filepath.Join(fakeDir, "rclone")
	os.WriteFile(fakeRclone, []byte("#!/bin/sh\necho 'rclone error'\nexit 1\n"), 0o755)

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeDir)
	defer os.Setenv("PATH", origPath)

	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "rclone", Remote: "remote:path", Debounce: "5s"})
	paths.StoreDir = filepath.Join(t.TempDir(), "store")

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
