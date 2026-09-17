package daemon

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/swarf/internal/mirror"
)

func walkDirs(root string, fn func(string) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return fn(path)
		}
		return nil
	})
}

// walkDirsSkipping is walkDirs with excluded subtrees pruned. rel paths
// are taken relative to root, matching what mirror.Skip expects.
func walkDirsSkipping(root string, skip mirror.Skip, fn func(string) error) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr == nil && skip != nil && rel != "." && skip(rel, true) {
			return filepath.SkipDir
		}
		return fn(p)
	})
}
