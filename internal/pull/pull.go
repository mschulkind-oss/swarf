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

	"golang.org/x/term"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/mirror"
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

	// Flush any pending project → store edits first. The daemon normally
	// does this on its own schedule, but a manual 'swarf pull' shouldn't
	// lose work the user just saved. Forward then reverse keeps our
	// invariant: the store is up-to-date with local edits before we start
	// reconciling peer state into it.
	mirrorProjectsToStore()

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

	// Mirror store back into every registered project's swarf/ directory so
	// the user sees the pulled content (including conflict sidecars) where
	// they actually work. Without this, pull updates only the store; the
	// project's swarf/ keeps showing stale files until the daemon's next
	// forward mirror accidentally undoes our pull.
	synced, unregistered := mirrorStoreBackToProjects()

	if len(result.ConflictFiles) > 0 {
		console.Warn(fmt.Sprintf("Pulled %d peer(s) with %d conflict file(s):", result.PeersMerged, len(result.ConflictFiles)))
		for _, f := range result.ConflictFiles {
			console.Infof("  %s", f)
		}
		console.Hint("Open each original file, edit it to reflect what you want, then `rm` the .conflict.* sidecar.")
		console.Hint("See 'swarf docs conflicts' for the full resolution workflow.")
	} else {
		console.Ok(fmt.Sprintf("Pulled %d peer(s) cleanly.", result.PeersMerged))
	}

	if len(synced) > 0 {
		console.Infof("Synced into %d project(s): %s", len(synced), strings.Join(synced, ", "))
	}
	if len(unregistered) > 0 {
		console.Warn(fmt.Sprintf("%d project(s) in the store aren't registered on this machine:", len(unregistered)))
		for _, slug := range unregistered {
			console.Infof("  %s", slug)
		}
		console.Hint("cd into each one and run 'swarf init' to register and materialize its files.")
	}
	return result, nil
}

// mirrorProjectsToStore flushes project → store for every registered
// drawer. Uses the tracked mirror so we only propagate deletes for files
// we have first-hand evidence of (i.e. they were in project/swarf/ last
// time we looked and are gone now). Files we never saw stay in the store
// untouched — that's the "empty project doesn't wipe the store" rule.
func mirrorProjectsToStore() {
	for _, d := range config.ReadDrawers() {
		src := paths.SwarfDir(d.Host)
		dst := filepath.Join(paths.StoreDir, d.Slug)
		if !paths.IsDir(src) {
			continue
		}
		if err := mirror.TrackedDir(src, dst, paths.ProjectManifest(d.Slug)); err != nil {
			slog.Warn("pull: forward mirror failed", "project", d.Slug, "err", err)
		}
	}
}

