package pull

import (
	"bytes"
	"context"
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
	"github.com/mschulkind-oss/swarf/internal/deletehold"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/synclock"
)

var (
	ErrNoConfig       = errors.New("no global config found — run 'swarf init' first")
	ErrNotGitRepo     = errors.New("store is not a git repository")
	ErrUnknownBackend = errors.New("unknown backend")
	ErrNoGitRemote    = errors.New("the store has no git remote (origin) and [sync] remote is empty, so there is nothing to pull from")
)

// Result summarizes what happened during a pull.
type Result struct {
	PeersSeen     int
	PeersMerged   int
	ConflictFiles []string
	// PeerFailures lists the peers the pull could not merge, each with
	// its error. The pull goes on to the other peers and still succeeds.
	PeerFailures []PeerFailure
	// Unregistered lists the store's projects that have no drawer on
	// this machine, so the pull could not mirror them into a project.
	Unregistered []string

	// applying is set once a peer's download finishes and pull starts
	// changing the store for it, even if that peer then fails.
	applying bool
	// applied maps each store path a peer's add or modify wrote to
	// that peer, so the reverse mirror can tell a file the user saved
	// over a peer's change during the pull.
	applied map[string]string
}

// PeerFailure is one peer a pull could not merge, and why.
type PeerFailure struct {
	Peer string
	Err  error
}

// Options tunes one pull.
type Options struct {
	// Resync ignores each peer's last-seen ref and unions every file in
	// the peer's current tree into the store ('swarf pull --resync'). The
	// only deletes it applies are the ones the peer made since that ref.
	Resync bool
}

// Run pulls once. A pull that finds another push, pull, or daemon cycle
// holding the sync lock waits for it, and gives up if ctx ends first.
// ctx also bounds the downloads: ending it kills the in-flight rclone
// or git fetch, skips the remaining peers, and returns ctx's error once
// whatever it applied from peers is mirrored back into the projects.
func Run(ctx context.Context, opts Options) error {
	_, err := RunWithResult(ctx, opts)
	return err
}

func RunWithResult(ctx context.Context, opts Options) (*Result, error) {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return nil, ErrNoConfig
	}

	// Hold the sync lock for the whole pull, so a daemon cycle or a
	// push cannot mirror or commit the store in the middle of it.
	release, err := synclock.Acquire(ctx, func() { console.Info(synclock.WaitMessage) })
	if err != nil {
		return nil, fmt.Errorf("sync lock: %w", err)
	}
	defer release()

	switch gc.Backend {
	case "git":
		return pullGit(ctx, gc, opts)
	case "rclone":
		return pullRclone(ctx, gc, opts)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownBackend, gc.Backend)
	}
}

// machineRefPrefix is the store-local namespace a git-backend pull
// fetches every machine branch into: refs/swarf-machines/<machine_id>
// is the store's copy of machines/<machine_id> as of the last fetch.
// These are not remote-tracking branches, so they stay out of sight of
// anyone looking at the store with git, and a push never sends them.
const machineRefPrefix = "refs/swarf-machines/"

// machineRefName is the store's copy of machineID's machine branch.
func machineRefName(machineID string) string {
	return machineRefPrefix + machineID
}

