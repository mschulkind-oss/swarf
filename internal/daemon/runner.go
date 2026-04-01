package daemon

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/sweep"
)

func Run(ctx context.Context) error {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		slog.Error("No global config found. Run 'swarf init' first.")
		return nil
	}

	duration, err := config.ParseDuration(gc.Debounce)
	if err != nil {
		duration = 5 * time.Second
	}

	backend := makeBackend(gc.Backend, gc.Remote)
	debouncer := NewDebouncer(duration, func() {
		relinkAllProjects()
		mirrorAllProjects()
		result := backend.Sync(paths.StoreDir)
		if result.Success && result.FilesChanged > 0 {
			slog.Info("sync: " + result.Message)
		} else if !result.Success {
			slog.Warn("sync: " + result.Message)
		}
	})
	defer debouncer.Cancel()

	return watchProjects(ctx, debouncer)
}

// relinkAllProjects re-creates missing symlinks from swarf/.links/ for all projects.
func relinkAllProjects() {
	drawers := config.ReadDrawers()
	for _, d := range drawers {
		if !paths.IsDir(paths.SwarfDir(d.Host)) {
			continue
		}
		result, err := link.Run(d.Host, true)
		if err != nil {
			continue
		}
		if len(result.Created) > 0 {
			slog.Info("re-linked", "project", d.Slug, "files", result.Created)
		}
	}
}

// mirrorAllProjects copies each project's swarf/ content into the central store,
// including deletions (files removed from swarf/ are removed from the store).
func mirrorAllProjects() {
	drawers := config.ReadDrawers()
	for _, d := range drawers {
		src := paths.SwarfDir(d.Host)
		dst := filepath.Join(paths.StoreDir, d.Slug)
		if !paths.IsDir(src) {
			continue
		}
		if err := mirrorDir(src, dst); err != nil {
			slog.Warn("mirror failed", "project", d.Slug, "err", err)
		}
	}
}

// mirrorDir recursively syncs src into dst, preserving structure.
// Files that exist in dst but not in src are deleted.
func mirrorDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	// Phase 1: Copy new/changed files from src to dst.
	var copyErr error
	filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("mirror: walk error", "path", path, "err", err)
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				slog.Warn("mirror: mkdir failed", "path", target, "err", err)
				copyErr = err
			}
			return nil
		}

		// Resolve symlinks: read the content, copy as regular file.
		srcInfo, err := os.Stat(path)
		if err != nil {
			slog.Warn("mirror: stat failed", "path", path, "err", err)
			return nil
		}

		if dstInfo, err := os.Stat(target); err == nil {
			if srcInfo.Size() == dstInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime()) {
				return nil
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("mirror: read failed", "path", path, "err", err)
			copyErr = err
			return nil
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		if err := os.WriteFile(target, data, srcInfo.Mode()); err != nil {
			slog.Warn("mirror: write failed", "path", target, "err", err)
			copyErr = err
		}
		return nil
	})

	// Phase 2: Delete files/dirs in dst that no longer exist in src.
	filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dst, path)
		if rel == "." {
			return nil
		}
		srcPath := filepath.Join(src, rel)
		if _, err := os.Lstat(srcPath); os.IsNotExist(err) {
			if d.IsDir() {
				os.RemoveAll(path)
				return filepath.SkipDir
			}
			os.Remove(path)
			slog.Debug("mirror: deleted stale file", "path", rel)
		}
		return nil
	})

	return copyErr
}

func makeBackend(backendType, remote string) backends.SyncBackend {
	if backendType == "rclone" {
		return &backends.RcloneBackend{Remote: remote}
	}
	return &backends.GitBackend{}
}

