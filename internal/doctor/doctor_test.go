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
	gc, c := doctor.CheckGlobalConfig()
	if c.OK {
		t.Fatal("expected config missing")
	}
	if gc != nil {
		t.Fatal("expected nil config")
	}
}

func TestCheckGlobalConfigPresent(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"})
	gc, c := doctor.CheckGlobalConfig()
	if !c.OK {
		t.Fatalf("expected config present: %s", c.Msg)
	}
	if gc == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestCheckGlobalConfigNoRemote(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	gc, c := doctor.CheckGlobalConfig()
	if c.OK {
		t.Fatal("expected no-remote failure")
	}
	if gc == nil {
		t.Fatal("expected non-nil config even without remote")
	}
}

func TestCheckStoreExists(t *testing.T) {
	testutil.InitializedSwarf(t)
	c := doctor.CheckStore(nil)
	if !c.OK {
		t.Fatalf("expected store to exist: %s", c.Msg)
	}
}

func TestCheckStoreMissing(t *testing.T) {
	testutil.GitRepo(t)
	c := doctor.CheckStore(nil)
	if c.OK {
		t.Fatal("expected store to not exist")
	}
}

func TestCheckStoreNoLongerAutoCreates(t *testing.T) {
	// Doctor must never create the store — that's init's job.
	testutil.GitRepo(t)
	gc := &config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"}
	c := doctor.CheckStore(gc)
	if c.OK {
		t.Fatalf("expected store failure, got: %s", c.Msg)
	}
	if paths.IsDir(paths.StoreDir) {
		t.Fatal("doctor should not have created the store directory")
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

func TestCheckLinksHealthy(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	target := filepath.Join(repo, "AGENTS.md")
	os.Symlink(source, target)
	c := doctor.CheckLinks(repo)
	if !c.OK {
		t.Fatalf("expected links healthy: %s", c.Msg)
	}
}

func TestCheckLinksReportsMissingNoLongerFixes(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	// No symlink created.

	c := doctor.CheckLinks(repo)
	if c.OK {
		t.Fatalf("expected failure for missing symlink, got ok: %s", c.Msg)
	}
	// Doctor must not auto-create it.
	if _, err := os.Lstat(filepath.Join(repo, "AGENTS.md")); err == nil {
		t.Fatal("doctor must not create the symlink")
	}
}

func TestCheckLinksNoDir(t *testing.T) {
	repo := testutil.GitRepo(t)
	c := doctor.CheckLinks(repo)
	if !c.OK {
		t.Fatal("expected ok when no links dir")
	}
}

func TestCheckSymlinksRelativeAllGood(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	target := filepath.Join(repo, "AGENTS.md")
	relPath, _ := filepath.Rel(filepath.Dir(target), source)
	os.Symlink(relPath, target)

	c := doctor.CheckSymlinksRelative(repo)
	if !c.OK {
		t.Fatalf("expected ok: %s", c.Msg)
	}
	if !strings.Contains(c.Msg, "All symlinks are relative") {
		t.Fatalf("unexpected msg: %s", c.Msg)
	}
}

func TestCheckSymlinksRelativeReportsAbsoluteNoLongerFixes(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	target := filepath.Join(repo, "AGENTS.md")
	os.Symlink(source, target) // absolute

	c := doctor.CheckSymlinksRelative(repo)
	if c.OK {
		t.Fatalf("expected failure for absolute symlink: %s", c.Msg)
	}
	// Doctor must not rewrite it.
	linkDest, _ := os.Readlink(target)
	if !filepath.IsAbs(linkDest) {
		t.Fatalf("doctor must not rewrite the symlink — still expected absolute, got %q", linkDest)
	}
}

func TestCheckSymlinksRelativeNoLinks(t *testing.T) {
	repo := testutil.GitRepo(t)
	c := doctor.CheckSymlinksRelative(repo)
	if !c.OK {
		t.Fatal("expected ok when no links dir")
	}
}

func TestCheckGitignore(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	exclude.UpdateExcludes(repo, nil)
	checks := doctor.CheckGitignore(repo)
	for _, c := range checks {
		if c.Name == paths.SwarfDirName+"/" && !c.OK {
			t.Fatalf("swarf dir should be gitignored: %s", c.Msg)
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
	os.MkdirAll(paths.StoreDir, 0o755)
	c := doctor.CheckStore(nil)
	if c.OK {
		t.Fatal("expected failure for non-git store")
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
	linksDir := paths.LinksDir(repo)
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

func TestRunChecks(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"})
	result := doctor.RunChecks(repo)
	if len(result.Project) == 0 {
		t.Fatal("expected project checks")
	}
	if len(result.System) == 0 {
		t.Fatal("expected system checks")
	}
	if result.InJail {
		t.Fatal("expected InJail=false with global config present")
	}
}

func TestRunChecksJailMode(t *testing.T) {
	testutil.InitializedSwarf(t)
	// No global config written — simulates jail environment.
	result := doctor.RunChecks("")
	if !result.InJail {
		t.Fatal("expected InJail=true without global config")
	}
	if len(result.System) != 0 {
		t.Fatal("expected no system checks in jail mode")
	}
}
