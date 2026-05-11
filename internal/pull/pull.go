package pull

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	ErrNoConfig       = errors.New("no global config found — run 'swarf init' first")
	ErrNotGitRepo     = errors.New("store is not a git repository")
	ErrUnknownBackend = errors.New("unknown backend")
)

// Result summarizes what happened during a pull.
type Result struct {
	PeersSeen     int
	PeersMerged   int
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

	switch gc.Backend {
	case "git":
		return nil, pullGit(gc)
	case "rclone":
		return pullRclone(gc)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownBackend, gc.Backend)
	}
}

func pullGit(gc *config.GlobalConfig) error {
	// Auto-bootstrap: if the store doesn't exist yet, clone it from the
	// configured remote. Users on a fresh machine can now just run
	// `swarf pull` instead of the (now removed) `swarf clone`.
	if !paths.IsDir(paths.StoreDir) || !gitexec.IsRepo(paths.StoreDir) {
		if gc.Remote == "" {
			return ErrNotGitRepo
		}
		if err := os.RemoveAll(paths.StoreDir); err != nil {
			return fmt.Errorf("clear store dir: %w", err)
		}
		if err := gitexec.Clone(gc.Remote, paths.StoreDir); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
		console.Ok(fmt.Sprintf("Cloned store from %s", gc.Remote))
		return nil
	}
	if err := gitexec.Pull(paths.StoreDir); err != nil {
		return fmt.Errorf("git pull: %w", err)
	}
	console.Ok("Pulled latest changes.")
	return nil
}

