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

func TestInitCreatesSwarfDir(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	if err := initialize.Run(testConfig); err != nil {
		t.Fatal(err)
	}
	sd := paths.SwarfDir(repo)
	fi, err := os.Stat(sd)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatal("expected swarf dir to be a directory")
	}
}

func TestInitCreatesLinksDir(t *testing.T) {
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	linksDir := paths.LinksDir(repo)
	fi, err := os.Stat(linksDir)
	if err != nil || !fi.IsDir() {
		t.Fatal("expected swarf/.links/ to be a directory")
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

func TestInitRegistersDrawer(t *testing.T) {
	testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	initialize.Run(testConfig)
	drawers := config.ReadDrawers()
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
}

func TestInitIsIdempotent(t *testing.T) {
	// Re-running init in an already-set-up project is a no-error refresh:
	// re-registers the drawer, re-creates any missing symlinks, touches
	// excludes. This is what 'swarf init' does when a user runs it after
	// deleting a linked file by accident.
	repo := testutil.GitRepo(t)
	config.WriteGlobalConfig(testConfig)
	if err := initialize.Run(testConfig); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Drop a file into .links/ and remove any symlink; a second init should
	// restore the symlink without erroring.
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	os.Remove(filepath.Join(repo, "AGENTS.md"))

	if err := initialize.Run(testConfig); err != nil {
		t.Fatalf("second init should be idempotent, got: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatalf("expected symlink recreated by re-init: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected a symlink after re-init")
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
	if !strings.Contains(content, "/"+paths.SwarfDirName+"/") {
		t.Fatal("expected swarf dir in exclude")
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