// pullGit is the multi-peer, file-delta pull for the git backend. Every
// machine pushes its store to its own machine branch (see
// backends.MachineBranch), so each machine branch other than this
// machine's is one peer, merged exactly as the rclone backend merges a
// peer (see mergePeer). The steps:
//
//  1. Create the store if there is none (a fresh machine no longer
//     clones), and give it the [sync] remote as origin if it has none.
//  2. Fetch every machine branch into refs/swarf-machines/ (see
//     machineRefPrefix), which brings each peer's history into the store.
//  3. Reinstall recovery: a store with no commits whose own machine
//     branch is already on the remote starts from that branch, so a
//     reinstalled machine gets its old data back and its next push
//     fast-forwards the branch.
//  4. Fold in each legacy branch that is not a tombstone (see
//     foldLegacy), then merge each peer, mirror the store back into the
//     projects, and report, as pullRclone does.
//  5. Retire each legacy branch the machine branch already holds (see
//     backends.RetireLegacyBranches).
func pullGit(ctx context.Context, gc *config.GlobalConfig, opts Options) (*Result, error) {
	if gc.Remote == "" && !gitexec.IsRepo(paths.StoreDir) {
		return nil, ErrNotGitRepo
	}
	if err := initialize.EnsureStore("", gc); err != nil {
		return nil, fmt.Errorf("ensure store: %w", err)
	}
	if !gitexec.IsRepo(paths.StoreDir) {
		return nil, ErrNotGitRepo
	}
	if !gitexec.RemoteExists(paths.StoreDir, "origin") {
		if gc.Remote == "" {
			return nil, ErrNoGitRemote
		}
		if err := gitexec.AddRemote(paths.StoreDir, "origin", gc.Remote); err != nil {
			return nil, fmt.Errorf("add remote origin: %w", err)
		}
	}
	remote := gitexec.RemoteURL(paths.StoreDir, "origin")

	refspec := "+refs/heads/" + backends.MachineSuffix + "/*:" + machineRefPrefix + "*"
	slog.Info("pull: fetching machine branches", "remote", remote)
	if err := gitexec.FetchRefspec(ctx, paths.StoreDir, "origin", refspec); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git fetch %s: %w", remote, err)
	}
	tips, err := gitexec.ListRefs(paths.StoreDir, machineRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("list machine branches: %w", err)
	}
	legacy, err := fetchLegacyBranches(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	self := config.EnsureMachineID()
	if own := tips[self]; own != "" && gitexec.RevParseHEAD(paths.StoreDir) == "" {
		branch := backends.MachineBranch(self)
		if err := gitexec.ResetHard(paths.StoreDir, own); err != nil {
			return nil, fmt.Errorf("start the store from %s: %w", branch, err)
		}
		// The branch already has this commit, so a push has nothing to
		// send until the store moves past it.
		if err := gitexec.UpdateRef(paths.StoreDir, backends.PushedRef(self), own); err != nil {
			slog.Warn("pull: could not record the pushed commit", "ref", backends.PushedRef(self), "err", err)
		}
		console.Ok(fmt.Sprintf("Started the store from this machine's branch %s on %s", branch, remote))
	}

	// Flush pending project → store edits before merging peers, as
	// pullRclone does. It runs after the recovery above, which replaces
	// the store's working tree.
	stamps := mirrorProjectsToStore(mirror.SkipPatterns(gc.Exclude))

	// Fold in the legacy branches before the peers: a peer that started
	// from the same legacy branch then shares its history with the store.
	result := &Result{}
	foldLegacyBranches(unfolded(legacy), legacy, remote, result)

	peers := make([]string, 0, len(tips))
	for id := range tips {
		peers = append(peers, id)
	}
	sort.Strings(peers)
	result, err = mergePeers(ctx, gc, result, peers, self, stamps, func(ctx context.Context, peer string, result *Result) error {
		// The fetch above already brought the peer's history into the
		// store; merging starts changing it.
		result.applying = true
		return mergePeer(peer, tips[peer], opts.Resync, result)
	})
	if err != nil {
		return result, err
	}
	RetireLegacyBranches(ctx)
	return result, nil
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
func pullRclone(ctx context.Context, gc *config.GlobalConfig, opts Options) (*Result, error) {
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
	stamps := mirrorProjectsToStore(mirror.SkipPatterns(gc.Exclude))

	self := config.EnsureMachineID()
	peers, err := listPeers(ctx, gc.Remote)
	if err != nil {
		return nil, err
	}

	if len(peers) == 0 {
		if legacy, reason := peekLegacyLayout(ctx, gc.Remote); legacy {
			return nil, fmt.Errorf("remote is in legacy flat layout (%s); run 'swarf doctor' for migration steps", reason)
		}
		console.Info("No peers found on remote yet.")
		return &Result{}, nil
	}

	return mergePeers(ctx, gc, &Result{}, peers, self, stamps, func(ctx context.Context, peer string, result *Result) error {
		return pullFromPeer(ctx, gc, peer, opts.Resync, result)
	})
}

// mergePeers is the part of a pull every backend shares. It runs
// pullPeer for each of peers except self — pullPeer brings that peer's
// store into this one and merges it with mergePeer — then mirrors the
// store back into every registered project and reports the result.
// stamps are from the forward mirror (mirrorProjectsToStore) the caller
// ran first. A peer that fails is reported and skipped; ending ctx skips
// the remaining peers. result collects the outcome; the caller may have
// started it (the git pull folds legacy branches into it first).
func mergePeers(ctx context.Context, gc *config.GlobalConfig, result *Result, peers []string, self string, stamps map[string]mirror.Stamps,
	pullPeer func(ctx context.Context, peer string, result *Result) error) (*Result, error) {
	result.PeersSeen = len(peers)
	others := 0
	for _, peer := range peers {
		if peer == self {
			continue
		}
		others++
		if ctx.Err() != nil {
			break
		}
		if err := pullPeer(ctx, peer, result); err != nil {
			console.Warn(fmt.Sprintf("peer %s: %v", peer, err))
			result.PeerFailures = append(result.PeerFailures, PeerFailure{Peer: peer, Err: err})
			continue
		}
		result.PeersMerged++
	}

	// An interrupted pull that applied nothing from a peer has nothing
	// to mirror back, so it leaves the projects alone.
	if err := ctx.Err(); err != nil && !result.applying {
		return result, err
	}

	// Mirror store back into every registered project's swarf/ directory so
	// the user sees the pulled content (including conflict sidecars) where
	// they actually work. Without this, pull updates only the store; the
	// project's swarf/ keeps showing stale files until the daemon's next
	// forward mirror accidentally undoes our pull.
	synced, unregistered, savedOver := mirrorStoreBackToProjects(mirror.SkipPatterns(gc.Exclude), stamps, result.applied)
	result.Unregistered = unregistered
	result.ConflictFiles = append(result.ConflictFiles, savedOver...)

	// An interrupted pull still mirrors back what it applied — skipping
	// that would leave the store ahead of the projects, and the next
	// forward mirror would undo the peers' edits — but it is not a
	// finished pull, so it reports the interruption instead of a summary.
	if err := ctx.Err(); err != nil {
		return result, err
	}

	if len(result.ConflictFiles) > 0 {
		console.Warn(fmt.Sprintf("Pulled %d peer(s) with %d conflict file(s):", result.PeersMerged, len(result.ConflictFiles)))
		for _, f := range result.ConflictFiles {
			console.Infof("  %s", f)
		}
		console.Hint("Open each original file, edit it to reflect what you want, then `rm` the .conflict.* sidecar.")
		console.Hint("See 'swarf docs conflicts' for the full resolution workflow.")
	} else if others == 0 {
		console.Info("No peers found on remote yet.")
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
//
// Returns the Stamps of each drawer's project, by slug, taken just before
// its forward pass, so the reverse mirror can tell which project files
// the user saved while pull was downloading peers.
func mirrorProjectsToStore(skip mirror.Skip) map[string]mirror.Stamps {
	stamps := make(map[string]mirror.Stamps)
	for _, d := range config.ReadDrawers() {
		src := paths.SwarfDir(d.Host)
		dst := filepath.Join(paths.StoreDir, d.Slug)
		stamps[d.Slug] = mirror.TakeStamps(src, skip)
		if !paths.IsDir(src) {
			continue
		}
		// A delete hold here is left for the reverse mirror to resolve:
		// the held files are still in the store, so it copies them back.
		guard := mirror.Guard{Marker: paths.DeleteHoldMarker(d.Slug)}
		if _, err := mirror.TrackedDir(src, dst, paths.ProjectManifest(d.Slug), skip, guard); err != nil {
			slog.Warn("pull: forward mirror failed", "project", d.Slug, "err", err)
		}
	}
	return stamps
}

// mirrorStoreBackToProjects copies the store's per-project subdirectories
// back into each registered drawer's swarf/ directory, matching what the
// daemon does in the forward direction. Deletes are manifest-gated
// (mirror.TrackedReverse): a project file missing from the store is
// deleted only when the manifest says swarf synced it before, so peer
// deletes propagate but a file saved during the pull survives. The
// manifest is then rewritten to the project's file set. stamps, from
// mirrorProjectsToStore, protect a synced file the user saved during the
// pull (the downloads can take minutes): the store's older copy does not
// overwrite it, and a peer's delete does not remove it. A drawer with no
// stamps, registered mid-pull, has no such protection.
//
// When a peer's add or modify (applied, store path → peer) landed on a
// file the user saved during the pull, the save stays and the store's
// copy — the peer's version — is written as a conflict sidecar beside
// it, as for any other divergent edit; otherwise the next forward pass
// would copy the save over the peer's version with nothing to show for
// it.
//
// Returns the slugs that were mirrored, the slugs present in the store
// that have no drawer on this machine (so the caller can tell the user to
// 'swarf init' them), and the conflict sidecars written for saves over a
// peer's change.
func mirrorStoreBackToProjects(skip mirror.Skip, stamps map[string]mirror.Stamps, applied map[string]string) (synced, unregistered, conflicts []string) {
	drawers := config.ReadDrawers()
	registered := make(map[string]string, len(drawers)) // slug → host
	for _, d := range drawers {
		registered[d.Slug] = d.Host
	}

	// Walk the store's top-level entries — each directory that isn't .git/
	// is a project slug.
	entries, err := os.ReadDir(paths.StoreDir)
	if err != nil {
		return nil, nil, nil
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
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
		manifest, marker := paths.ProjectManifest(slug), paths.DeleteHoldMarker(slug)
		// Read what a delete hold is holding before the reverse mirror
		// rewrites the manifest, which erases that evidence.
		var held []string
		if _, holding := deletehold.Read(marker); holding {
			held = mirror.HeldPaths(dst, src, manifest, skip)
		}
		conflicts = append(conflicts, sidecarSavesOverPeerChanges(slug, dst, skip, stamps[slug], applied, ts)...)
		if err := mirror.TrackedReverse(src, dst, manifest, skip, stamps[slug]); err != nil {
			slog.Warn("pull: reverse mirror failed", "project", slug, "err", err)
			continue
		}
		// The reverse mirror just copied back any files a delete hold
		// kept in the store, which is the "restore the files" way out.
		if release, ok := mirror.ClearResolvedHold(dst, src, held, marker); ok {
			console.Ok(holdReleaseMessage(slug, release))
		}
		synced = append(synced, slug)
	}
	sort.Strings(synced)
	sort.Strings(unregistered)
	return synced, unregistered, conflicts
}

// sidecarSavesOverPeerChanges writes, into the store, a conflict sidecar
// of each file under slug that a peer's add or modify wrote this pull
// (applied) and that the user saved differently in the project (dst)
// since stamps. The reverse mirror that follows copies the sidecars into
// the project and leaves the saves in place. Returns the sidecars'
// store paths.
func sidecarSavesOverPeerChanges(slug, dst string, skip mirror.Skip, stamps mirror.Stamps, applied map[string]string, ts string) []string {
	var rels []string
	for path := range applied {
		if rel, ok := strings.CutPrefix(path, slug+"/"); ok {
			rels = append(rels, rel)
		}
	}
	sort.Strings(rels)
	var sidecars []string
	for _, rel := range mirror.Protected(dst, skip, stamps, rels) {
		path := slug + "/" + rel
		peerContent, err := os.ReadFile(filepath.Join(paths.StoreDir, path))
		if err != nil {
			continue
		}
		saved, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil || bytes.Equal(saved, peerContent) {
			continue
		}
		sidecar, err := writeConflictSidecar(path, applied[path], ts, peerContent)
		if err != nil {
			slog.Warn("pull: writing conflict sidecar failed", "path", path, "err", err)
			continue
		}
		sidecars = append(sidecars, sidecar)
	}
	return sidecars
}

// holdReleaseMessage tells the user how a delete hold on slug ended
// during pull: the held files came back from the store, or they were no
// longer in the store either (deleted on another machine), or some of
// each.
func holdReleaseMessage(slug string, r mirror.HoldRelease) string {
	switch {
	case r.Gone == 0 && r.Restored == 0:
		return fmt.Sprintf("%s: delete hold cleared.", slug)
	case r.Gone == 0:
		return fmt.Sprintf("%s: restored the files a delete hold kept in the store; hold cleared.", slug)
	case r.Restored == 0:
		return fmt.Sprintf("%s: the files a delete hold kept are no longer in the store (deleted on another machine), so there was nothing to copy back; hold cleared.", slug)
	default:
		return fmt.Sprintf("%s: restored %d file(s) a delete hold kept in the store; %d more are no longer in the store (deleted on another machine); hold cleared.", slug, r.Restored, r.Gone)
	}
}

// listPeers returns the machine IDs found under <remote>/machines/.
// Returns an empty slice if the machines/ directory doesn't exist yet.
func listPeers(ctx context.Context, remote string) ([]string, error) {
	machinesPath := strings.TrimRight(remote, "/") + "/" + backends.MachineSuffix
	cmd := exec.CommandContext(ctx, "rclone", "lsf", "--dirs-only", machinesPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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
func peekLegacyLayout(ctx context.Context, remote string) (bool, string) {
	cmd := exec.CommandContext(ctx, "rclone", "lsf", "--dirs-only", strings.TrimRight(remote, "/"))
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

// pullFromPeer refreshes the peer's cache, fetches the peer's objects from
// it into the store, and merges the peer's tip with mergePeer.
func pullFromPeer(ctx context.Context, gc *config.GlobalConfig, peer string, resync bool, result *Result) error {
	peerCache := paths.PeerCacheDir(peer)
	if err := os.MkdirAll(peerCache, 0o755); err != nil {
		return fmt.Errorf("mkdir peer cache: %w", err)
	}
	peerRemote := strings.TrimRight(gc.Remote, "/") + "/" + backends.MachineSuffix + "/" + peer
	slog.Info("pull: copying peer", "peer", peer, "from", peerRemote, "to", peerCache)
	if err := runRcloneSyncStreaming(ctx, peerRemote, peerCache, backends.ListingFlags(gc.NoFastList)...); err != nil {
		return fmt.Errorf("rclone sync from peer: %w", err)
	}
	result.applying = true

	// Register the peer cache as a git remote so we can fetch its objects.
	// Fetching puts the peer's whole history in the store, which is where
	// mergePeer reads every diff and file version from.
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
	return mergePeer(peer, gitexec.ReadRef(paths.StoreDir, "FETCH_HEAD"), resync, result)
}

// mergePeer computes a file delta from the last-seen peer tip to peerHead
// and applies it to the local store. peerHead and its history must already
// be in the store's objects; every diff and file version is read from
// there. peer names the peer in conflict sidecars, the commit message, and
// its last-seen ref. With resync it applies the deletes the peer made
// since the last-seen ref, then the peer's whole tree in content-union
// mode instead of the rest of the diff.
func mergePeer(peer, peerHead string, resync bool, result *Result) error {
	if peerHead == "" {
		// Peer store exists but has no commits yet — nothing to pull.
		return nil
	}

	lastSeen := gitexec.ReadRef(paths.StoreDir, peerRefName(peer))
	if lastSeen == peerHead && !resync {
		return nil
	}

	// The store keeps the objects of every peer tip it has pulled, so it
	// can still read lastSeen after the peer rebuilt its store or rewrote
	// its history (an amend or reset) and dropped that commit from it. A diff from it would then read every
	// file the new history lacks as a peer delete, so lastSeen counts only
	// while it is an ancestor of peerHead. A plain pull fails the peer
	// without it; a resync unions the peer's tree and applies no deletes.
	seenInHistory := false
	if lastSeen != "" {
		base, err := gitexec.MergeBase(paths.StoreDir, lastSeen, peerHead)
		if err != nil && !resync {
			return fmt.Errorf("merge-base: %w", err)
		}
		seenInHistory = err == nil && base == lastSeen
	}
	if lastSeen != "" && !seenInHistory && !resync {
		return fmt.Errorf("diff peer: last-seen commit %s is not in the peer's history (the peer rebuilt its store or rewrote its history); run 'swarf pull --resync' to merge its current tree", lastSeen)
	}

	conflicts, err := applyPeer(peer, peerHead, lastSeen, seenInHistory, resync, result)
	if err != nil {
		return err
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

// applyPeer writes the peer's changes up to peerHead into the store's
// working tree, and returns the conflict sidecars it wrote; mergePeer
// commits the result. lastSeen is the peer's last-seen commit, or "" for
// none, and seenInHistory says whether it is an ancestor of peerHead.
// With no lastSeen, the base is the merge-base of HEAD and peerHead, and
// a peer that shares no history with the store is unioned in with no
// deletes. Every file the add or modify rule writes is recorded in
// result.applied under peer.
func applyPeer(peer, peerHead, lastSeen string, seenInHistory, resync bool, result *Result) ([]string, error) {
	localHead := gitexec.RevParseHEAD(paths.StoreDir)

	// Figure out which "mode" this pull is in. Three cases:
	//   1. Bootstrap  — no local HEAD at all. Treat every peer file as an add.
	//   2. Incremental — lastSeen is known. Diff lastSeen..peerHead.
	//   3. Post-upgrade / first contact — lastSeen empty but local has HEAD.
	//      Try merge-base (recovers linked histories from the old design).
	//      If none, fall back to content-union (adds/modifies, no deletes).
	// A resync forces content-union whatever the ref says: the local store
	// may have lost files since lastSeen, and only the peer's full tree
	// shows them.
	unionMode := resync
	effectiveOld := lastSeen
	if resync {
		effectiveOld = ""
	} else if lastSeen == "" {
		if localHead == "" {
			// Bootstrap: diff from empty tree gives us every file as an add.
			effectiveOld = ""
		} else {
			base, err := gitexec.MergeBase(paths.StoreDir, localHead, peerHead)
			if err != nil {
				return nil, fmt.Errorf("merge-base: %w", err)
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
		entries, err = gitexec.DiffNameStatus(paths.StoreDir, "", peerHead)
		if err != nil {
			return nil, fmt.Errorf("list peer tree: %w", err)
		}
	} else {
		entries, err = gitexec.DiffNameStatus(paths.StoreDir, effectiveOld, peerHead)
		if err != nil {
			return nil, fmt.Errorf("diff peer: %w", err)
		}
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	var conflicts []string

	// A resync can still tell whether the peer changed a file since the
	// last pull, as long as lastSeen is in the peer's history. Without it
	// (no ref yet, or the peer rebuilt its store) union mode falls back to
	// searching history.
	seenOld := ""
	if resync && seenInHistory {
		seenOld = lastSeen
	}

	// A resync still owes the deletes the peer committed since lastSeen:
	// the union below skips deletes, and the ref advances past them, so
	// no later pull would apply them. Apply them as a plain pull would —
	// removing a file only when the local copy matches what the peer had
	// just before deleting it.
	if resync && lastSeen != "" && !seenInHistory {
		// lastSeen is not in the peer's history (its store was rebuilt),
		// so its deletes since then cannot be known.
		slog.Warn("pull: resync cannot list peer deletes since last pull; applying none", "peer", peer, "last_seen", lastSeen)
	}
	if resync && seenInHistory && lastSeen != peerHead {
		pending, err := gitexec.DiffNameStatus(paths.StoreDir, lastSeen, peerHead)
		if err != nil {
			return nil, fmt.Errorf("diff peer deletes since last pull: %w", err)
		}
		for _, e := range pending {
			if e.Status != "D" || isStoreMetadata(e.Path) {
				continue
			}
			c, err := applyDelete(peer, lastSeen, peerHead, e.Path, ts)
			if err != nil {
				return nil, err
			}
			if c != "" {
				conflicts = append(conflicts, c)
			}
		}
	}

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
			c, wrote, err := applyAddOrModify(peer, effectiveOld, seenOld, peerHead, e.Path, ts)
			if err != nil {
				return nil, err
			}
			if wrote {
				if result.applied == nil {
					result.applied = make(map[string]string)
				}
				result.applied[e.Path] = peer
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
			c, err := applyDelete(peer, effectiveOld, peerHead, e.Path, ts)
			if err != nil {
				return nil, err
			}
			if c != "" {
				conflicts = append(conflicts, c)
			}
		}
	}
	return conflicts, nil
}

// applyAddOrModify brings one peer add/modify into the local working tree.
// Returns the path of a conflict sidecar if local diverged, or "" otherwise,
// and whether it wrote the peer's content to the store's copy.
//
// Three-way comparison on modify:
//   - local == peerNew  → already identical, no-op.
//   - local == peerOld  → local is at the version the peer started from;
//     peer simply moved forward, apply cleanly.
//   - otherwise         → both sides changed the same file, conflict sidecar.
//
// oldRef is the ref to use for "the version the peer had before this
// change" — lastSeen or merge-base, whichever the caller resolved. In
// union mode oldRef is empty; seenOld is then the last-seen ref when it
// is known to be in the peer's history (a resync), or "" otherwise.
func applyAddOrModify(peer, oldRef, seenOld, peerHead, rel, ts string) (sidecar string, wrote bool, err error) {
	peerContent, err := gitexec.ShowFile(paths.StoreDir, peerHead, rel)
	if err != nil {
		return "", false, fmt.Errorf("read peer content for %s: %w", rel, err)
	}

	localPath := filepath.Join(paths.StoreDir, rel)
	localContent, readErr := os.ReadFile(localPath)
	if readErr == nil && bytes.Equal(localContent, peerContent) {
		return "", false, nil
	}

	if os.IsNotExist(readErr) {
		// Fresh add. No conflict possible.
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return "", false, fmt.Errorf("mkdir for %s: %w", rel, err)
		}
		if err := os.WriteFile(localPath, peerContent, 0o644); err != nil {
			return "", false, fmt.Errorf("write %s: %w", rel, err)
		}
		return "", true, nil
	}

	// Three-way merge. Check whether local is at some historical state the
	// peer has been through — if so, peer is strictly ahead and we apply
	// their new content cleanly.
	if peerHasVersionMatching(oldRef, peerHead, rel, localContent) {
		if err := os.WriteFile(localPath, peerContent, 0o644); err != nil {
			return "", false, fmt.Errorf("write %s: %w", rel, err)
		}
		return "", true, nil
	}

	// In union mode (no trusted old ref), a local copy edited while the
	// peer left the file alone is an ordinary local edit: keep local with
	// no sidecar, as a plain pull would for a file the peer has not
	// touched. With a known last-seen ref, "left alone" means the peer's
	// content is unchanged since that ref, so a peer that changed the file
	// back to an older version still conflicts. Without one, the best
	// available sign is that the local store's history already had the
	// peer's current content.
	if oldRef == "" {
		if seenOld != "" {
			if peerSeen, err := gitexec.ShowFile(paths.StoreDir, seenOld, rel); err == nil && bytes.Equal(peerSeen, peerContent) {
				return "", false, nil
			}
		} else if localHead := gitexec.RevParseHEAD(paths.StoreDir); localHead != "" &&
			historyHasVersion(paths.StoreDir, localHead, rel, peerContent) {
			return "", false, nil
		}
	}

	// Both sides diverged from every state the peer has been through.
	// Keep local, save peer version as a sidecar.
	sidecar, err = writeConflictSidecar(rel, peer, ts, peerContent)
	return sidecar, false, err
}

// peerHasVersionMatching reports whether the peer's history contains any
// version of `rel` whose content equals `localContent`. When oldRef is
// non-empty that is the version at oldRef or any version the peer had
// after it (see peerWentThrough); when oldRef is empty (unrelated
// histories), we walk the peer's whole log for the file and check each
// commit, so content-identical divergent histories still merge cleanly.
func peerHasVersionMatching(oldRef, peerHead, rel string, localContent []byte) bool {
	if oldRef != "" {
		return peerWentThrough(oldRef, peerHead, rel, localContent)
	}
	// Union-mode path: walk peer history for this file.
	return historyHasVersion(paths.StoreDir, peerHead, rel, localContent)
}

// peerWentThrough reports whether the peer had `rel` with exactly
// content at oldRef or at any commit after it up to peerHead. A local
// copy that matches one of those is a state the peer moved on from, not
// a divergent edit: most often the peer pulled this machine's edit and
// then changed or deleted the file again, so only the later commits
// have this machine's version.
func peerWentThrough(oldRef, peerHead, rel string, content []byte) bool {
	if peerOld, err := gitexec.ShowFile(paths.StoreDir, oldRef, rel); err == nil && bytes.Equal(content, peerOld) {
		return true
	}
	return historyHasVersion(paths.StoreDir, oldRef+".."+peerHead, rel, content)
}

// historyHasVersion reports whether any commit in revs (a commit, whose
// whole history is searched, or a range such as "a..b") in the repo at
// dir has `rel` with exactly `content`.
func historyHasVersion(dir, revs, rel string, content []byte) bool {
	cmd := exec.Command("git", "-C", dir, "log", "--format=%H", revs, "--", rel)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, sha := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if sha == "" {
			continue
		}
		got, err := gitexec.ShowFile(dir, sha, rel)
		if err == nil && bytes.Equal(content, got) {
			return true
		}
	}
	return false
}

// applyDelete removes a file locally if the local copy matches a version
// the peer had between oldRef and deleting it at peerHead (see
// peerWentThrough); otherwise the local content has diverged from every
// version the peer deleted, so we keep ours and leave a sidecar with the
// peer's content at oldRef for reference.
func applyDelete(peer, oldRef, peerHead, rel, ts string) (string, error) {
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
	preDelete, err := gitexec.ShowFile(paths.StoreDir, oldRef, rel)
	if err != nil {
		// Couldn't recover pre-delete content — be conservative, keep local.
		return writeConflictSidecar(rel, peer, ts, nil)
	}

	if peerWentThrough(oldRef, peerHead, rel, localContent) {
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
func runRcloneSyncStreaming(ctx context.Context, src, dst string, flags ...string) error {
	if err := runRcloneSyncOnce(ctx, src, dst, flags...); err == nil {
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
	return runRcloneSyncOnce(ctx, src, dst, flags...)
}

// rcloneWaitDelay bounds how long a killed rclone's leftover pipe
// holders can delay a pull's return (exec.Cmd.WaitDelay).
const rcloneWaitDelay = 2 * time.Second

func runRcloneSyncOnce(ctx context.Context, src, dst string, flags ...string) error {
	args := append([]string{"sync", src, dst, "-v"}, flags...)
	if term.IsTerminal(int(os.Stderr.Fd())) {
		args = append(args, "--progress", "--stats=1s")
	} else {
		args = append(args, "--stats=5s")
	}
	cmd := exec.CommandContext(ctx, "rclone", args...)
	// Once ctx kills rclone, stop waiting on its stderr pipe after a
	// moment: a process it left behind can hold the pipe open for as long
	// as it lives, and a cancelled pull should return promptly.
	cmd.WaitDelay = rcloneWaitDelay
	cmd.Stdout = os.Stderr
	var errBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
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
