package pull

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	ErrNoConfig       = errors.New("no global config found — run 'swarf init' first")
	ErrNoStore        = errors.New("store does not exist — run 'swarf clone' first")
	ErrNotGitRepo     = errors.New("store is not a git repository")
	ErrUnknownBackend = errors.New("unknown backend")
)

// Result summarizes what happened during a pull.
type Result struct {
	PeersSeen     int
	PeersMerged   int   // fast-forward or clean merge
	ConflictFiles []string
}

func Run() error {
	_, err := RunWithResult()
	return err
}

func RunWithResult() (*Result, error) {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return nil, ErrNoConfig
	}
	if !paths.IsDir(paths.StoreDir) {
		return nil, ErrNoStore
	}

	switch gc.Backend {
	case "git":
		return nil, pullGit()
	case "rclone":
		return pullRclone(gc)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownBackend, gc.Backend)
	}
}

func pullGit() error {
	if !gitexec.IsRepo(paths.StoreDir) {
		return ErrNotGitRepo
	}
	if err := gitexec.Pull(paths.StoreDir); err != nil {
		return fmt.Errorf("git pull: %w", err)
	}
	console.Ok("Pulled latest changes.")
	return nil
}

// pullRclone is the multi-peer, merge-aware pull used by the rclone backend.
// Layout assumption: <remote>/machines/<peer_id>/ is a full mirror of each
// peer's store (working files + .git/). We copy each peer down to a local
// cache, register it as a git remote on the store, fetch, and merge.
func pullRclone(gc *config.GlobalConfig) (*Result, error) {
	if _, err := exec.LookPath("rclone"); err != nil {
		return nil, errors.New("rclone is not installed")
	}
	if !gitexec.IsRepo(paths.StoreDir) {
		return nil, ErrNotGitRepo
	}

	self := config.EnsureMachineID()
	peers, err := listPeers(gc.Remote)
	if err != nil {
		return nil, err
	}

	// Warn when remote is still in the legacy flat layout. In that case peers
	// == nil and the user needs to migrate.
	if len(peers) == 0 {
		if legacy, reason := peekLegacyLayout(gc.Remote); legacy {
			return nil, fmt.Errorf("remote is in legacy flat layout (%s); run 'swarf doctor' for migration steps", reason)
		}
		console.Info("No peers found on remote yet.")
		return &Result{}, nil
	}

	result := &Result{PeersSeen: len(peers)}
	for _, peer := range peers {
		if peer == self {
			continue
		}
		if err := pullFromPeer(gc.Remote, peer, result); err != nil {
			console.Warn(fmt.Sprintf("peer %s: %v", peer, err))
			continue
		}
		result.PeersMerged++
	}

	if len(result.ConflictFiles) > 0 {
		console.Warn(fmt.Sprintf("Pulled %d peer(s) with %d conflict file(s):", result.PeersMerged, len(result.ConflictFiles)))
		for _, f := range result.ConflictFiles {
			console.Infof("  %s", f)
		}
		console.Hint("Open each original file, edit it to reflect what you want, then `rm` the .conflict.* sidecar.")
	} else {
		console.Ok(fmt.Sprintf("Pulled %d peer(s) cleanly.", result.PeersMerged))
	}
	return result, nil
}

// listPeers returns the machine IDs found under <remote>/machines/.
// Returns an empty slice if the machines/ directory doesn't exist yet.
func listPeers(remote string) ([]string, error) {
	machinesPath := strings.TrimRight(remote, "/") + "/" + backends.MachineSuffix
	cmd := exec.Command("rclone", "lsf", "--dirs-only", machinesPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// A missing machines/ directory is not an error — it's the empty case.
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(msg, "directory not found") || strings.Contains(msg, "not found") || strings.Contains(msg, "doesn't exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("rclone lsf %s: %s", machinesPath, strings.TrimSpace(string(out)))
	}
	var peers []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimRight(strings.TrimSpace(line), "/")
		if name == "" {
			continue
		}
		peers = append(peers, name)
	}
	sort.Strings(peers)
	return peers, nil
}