// pullRclone is the multi-peer, file-delta pull used by the rclone backend.
// Layout assumption: <remote>/machines/<peer_id>/ is a full mirror of each
// peer's store (working files + .git/). For each peer other than self we:
//
//  1. `rclone sync` the peer's folder down to ~/.cache/swarf/peers/<peer>/.
//  2. Register the peer cache as a local git remote and fetch its HEAD so
//     its objects (including historical commit trees) are reachable from
//     the local store — needed for DiffNameStatus and ShowFile.
//  3. Compute last-seen via refs/swarf-peers/<peer>, falling back to
//     merge-base on first contact and to content-union for unrelated
//     histories.
//  4. Apply the resulting A/M/D entries to the local working tree, creating
//     conflict sidecars where local has diverged.
//  5. Commit any changes and advance refs/swarf-peers/<peer>.
func pullRclone(gc *config.GlobalConfig) (*Result, error) {
	if _, err := exec.LookPath("rclone"); err != nil {
		return nil, errors.New("rclone is not installed")
	}

	// Auto-bootstrap: create an empty store so the file-delta path can treat
	// every peer file as an add. Previously this was `swarf clone`'s job.
	if err := initialize.EnsureStore("", gc); err != nil {
		return nil, fmt.Errorf("ensure store: %w", err)
	}
	if !gitexec.IsRepo(paths.StoreDir) {
		return nil, ErrNotGitRepo
	}

	self := config.EnsureMachineID()
	peers, err := listPeers(gc.Remote)
	if err != nil {
		return nil, err
	}

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

// peerRefName is the per-peer "last-seen SHA" ref we advance after each
// successful pull. Stored under refs/swarf-peers/ so it never collides with
// branch or tag refs and never ships with a normal `git push`.
func peerRefName(peer string) string {
	return "refs/swarf-peers/" + peer
}

// pullFromPeer refreshes the peer's cache, computes a file delta against the
// last-seen peer HEAD, and applies it to the local store.
func pullFromPeer(remote, peer string, result *Result) error {
	peerCache := paths.PeerCacheDir(peer)
	if err := os.MkdirAll(peerCache, 0o755); err != nil {
		return fmt.Errorf("mkdir peer cache: %w", err)
	}
	peerRemote := strings.TrimRight(remote, "/") + "/" + backends.MachineSuffix + "/" + peer
	slog.Info("pull: copying peer", "peer", peer, "from", peerRemote, "to", peerCache)
	if err := runRcloneSyncStreaming(peerRemote, peerCache); err != nil {
		return fmt.Errorf("rclone sync from peer: %w", err)
	}

	// Register the peer cache as a git remote so we can fetch its objects.
	// Fetching makes ShowFile work for arbitrary commits in the peer's history
	// (needed to recover the pre-delete content of a file the peer deleted).
	remoteName := "peer-" + peer
	if gitexec.RemoteExists(paths.StoreDir, remoteName) {
		if err := gitexec.SetRemoteURL(paths.StoreDir, remoteName, peerCache); err != nil {
			return fmt.Errorf("set-url %s: %w", remoteName, err)
		}
	} else {
		if err := gitexec.AddRemote(paths.StoreDir, remoteName, peerCache); err != nil {
			return fmt.Errorf("add remote %s: %w", remoteName, err)
		}
	}
	if err := gitexec.FetchHead(paths.StoreDir, remoteName); err != nil {
		return fmt.Errorf("fetch %s: %w", remoteName, err)
	}

	peerHead := gitexec.RevParseHEAD(peerCache)
	if peerHead == "" {
		// Peer store exists but has no commits yet — nothing to pull.
		return nil
	}

	lastSeen := gitexec.ReadRef(paths.StoreDir, peerRefName(peer))
	if lastSeen == peerHead {
		return nil
	}

	localHead := gitexec.RevParseHEAD(paths.StoreDir)

	// Figure out which "mode" this pull is in. Three cases:
	//   1. Bootstrap  — no local HEAD at all. Treat every peer file as an add.
	//   2. Incremental — lastSeen is known. Diff lastSeen..peerHead.
	//   3. Post-upgrade / first contact — lastSeen empty but local has HEAD.
	//      Try merge-base (recovers linked histories from the old design).
	//      If none, fall back to content-union (adds/modifies, no deletes).
	unionMode := false
	effectiveOld := lastSeen
	if lastSeen == "" {
		if localHead == "" {
			// Bootstrap: diff from empty tree gives us every file as an add.
			effectiveOld = ""
		} else {
			base, err := gitexec.MergeBase(paths.StoreDir, localHead, peerHead)
			if err != nil {
				return fmt.Errorf("merge-base: %w", err)
			}
			if base == "" {
				// Unrelated histories — can't tell "peer deleted" from "peer
				// never had". Union every peer file in without deletes.
				unionMode = true
			} else {
				effectiveOld = base
			}
		}
	}

	var (
		entries []gitexec.DiffEntry
		err     error
	)
	if unionMode {
		entries, err = gitexec.DiffNameStatus(peerCache, "", peerHead)
		if err != nil {
			return fmt.Errorf("list peer tree: %w", err)
		}
	} else {
		entries, err = gitexec.DiffNameStatus(peerCache, effectiveOld, peerHead)
		if err != nil {
			return fmt.Errorf("diff peer: %w", err)
		}
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	var conflicts []string
	for _, e := range entries {
		switch e.Status {
		case "A", "M", "T":
			c, err := applyAddOrModify(peer, peerCache, peerHead, e.Path, ts)
			if err != nil {
				return err
			}
			if c != "" {
				conflicts = append(conflicts, c)
			}
		case "D":
			if unionMode {
				// Without a common ancestor we can't trust "peer deleted"
				// vs. "peer never had it"; skip.
				continue
			}
			c, err := applyDelete(peer, peerCache, effectiveOld, e.Path, ts)
			if err != nil {
				return err
			}
			if c != "" {
				conflicts = append(conflicts, c)
			}
		}
	}

	// Stage everything we touched and, if there are changes, make a commit.
	if err := gitexec.AddAll(paths.StoreDir); err != nil {
		return fmt.Errorf("add changes: %w", err)
	}
	if strings.TrimSpace(gitexec.StatusPorcelain(paths.StoreDir)) != "" {
		msg := fmt.Sprintf("auto: sync from %s", peer)
		if err := gitexec.Commit(paths.StoreDir, msg); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
	}

	if err := gitexec.UpdateRef(paths.StoreDir, peerRefName(peer), peerHead); err != nil {
		return fmt.Errorf("update-ref: %w", err)
	}

	result.ConflictFiles = append(result.ConflictFiles, conflicts...)
	return nil
}

// applyAddOrModify brings one peer add/modify into the local working tree.
// Returns the path of a conflict sidecar if local diverged, or "" otherwise.
func applyAddOrModify(peer, peerCache, peerHead, rel, ts string) (string, error) {
	peerContent, err := gitexec.ShowFile(peerCache, peerHead, rel)
	if err != nil {
		// The peer's working tree should still have this file since the diff
		// came from peerHead; fall back to reading it directly.
		peerContent, err = os.ReadFile(filepath.Join(peerCache, rel))
		if err != nil {
			return "", fmt.Errorf("read peer content for %s: %w", rel, err)
		}
	}

	localPath := filepath.Join(paths.StoreDir, rel)
	localContent, readErr := os.ReadFile(localPath)
	if readErr == nil && bytes.Equal(localContent, peerContent) {
		return "", nil // already identical — nothing to do
	}

	if os.IsNotExist(readErr) {
		// Fresh add. No conflict possible.
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return "", fmt.Errorf("mkdir for %s: %w", rel, err)
		}
		if err := os.WriteFile(localPath, peerContent, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", rel, err)
		}
		return "", nil
	}

	// Local exists and differs from peer. If local matches what the peer had
	// at lastSeen/merge-base, the peer simply moved forward — apply their
	// change cleanly. Otherwise both sides diverged: keep ours, drop theirs
	// as a sidecar.
	//
	// Detecting "peer moved forward while local stayed put" requires knowing
	// the old peer SHA; for modifies we take the conservative route and
	// always conflict-sidecar when bytes differ. The committed content on
	// disk is authoritative and the sidecar is cheap.
	return writeConflictSidecar(rel, peer, ts, peerContent)
}

// applyDelete removes a file locally if the local copy matches what the peer
// had just before deleting it; otherwise the local content has diverged from
// the version the peer deleted, so we keep ours and leave a sidecar with the
// peer's pre-delete content for reference.
func applyDelete(peer, peerCache, oldRef, rel, ts string) (string, error) {
	localPath := filepath.Join(paths.StoreDir, rel)
	localContent, err := os.ReadFile(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // already absent locally
		}
		return "", fmt.Errorf("read local %s: %w", rel, err)
	}

	// oldRef == "" only happens in bootstrap mode, which doesn't produce
	// delete entries, so we always have a ref here.
	preDelete, err := gitexec.ShowFile(peerCache, oldRef, rel)
	if err != nil {
		// Couldn't recover pre-delete content — be conservative, keep local.
		return writeConflictSidecar(rel, peer, ts, nil)
	}

	if bytes.Equal(localContent, preDelete) {
		if err := os.Remove(localPath); err != nil {
			return "", fmt.Errorf("remove %s: %w", rel, err)
		}
		return "", nil
	}

	// Local diverged from the version the peer deleted. Keep ours; leave a
	// breadcrumb of the peer's pre-delete content in a sidecar.
	return writeConflictSidecar(rel, peer, ts, preDelete)
}

