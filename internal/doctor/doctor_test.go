package doctor_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/doctor"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestCheckGlobalConfigMissing(t *testing.T) {
	testutil.GitRepo(t)
	c := doctor.CheckGlobalConfig()
	if c.OK {
		t.Fatal("expected config missing")
	}
}

func TestCheckGlobalConfigPresent(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"})
	c := doctor.CheckGlobalConfig()
	if !c.OK {
		t.Fatalf("expected config present: %s", c.Msg)
	}
}

func TestCheckGlobalConfigNoRemote(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	c := doctor.CheckGlobalConfig()
	if c.OK {
		t.Fatal("expected no remote error")
	}
}

func TestCheckStoreExists(t *testing.T) {
	testutil.InitializedSwarf(t)
	c := doctor.CheckStoreExists()
	if !c.OK {
		t.Fatalf("expected store to exist: %s", c.Msg)
	}
}

func TestCheckStoreMissing(t *testing.T) {
	testutil.GitRepo(t)
	c := doctor.CheckStoreExists()
	if c.OK {
		t.Fatal("expected store to not exist")
	}
}

func TestCheckStoreRemoteMissing(t *testing.T) {
	testutil.InitializedSwarf(t)
	c := doctor.CheckStoreRemote()
	if c.OK {
		t.Fatal("expected no remote")
	}
}

func TestCheckDaemonNotRunning(t *testing.T) {
	tmp := t.TempDir()
	paths.PIDFile = filepath.Join(tmp, "nonexistent.pid")
	c := doctor.CheckDaemonRunning()
	if c.OK {
		t.Fatal("expected daemon not running")
	}
}

func TestCheckDaemonStalePid(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "daemon.pid")
	os.WriteFile(pidFile, []byte("999999999"), 0o644)
	paths.PIDFile = pidFile
	c := doctor.CheckDaemonRunning()
	if c.OK {
		t.Fatal("expected daemon not running (stale)")
	}
}

func TestCheckSwarfDirExists(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	c := doctor.CheckSwarfDirExists(repo)
	if !c.OK {
		t.Fatalf("expected .swarf to exist: %s", c.Msg)
	}
	if !strings.Contains(c.Msg, "directory exists") {
		t.Fatalf("expected 'directory exists' in message: %s", c.Msg)
	}
}

func TestCheckSwarfDirMissing(t *testing.T) {
	repo := testutil.GitRepo(t)
	c := doctor.CheckSwarfDirExists(repo)
	if c.OK {
		t.Fatal("expected .swarf missing")
	}
}

func TestCheckSwarfDirPlainDir(t *testing.T) {
	repo := testutil.GitRepo(t)
	os.MkdirAll(filepath.Join(repo, ".swarf"), 0o755)
	c := doctor.CheckSwarfDirExists(repo)
	if !c.OK {
		t.Fatalf("expected .swarf dir ok: %s", c.Msg)
	}
	if !strings.Contains(c.Msg, "directory exists") {
		t.Fatalf("expected 'directory exists': %s", c.Msg)
	}
}

func TestCheckMiseLocalMissing(t *testing.T) {
	repo := testutil.GitRepo(t)
	c := doctor.CheckMiseLocal(repo)
	if c.OK {
		t.Fatal("expected mise local missing")
	}
}

func TestCheckMiseLocalPresent(t *testing.T) {
	repo := testutil.GitRepo(t)
	os.WriteFile(filepath.Join(repo, ".mise.local.toml"), []byte("[hooks]\nenter = \"swarf enter\"\n"), 0o644)
	c := doctor.CheckMiseLocal(repo)
	if !c.OK {
		t.Fatalf("expected mise local present: %s", c.Msg)
	}
}

func TestCheckMiseLocalMissingHook(t *testing.T) {
	repo := testutil.GitRepo(t)
	os.WriteFile(filepath.Join(repo, ".mise.local.toml"), []byte("[tools]\npython = '3.13'\n"), 0o644)
	c := doctor.CheckMiseLocal(repo)
	if c.OK {
		t.Fatal("expected missing hook")
	}
}

