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
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	ErrNotGitRepo         = errors.New("not inside a git repository")
	ErrAlreadyInitialized = errors.New("swarf is already initialized here")
)

// EnsureStore initializes the central store (backup mirror) if it doesn't exist.
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
func Run(globalConfig *config.GlobalConfig) error {
	hostRoot := gitexec.GetRepoRoot("")
	if hostRoot == "" {
		return ErrNotGitRepo
	}

	sd := paths.SwarfDir(hostRoot)
	slug := paths.ProjectSlug(hostRoot)

	if fi, err := os.Lstat(sd); err == nil {
		if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return ErrAlreadyInitialized
		}
	}

	if err := EnsureStore(hostRoot, globalConfig); err != nil {
		return err
	}

	// Create swarf/ as a real directory in the project.
	// If the store already has content for this project (e.g., after clone),
	// seed the local swarf/ from the store mirror.
	storeProject := paths.StoreProjectDir(hostRoot)
	if paths.IsDir(storeProject) {
		if err := copyDir(storeProject, sd); err != nil {
			return fmt.Errorf("seed from store: %w", err)
		}
		console.Ok(fmt.Sprintf("Restored %s from store.", slug))
	} else {
		linksDir := filepath.Join(sd, "links")
		if err := os.MkdirAll(linksDir, 0o755); err != nil {
			return fmt.Errorf("create swarf/: %w", err)
		}
	}

	exclude.UpdateExcludes(hostRoot, nil)
	config.RegisterDrawer(slug, hostRoot)

	// Re-create symlinks from swarf/links/ (e.g. after clone + init).
	link.Run(hostRoot, true)

	console.Ok(fmt.Sprintf("Initialized swarf for %s", slug))
	console.Infof("  Backend: %s", globalConfig.Backend)
	if globalConfig.Remote != "" {
		console.Infof("  Remote: %s", globalConfig.Remote)
	}
	return nil
}

// copyDir recursively copies src into dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		return os.WriteFile(target, data, info.Mode())
	})
}
