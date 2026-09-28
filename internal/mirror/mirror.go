// Package mirror keeps two directories in byte-level sync. It's used in
// both directions of the swarf data flow:
//
//   - Daemon (forward):  project/swarf/  →  store/<project>/
//   - Pull   (reverse):  store/<project>/  →  project/swarf/
//
// Both directions propagate deletes only through the manifest: a file
// missing from the source is removed from the destination only when the
// manifest says swarf synced it before (TrackedDir forward,
// TrackedReverse reverse). Forward always runs right before pull's
// reverse, so the store is up-to-date with local edits first.
package mirror

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/swarf/internal/deletehold"
)

// Dir mirrors src into dst destructively: files in src are copied/updated
// under dst, and files in dst missing from src are deleted. Pull's
// reverse (store → project) mirror used this until it moved to
// TrackedReverse, which deletes only files the manifest lists; nothing
// in swarf calls Dir now, and a new caller should prefer one of the
// manifest-gated variants.
//
// For the project → store direction, prefer CopyOnly: a project/swarf/
// that looks empty or partial is never the right signal to wipe the
// store, because the store is the cross-machine backup and there are too
// many ways project/swarf can transiently look wrong (user deleted it by
// accident, it hasn't been seeded yet, a subdirectory is being rebuilt).
// Deletions in the project are propagated by the daemon, which watches
// for real fsnotify events, not by one-shot mirror passes.
//
// Errors from individual files are logged but don't short-circuit.
func Dir(src, dst string, skip Skip) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dst, err)
	}

	var lastErr error

	// Copy/update.
	filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("mirror: walk error", "path", path, "err", err)
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if skips(skip, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if err := mkdirAllRepair(target); err != nil {
				slog.Warn("mirror: mkdir failed", "path", target, "err", err)
				lastErr = err
			}
			return nil
		}

		srcInfo, err := statResolvable(path)
		if err != nil {
			slog.Debug("mirror: skipping unreadable entry", "path", path, "err", err)
			return nil
		}

		// Skip when size + mtime match — cheap short-circuit that keeps
		// repeated calls idempotent.
		if upToDate(srcInfo, target) {
			return nil
		}

		data, err := readFile(path)
		if err != nil {
			slog.Warn("mirror: read failed", "path", path, "err", err)
			lastErr = err
			return nil
		}
		if err := mkdirAllRepair(filepath.Dir(target)); err != nil {
			slog.Warn("mirror: mkdir for file failed", "path", target, "err", err)
			lastErr = err
			return nil
		}
		if err := os.WriteFile(target, data, srcInfo.Mode()); err != nil {
			slog.Warn("mirror: write failed", "path", target, "err", err)
			lastErr = err
		}
		return nil
	})

	// Delete anything in dst that's no longer in src.
	filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dst, path)
		if rel == "." {
			return nil
		}
		// An excluded entry is absent from src by design. Deleting it
		// here would delete the very files the exclude was added to keep
		// out of the store, from the project the user works in.
		if skips(skip, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
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

	return lastErr
}

// TrackedReverse mirrors src (the store) into dst (the project) for
// pull's reverse step, deleting only what swarf has seen synced. Files in
// src are copied/updated under dst as Dir does. A dst file is deleted
// only when the manifest at manifestPath lists it — swarf synced it
// before — and src no longer has it, which is how a peer's delete
// reaches this project. A dst file the manifest has never listed was
// saved since the last forward pass (for instance while pull was
// downloading peers), so it is left for the next forward pass to copy
// into the store rather than deleted for being absent from it.
//
// Afterwards the manifest is rewritten to dst's current file set, so the
// next forward pass starts from what pull left behind: files pull just
// copied in count as synced, and files it deleted are no longer listed.
//
// since protects the project files the user changed during the pull:
// it is a Stamps taken of dst when pull's forward mirror ran, and a dst
// file that since exists at a different size or modification time than
// it recorded (including one created since) is neither overwritten nor
// deleted — the next forward pass copies it into the store instead. A
// nil since protects nothing.
//
// Paths matched by skip are neither copied, deleted, nor recorded, as in
// every other mirror pass. Errors from individual files are logged and
// do not abort; the last one is returned.
func TrackedReverse(src, dst, manifestPath string, skip Skip, since Stamps) error {
	return TrackedReverseKeeping(src, dst, manifestPath, skip, since, nil)
}

