package pull

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/mirror"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

// Migrating off a legacy branch: every git-backend pull or push fetches
// the remote's legacy branches (branches not under machines/; see
// backends.LegacyRefPrefix), skipping tombstones, and folds each into
// the store so its tip becomes an ancestor of HEAD. Once the machine
// branch has been pushed with that tip, the branch is retired (see
// backends.RetireLegacyBranches).

// fetchLegacyBranches lists the legacy branches on origin that are not
// tombstones, fetches each into the store at backends.LegacyRef, and
// returns their tips by branch name. The store's copy of a branch that
// left the remote or became a tombstone is removed. It also stops the
// store's local branches from tracking origin: a store cloned before
// machine branches tracked the legacy branch, and push now always
// targets the machine branch, so the local branch name no longer
// matters.
func fetchLegacyBranches(ctx context.Context) (map[string]string, error) {
	if err := gitexec.UntrackUpstreams(paths.StoreDir, "origin"); err != nil {
		slog.Warn("legacy: could not stop tracking the legacy branch", "err", err)
	}
	heads, err := gitexec.LsRemoteHeads(ctx, paths.StoreDir, "origin")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("list legacy branches: %w", err)
	}
	wanted := make(map[string]bool)
	var refspecs []string
	for name, sha := range heads {
		if strings.HasPrefix(name, backends.MachineSuffix+"/") {
			continue
		}
		// A tombstone this store already has (it wrote it, or fetched it
		// before) needs no fetch to be recognized.
		if backends.IsTombstone(paths.StoreDir, sha) {
			continue
		}
		wanted[name] = true
		refspecs = append(refspecs, "+refs/heads/"+name+":"+backends.LegacyRef(name))
	}
	known, err := gitexec.ListRefs(paths.StoreDir, backends.LegacyRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("list legacy branch copies: %w", err)
	}
	for name := range known {
		if !wanted[name] {
			gitexec.DeleteRef(paths.StoreDir, backends.LegacyRef(name))
		}
	}
	if len(refspecs) == 0 {
		return nil, nil
	}
	sort.Strings(refspecs)
	if err := gitexec.FetchRefspec(ctx, paths.StoreDir, "origin", refspecs...); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("fetch legacy branches: %w", err)
	}
	tips := make(map[string]string)
	for name := range wanted {
		tip := gitexec.ReadRef(paths.StoreDir, backends.LegacyRef(name))
		if tip == "" {
			continue
		}
		if backends.IsTombstone(paths.StoreDir, tip) {
			gitexec.DeleteRef(paths.StoreDir, backends.LegacyRef(name))
			continue
		}
		tips[name] = tip
	}
	return tips, nil
}

