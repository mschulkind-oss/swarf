package unlink

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	ErrNoProject = errors.New("not inside a swarf project — run 'swarf init' first")
	ErrNoLinks   = errors.New("swarf .links/ does not exist")
)

// Run reverses sweep: replaces symlinks with the actual file contents and
// removes the file from swarf/.links/. Works inside jails where the daemon
// isn't available.
func Run(filePaths []string, hostRoot string) error {
	if hostRoot == "" {
		hostRoot = paths.FindHostRoot("")
	}
	if hostRoot == "" {
		return ErrNoProject
	}
	ld := paths.LinksDir(hostRoot)
	if !paths.IsDir(ld) {
		return ErrNoLinks
	}

	var unlinked []string
	for _, p := range filePaths {
		if rel, ok := unlinkOne(p, hostRoot, ld); ok {
			unlinked = append(unlinked, rel)
		}
	}

	if len(unlinked) > 0 {
		exclude.RemoveExcludes(hostRoot, unlinked)
		purgeFromStore(hostRoot, unlinked)
	}
	return nil
}

// purgeFromStore removes the unlinked files from the central store's copy of
// this project and drops them from the forward-mirror manifest.
//
// Without this, unlink only half-reverses a sweep: swarf/.links/<rel> is gone
// locally, but store/<slug>/.links/<rel> survives — and init seeds
// project/swarf/ *additively* from the store, so the next 'swarf init' (or
// pull's reverse mirror) copies the entry straight back. The host file stays a
// regular file, so the resurrected .links/ entry is never re-linked and doctor
// reports the path as not-gitignored / still-tracked / "not a symlink" on
// every run, with no command that fixes it.
//
// The manifest entry must go too. It records what the forward mirror last saw
// in project/swarf/; leaving <rel> in it makes the next mirror pass treat the
// file as a fresh user deletion, which is harmless but redundant. Dropping it
// keeps the manifest an accurate picture of the project.
func purgeFromStore(hostRoot string, unlinked []string) {
	slug := paths.ProjectSlug(hostRoot)
	storeLinks := filepath.Join(paths.StoreDir, slug, ".links")
	if !paths.IsDir(storeLinks) {
		return
	}
	for _, rel := range unlinked {
		target := filepath.Join(storeLinks, rel)
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			console.Warn(fmt.Sprintf("%s: failed to remove store copy: %v", rel, err))
			continue
		}
		cleanEmptyParents(filepath.Dir(target), storeLinks)
	}
	dropFromManifest(paths.ProjectManifest(slug), unlinked)
}

// dropFromManifest rewrites the forward-mirror manifest without the given
// project-relative .links/ entries. Missing manifest is not an error — that's
// the "first run, no deletes yet" state mirror.TrackedDir relies on.
func dropFromManifest(manifestPath string, unlinked []string) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return
	}
	drop := make(map[string]bool, len(unlinked))
	for _, rel := range unlinked {
		drop[filepath.ToSlash(filepath.Join(".links", rel))] = true
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || drop[trimmed] {
			continue
		}
		kept = append(kept, trimmed)
	}
	if err := os.WriteFile(manifestPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		console.Warn(fmt.Sprintf("failed to update mirror manifest: %v", err))
	}
}

func unlinkOne(pathStr, hostRoot, linksDir string) (string, bool) {
	target := pathStr
	if !filepath.IsAbs(target) {
		cwd, _ := os.Getwd()
		target = filepath.Join(cwd, target)
	}

	rel, err := filepath.Rel(hostRoot, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		console.Error(fmt.Sprintf("%s is not inside the project root.", pathStr))
		return "", false
	}

	source := filepath.Join(linksDir, rel)

	// Verify the source exists in swarf/.links/.
	if _, err := os.Stat(source); os.IsNotExist(err) {
		console.Error(fmt.Sprintf("%s is not a swept file (not in %s/.links/).", rel, paths.SwarfDirName))
		return "", false
	}

	// Read the content from swarf/.links/.
	data, err := os.ReadFile(source)
	if err != nil {
		console.Error(fmt.Sprintf("Failed to read %s: %v", rel, err))
		return "", false
	}
	srcInfo, err := os.Stat(source)
	if err != nil {
		console.Error(fmt.Sprintf("Failed to stat %s: %v", rel, err))
		return "", false
	}

	// Remove the symlink (or whatever is at target).
	os.Remove(target)

	// Write the file contents back as a regular file.
	os.MkdirAll(filepath.Dir(target), 0o755)
	if err := os.WriteFile(target, data, srcInfo.Mode()); err != nil {
		console.Error(fmt.Sprintf("Failed to write %s: %v", rel, err))
		return "", false
	}

	// Remove from swarf/.links/.
	os.Remove(source)

	// Clean up empty parent dirs in swarf/.links/.
	cleanEmptyParents(filepath.Dir(source), linksDir)

	console.Infof("  unlinked %s", rel)
	return rel, true
}

// cleanEmptyParents removes empty directories between child and stop (exclusive).
func cleanEmptyParents(dir, stop string) {
	for dir != stop && dir != "." && dir != "/" {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}