// TrackedReverseKeeping is TrackedReverse, except that it leaves each
// path in keep alone in dst — neither copied nor deleted — and keeps it
// in the manifest. A push that folds in a legacy branch passes the paths
// a delete hold is holding (HeldPaths): copying them back would resolve
// the hold by restoring the files, which only a 'swarf pull' the user
// chose may do, and keeping them in the manifest lets the next forward
// pass hold them again.
func TrackedReverseKeeping(src, dst, manifestPath string, skip Skip, since Stamps, keep []string) error {
	oldSet := readManifest(manifestPath)
	kept := make(map[string]struct{}, len(keep))
	for _, rel := range keep {
		kept[rel] = struct{}{}
	}
	// Leave out of the copy and the deletes, but not the manifest below,
	// each file changed since the stamps (it is still a synced file) and
	// each kept path.
	pass := skip
	if since != nil || len(kept) > 0 {
		pass = func(rel string, isDir bool) bool {
			if skips(skip, rel, isDir) {
				return true
			}
			if isDir {
				return false
			}
			if _, ok := kept[rel]; ok {
				return true
			}
			return since != nil && since.changed(dst, rel)
		}
	}
	lastErr := CopyOnly(src, dst, pass)

	for rel := range oldSet {
		if skipsEntry(pass, rel) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(src, rel)); !os.IsNotExist(err) {
			continue
		}
		target := filepath.Join(dst, rel)
		if err := os.Remove(target); err == nil {
			slog.Debug("mirror: deleted file gone from store", "path", rel)
			removeEmptyDirsUpTo(target, dst)
		}
	}

	newSet := fileSet(dst, skip)
	for rel := range kept {
		if !skipsEntry(skip, rel) {
			newSet[rel] = struct{}{}
		}
	}
	if err := writeManifest(manifestPath, newSet); err != nil {
		slog.Warn("mirror: writing manifest failed", "path", manifestPath, "err", err)
	}
	return lastErr
}

// Stamps records the size and modification time of each file under a
// directory at one moment, so a later pass can tell which files were
// written since. Pull takes one of each project when its forward mirror
// runs (TakeStamps) and hands it to TrackedReverse, whose copy would
// otherwise overwrite a save the user made while pull was downloading
// peers. The name is coined here.
type Stamps map[string]stamp

type stamp struct {
	size    int64
	modTime time.Time
}

// TakeStamps records every file under root that skip does not exclude,
// as the file resolves (a link records its target). A missing root
// yields an empty, non-nil Stamps: every file found there later counts
// as written since.
func TakeStamps(root string, skip Skip) Stamps {
	s := Stamps{}
	for rel := range fileSet(root, skip) {
		if info, err := os.Stat(filepath.Join(root, rel)); err == nil {
			s[rel] = stamp{info.Size(), info.ModTime()}
		}
	}
	return s
}

// changed reports whether root/rel exists now but not as s recorded it:
// created since, or at a different size or modification time. A file
// removed since is not changed — nothing of the user's is at stake.
func (s Stamps) changed(root, rel string) bool {
	info, err := os.Stat(filepath.Join(root, rel))
	if err != nil || info.IsDir() {
		return false
	}
	was, ok := s[rel]
	return !ok || was.size != info.Size() || !was.modTime.Equal(info.ModTime())
}

// Protected returns the paths among rels that TrackedReverse, given
// since and skip, leaves alone in root because they changed there since
// the stamps: not excluded by skip, and existing at a different size or
// modification time than since recorded. A nil since protects nothing.
func Protected(root string, skip Skip, since Stamps, rels []string) []string {
	if since == nil {
		return nil
	}
	var out []string
	for _, rel := range rels {
		if !skipsEntry(skip, rel) && since.changed(root, rel) {
			out = append(out, rel)
		}
	}
	return out
}

// fileSet returns the drawer-relative paths of the files under root that
// a copy pass would copy: not skipped, and resolvable to a file (not a
// link to a directory). It is the set a forward pass records in the manifest.
func fileSet(root string, skip Skip) map[string]struct{} {
	set := make(map[string]struct{})
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if skips(skip, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, err := statResolvable(path); err == nil && !info.IsDir() {
			set[rel] = struct{}{}
		}
		return nil
	})
	return set
}

// Guard configures the delete hold for one TrackedDir pass. The zero
// Guard still holds; it just records no hold marker.
type Guard struct {
	// Marker is the hold marker path (paths.DeleteHoldMarker). Empty
	// means a hold is returned but not recorded.
	Marker string
	// AllowDeletes applies the deletes even when they would hold: the
	// user confirmed them with 'swarf push --allow-deletes'.
	AllowDeletes bool
}