// unfolded returns the names, sorted, of the legacy branches in tips
// whose tip is not yet an ancestor of the store's HEAD.
func unfolded(tips map[string]string) []string {
	head := gitexec.RevParseHEAD(paths.StoreDir)
	var names []string
	for name, tip := range tips {
		if head == "" || !gitexec.IsAncestor(paths.StoreDir, tip, head) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// foldLegacy folds the legacy branch name, at tip, into the store, so
// tip becomes an ancestor of HEAD. Either way it first applies tip as a
// peer's changes — the same file rules and conflict sidecars as
// mergePeer, with the merge-base as the base (every file an add in a
// store with no commits) — so the store's uncommitted files, such as the
// forward mirror's, count as local edits and are kept. Then, with no
// commits in the store or HEAD an ancestor of tip, it fast-forwards the
// store's branch to tip, leaving whatever the store has that tip does
// not staged for the next push to commit. Otherwise it commits the
// result with tip as a second parent. A tip already in HEAD's history is
// left alone, and folded reports false.
func foldLegacy(name, tip string, result *Result) (folded bool, err error) {
	head := gitexec.RevParseHEAD(paths.StoreDir)
	if head != "" && gitexec.IsAncestor(paths.StoreDir, tip, head) {
		return false, nil
	}
	// The branch name labels its conflict sidecars, so a slash in it
	// must not become a directory.
	label := strings.ReplaceAll(name, "/", "-")
	conflicts, err := applyPeer(label, tip, "", false, false, result)
	if err != nil {
		return false, err
	}
	if err := gitexec.AddAll(paths.StoreDir); err != nil {
		return false, fmt.Errorf("add changes: %w", err)
	}
	tree, err := gitexec.WriteTree(paths.StoreDir)
	if err != nil {
		return false, fmt.Errorf("write tree: %w", err)
	}
	msg := fmt.Sprintf("auto: fold in legacy branch %s", name)
	commit := tip
	if head != "" && !gitexec.IsAncestor(paths.StoreDir, head, tip) {
		if commit, err = gitexec.CommitTree(paths.StoreDir, tree, msg, head, tip); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
	}
	// HEAD names the store's current branch, whatever it is called, so
	// this moves that branch (or starts it, in a store with no commits).
	if err := gitexec.UpdateRef(paths.StoreDir, "HEAD", commit); err != nil {
		return false, fmt.Errorf("update HEAD: %w", err)
	}
	result.ConflictFiles = append(result.ConflictFiles, conflicts...)
	return true, nil
}

// foldLegacyBranches folds each of names (from tips) into the store with
// foldLegacy and says so. A branch that fails is reported in result's
// PeerFailures, and the others are still folded.
func foldLegacyBranches(names []string, tips map[string]string, remote string, result *Result) {
	for _, name := range names {
		result.applying = true
		folded, err := foldLegacy(name, tips[name], result)
		if err != nil {
			console.Warn(fmt.Sprintf("legacy branch %s: %v", name, err))
			result.PeerFailures = append(result.PeerFailures, PeerFailure{Peer: "legacy branch " + name, Err: err})
			continue
		}
		if folded {
			console.Ok(fmt.Sprintf("Folded legacy branch %s on %s into the store; it is retired once this machine's branch is pushed.", name, remote))
		}
	}
}

// FoldLegacyBranches is the migration step a git-backend push (and each
// daemon cycle) runs after its forward mirror and before it commits: it
// fetches the remote's legacy branches, folds into the store each one it
// does not have yet, and mirrors the store back into the projects, so
// the push that follows carries the legacy content to the machine
// branch and the projects show it. stamps are the projects' Stamps
// (mirror.TakeStamps), by slug, taken just before the caller's forward
// mirror, which protect the saves made since from the reverse mirror.
// That mirror leaves an active delete hold as it is (see reverseMirror):
// the caller's forward mirror already held or, with --allow-deletes,
// applied the deletes, and only a 'swarf pull' the user chose may
// restore held files. A store with no origin has nothing to fold. skip
// is the [sync] exclude set (mirror.SkipPatterns). The caller holds the
// sync lock.
func FoldLegacyBranches(ctx context.Context, skip mirror.Skip, stamps map[string]mirror.Stamps) error {
	if !gitexec.IsRepo(paths.StoreDir) || !gitexec.RemoteExists(paths.StoreDir, "origin") {
		return nil
	}
	tips, err := fetchLegacyBranches(ctx)
	if err != nil {
		return err
	}
	names := unfolded(tips)
	if len(names) == 0 {
		return nil
	}
	result := &Result{}
	foldLegacyBranches(names, tips, gitexec.RemoteURL(paths.StoreDir, "origin"), result)
	_, _, savedOver := reverseMirror(skip, stamps, result.applied, true)
	conflicts := append(result.ConflictFiles, savedOver...)
	if len(conflicts) > 0 {
		console.Warn(fmt.Sprintf("Folding in the legacy branch left %d conflict file(s):", len(conflicts)))
		for _, f := range conflicts {
			console.Infof("  %s", f)
		}
		console.Hint("See 'swarf docs conflicts' for the full resolution workflow.")
	}
	return nil
}

// RetireLegacyBranches retires each legacy branch this store has folded
// in and pushed to its machine branch (see backends.RetireLegacyBranches)
// and says what it did.
func RetireLegacyBranches(ctx context.Context) {
	if !gitexec.IsRepo(paths.StoreDir) {
		return
	}
	remote := gitexec.RemoteURL(paths.StoreDir, "origin")
	branch := backends.MachineBranch(config.EnsureMachineID())
	for _, r := range backends.RetireLegacyBranches(ctx, paths.StoreDir, config.EnsureMachineID()) {
		switch {
		case r.Err != nil:
			console.Warn(fmt.Sprintf("Could not retire legacy branch %s on %s: %v", r.Branch, remote, r.Err))
			console.Hint("Its content is safe on " + branch + "; 'swarf doctor' says what is left to do.")
		case r.Moved:
			console.Info(fmt.Sprintf("Legacy branch %s on %s moved since it was folded in (a machine still on an older swarf pushed to it); the next pull or push folds in its new commits.", r.Branch, remote))
		case r.Tombstoned:
			console.Ok(fmt.Sprintf("Retired legacy branch %s on %s: the remote refused to delete it, so it now holds a tombstone (%s). Its content is on %s.", r.Branch, remote, backends.TombstoneFile, branch))
		default:
			console.Ok(fmt.Sprintf("Retired legacy branch %s on %s: deleted it; its content is on %s.", r.Branch, remote, branch))
		}
	}
}