// writeConflictSidecar writes peer content alongside the canonical path as
// <rel>.conflict.<peer>.<ts> so the user can compare. If content is nil or
// empty we still emit an empty sidecar so the user notices the conflict.
func writeConflictSidecar(rel, peer, ts string, peerContent []byte) (string, error) {
	sidecar := fmt.Sprintf("%s.conflict.%s.%s", rel, peer, ts)
	full := filepath.Join(paths.StoreDir, sidecar)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, peerContent, 0o644); err != nil {
		return "", fmt.Errorf("write sidecar %s: %w", sidecar, err)
	}
	return sidecar, nil
}

// runRcloneSyncStreaming runs `rclone sync src dst` with periodic one-line
// progress stats streamed straight to our stdout/stderr, so a user watching
// 'swarf pull' sees activity instead of a silent block. On failure we still
// capture stderr (via a tee) so the error message surfaces in the returned
// error even though it also appeared on screen.
func runRcloneSyncStreaming(src, dst string) error {
	cmd := exec.Command("rclone", "sync", src, dst,
		"--stats=5s", "--stats-one-line", "-v")
	cmd.Stdout = os.Stderr // rclone stats go to stderr anyway; keep both there
	var errBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(errBuf.String())
		// Keep the error message short — the live stream already showed the detail.
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return fmt.Errorf("%s", tail)
	}
	return nil
}
