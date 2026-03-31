package daemon

import (
	"os"
	"path/filepath"
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