func TestCheckLinksHealthy(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(repo, ".swarf", "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	target := filepath.Join(repo, "AGENTS.md")
	os.Symlink(source, target)
	c := doctor.CheckLinksHealthy(repo)
	if !c.OK {
		t.Fatalf("expected links healthy: %s", c.Msg)
	}
}

func TestCheckBrokenSymlink(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(repo, ".swarf", "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	target := filepath.Join(repo, "AGENTS.md")
	os.Symlink(filepath.Join(repo, "nonexistent"), target)
	c := doctor.CheckLinksHealthy(repo)
	if c.OK {
		t.Fatal("expected broken symlink detected")
	}
}

func TestCheckLinksNoDir(t *testing.T) {
	repo := testutil.GitRepo(t)
	c := doctor.CheckLinksHealthy(repo)
	if !c.OK {
		t.Fatal("expected ok when no links dir")
	}
}

func TestCheckGitignore(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	exclude.UpdateExcludes(repo, nil)
	checks := doctor.CheckGitignore(repo)
	for _, c := range checks {
		if c.Name == ".swarf/" && !c.OK {
			t.Fatalf(".swarf/ should be gitignored: %s", c.Msg)
		}
		if c.Name == ".mise.local.toml" && !c.OK {
			t.Fatalf(".mise.local.toml should be gitignored: %s", c.Msg)
		}
	}
}

func TestCheckGitignoreNotInRepo(t *testing.T) {
	tmp := t.TempDir()
	checks := doctor.CheckGitignore(tmp)
	if len(checks) != 1 || checks[0].OK {
		t.Fatal("expected 'not in repo' check failure")
	}
}

func TestCheckStoreNotGitRepo(t *testing.T) {
	testutil.GitRepo(t)
	// Create store dir that isn't a git repo
	os.MkdirAll(paths.StoreDir, 0o755)
	c := doctor.CheckStoreExists()
	if c.OK {
		t.Fatal("expected failure for non-git store")
	}
	if !strings.Contains(c.Msg, "not a git repository") {
		t.Fatalf("expected 'not a git repository': %s", c.Msg)
	}
}

func TestCheckStoreRemoteNoStore(t *testing.T) {
	testutil.GitRepo(t)
	c := doctor.CheckStoreRemote()
	if c.OK {
		t.Fatal("expected failure when store doesn't exist")
	}
}

func TestCheckStoreRemotePresent(t *testing.T) {
	testutil.InitializedSwarf(t)
	// Add a remote to the store
	os.MkdirAll(paths.StoreDir, 0o755)
	cmd := exec.Command("git", "-C", paths.StoreDir, "remote", "add", "origin", "https://example.com/repo.git")
	cmd.Run()
	c := doctor.CheckStoreRemote()
	if !c.OK {
		t.Fatalf("expected remote present: %s", c.Msg)
	}
}

func TestCheckRemoteReachableNoConfig(t *testing.T) {
	testutil.GitRepo(t)
	c := doctor.CheckRemoteReachable()
	if c.OK {
		t.Fatal("expected failure with no config")
	}
}

func TestCheckRemoteReachableGitBadRemote(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "/nonexistent/repo.git", Debounce: "5s"})
	c := doctor.CheckRemoteReachable()
	if c.OK {
		t.Fatal("expected failure for bad git remote")
	}
}

func TestCheckRemoteReachableUnknownBackend(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "s3", Remote: "bucket", Debounce: "5s"})
	c := doctor.CheckRemoteReachable()
	if c.OK {
		t.Fatal("expected failure for unknown backend")
	}
	if !strings.Contains(c.Msg, "Unknown backend") {
		t.Fatalf("expected 'Unknown backend': %s", c.Msg)
	}
}

func TestCheckDaemonBadPidContent(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "daemon.pid")
	os.WriteFile(pidFile, []byte("notanumber"), 0o644)
	paths.PIDFile = pidFile
	c := doctor.CheckDaemonRunning()
	if c.OK {
		t.Fatal("expected daemon not running for bad PID")
	}
}

func TestCheckGitignoreLinkedFiles(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	// Create a linked file and set up excludes
	linksDir := filepath.Join(repo, ".swarf", "links")
	os.WriteFile(filepath.Join(linksDir, "AGENTS.md"), []byte("# Agents\n"), 0o644)
	exclude.UpdateExcludes(repo, []string{"AGENTS.md"})
	checks := doctor.CheckGitignore(repo)
	foundAgents := false
	for _, c := range checks {
		if c.Name == "AGENTS.md" {
			foundAgents = true
			if !c.OK {
				t.Fatalf("expected AGENTS.md to be gitignored: %s", c.Msg)
			}
		}
	}
	if !foundAgents {
		t.Fatal("expected AGENTS.md check in results")
	}
}

func TestRunAllChecks(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"})
	checks := doctor.RunAllChecks(repo)
	if len(checks) == 0 {
		t.Fatal("expected some checks")
	}
}