// TrackedDir mirrors src into dst, propagating only the deletions that
// we have first-hand evidence of. Uses a per-source manifest at
// manifestPath that records the src files the previous pass left a copy
// of in dst (a file whose copy failed and that dst never had is left
// out, as is a link to a directory, which is never copied).
// Files in the old manifest but missing from src now (and present in
// dst) are true deletions and get removed from dst. Files that never
// existed in src (according to the manifest) are untouched in dst,
// which keeps a never-seeded or transiently-empty src from wiping dst.
//
// On first run, manifestPath doesn't exist and no deletes happen —
// only copies. Subsequent runs get delete-propagation right without
// the "empty project wipes the store" failure mode.
//
// A manifest can also be stale: it lists files that something other
// than the user removed from src between passes (Sync Scenarios,
// Scenario 6). When the deletes would remove a suspiciously large share
// of the tracked files (deletehold.Triggered), the pass enters a delete
// hold: it applies none of those deletes, still copies adds and edits,
// keeps the held paths in the manifest, writes the hold marker, and
// returns the hold. Any pass that does not hold removes the marker.
// Paths matched by skip are pruned from dst as always and never count
// toward a hold — dropping them is deliberate.
//
// Returns the hold (nil when the pass did not hold) and the last fatal
// error encountered (or nil). Per-file errors are logged and do not
// abort.
func TrackedDir(src, dst, manifestPath string, skip Skip, guard Guard) (*deletehold.Hold, error) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dst, err)
	}

	oldSet := readManifest(manifestPath)
	newSet := make(map[string]struct{})
	var lastErr error

	// Copy every current src file into dst.
	filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("mirror: walk error", "path", path, "err", err)
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if skips(skip, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if err := mkdirAllRepair(target); err != nil {
				slog.Warn("mirror: mkdir failed", "path", target, "err", err)
				lastErr = err
			}
			return nil
		}

		srcInfo, err := statResolvable(path)
		if err != nil {
			slog.Debug("mirror: skipping unreadable entry", "path", path, "err", err)
			return nil
		}
		if srcInfo.IsDir() {
			// A link to a directory: the mirror copies file bytes only,
			// so this never reaches dst and is not recorded as synced.
			slog.Warn("mirror: skipping link to a directory", "path", path)
			return nil
		}

		// The manifest records what dst has a copy of. A failed copy is
		// recorded only when dst already holds the file from an earlier
		// pass, so it is not read as a delete here, and a file that never
		// reached dst is never grounds for TrackedReverse to delete it.
		if err := copyFile(path, target, srcInfo); err != nil {
			lastErr = err
			if _, err := os.Lstat(target); err != nil {
				return nil
			}
		}
		newSet[rel] = struct{}{}
		return nil
	})

	plan := planDeletes(oldSet, newSet, dst, skip)
	holding := plan.holds() && !guard.AllowDeletes

	// Delete from dst anything the manifest says we used to have in src
	// but we don't anymore. We only touch files we have first-hand
	// evidence of, never files that appeared in dst from some other
	// source (e.g. peer content we just pulled). Excluded paths go
	// even during a hold; the tracked deletes wait for the user.
	deletes := plan.excluded
	if !holding {
		deletes = append(deletes, plan.atStake...)
	}
	for _, rel := range deletes {
		target := filepath.Join(dst, rel)
		if err := os.Remove(target); err == nil {
			slog.Debug("mirror: deleted tracked file", "path", rel)
			// Best-effort cleanup of newly-empty parent dirs, stopping at dst.
			removeEmptyDirsUpTo(target, dst)
		}
	}

	var hold *deletehold.Hold
	if holding {
		// Keep the held paths in the manifest so the next pass sees the
		// same deletes, and holds again, until the files come back or
		// the user confirms.
		for _, rel := range plan.atStake {
			newSet[rel] = struct{}{}
		}
		hold = deletehold.NewHold(guard.Marker, plan.atStake, time.Now())
		if guard.Marker != "" {
			if err := deletehold.Write(guard.Marker, hold); err != nil {
				slog.Warn("mirror: writing hold marker failed", "path", guard.Marker, "err", err)
			}
		}
	} else if guard.Marker != "" {
		if err := deletehold.Remove(guard.Marker); err != nil {
			slog.Warn("mirror: removing hold marker failed", "path", guard.Marker, "err", err)
		}
	}

	if err := writeManifest(manifestPath, newSet); err != nil {
		slog.Warn("mirror: writing manifest failed", "path", manifestPath, "err", err)
	}
	return hold, lastErr
}

