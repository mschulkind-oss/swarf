package backends

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/gitexec"
)

// A legacy branch is any branch on a git remote that is not under
// machines/ — in practice the main (or master) branch every machine
// pushed to before each got its own machine branch (see MachineBranch).
// A git-backend pull or push folds each legacy branch into the store,
// and once the machine branch has been pushed with its content, retires
// it: removes it from the remote by deleting it or, when the remote
// refuses the delete, by replacing it with a tombstone.

// LegacyRefPrefix is the store-local namespace where a git-backend pull
// or push fetches each legacy branch: refs/swarf-legacy/<branch> is the
// store's copy of <branch> as of the last fetch, and is removed once the
// branch is retired, becomes a tombstone, or leaves the remote.
const LegacyRefPrefix = "refs/swarf-legacy/"

// LegacyRef is the store's copy of the legacy branch branch.
func LegacyRef(branch string) string {
	return LegacyRefPrefix + branch
}

// TombstoneFile is the one file in a tombstone: a commit with no
// parents that replaces a legacy branch the remote refused to delete,
// saying the store now uses machine branches and that this branch is
// intentionally empty. Its message carries TombstoneTrailer, which is
// how swarf recognizes it (IsTombstone).
const TombstoneFile = "SWARF-BRANCHES.md"

// TombstoneTrailer is the commit-message trailer that marks a tombstone.
const TombstoneTrailer = "Swarf-Tombstone: true"

const tombstoneText = `# This branch is intentionally empty

This remote is a swarf store: an archive, not something meant to be
browsed. Each machine now pushes its store to its own branch,
machines/<machine_id>, and this branch's content was folded into them
before it was replaced with this commit.

A machine still running an older swarf cannot push here any more (the
push is rejected as a non-fast-forward), so nothing it writes is lost.
Upgrade swarf on every machine that uses this remote.
`

const tombstoneMessage = "swarf: retire this branch\n\n" +
	"The store now uses one machine branch per machine (machines/<machine_id>);\n" +
	"this branch was folded into them and is intentionally empty.\n\n" +
	TombstoneTrailer + "\n"

// IsTombstone reports whether commit, which the repository at dir must
// already have, is a tombstone: it has no parents and its message ends
// with TombstoneTrailer.
func IsTombstone(dir, commit string) bool {
	parents, msg, ok := gitexec.CommitInfo(dir, commit)
	if !ok || len(parents) != 0 {
		return false
	}
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	return strings.TrimSpace(lines[len(lines)-1]) == TombstoneTrailer
}

// makeTombstone writes a tombstone commit into the repository at dir,
// without touching any branch, and returns it.
func makeTombstone(dir string) (string, error) {
	blob, err := gitexec.HashBlob(dir, []byte(tombstoneText))
	if err != nil {
		return "", fmt.Errorf("write %s: %w", TombstoneFile, err)
	}
	tree, err := gitexec.MkTreeFile(dir, TombstoneFile, blob)
	if err != nil {
		return "", fmt.Errorf("write tombstone tree: %w", err)
	}
	return gitexec.CommitTree(dir, tree, tombstoneMessage)
}

// Retirement is what RetireLegacyBranches did with one legacy branch.
type Retirement struct {
	Branch string
	// Tombstoned means the remote refused deleting the branch, so it was
	// replaced with a tombstone instead.
	Tombstoned bool
	// Moved means the branch was left alone because it no longer points
	// at the commit the store folded in: something pushed to it since,
	// and the next pull or push folds that in first.
	Moved bool
	// Err is set when the remote refused both deleting the branch and
	// replacing it; the refusal is recorded for RetireRefusal.
	Err error
}

// retireRefusalKey is the store config key recording why the remote
// last refused retiring branch, for 'swarf doctor'.
func retireRefusalKey(branch string) string {
	return "swarf-retire." + branch + ".refused"
}

// RetireRefusal is why the remote last refused retiring the legacy
// branch branch, or "" when it has not.
func RetireRefusal(storePath, branch string) string {
	return gitexec.ConfigGet(storePath, retireRefusalKey(branch))
}

// RetireLegacyBranches retires each legacy branch the store has folded
// in (see LegacyRefPrefix) whose copy is an ancestor of the commit last
// pushed to machineID's machine branch (PushedRef): the machine branch
// then has all of its content, so the remote no longer needs it. The
// branch is deleted, or, when the remote refuses (GitHub refuses
// deleting the default branch), force-pushed to a tombstone. Both
// updates are conditional on the remote branch still pointing at the
// folded-in commit, so a branch that moved in between is left for the
// next pass. It contacts the remote only when there is a branch to
// retire.
func RetireLegacyBranches(ctx context.Context, storePath, machineID string) []Retirement {
	pushed := gitexec.ReadRef(storePath, PushedRef(machineID))
	if pushed == "" {
		return nil
	}
	legacy, err := gitexec.ListRefs(storePath, LegacyRefPrefix)
	if err != nil {
		slog.Warn("retire: could not list legacy branches", "err", err)
		return nil
	}
	var done []Retirement
	for branch, tip := range legacy {
		if ctx.Err() != nil {
			break
		}
		if !gitexec.IsAncestor(storePath, tip, pushed) {
			continue
		}
		r := retireOne(ctx, storePath, branch, tip)
		switch {
		case r.Err != nil:
			reason := strings.Join(strings.Fields(r.Err.Error()), " ")
			if reason != RetireRefusal(storePath, branch) {
				slog.Warn("retire: the remote refused retiring a legacy branch", "branch", branch, "err", reason)
			}
			if err := gitexec.ConfigSet(storePath, retireRefusalKey(branch), reason); err != nil {
				slog.Warn("retire: could not record the refusal", "branch", branch, "err", err)
			}
		default:
			// Retired or moved: either way the copy is stale, and the
			// next pass fetches the branch again if it is still there.
			gitexec.DeleteRef(storePath, LegacyRef(branch))
			gitexec.ConfigUnset(storePath, retireRefusalKey(branch))
			if r.Moved {
				slog.Info("retire: legacy branch moved since it was folded in; leaving it for the next pass", "branch", branch)
			} else {
				slog.Info("retire: retired legacy branch", "branch", branch, "tombstone", r.Tombstoned)
			}
		}
		done = append(done, r)
	}
	return done
}

// retireOne deletes branch on origin while it still points at tip, and
// replaces it with a tombstone when the remote refuses the delete.
func retireOne(ctx context.Context, storePath, branch, tip string) Retirement {
	ref := "refs/heads/" + branch
	delErr := gitexec.PushLease(ctx, storePath, "origin", "", ref, tip)
	if delErr == nil {
		return Retirement{Branch: branch}
	}
	if errors.Is(delErr, gitexec.ErrStaleLease) {
		return Retirement{Branch: branch, Moved: true}
	}
	tomb, err := makeTombstone(storePath)
	if err != nil {
		return Retirement{Branch: branch, Err: fmt.Errorf("delete refused (%v); tombstone: %w", delErr, err)}
	}
	err = gitexec.PushLease(ctx, storePath, "origin", tomb, ref, tip)
	switch {
	case err == nil:
		return Retirement{Branch: branch, Tombstoned: true}
	case errors.Is(err, gitexec.ErrStaleLease):
		return Retirement{Branch: branch, Moved: true}
	default:
		return Retirement{Branch: branch, Err: fmt.Errorf("delete refused: %v; tombstone refused: %w", delErr, err)}
	}
}
