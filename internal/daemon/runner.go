package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/pull"
	"github.com/mschulkind-oss/swarf/internal/sweep"
	"github.com/mschulkind-oss/swarf/internal/synclock"
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

	// Scheduled pulls stop when the daemon does, however watchProjects
	// returns. Deferred waits run last-in first-out, so cancel runs
	// before the wait for the pull goroutine.
	ctx, cancel := context.WithCancel(ctx)
	var pulls sync.WaitGroup
	defer pulls.Wait()
	defer cancel()

	backend := makeBackend(gc)
	skip := mirror.SkipPatterns(gc.Exclude)
	debouncer := NewDebouncer(duration, func() { syncCycle(ctx, backend, skip) })
	defer debouncer.Cancel()

	interval, note := pullSchedule(gc)
	if note != "" {
		slog.Warn(note)
	}
	if interval > 0 {
		slog.Info("Scheduled pull enabled", "every", interval)
		pulls.Add(1)
		go func() {
			defer pulls.Done()
			runScheduledPulls(ctx, interval, func(ctx context.Context) error {
				return pull.Run(ctx, pull.Options{})
			})
		}()
	}

	return watchProjects(ctx, debouncer, skip)
}

// syncCycle is one daemon cycle:
//  1. Re-link: restore any missing symlinks from swarf/.links/
//  2. Forward mirror: project/swarf → store/<slug>/
//  3. Update store README
//  4. Backend sync: commit + push
//
// The CLI 'swarf push' runs the same steps via internal/push.
//
// ctx is the daemon's signal context: on SIGTERM it's cancelled, which
// kills any in-flight rclone/git-push child so shutdown is prompt rather
// than blocking on a slow network transfer.
//
// The whole cycle holds the sync lock, so a 'swarf push' or 'swarf pull'
// run meanwhile waits for it rather than mirroring into the same store,
// and vice versa.
func syncCycle(ctx context.Context, backend backends.SyncBackend, skip mirror.Skip) {
	release, err := synclock.Acquire(ctx, func() {
		slog.Info("sync: waiting for the sync lock held by another push or pull")
	})
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("sync: could not take the sync lock", "err", err)
		}
		return
	}
	defer release()
	relinkAllProjects()
	mirrorAllProjects(skip)
	initialize.WriteStoreReadme()
	result := backend.Sync(ctx, paths.StoreDir)
	if result.Success && result.FilesChanged > 0 {
		slog.Info("sync: " + result.Message)
	} else if !result.Success {
		slog.Warn("sync: " + result.Message)
	}
}

// loggedRelinkWarnings remembers relink warnings we've already emitted so a
// permanently-stuck link (a real file sitting where a swept symlink belongs)
// is reported once rather than re-spammed to the journal on every debounce
// cycle. Cleared entries (the conflict went away) drop out so a recurrence is
// logged afresh.
var loggedRelinkWarnings = map[string]bool{}

// relinkAllProjects re-creates missing symlinks from swarf/.links/ for all projects.
func relinkAllProjects() {
	drawers := config.ReadDrawers()
	seen := make(map[string]bool)
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
		// Log each unresolved conflict (e.g. "real file exists, won't
		// overwrite") only the first time we see it. Without this the daemon
		// re-announces the same stuck links on every cycle, drowning the
		// journal — exactly the noise the shutdown audit surfaced.
		for _, w := range result.Warnings {
			seen[w] = true
			if !loggedRelinkWarnings[w] {
				slog.Warn("re-link skipped", "project", d.Slug, "detail", w)
			}
		}
	}
	// Remember exactly the conflicts seen this cycle: repeats next cycle are
	// suppressed, and any that vanished drop out so a recurrence re-logs.
	loggedRelinkWarnings = seen
}

