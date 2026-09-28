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
	"github.com/mschulkind-oss/swarf/internal/deletehold"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/pull"
	"github.com/mschulkind-oss/swarf/internal/synclock"
)

var ErrNoConfig = errors.New("no global config found — run 'swarf init' first")

// Options tunes one push.
type Options struct {
	// AllowDeletes applies deletes a delete hold would otherwise keep
	// back, for this one pass ('swarf push --allow-deletes').
	AllowDeletes bool
	// TakeOver uploads even when this machine's remote subtree (rclone)
	// or machine branch (git) was written by a different store with the
	// same machine_id ('swarf push --take-over').
	TakeOver bool
}

// Run executes one full push cycle. The context governs the wait for the
// sync lock and the network portion of the backend sync, so an
// interrupted `swarf push` (Ctrl-C) stops waiting or aborts the in-flight
// rclone/git-push rather than blocking.
func Run(ctx context.Context, opts Options) error {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return ErrNoConfig
	}
	// Hold the sync lock for the whole cycle, so a pull or a daemon
	// cycle cannot rewrite the store or the manifests under us.
	release, err := synclock.Acquire(ctx, func() { console.Info(synclock.WaitMessage) })
	if err != nil {
		return fmt.Errorf("sync lock: %w", err)
	}
	defer release()

	// Make sure the store exists — same auto-bootstrap behavior pull uses.
	if err := initialize.EnsureStore("", gc); err != nil {
		return fmt.Errorf("ensure store: %w", err)
	}

	skip := mirror.SkipPatterns(gc.Exclude)
	stamps := make(map[string]mirror.Stamps)
	// Forward mirror: project/swarf/ → store/<slug>/ for every drawer.
	// TrackedDir keeps a manifest of files we've previously observed so
	// a never-seeded or transiently-empty project directory can't wipe
	// the store, while intentional user deletes still propagate on the
	// next push. A pass that would delete a suspiciously large share of
	// a project's tracked files holds those deletes instead (see package
	// deletehold) unless the user passed --allow-deletes.
	for _, d := range config.ReadDrawers() {
		src := paths.SwarfDir(d.Host)
		dst := filepath.Join(paths.StoreDir, d.Slug)
		if gc.Backend == "git" {
			stamps[d.Slug] = mirror.TakeStamps(src, skip)
		}
		if !paths.IsDir(src) {
			continue
		}
		guard := mirror.Guard{Marker: paths.DeleteHoldMarker(d.Slug), AllowDeletes: opts.AllowDeletes}
		hold, err := mirror.TrackedDir(src, dst, paths.ProjectManifest(d.Slug), skip, guard)
		if err != nil {
			slog.Warn("push: mirror failed", "project", d.Slug, "err", err)
		}
		if hold != nil {
			console.Warn(hold.Summary(d.Slug))
			for _, line := range deletehold.WaysOut(gc.Backend, dst) {
				console.Hint(line)
			}
		}
	}

	// Git backend: fold in any legacy branch still on the remote, so
	// this push carries its content to the machine branch. It runs after
	// the forward mirror, so the deletes that mirror held stay held and
	// the ones --allow-deletes applied stay applied.
	if gc.Backend == "git" {
		if err := pull.FoldLegacyBranches(ctx, skip, stamps); err != nil {
			console.Warn(fmt.Sprintf("Could not fold in the remote's legacy branches: %v", err))
		}
	}

	// Refresh the store README to reflect the current drawer list.
	initialize.WriteStoreReadme()

	// Backend commits and pushes.
	backend := makeBackend(gc, opts.TakeOver)
	result := backend.Sync(ctx, paths.StoreDir)
	if !result.Success {
		return fmt.Errorf("sync failed: %s", result.Message)
	}
	if gc.Backend == "git" {
		pull.RetireLegacyBranches(ctx)
	}
	switch {
	case result.FilesChanged > 0 && result.Pushed:
		console.Ok(fmt.Sprintf("Pushed %d file(s).", result.FilesChanged))
	case result.FilesChanged > 0:
		// Git backend with no remote configured.
		console.Ok(fmt.Sprintf("Committed %d file(s) locally; no remote to push to.", result.FilesChanged))
	case result.Pushed:
		console.Ok("Pushed commits an earlier push did not send.")
	default:
		console.Ok("Nothing to push.")
	}
	return nil
}

func makeBackend(gc *config.GlobalConfig, takeOver bool) backends.SyncBackend {
	if gc.Backend == "rclone" {
		return &backends.RcloneBackend{
			Remote:     gc.Remote,
			MachineID:  config.EnsureMachineID(),
			NoFastList: gc.NoFastList,
			TakeOver:   takeOver,
		}
	}
	return &backends.GitBackend{MachineID: config.EnsureMachineID(), TakeOver: takeOver}
}
