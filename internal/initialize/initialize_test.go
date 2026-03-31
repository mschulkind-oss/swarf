package initialize_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

var testConfig = &config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"}

func TestInitCreatesSymlink(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	if err := initialize.Run(testConfig); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(repo, ".swarf")
	fi, err := os.Lstat(sd)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected .swarf to be a symlink")
	}
	target, _ := os.Readlink(sd)
	if !strings.HasPrefix(target, paths.StoreDir) {
		t.Fatalf("expected symlink into store, got %s", target)
	}
}

func TestInitCreatesLinksDir(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	linksDir := filepath.Join(repo, ".swarf", "links")
	fi, err := os.Stat(linksDir)
	if err != nil || !fi.IsDir() {
		t.Fatal("expected .swarf/links/ to be a directory")
	}
}

func TestInitNoSkeletonFiles(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	entries, _ := os.ReadDir(filepath.Join(repo, ".swarf"))
	for _, e := range entries {
		if e.Name() == "docs" || e.Name() == "open-questions.md" {
			t.Fatalf("unexpected skeleton file: %s", e.Name())
		}
	}
}

func TestInitCreatesStoreGitRepo(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	if fi, err := os.Stat(filepath.Join(paths.StoreDir, ".git")); err != nil || !fi.IsDir() {
		t.Fatal("expected store to be a git repo")
	}
}

func TestInitCreatesMiseLocal(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	data, err := os.ReadFile(filepath.Join(repo, ".mise.local.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "swarf enter") {
		t.Fatal("expected swarf enter in .mise.local.toml")
	}
	if !strings.Contains(content, "[hooks]") {
		t.Fatal("expected [hooks] section")
	}
}

func TestInitRegistersDrawer(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	drawers := config.ReadDrawers()
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
}

func TestInitRefusesIfAlreadyInitialized(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	err := initialize.Run(testConfig)
	if err == nil {
		t.Fatal("expected error for already initialized")
	}
	if !strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitUpdatesGitInfoExclude(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "/.swarf/") {
		t.Fatal("expected /.swarf/ in exclude")
	}
	if !strings.Contains(content, "/.mise.local.toml") {
		t.Fatal("expected /.mise.local.toml in exclude")
	}
}

func TestInitStoreCommit(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	slug := filepath.Base(repo)
	cmd := exec.Command("git", "log", "--oneline", "-1")
	cmd.Dir = paths.StoreDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "init: "+slug) {
		t.Fatalf("expected commit message with 'init: %s', got %s", slug, out)
	}
}

func TestInitWithGitRemote(t *testing.T) {
	testutil.GitRepo(t)
	bare := testutil.BareRemote(t)
	gc := &config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s"}
	config.WriteGlobalConfig(gc)
	if err := initialize.Run(gc); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "remote", "-v")
	cmd.Dir = paths.StoreDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), bare) {
		t.Fatalf("expected remote %s in output, got %s", bare, out)
	}
}

func TestInitExistingProjectInStore(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	slug := filepath.Base(repo)
	projDir := filepath.Join(paths.StoreDir, slug)

	// Pre-create store and project
	os.MkdirAll(paths.StoreDir, 0o755)
	exec.Command("git", "-C", paths.StoreDir, "init").Run()
	exec.Command("git", "-C", paths.StoreDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", paths.StoreDir, "config", "user.name", "Test").Run()
	os.MkdirAll(filepath.Join(projDir, "links"), 0o755)
	os.WriteFile(filepath.Join(projDir, "links", "AGENTS.md"), []byte("# Agents\n"), 0o644)

	err := initialize.Run(testConfig)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(filepath.Join(repo, ".swarf"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected .swarf to be a symlink")
	}
}

func TestInitExistingMiseLocalWarns(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	os.WriteFile(filepath.Join(repo, ".mise.local.toml"), []byte("[tools]\npython = '3.13'\n"), 0o644)

	err := initialize.Run(testConfig)
	if err != nil {
		t.Fatal(err)
	}

	// Original content should be preserved
	data, _ := os.ReadFile(filepath.Join(repo, ".mise.local.toml"))
	if !strings.Contains(string(data), "python") {
		t.Fatal("expected existing content preserved")
	}
}

func TestInitNotInGitRepo(t *testing.T) {
	tmp := t.TempDir()
	os.Chdir(tmp)
	t.Cleanup(func() {})
	err := initialize.Run(testConfig)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not inside a git repository") {
		t.Fatalf("unexpected error: %v", err)
	}
}