// peekLegacyLayout returns (true, reason) when the remote has a .git/
// directory at its root, matching the pre-multi-machine flat layout.
func peekLegacyLayout(remote string) (bool, string) {
	cmd := exec.Command("rclone", "lsf", "--dirs-only", strings.TrimRight(remote, "/"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimRight(strings.TrimSpace(line), "/")
		if name == ".git" {
			return true, "found .git/ at remote root"
		}
	}
	return false, ""
}

// pullFromPeer fetches one peer's commits and merges them into the local store.
// Conflicts are resolved by keeping the local version and writing the peer's
// conflicting version as a sidecar file.
func pullFromPeer(remote, peer string, result *Result) error {
	peerCache := paths.PeerCacheDir(peer)
	if err := os.MkdirAll(peerCache, 0o755); err != nil {
		return fmt.Errorf("mkdir peer cache: %w", err)
	}
	peerRemote := strings.TrimRight(remote, "/") + "/" + backends.MachineSuffix + "/" + peer
	slog.Info("pull: copying peer", "peer", peer, "from", peerRemote, "to", peerCache)
	cmd := exec.Command("rclone", "sync", peerRemote, peerCache, "-v")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rclone sync from peer: %s", strings.TrimSpace(string(out)))
	}

	// Register (or update) the peer as a git remote on the store so we can
	// fetch its objects.
	remoteName := "peer-" + peer
	peerGitURL := peerCache
	if gitexec.RemoteExists(paths.StoreDir, remoteName) {
		if err := gitexec.SetRemoteURL(paths.StoreDir, remoteName, peerGitURL); err != nil {
			return fmt.Errorf("set-url %s: %w", remoteName, err)
		}
	} else {
		if err := gitexec.AddRemote(paths.StoreDir, remoteName, peerGitURL); err != nil {
			return fmt.Errorf("add remote %s: %w", remoteName, err)
		}
	}
	if err := gitexec.FetchHead(paths.StoreDir, remoteName); err != nil {
		return fmt.Errorf("fetch %s: %w", remoteName, err)
	}

	// Empty-store bootstrap: if we have no HEAD yet, reset to the peer's tip.
	// This handles the normal second-machine path before any local syncs have
	// happened, and is equivalent to an initial clone.
	if gitexec.RevParseHEAD(paths.StoreDir) == "" {
		if err := gitexec.ResetHardTo(paths.StoreDir, "FETCH_HEAD"); err != nil {
			return fmt.Errorf("bootstrap from peer %s: %w", peer, err)
		}
		slog.Info("pull: bootstrapped store from peer", "peer", peer)
		return nil
	}

	// The peer's HEAD ref came across as FETCH_HEAD. Try fast-forward first.
	ref := "FETCH_HEAD"
	if err := gitexec.MergeFF(paths.StoreDir, ref); err == nil {
		slog.Info("pull: fast-forward merged peer", "peer", peer)
		return nil
	}

	// Fall back to a real merge. On conflicts, keep ours and write theirs as
	// a sidecar file. No data is lost: both sides' histories remain in git,
	// and the peer's working-tree version is visible on disk.
	msg := fmt.Sprintf("auto: merge peer %s", peer)
	clean, err := gitexec.MergeNoCommit(paths.StoreDir, ref, msg)
	if err != nil {
		// Unexpected failure (e.g., unrelated histories). Abort and report.
		_ = gitexec.AbortMerge(paths.StoreDir)
		return fmt.Errorf("merge from %s: %w", remoteName, err)
	}

	if !clean {
		conflicts, err := writeConflictSidecars(peer)
		if err != nil {
			_ = gitexec.AbortMerge(paths.StoreDir)
			return fmt.Errorf("resolve conflicts: %w", err)
		}
		result.ConflictFiles = append(result.ConflictFiles, conflicts...)
	}

	// Finalize the merge commit. `git commit` with no args uses the staged
	// merge (MERGE_MSG is set by our --no-commit merge).
	if err := gitexec.Commit(paths.StoreDir, msg); err != nil {
		// If there was literally nothing to merge after staging (rare race),
		// that's not an error.
		if strings.TrimSpace(gitexec.StatusPorcelain(paths.StoreDir)) == "" {
			return nil
		}
		return fmt.Errorf("commit merge: %w", err)
	}
	return nil
}

// writeConflictSidecars resolves each conflicted path by keeping our version
// and writing the peer's version alongside as <path>.conflict.<peer>.<ts>.
// Returns the list of sidecar paths created (relative to the store root).
func writeConflictSidecars(peer string) ([]string, error) {
	paths_ := gitexec.UnmergedPaths(paths.StoreDir)
	if len(paths_) == 0 {
		return nil, nil
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	var created []string
	for _, rel := range paths_ {
		theirs, err := gitexec.ShowStage(paths.StoreDir, 3, rel)
		if err != nil {
			// Delete/modify conflicts have no stage 3 — fall back to stage 2
			// (ours) being absent instead. If neither exists, skip and let
			// git commit the resolution as-is.
			theirs = nil
		}
		if err := gitexec.CheckoutOurs(paths.StoreDir, rel); err != nil {
			return created, fmt.Errorf("checkout ours for %s: %w", rel, err)
		}
		if err := gitexec.AddPath(paths.StoreDir, rel); err != nil {
			return created, fmt.Errorf("add %s: %w", rel, err)
		}
		if len(theirs) > 0 {
			sidecar := fmt.Sprintf("%s.conflict.%s.%s", rel, peer, ts)
			full := filepath.Join(paths.StoreDir, sidecar)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return created, err
			}
			if err := os.WriteFile(full, theirs, 0o644); err != nil {
				return created, fmt.Errorf("write sidecar %s: %w", sidecar, err)
			}
			if err := gitexec.AddPath(paths.StoreDir, sidecar); err != nil {
				return created, fmt.Errorf("add sidecar %s: %w", sidecar, err)
			}
			created = append(created, sidecar)
		}
	}
	return created, nil
}
