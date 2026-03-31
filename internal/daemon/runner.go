package daemon

import (
	"context"
	"log/slog"
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

	if err := waitForStore(ctx); err != nil {
		return err
	}

	duration, err := config.ParseDuration(gc.Debounce)
	if err != nil {
		duration = 5 * time.Second
	}

	backend := makeBackend(gc.Backend, gc.Remote)
	debouncer := NewDebouncer(duration, func() {
		result := backend.Sync(paths.StoreDir)
		if result.Success && result.FilesChanged > 0 {
			slog.Info("store: " + result.Message)
		} else if !result.Success {
			slog.Warn("store: " + result.Message)
		}
	})
	defer debouncer.Cancel()

	return watchStore(ctx, backend, debouncer)
}

func waitForStore(ctx context.Context) error {
	for !paths.IsDir(paths.StoreDir) {
		slog.Info("Store directory does not exist. Waiting...", "path", paths.StoreDir)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

func makeBackend(backendType, remote string) backends.SyncBackend {
	if backendType == "rclone" {
		return &backends.RcloneBackend{Remote: remote}
	}
	return &backends.GitBackend{}
}

func watchStore(ctx context.Context, backend backends.SyncBackend, debouncer *Debouncer) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := addRecursive(watcher, paths.StoreDir); err != nil {
		return err
	}

	slog.Info("Watching store", "path", paths.StoreDir)

	for {
		select {
		case <-ctx.Done():
			if backend.HasChanges(paths.StoreDir) {
				slog.Info("Final sync before shutdown")
				backend.Sync(paths.StoreDir)
			}
			return ctx.Err()
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
