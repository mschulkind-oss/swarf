package paths

import (
	"os"
	"path/filepath"
)

const SwarfDirName = ".swarf"

var (
	ConfigDir        = xdg("XDG_CONFIG_HOME", ".config") + "/swarf"
	StoreDir         = xdg("XDG_DATA_HOME", ".local/share") + "/swarf"
	GlobalConfigTOML = ConfigDir + "/config.toml"
	DrawersTOML      = ConfigDir + "/drawers.toml"
	PIDFile          = ConfigDir + "/daemon.pid"
	LogFile          = ConfigDir + "/daemon.log"
)

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func SwarfDir(hostRoot string) string     { return filepath.Join(hostRoot, SwarfDirName) }
func LinksDir(hostRoot string) string     { return filepath.Join(SwarfDir(hostRoot), "links") }
func StoreProjectDir(hostRoot string) string { return filepath.Join(StoreDir, ProjectSlug(hostRoot)) }

func ProjectSlug(hostRoot string) string {
	resolved, err := filepath.EvalSymlinks(hostRoot)
	if err != nil {
		resolved = hostRoot
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return filepath.Base(resolved)
	}
	return filepath.Base(abs)
}

// FindHostRoot walks up from start looking for a .swarf/ directory or symlink.
func FindHostRoot(start string) string {
	if start == "" {
		start, _ = os.Getwd()
	}
	current, _ := filepath.Abs(start)
	for {
		candidate := filepath.Join(current, SwarfDirName)
		fi, err := os.Lstat(candidate)
		if err == nil && (fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// IsDir checks if path exists and is a directory.
func IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
