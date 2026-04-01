package sweep

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
	ErrNoLinks   = errors.New("swarf links/ does not exist — run 'swarf init' first")
)

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

	var swept []string
	for _, p := range filePaths {
		if rel, ok := sweepOne(p, hostRoot, ld); ok {
			swept = append(swept, rel)
		}
	}

	if len(swept) > 0 {
		exclude.AddLinkedExcludes(hostRoot, swept)
	}
	return nil
}

func sweepOne(pathStr, hostRoot, linksDir string) (string, bool) {
	source := pathStr
	if !filepath.IsAbs(source) {
		cwd, _ := os.Getwd()
		source = filepath.Join(cwd, source)
	}

	// Check symlink before resolving
	if fi, err := os.Lstat(source); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		rel, _ := filepath.Rel(hostRoot, source)
		console.Warn(fmt.Sprintf("%s is already a symlink, skipping.", rel))
		return "", false
	}

	// Check if inside swarf dir BEFORE resolving
	if rel, err := filepath.Rel(hostRoot, source); err == nil {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == paths.SwarfDirName {
				console.Error(fmt.Sprintf("%s is already inside %s/.", pathStr, paths.SwarfDirName))
				return "", false
			}
		}
	}

	resolved, err := filepath.EvalSymlinks(source)
	if err == nil {
		source = resolved
	}

	rel, err := filepath.Rel(hostRoot, source)
	if err != nil || strings.HasPrefix(rel, "..") {
		console.Error(fmt.Sprintf("%s is not inside the project root.", pathStr))
		return "", false
	}

	if _, err := os.Stat(source); os.IsNotExist(err) {
		console.Error(fmt.Sprintf("%s does not exist.", pathStr))
		return "", false
	}

	dest := filepath.Join(linksDir, rel)
	if _, err := os.Stat(dest); err == nil {
		console.Warn(fmt.Sprintf("%s already exists in %s/.links/, skipping.", rel, paths.SwarfDirName))
		return "", false
	}

	os.MkdirAll(filepath.Dir(dest), 0o755)
	if err := os.Rename(source, dest); err != nil {
		console.Error(fmt.Sprintf("Failed to move %s: %v", rel, err))
		return "", false
	}
	relTarget, err := filepath.Rel(filepath.Dir(source), dest)
	if err != nil {
		relTarget = dest // fallback to absolute
	}
	if err := os.Symlink(relTarget, source); err != nil {
		console.Error(fmt.Sprintf("Failed to create symlink for %s: %v", rel, err))
		return "", false
	}
	console.Infof("  swept %s", rel)
	return rel, true
}
