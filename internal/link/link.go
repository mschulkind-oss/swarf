// Package link manages symlinks between swarf/.links/ and the host project.
//
// Invariant: for every entry swarf/.links/<rel>, the healthy host state is:
//  1. host <rel> is a relative symlink → .links/<rel>
//  2. /<rel> is in the managed .git/info/exclude block
//  3. <rel> is not tracked in the git index
package link

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var ErrNoProject = errors.New("not inside a swarf project — run 'swarf init' first")

type Result struct {
	Created  []string
	Skipped  []string
	Healed   []string
	Warnings []string
}

// Run walks swarf/.links/ and ensures each entry has a correct relative
// symlink in the host tree. When fix is true, it also untracks swept files
// that are still in the git index.
func Run(hostRoot string, quiet bool) (Result, error) {
	return RunWithFix(hostRoot, quiet, false)
}

// RunWithFix is like Run but when fix is true, it also runs git rm --cached
// on swept files that are still tracked by git.
func RunWithFix(hostRoot string, quiet bool, fix bool) (Result, error) {
	if hostRoot == "" {
		hostRoot = paths.FindHostRoot("")
	}
	if hostRoot == "" {
		return Result{}, ErrNoProject
	}

	ld := paths.LinksDir(hostRoot)
	result := Result{}

	if !paths.IsDir(ld) {
		return result, nil
	}
	entries, err := os.ReadDir(ld)
	if err != nil || len(entries) == 0 {
		return result, nil
	}

	filepath.Walk(ld, func(source string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(ld, source)
		target := filepath.Join(hostRoot, rel)
		result.processLink(source, target, rel, hostRoot, quiet, fix)
		return nil
	})

	// Print unresolved conflicts to the console only for interactive runs.
	// In quiet mode (the daemon) warnings are returned via result.Warnings so
	// the caller can dedup them — printing here would re-spam the journal every
	// relink cycle, the exact regression the quiet flag exists to prevent.
	if !quiet {
		for _, msg := range result.Warnings {
			console.Warn(msg)
		}
	}

	// Always ensure excludes for every .links/ entry, regardless of host state.
	var allLinked []string
	allLinked = append(allLinked, result.Created...)
	allLinked = append(allLinked, result.Skipped...)
	allLinked = append(allLinked, result.Healed...)
	if len(allLinked) > 0 {
		normalized := make([]string, len(allLinked))
		for i, p := range allLinked {
			normalized[i] = strings.ReplaceAll(p, "\\", "/")
		}
		exclude.AddLinkedExcludes(hostRoot, normalized)
	}

	return result, nil
}

func (r *Result) processLink(source, target, rel, hostRoot string, quiet, fix bool) {
	fi, err := os.Lstat(target)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		// Symlink exists — check it points to the right place.
		resolved, _ := filepath.EvalSymlinks(target)
		sourceResolved, _ := filepath.EvalSymlinks(source)
		if resolved == sourceResolved {
			r.maybeUntrack(hostRoot, rel, fix)
			r.Skipped = append(r.Skipped, rel)
			return
		}
		os.Remove(target) // stale symlink → will recreate below
	} else if err == nil {
		// Regular file where symlink should be — heal it.
		r.healRegularFile(source, target, rel, hostRoot, quiet, fix)
		return
	}

	// Target missing or was a stale symlink we just removed — create link.
	os.MkdirAll(filepath.Dir(target), 0o755)
	relSource, err := filepath.Rel(filepath.Dir(target), source)
	if err != nil {
		relSource = source
	}
	if err := os.Symlink(relSource, target); err != nil {
		console.Warn(fmt.Sprintf("Failed to link %s: %v", rel, err))
		return
	}
	r.maybeUntrack(hostRoot, rel, fix)
	r.Created = append(r.Created, rel)
	if !quiet {
		console.Infof("  linked %s", rel)
	}
}

// healRegularFile handles the case where a regular file exists at the host
// path instead of a symlink. This happens when an atomic-save editor or
// git checkout replaces the symlink with a regular file.
//
// The host file is assumed to be the newer write (once the file is untracked
// and excluded, git can never re-materialize it, so any regular file is a
// genuine user/editor write). Content is copied into .links/ before
// restoring the symlink, so no data is ever lost.
func (r *Result) healRegularFile(source, target, rel, hostRoot string, quiet, fix bool) {
	hostData, err := os.ReadFile(target)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: cannot read host file: %v", rel, err))
		return
	}

	linksData, err := os.ReadFile(source)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: cannot read .links/ copy: %v", rel, err))
		return
	}

	if !bytes.Equal(hostData, linksData) {
		// Host content is newer — propagate into .links/ before replacing.
		info, _ := os.Stat(target)
		if err := os.WriteFile(source, hostData, info.Mode()); err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: failed to update .links/ copy: %v", rel, err))
			return
		}
		if !quiet {
			console.Infof("  healed %s (content updated in .links/)", rel)
		}
	} else {
		if !quiet {
			console.Infof("  healed %s (identical, restored symlink)", rel)
		}
	}

	// Replace the regular file with a symlink.
	os.Remove(target)
	relSource, err := filepath.Rel(filepath.Dir(target), source)
	if err != nil {
		relSource = source
	}
	if err := os.Symlink(relSource, target); err != nil {
		// Restore the file if symlink creation fails.
		os.WriteFile(target, hostData, 0o644)
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: failed to create symlink: %v", rel, err))
		return
	}

	r.maybeUntrack(hostRoot, rel, fix)
	r.Healed = append(r.Healed, rel)
}

// maybeUntrack runs git rm --cached on a swept file that is still tracked.
// Only runs when fix is true — the daemon should not silently mutate the
// user's git index.
func (r *Result) maybeUntrack(hostRoot, rel string, fix bool) {
	if !fix {
		return
	}
	if !gitexec.IsTracked(hostRoot, rel) {
		return
	}
	if err := gitexec.RmCached(hostRoot, rel); err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: git rm --cached failed: %v", rel, err))
		return
	}
}
