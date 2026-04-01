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
	"github.com/mschulkind-oss/swarf/internal/paths"
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

// mirrorAllProjects copies each project's .swarf/ content into the central store.
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

// mirrorDir recursively copies src into dst, preserving structure.
func mirrorDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Only copy if source is newer or different size
		srcInfo, err := d.Info()
		if err != nil {
			return nil
		}
		if dstInfo, err := os.Stat(target); err == nil {
			if srcInfo.Size() == dstInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime()) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		os.MkdirAll(filepath.Dir(target), 0o755)
		return os.WriteFile(target, data, srcInfo.Mode())
	})
}

func makeBackend(backendType, remote string) backends.SyncBackend {
	if backendType == "rclone" {
		return &backends.RcloneBackend{Remote: remote}
	}
	return &backends.GitBackend{}
}

// watchProjects watches all registered project .swarf/ dirs for changes.
func watchProjects(ctx context.Context, debouncer *Debouncer) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Ensure the store exists for the backend
	os.MkdirAll(paths.StoreDir, 0o755)

	watched := make(map[string]bool)
	refreshWatches := func() {
		drawers := config.ReadDrawers()
		for _, d := range drawers {
			sd := paths.SwarfDir(d.Host)
			if !paths.IsDir(sd) || watched[sd] {
				continue
			}
			if err := addRecursive(watcher, sd); err == nil {
				watched[sd] = true
				slog.Info("Watching project", "name", d.Slug, "path", sd)
			}
		}
	}

	refreshWatches()
	if len(watched) == 0 {
		slog.Info("No projects found. Waiting for first 'swarf init'...")
	}

	// Periodically check for new projects
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final mirror + sync before shutdown
			mirrorAllProjects()
			return ctx.Err()
		case <-ticker.C:
			refreshWatches()
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
			debouncer.Trigger()
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
