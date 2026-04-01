package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

// GitRepo creates a temporary git repo, sets up isolated swarf paths, and
// chdirs into it. Returns the repo path. Cleanup is automatic.
func GitRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "project")
	os.MkdirAll(repo, 0o755)

	run(t, repo, "git", "init")
	run(t, repo, "git", "config", "user.email", "test@test.com")
	run(t, repo, "git", "config", "user.name", "Test")

	// Isolate swarf paths
	configDir := filepath.Join(tmp, "config", "swarf")
	storeDir := filepath.Join(tmp, "data", "swarf")
	os.MkdirAll(configDir, 0o755)

	paths.ConfigDir = configDir
	paths.StoreDir = storeDir
	paths.GlobalConfigTOML = filepath.Join(configDir, "config.toml")
	paths.DrawersTOML = filepath.Join(configDir, "drawers.toml")
	paths.PIDFile = filepath.Join(configDir, "daemon.pid")
	paths.LogFile = filepath.Join(configDir, "daemon.log")

	oldDir, _ := os.Getwd()
	os.Chdir(repo)
	t.Cleanup(func() { os.Chdir(oldDir) })

	return repo
}

// InitializedSwarf creates a git repo with swarf fully initialized:
// store as git repo, project dir with .swarf/ as a real directory.
func InitializedSwarf(t *testing.T) string {
	t.Helper()
	repo := GitRepo(t)
	slug := filepath.Base(repo)

	// Create store (backup mirror)
	os.MkdirAll(paths.StoreDir, 0o755)
	run(t, paths.StoreDir, "git", "init")
	run(t, paths.StoreDir, "git", "config", "user.email", "test@test.com")
	run(t, paths.StoreDir, "git", "config", "user.name", "Test")

	// Create project dir in store (mirror target)
	projDir := filepath.Join(paths.StoreDir, slug)
	os.MkdirAll(filepath.Join(projDir, "links"), 0o755)

	// Create .swarf/ as a real directory in the project
	swarfDir := filepath.Join(repo, ".swarf")
	linksDir := filepath.Join(swarfDir, "links")
	os.MkdirAll(linksDir, 0o755)

	return repo
}

// BareRemote creates a bare git repo suitable for use as a push target.
func BareRemote(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	run(t, "", "git", "init", "--bare", bare)
	return bare
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %s\n%s", name, args, err, out)
	}
}