// watchProjects watches all registered project swarf/ dirs and project roots
// (for auto-sweep targets) for changes.
func watchProjects(ctx context.Context, debouncer *Debouncer) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	os.MkdirAll(paths.StoreDir, 0o755)

	watchedSwarf := make(map[string]bool) // swarf/ dirs
	watchedRoots := make(map[string]bool) // project roots for auto-sweep

	refreshWatches := func() {
		current := make(map[string]bool)
		drawers := config.ReadDrawers()
		for _, d := range drawers {
			sd := paths.SwarfDir(d.Host)
			current[sd] = true

			// Watch swarf/ dir for sync.
			if paths.IsDir(sd) && !watchedSwarf[sd] {
				if err := addRecursive(watcher, sd); err == nil {
					watchedSwarf[sd] = true
					slog.Info("Watching project", "name", d.Slug, "path", sd)
				}
			}

			// Watch project root and auto-sweep target parent dirs.
			if !watchedRoots[d.Host] {
				watcher.Add(d.Host)
				watchedRoots[d.Host] = true
				// Watch parent dirs of auto-sweep targets (for nested paths).
				gc := config.ReadGlobalConfig()
				if gc != nil {
					for _, p := range gc.AutoSweep {
						parentDir := filepath.Dir(filepath.Join(d.Host, p))
						if paths.IsDir(parentDir) {
							watcher.Add(parentDir)
						}
					}
				}
			}
		}

		// Remove watches for projects that are no longer registered.
		for sd := range watchedSwarf {
			if !current[sd] {
				watcher.Remove(sd)
				delete(watchedSwarf, sd)
				slog.Info("Unwatched removed project", "path", sd)
			}
		}
	}

	refreshWatches()
	if len(watchedSwarf) == 0 {
		slog.Info("No projects found. Waiting for first 'swarf init'...")
	}

	// Startup: auto-sweep, re-link, and catch up on missed changes.
	autoSweepAll()
	relinkAllProjects()
	debouncer.Trigger()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Build auto-sweep filename set for fast event filtering.
	sweepNames := make(map[string]bool)
	gc := config.ReadGlobalConfig()
	if gc != nil {
		for _, p := range gc.AutoSweep {
			sweepNames[filepath.Base(p)] = true
		}
	}

	for {
		select {
		case <-ctx.Done():
			debouncer.Flush()
			return ctx.Err()
		case <-ticker.C:
			refreshWatches()
			autoSweepAll()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if strings.Contains(event.Name, "/.git/") || strings.HasSuffix(event.Name, "/.git") {
				continue
			}
			if event.Has(fsnotify.Create) && paths.IsDir(event.Name) {
				watcher.Add(event.Name)
			}

			// If an auto-sweep target was created, sweep it immediately.
			if event.Has(fsnotify.Create) && sweepNames[filepath.Base(event.Name)] {
				autoSweepAll()
			}

			// Only trigger sync debouncer for changes inside swarf/ dirs.
			for sd := range watchedSwarf {
				if strings.HasPrefix(event.Name, sd) {
					debouncer.Trigger()
					break
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("watcher error", "err", err)
		}
	}
}

func addRecursive(w *fsnotify.Watcher, root string) error {
	return walkDirs(root, func(dir string) error {
		if strings.HasSuffix(dir, "/.git") || strings.Contains(dir, "/.git/") {
			return nil
		}
		return w.Add(dir)
	})
}

// autoSweepAll checks auto-sweep targets across all projects and sweeps
// any that exist as regular files (not already symlinks).
func autoSweepAll() {
	gc := config.ReadGlobalConfig()
	if gc == nil || len(gc.AutoSweep) == 0 {
		return
	}

	drawers := config.ReadDrawers()
	for _, d := range drawers {
		if !paths.IsDir(paths.SwarfDir(d.Host)) {
			continue
		}
		var toSweep []string
		for _, p := range gc.AutoSweep {
			target := filepath.Join(d.Host, p)
			fi, err := os.Lstat(target)
			if err == nil && fi.Mode()&os.ModeSymlink == 0 && !fi.IsDir() {
				toSweep = append(toSweep, p)
			}
		}
		if len(toSweep) > 0 {
			if err := sweep.Run(toSweep, d.Host); err != nil {
				slog.Warn("auto-sweep failed", "project", d.Slug, "err", err)
			} else {
				slog.Info("auto-sweep", "project", d.Slug, "files", toSweep)
			}
		}
	}
}