// mirrorStoreBackToProjects copies the store's per-project subdirectories
// back into each registered drawer's swarf/ directory, matching what the
// daemon does in the forward direction. Destructive: files present in the
// project but missing from the store are deleted, so peer deletes propagate.
// We accept a small race window — if the user edits project/swarf/ during
// a pull, their change can be clobbered here, same risk the daemon's
// forward mirror already has.
//
// Returns the slugs that were mirrored, and the slugs present in the store
// that have no drawer on this machine (so the caller can tell the user to
// 'swarf init' them).
func mirrorStoreBackToProjects() (synced, unregistered []string) {
	drawers := config.ReadDrawers()
	registered := make(map[string]string, len(drawers)) // slug → host
	for _, d := range drawers {
		registered[d.Slug] = d.Host
	}

	// Walk the store's top-level entries — each directory that isn't .git/
	// is a project slug.
	entries, err := os.ReadDir(paths.StoreDir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".git" {
			continue
		}
		slug := e.Name()
		host, ok := registered[slug]
		if !ok {
			unregistered = append(unregistered, slug)
			continue
		}
		src := filepath.Join(paths.StoreDir, slug)
		dst := paths.SwarfDir(host)
		if err := mirror.Dir(src, dst); err != nil {
			slog.Warn("pull: reverse mirror failed", "project", slug, "err", err)
			continue
		}
		synced = append(synced, slug)
	}
	sort.Strings(synced)
	sort.Strings(unregistered)
	return synced, unregistered
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

// isStoreMetadata reports whether `path` (relative to the store root) is a
// per-machine metadata file that pull should ignore. These files are
// regenerated on every push from the local drawer list, so they legitimately
// differ across machines — pulling them in would create spurious conflicts.
func isStoreMetadata(path string) bool {
	switch path {
	case "README.md":
		return true
	}
	return false
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
		return fmt.Errorf("fetch %s (peer-cache %s): %w", remoteName, peerCache, err)
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
		if isStoreMetadata(e.Path) {
			// Skip store-local metadata files (e.g. the auto-generated
			// README.md). Each machine regenerates these from its own
			// drawer list, so pulling them would create spurious
			// conflicts that users can't meaningfully resolve.
			continue
		}
		switch e.Status {
		case "A", "M", "T":
			c, err := applyAddOrModify(peer, peerCache, effectiveOld, peerHead, e.Path, ts)
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
//
// Three-way comparison on modify:
//   - local == peerNew  → already identical, no-op.
//   - local == peerOld  → local is at the version the peer started from;
//                         peer simply moved forward, apply cleanly.
//   - otherwise         → both sides changed the same file, conflict sidecar.
//
// oldRef is the ref to use for "the version the peer had before this
// change" — lastSeen or merge-base, whichever the caller resolved.
func applyAddOrModify(peer, peerCache, oldRef, peerHead, rel, ts string) (string, error) {
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
		return "", nil
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

	// Three-way merge. Check whether local is at some historical state the
	// peer has been through — if so, peer is strictly ahead and we apply
	// their new content cleanly.
	if peerHasVersionMatching(peerCache, oldRef, peerHead, rel, localContent) {
		if err := os.WriteFile(localPath, peerContent, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", rel, err)
		}
		return "", nil
	}

	// Both sides diverged from every state the peer has been through.
	// Keep local, save peer version as a sidecar.
	return writeConflictSidecar(rel, peer, ts, peerContent)
}

// peerHasVersionMatching reports whether the peer's history contains any
// version of `rel` whose content equals `localContent`. When oldRef is
// non-empty this boils down to a single ShowFile(oldRef); when oldRef is
// empty (unrelated histories), we walk the peer's log for the file and
// check each commit, so content-identical divergent histories still merge
// cleanly.
func peerHasVersionMatching(peerCache, oldRef, peerHead, rel string, localContent []byte) bool {
	if oldRef != "" {
		peerOld, err := gitexec.ShowFile(peerCache, oldRef, rel)
		return err == nil && bytes.Equal(localContent, peerOld)
	}
	// Union-mode path: walk peer history for this file.
	cmd := exec.Command("git", "-C", peerCache, "log", "--format=%H", peerHead, "--", rel)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, sha := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if sha == "" {
			continue
		}
		content, err := gitexec.ShowFile(peerCache, sha, rel)
		if err == nil && bytes.Equal(localContent, content) {
			return true
		}
	}
	return false
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

// runRcloneSyncStreaming runs `rclone sync src dst` with progress output
// streamed to our stderr so a user watching 'swarf pull' isn't staring at
// a blank terminal for minutes. On a TTY we use rclone's live --progress
// dashboard (clears and redraws in place). Off a TTY — piped to a file,
// journald, etc. — we use multi-line stats so the listing phase shows
// "Checks: N/M" counters rather than only "0 B / 0 B, -, 0 B/s, ETA -".
//
// Why the distinction: Google Drive (and other cloud backends) spend most
// of their time listing and checksumming before any bytes transfer. One-
// line stats were hiding the "Checks" counter; users saw only the bytes
// line tick past with zeros and assumed the process was hung.
//
// Self-heal: if rclone reports "not a directory" errors (a stale file
// blocks a path that now needs to be a directory), we wipe the
// destination and retry once. This recovers peer caches left over from
// older layouts without the user having to clean up by hand.
func runRcloneSyncStreaming(src, dst string) error {
	if err := runRcloneSyncOnce(src, dst); err == nil {
		return nil
	} else if !isNotADirectoryError(err) {
		return err
	}
	slog.Warn("pull: peer cache has stale non-directory entries; wiping and retrying", "cache", dst)
	if rmErr := os.RemoveAll(dst); rmErr != nil {
		return fmt.Errorf("wipe peer cache %s: %w", dst, rmErr)
	}
	if mkErr := os.MkdirAll(dst, 0o755); mkErr != nil {
		return fmt.Errorf("recreate peer cache %s: %w", dst, mkErr)
	}
	return runRcloneSyncOnce(src, dst)
}

func runRcloneSyncOnce(src, dst string) error {
	args := []string{"sync", src, dst, "-v"}
	if term.IsTerminal(int(os.Stderr.Fd())) {
		args = append(args, "--progress", "--stats=1s")
	} else {
		args = append(args, "--stats=5s")
	}
	cmd := exec.Command("rclone", args...)
	cmd.Stdout = os.Stderr
	var errBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(errBuf.String())
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		return fmt.Errorf("%s", tail)
	}
	return nil
}

// isNotADirectoryError reports whether an rclone failure mentions a
// "not a directory" mkdir error. rclone exits with a generic non-zero
// code and puts the actual error lines in its stderr output, so we
// pattern-match on the captured tail we returned.
func isNotADirectoryError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not a directory")
}
