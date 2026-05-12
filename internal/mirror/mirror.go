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
)

// Dir mirrors src into dst: any file in src is copied/updated under dst,
// and any file in dst that isn't in src is deleted. Directories are
// created as needed. Symlinks are dereferenced — content is copied as a
// regular file, matching the existing project→store behavior.
//
// Errors from individual files are logged but don't short-circuit; the
// returned error is the last fatal error encountered (usually during the
// initial MkdirAll). Partial progress on transient errors is preferred
// to aborting — the next mirror pass will catch up.
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
