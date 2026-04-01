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
	ErrNoLinks   = errors.New("swarf links/ does not exist")
)

// Run reverses sweep: replaces symlinks with the actual file contents and
// removes the file from swarf/links/. Works inside jails where the daemon
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
	}
	return nil
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

	// Verify the source exists in swarf/links/.
	if _, err := os.Stat(source); os.IsNotExist(err) {
		console.Error(fmt.Sprintf("%s is not a swept file (not in %s/links/).", rel, paths.SwarfDirName))
		return "", false
	}

	// Read the content from swarf/links/.
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

	// Remove from swarf/links/.
	os.Remove(source)

	// Clean up empty parent dirs in swarf/links/.
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