// deletePlan sorts the manifest entries missing from src into what a
// forward pass may delete from dst.
type deletePlan struct {
	tracked  int      // manifest entries not matched by a sync exclude
	missing  int      // tracked entries gone from src
	atStake  []string // missing entries that still exist in dst
	excluded []string // manifest entries matched by a sync exclude
}

func planDeletes(oldSet, newSet map[string]struct{}, dst string, skip Skip) deletePlan {
	var p deletePlan
	for rel := range oldSet {
		if skipsEntry(skip, rel) {
			p.excluded = append(p.excluded, rel)
			continue
		}
		p.tracked++
		if _, stillPresent := newSet[rel]; stillPresent {
			continue
		}
		p.missing++
		if _, err := os.Lstat(filepath.Join(dst, rel)); err == nil {
			p.atStake = append(p.atStake, rel)
		}
	}
	return p
}

func (p deletePlan) holds() bool {
	return deletehold.Triggered(p.tracked, p.missing, len(p.atStake))
}

// HeldPaths returns the paths a forward pass from src to dst would hold
// right now: the manifest entries gone from src that dst still has, when
// there are enough of them for a delete hold (deletehold.Triggered). Nil
// when no pass would hold. Read-only.
//
// Pull reads them before its reverse mirror, which rewrites the manifest
// and so erases the evidence of what was held, and hands them to
// ClearResolvedHold afterwards.
func HeldPaths(src, dst, manifestPath string, skip Skip) []string {
	plan := planDeletes(readManifest(manifestPath), fileSet(src, skip), dst, skip)
	if !plan.holds() {
		return nil
	}
	return plan.atStake
}

// HoldRelease says how the held paths of a delete hold stood when
// ClearResolvedHold removed its hold marker.
type HoldRelease struct {
	// Restored counts held paths the project has again: pull's reverse
	// mirror copied them back from the store.
	Restored int
	// Gone counts held paths the store no longer has either, so there
	// was nothing to copy back — typically a peer deleted them and pull
	// merged that delete into the store.
	Gone int
}

// ClearResolvedHold removes the hold marker at marker once none of held
// (the paths HeldPaths returned before pull's reverse mirror) is still
// missing from project while the store has it. Each held path has then
// either come back to project or left the store too; the release counts
// which. A path still missing from project and present in store — its
// copy back failed — keeps the marker for the next forward pass to
// judge. Read-only apart from the marker. ok reports whether a marker
// was removed.
func ClearResolvedHold(project, store string, held []string, marker string) (release HoldRelease, ok bool) {
	if _, exists := deletehold.Read(marker); !exists {
		return HoldRelease{}, false
	}
	for _, rel := range held {
		if _, err := os.Lstat(filepath.Join(project, rel)); err == nil {
			release.Restored++
			continue
		}
		if _, err := os.Lstat(filepath.Join(store, rel)); os.IsNotExist(err) {
			release.Gone++
			continue
		}
		return HoldRelease{}, false
	}
	if err := deletehold.Remove(marker); err != nil {
		slog.Warn("mirror: removing hold marker failed", "path", marker, "err", err)
		return HoldRelease{}, false
	}
	return release, true
}

// Skip decides whether one entry is outside the mirror's remit. rel is
// the entry's path relative to the drawer's swarf/ directory, in slash
// form; isDir says whether it is a directory. A nil Skip mirrors
// everything.
//
// The same Skip has to be handed to both directions. Forward, it keeps
// the entry out of the store. Reverse, it also protects the entry from
// the delete pass: the store legitimately has no copy of an excluded
// path, and the reverse mirror deletes whatever the store lacks, so a
// Skip missing there would delete the excluded files out of the user's
// project — the opposite of what excluding them asked for.
type Skip func(rel string, isDir bool) bool

