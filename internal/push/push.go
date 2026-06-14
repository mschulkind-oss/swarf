// Package push performs one sync cycle on demand: mirror every
// registered project's swarf/ into the central store, then commit and
// push via the configured backend. This is exactly what the daemon does
// on each debounce, exposed as a CLI-callable function so 'swarf push'
// can force a flush without waiting for the daemon.
//
// Also used by integration tests to drive deterministic pushes without
// racing against the daemon's debouncer.
package push

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var ErrNoConfig = errors.New("no global config found — run 'swarf init' first")

// Run executes one full push cycle. The context governs the network portion
// of the backend sync, so an interrupted `swarf push` (Ctrl-C) aborts the
// in-flight rclone/git-push rather than blocking.
func Run(ctx context.Context) error {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return ErrNoConfig
	}

	// Make sure the store exists — same auto-bootstrap behavior pull uses.
	if err := initialize.EnsureStore("", gc); err != nil {
		return fmt.Errorf("ensure store: %w", err)
	}

	// Forward mirror: project/swarf/ → store/<slug>/ for every drawer.
	// TrackedDir keeps a manifest of files we've previously observed so
	// a never-seeded or transiently-empty project directory can't wipe
	// the store, while intentional user deletes still propagate on the
	// next push.
	for _, d := range config.ReadDrawers() {
		src := paths.SwarfDir(d.Host)
		dst := filepath.Join(paths.StoreDir, d.Slug)
		if !paths.IsDir(src) {
			continue
		}
		if err := mirror.TrackedDir(src, dst, paths.ProjectManifest(d.Slug)); err != nil {
			slog.Warn("push: mirror failed", "project", d.Slug, "err", err)
		}
	}

	// Refresh the store README to reflect the current drawer list.
	initialize.WriteStoreReadme()

	// Backend commits and pushes.
	backend := makeBackend(gc)
	result := backend.Sync(ctx, paths.StoreDir)
	if !result.Success {
		return fmt.Errorf("sync failed: %s", result.Message)
	}
	if result.FilesChanged > 0 {
		console.Ok(fmt.Sprintf("Pushed %d file(s).", result.FilesChanged))
	} else {
		console.Ok("Nothing to push.")
	}
	return nil
}

func makeBackend(gc *config.GlobalConfig) backends.SyncBackend {
	if gc.Backend == "rclone" {
		return &backends.RcloneBackend{Remote: gc.Remote, MachineID: config.EnsureMachineID()}
	}
	return &backends.GitBackend{}
}
