package enter

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/sweep"
)

// Run executes the mise enter hook: link files, then auto-sweep.
// Errors are silently ignored — mise hooks shouldn't be noisy.
func Run() {
	hostRoot := paths.FindHostRoot("")
	if hostRoot == "" {
		return
	}

	link.Run(hostRoot, true)

	gc := config.ReadGlobalConfig()
	if gc == nil || len(gc.AutoSweep) == 0 {
		return
	}

	var toSweep []string
	for _, p := range gc.AutoSweep {
		target := filepath.Join(hostRoot, p)
		fi, err := os.Lstat(target)
		if err == nil && fi.Mode()&os.ModeSymlink == 0 {
			toSweep = append(toSweep, p)
		}
	}

	if len(toSweep) > 0 {
		sweep.Run(toSweep, hostRoot)
	}
}