// SkipPatterns builds a Skip from the [sync] exclude patterns. Returns
// nil for an empty list, which mirrors everything.
//
// Three pattern forms, each matched against the drawer-relative path:
//
//   - trailing slash (".yolo/") — a directory of that name at any depth,
//     and everything beneath it.
//   - contains a slash ("home/ssh/*") — a glob over the whole relative
//     path, plus everything beneath a match.
//   - no slash ("*.tmp") — a glob over the file's base name, at any depth.
//
// Globbing is path.Match, so * does not cross a slash.
func SkipPatterns(patterns []string) Skip {
	var cleaned []string
	for _, p := range patterns {
		if p = strings.TrimSpace(filepath.ToSlash(p)); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return func(rel string, _ bool) bool {
		rel = strings.Trim(filepath.ToSlash(rel), "/")
		if rel == "" || rel == "." {
			return false
		}
		segments := strings.Split(rel, "/")
		for _, p := range cleaned {
			switch {
			case strings.HasSuffix(p, "/"):
				name := strings.TrimSuffix(p, "/")
				for _, seg := range segments {
					if ok, _ := path.Match(name, seg); ok {
						return true
					}
				}
			case strings.Contains(p, "/"):
				if ok, _ := path.Match(p, rel); ok {
					return true
				}
				if strings.HasPrefix(rel, p+"/") {
					return true
				}
			default:
				if ok, _ := path.Match(p, path.Base(rel)); ok {
					return true
				}
			}
		}
		return false
	}
}

// skips reports whether skip excludes rel, tolerating a nil Skip.
func skips(skip Skip, rel string, isDir bool) bool {
	return skip != nil && skip(rel, isDir)
}

// skipsEntry reports whether skip excludes the file rel as the walk sees
// it: excluded itself, or beneath an excluded directory the walk prunes
// whole. A directory pattern such as "home/ssh/*" matches the directory
// "home/ssh/keys" but not the path of a file inside it, so checking the
// file alone would miss it.
func skipsEntry(skip Skip, rel string) bool {
	if skip == nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' && skip(rel[:i], true) {
			return true
		}
	}
	return skip(rel, false)
}

// upToDate reports whether target already holds what srcInfo describes.
// Size plus "src is no newer" is the whole test — cheap enough to run on
// every file of every pass, which is what keeps repeated mirrors
// idempotent.
//
// srcInfo has to come from statResolvable rather than from the walk's own
// directory entry: the walk reports a symlink as itself, and the mirror
// copies the bytes the link points at.
func upToDate(srcInfo os.FileInfo, target string) bool {
	dstInfo, err := os.Stat(target)
	if err != nil {
		return false
	}
	return srcInfo.Size() == dstInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime())
}

// PendingFiles counts the files under src that a copy pass would write
// into dst right now. It is the read-only half of the predicate Dir,
// TrackedDir and CopyOnly copy by, exported so that code reporting sync
// state (swarf status) cannot drift from the code doing the syncing.
//
// Entries the mirror will never copy are not pending: a broken symlink, a
// link to a directory, anything unreadable. No copy is coming for those,
// so counting them yields a number that can never reach zero — a
// permanent false alarm. The copy pass logs them when it meets them,
// which is the right place to surface a stuck file.
//
// skip must be the same Skip the copy pass is given, for the same
// reason: an excluded file is never going to be copied either.
func PendingFiles(src, dst string, skip Skip) int {
	pending := 0
	filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if skips(skip, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		srcInfo, err := statResolvable(path)
		if err != nil || srcInfo.IsDir() {
			return nil
		}
		if !upToDate(srcInfo, filepath.Join(dst, rel)) {
			pending++
		}
		return nil
	})
	return pending
}

// readManifest returns the set of paths previously written by
// writeManifest at path. Returns an empty set (not nil) when the file
// doesn't exist — that's the "first run" signal callers use to decide
// no deletes should fire yet.
func readManifest(path string) map[string]struct{} {
	out := make(map[string]struct{})
	data, err := readFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out[line] = struct{}{}
	}
	return out
}

