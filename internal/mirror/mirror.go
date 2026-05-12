// Package mirror keeps two directories in byte-level sync. It's used in
// both directions of the swarf data flow:
//
//   - Daemon (forward):  project/swarf/  →  store/<project>/
//   - Pull   (reverse):  store/<project>/  →  project/swarf/
//
// The model is deliberately destructive in both directions — files missing
// from the source are removed from the destination. The assumption is that
// we control the schedule (forward always runs right before pull's reverse,
// so the store is up-to-date with local edits first) and that losing a
// tiny amount of uncommitted work mid-operation is acceptable. Cross-
// machine sync already has that property: rclone sync is destructive too.
package mirror

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Dir mirrors src into dst destructively: files in src are copied/updated
// under dst, and files in dst missing from src are deleted. Used for the
// reverse (store → project) mirror in pull, where deletes in the store
// should propagate to the project.
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
func Dir(src, dst string) error {
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
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				slog.Warn("mirror: mkdir failed", "path", target, "err", err)
				lastErr = err
			}
			return nil
		}

		srcInfo, err := os.Stat(path)
		if err != nil {
			slog.Warn("mirror: stat failed", "path", path, "err", err)
			return nil
		}

		// Skip when size + mtime match — cheap short-circuit that keeps
		// repeated calls idempotent.
		if dstInfo, err := os.Stat(target); err == nil {
			if srcInfo.Size() == dstInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime()) {
				return nil
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("mirror: read failed", "path", path, "err", err)
			lastErr = err
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
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

// TrackedDir mirrors src into dst, propagating only the deletions that
// we have first-hand evidence of. Uses a per-source manifest at
// manifestPath that records the file set seen on the previous pass.
// Files in the old manifest but missing from src now (and present in
// dst) are true deletions and get removed from dst. Files that never
// existed in src (according to the manifest) are untouched in dst,
// which keeps a never-seeded or transiently-empty src from wiping dst.
//
// On first run, manifestPath doesn't exist and no deletes happen —
// only copies. Subsequent runs get delete-propagation right without
// the "empty project wipes the store" failure mode.
//
// Returns the last fatal error encountered (or nil). Per-file errors
// are logged and do not abort.
func TrackedDir(src, dst, manifestPath string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dst, err)
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
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				slog.Warn("mirror: mkdir failed", "path", target, "err", err)
				lastErr = err
			}
			return nil
		}

		newSet[rel] = struct{}{}

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
			lastErr = err
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
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

	// Delete from dst anything the manifest says we used to have in src
	// but we don't anymore. We only touch files we have first-hand
	// evidence of, never files that appeared in dst from some other
	// source (e.g. peer content we just pulled).
	for rel := range oldSet {
		if _, stillPresent := newSet[rel]; stillPresent {
			continue
		}
		target := filepath.Join(dst, rel)
		if err := os.Remove(target); err == nil {
			slog.Debug("mirror: deleted tracked file", "path", rel)
			// Best-effort cleanup of newly-empty parent dirs, stopping at dst.
			removeEmptyDirsUpTo(target, dst)
		}
	}

	if err := writeManifest(manifestPath, newSet); err != nil {
		slog.Warn("mirror: writing manifest failed", "path", manifestPath, "err", err)
	}
	return lastErr
}

// readManifest returns the set of paths previously written by
// writeManifest at path. Returns an empty set (not nil) when the file
// doesn't exist — that's the "first run" signal callers use to decide
// no deletes should fire yet.
func readManifest(path string) map[string]struct{} {
	out := make(map[string]struct{})
	data, err := os.ReadFile(path)
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
func CopyOnly(src, dst string) error {
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
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				slog.Warn("mirror: mkdir failed", "path", target, "err", err)
				lastErr = err
			}
			return nil
		}

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
			lastErr = err
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
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