// mirrorAllProjects copies each project's swarf/ content into the central
// store. Uses TrackedDir so the first pass after init/pull only copies
// (no deletes against content we've never observed locally), and
// subsequent passes correctly delete files the user removed — unless
// they are enough to put the project in a delete hold.
func mirrorAllProjects(skip mirror.Skip) {
	drawers := config.ReadDrawers()
	for _, d := range drawers {
		src := paths.SwarfDir(d.Host)
		dst := filepath.Join(paths.StoreDir, d.Slug)
		if !paths.IsDir(src) {
			continue
		}
		hold, err := mirror.TrackedDir(src, dst, paths.ProjectManifest(d.Slug), skip,
			mirror.Guard{Marker: paths.DeleteHoldMarker(d.Slug)})
		if err != nil {
			slog.Warn("mirror failed", "project", d.Slug, "err", err)
		}
		// A delete hold persists across cycles until the user acts, so
		// log it once when it appears — the hold marker's absence before
		// this pass is what makes it new — not on every debounce.
		if hold != nil && hold.New {
			slog.Warn("delete hold: deletes not applied to the store",
				"project", d.Slug, "files", hold.Count, "sample", hold.Sample,
				"fix", "restore the files (swarf pull, for rclone) or run 'swarf push --allow-deletes'")
		}
	}
}

func makeBackend(gc *config.GlobalConfig) backends.SyncBackend {
	if gc.Backend == "rclone" {
		return &backends.RcloneBackend{
			Remote:     gc.Remote,
			MachineID:  config.EnsureMachineID(),
			NoFastList: gc.NoFastList,
		}
	}
	return &backends.GitBackend{}
}

// watchProjects watches all registered project swarf/ dirs and project roots
// (for auto-sweep targets) for changes.
func watchProjects(ctx context.Context, debouncer *Debouncer, skip mirror.Skip) error {
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
				if err := addRecursive(watcher, sd, skip); err == nil {
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
			// Shutdown (SIGTERM/SIGINT). Do NOT run a final flush here: the
			// flush would shell out to rclone/git-push synchronously and the
			// network call can block reboot for a minute or more. Local git
			// commits from the last debounce cycle are already on disk, and
			// the next boot's startup Trigger() catches the remote up.
			//
			// ctx is already cancelled, so any sync still in flight has its
			// rclone/git-push child killed. Drain waits for that callback to
			// unwind (fast, since the child is being killed) so we don't exit
			// out from under it and orphan the process.
			debouncer.Drain()
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
			// An excluded path is outside sync entirely: it is not watched,
			// and it does not wake a cycle. Excluded trees are exactly the
			// ones that churn — jail state, build output — so watching one
			// spends inotify watches to schedule remote syncs with nothing
			// to copy.
			excluded := isExcluded(event.Name, watchedSwarf, skip)

			if event.Has(fsnotify.Create) && !excluded && paths.IsDir(event.Name) {
				watcher.Add(event.Name)
			}

			// If an auto-sweep target was created, sweep it immediately.
			if event.Has(fsnotify.Create) && sweepNames[filepath.Base(event.Name)] {
				autoSweepAll()
			}

			// Only trigger sync debouncer for changes inside swarf/ dirs.
			if !excluded {
				for sd := range watchedSwarf {
					if strings.HasPrefix(event.Name, sd) {
						debouncer.Trigger()
						break
					}
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

// isExcluded reports whether an event path falls under one of the watched
// swarf/ dirs at a drawer-relative path the excludes cover. A path
// outside every watched drawer is not excluded — it is simply not ours.
func isExcluded(name string, watchedSwarf map[string]bool, skip mirror.Skip) bool {
	if skip == nil {
		return false
	}
	for sd := range watchedSwarf {
		if !strings.HasPrefix(name, sd) {
			continue
		}
		rel, err := filepath.Rel(sd, name)
		if err != nil {
			continue
		}
		if skip(rel, false) {
			return true
		}
	}
	return false
}

// addRecursive watches root and every directory under it, minus the
// excluded subtrees: an excluded directory is outside sync entirely, and
// a churning one (a jail's state directory, a build tree) would otherwise
// spend inotify watches and wake a sync cycle that has nothing to copy.
func addRecursive(w *fsnotify.Watcher, root string, skip mirror.Skip) error {
	return walkDirsSkipping(root, skip, func(dir string) error {
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