func writeManifest(path string, set map[string]struct{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Sort for deterministic on-disk ordering (eases debugging).
	lines := make([]string, 0, len(set))
	for rel := range set {
		lines = append(lines, rel)
	}
	// No sort import needed; use a simple write — order doesn't matter
	// for correctness, and a deterministic order is nice-to-have.
	// We rely on map iteration being whatever it is for now.
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// removeEmptyDirsUpTo removes the parent of `file` if it's empty, and
// continues upward until it hits `stop` or a non-empty directory. Used
// after a delete to keep dst tidy without accidentally blowing away
// directories that came from elsewhere (peer content, etc.).
func removeEmptyDirsUpTo(file, stop string) {
	dir := filepath.Dir(file)
	for dir != stop && strings.HasPrefix(dir, stop+string(os.PathSeparator)) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// CopyOnly copies src into dst without ever deleting anything in dst.
// Prefer TrackedDir when deletes should propagate — this is the purely
// additive variant for callers that never delete.
//
// Returns the last fatal error encountered (or nil). Per-file errors
// are logged and do not abort.
func CopyOnly(src, dst string, skip Skip) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dst, err)
	}
	var lastErr error
	filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("mirror: walk error", "path", path, "err", err)
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if skips(skip, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if err := mkdirAllRepair(target); err != nil {
				slog.Warn("mirror: mkdir failed", "path", target, "err", err)
				lastErr = err
			}
			return nil
		}

		srcInfo, err := statResolvable(path)
		if err != nil {
			slog.Debug("mirror: skipping unreadable entry", "path", path, "err", err)
			return nil
		}
		if upToDate(srcInfo, target) {
			return nil
		}
		data, err := readFile(path)
		if err != nil {
			slog.Warn("mirror: read failed", "path", path, "err", err)
			lastErr = err
			return nil
		}
		if err := mkdirAllRepair(filepath.Dir(target)); err != nil {
			slog.Warn("mirror: mkdir for file failed", "path", target, "err", err)
			lastErr = err
			return nil
		}
		if err := os.WriteFile(target, data, srcInfo.Mode()); err != nil {
			slog.Warn("mirror: write failed", "path", target, "err", err)
			lastErr = err
		}
		return nil
	})
	return lastErr
}

// mkdirAllRepair is os.MkdirAll with one extra behavior: if some
// component along the path exists as a non-directory (a regular file or
// symlink), remove it first and retry. That lets the mirror recover
// when the destination tree has stale state from an older layout where
// a path that now needs to be a directory is sitting as a file — real
// case: a previous sync copied `foo/bar.pdf` before we started tracking
// `foo/bar/` as a directory, and on the next pass the store has a file
// at `foo/bar` blocking the mkdir.
func mkdirAllRepair(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err == nil {
		return nil
	}
	// Walk ancestors and heal any non-directory we find.
	parts := strings.Split(filepath.Clean(dir), string(os.PathSeparator))
	// Re-prefix the leading separator on absolute paths.
	if strings.HasPrefix(dir, string(os.PathSeparator)) && len(parts) > 0 && parts[0] == "" {
		parts[0] = string(os.PathSeparator)
	}
	var cur string
	for i, p := range parts {
		if i == 0 {
			cur = p
		} else {
			cur = filepath.Join(cur, p)
		}
		if cur == "" {
			continue
		}
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				if err := os.Mkdir(cur, 0o755); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if info.IsDir() {
			continue
		}
		// Non-dir in the way: remove it and replace with a dir.
		slog.Info("mirror: replacing non-directory with directory", "path", cur)
		if err := os.Remove(cur); err != nil {
			return err
		}
		if err := os.Mkdir(cur, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies the file at path to target unless target is already
// up to date with srcInfo (statResolvable's result for path), creating
// target's parent directories. Failures are logged and returned.
func copyFile(path, target string, srcInfo os.FileInfo) error {
	if upToDate(srcInfo, target) {
		return nil
	}
	data, err := readFile(path)
	if err != nil {
		slog.Warn("mirror: read failed", "path", path, "err", err)
		return err
	}
	if err := mkdirAllRepair(filepath.Dir(target)); err != nil {
		slog.Warn("mirror: mkdir for file failed", "path", target, "err", err)
		return err
	}
	if err := os.WriteFile(target, data, srcInfo.Mode()); err != nil {
		slog.Warn("mirror: write failed", "path", target, "err", err)
		return err
	}
	return nil
}

// readFile reads a source file for a copy pass. A variable so tests can
// make a read fail.
var readFile = os.ReadFile

// statResolvable returns os.Stat(path), but treats broken symlinks (the
// link exists but the target doesn't) as a non-fatal "skip this entry"
// signal by returning fs.ErrNotExist. The caller can log at debug and
// move on without drowning output in warnings for every stale symlink
// in a large tree.
func statResolvable(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info, nil
	}
	// If Lstat succeeds the entry exists but points at nothing — broken
	// symlink. Normalize to ErrNotExist so callers have one case to handle.
	if _, lerr := os.Lstat(path); lerr == nil {
		return nil, os.ErrNotExist
	}
	return nil, err
}
