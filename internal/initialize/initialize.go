package initialize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

const MiseHook = `command -v swarf >/dev/null && [ -d .swarf/links ] && swarf enter`
const MiseLocalTOML = "[hooks]\nenter = \"" + MiseHook + "\"\n"

var (
	ErrNotGitRepo        = errors.New("not inside a git repository")
	ErrAlreadyInitialized = errors.New("swarf is already initialized here")
)

// EnsureStore initializes the central store if it doesn't exist yet.
func EnsureStore(hostRoot string, gc *config.GlobalConfig) error {
	if paths.IsDir(paths.StoreDir) && gitexec.IsRepo(paths.StoreDir) {
		return nil
	}

	if err := os.MkdirAll(paths.StoreDir, 0o755); err != nil {
		return fmt.Errorf("create store: %w", err)
	}
	if err := gitexec.Init(paths.StoreDir); err != nil {
		return fmt.Errorf("git init store: %w", err)
	}

	for _, key := range []string{"user.name", "user.email"} {
		if val := gitexec.ConfigGet(hostRoot, key); val != "" {
			gitexec.ConfigSet(paths.StoreDir, key, val)
		}
	}

	if gc.Backend == "git" && gc.Remote != "" {
		gitexec.AddRemote(paths.StoreDir, "origin", gc.Remote)
	}

	console.Ok(fmt.Sprintf("Created central store at %s", paths.StoreDir))
	return nil
}

// Run initializes swarf for the current project.
// globalConfig must be non-nil (caller handles prompting or reading config).
func Run(globalConfig *config.GlobalConfig) error {
	hostRoot := gitexec.GetRepoRoot("")
	if hostRoot == "" {
		return ErrNotGitRepo
	}

	sd := paths.SwarfDir(hostRoot)
	slug := paths.ProjectSlug(hostRoot)
	projDir := paths.StoreProjectDir(hostRoot)

	if fi, err := os.Lstat(sd); err == nil {
		if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return ErrAlreadyInitialized
		}
	}

	if err := EnsureStore(hostRoot, globalConfig); err != nil {
		return err
	}

	if paths.IsDir(projDir) {
		console.Ok(fmt.Sprintf("Found existing project '%s' in store.", slug))
	} else {
		if err := os.MkdirAll(filepath.Join(projDir, "links"), 0o755); err != nil {
			return fmt.Errorf("create project dir: %w", err)
		}
		os.WriteFile(filepath.Join(projDir, "links", ".gitkeep"), []byte(""), 0o644)
	}

	if err := os.Symlink(projDir, sd); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	console.Ok(fmt.Sprintf("Linked .swarf → %s", projDir))

	misePath := filepath.Join(hostRoot, ".mise.local.toml")
	if _, err := os.Stat(misePath); err == nil {
		console.Warn(".mise.local.toml already exists. Add this hook manually:")
		console.Info(fmt.Sprintf("  [hooks]\n  enter = \"%s\"", MiseHook))
	} else {
		os.WriteFile(misePath, []byte(MiseLocalTOML), 0o644)
		console.Ok("Created .mise.local.toml with enter hook.")
	}

	exclude.UpdateExcludes(hostRoot, nil)
	config.RegisterDrawer(slug, hostRoot)

	gitexec.AddAll(paths.StoreDir)
	gitexec.Commit(paths.StoreDir, "init: "+slug) // ignore error (empty commit)

	console.Ok(fmt.Sprintf("Initialized swarf for %s", slug))
	console.Infof("  Backend: %s", globalConfig.Backend)
	if globalConfig.Remote != "" {
		console.Infof("  Remote: %s", globalConfig.Remote)
	}
	return nil
}
