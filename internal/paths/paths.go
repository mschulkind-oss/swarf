package paths

import (
	"os"
	"path/filepath"
)

var SwarfDirName = "swarf"

var (
	ConfigDir        = xdg("XDG_CONFIG_HOME", ".config") + "/swarf"
	StoreDir         = xdg("XDG_DATA_HOME", ".local/share") + "/swarf"
	CacheDir         = xdg("XDG_CACHE_HOME", ".cache") + "/swarf"
	PeersDir         = CacheDir + "/peers"
	GlobalConfigTOML = ConfigDir + "/config.toml"
	DrawersTOML      = ConfigDir + "/drawers.toml"
	PIDFile          = ConfigDir + "/daemon.pid"
	LogFile          = ConfigDir + "/daemon.log"
	LastCommitFile   = ConfigDir + "/last-commit"
	LastPushFile     = ConfigDir + "/last-push"
)

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func SwarfDir(hostRoot string) string        { return filepath.Join(hostRoot, SwarfDirName) }
func LinksDir(hostRoot string) string        { return filepath.Join(SwarfDir(hostRoot), ".links") }
func StoreProjectDir(hostRoot string) string { return filepath.Join(StoreDir, ProjectSlug(hostRoot)) }
func PeerCacheDir(peerID string) string      { return filepath.Join(PeersDir, peerID) }

// ProjectManifest returns the path where the forward mirror records the
// set of files it last observed in project/swarf/<slug>/. Used by
// mirror.TrackedDir to distinguish "user deleted this" from "never
// seeded" — see mirror.TrackedDir for the full argument.
func ProjectManifest(slug string) string {
	return filepath.Join(CacheDir, "manifests", slug+".txt")
}

// DeleteHoldMarker returns the path of the hold marker for slug: the
// file that records a delete hold, written by mirror.TrackedDir when the
// forward mirror holds its deletes and removed by the next pass that
// does not. See package deletehold.
func DeleteHoldMarker(slug string) string {
	return filepath.Join(CacheDir, "delete-holds", slug+".json")
}

// SyncLockFile returns the path of the sync lock: the advisory lock
// held for the duration of any push, pull, or daemon cycle so two of
// them never touch the store at the same time. See package synclock.
func SyncLockFile() string {
	return filepath.Join(CacheDir, "sync.lock")
}

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
