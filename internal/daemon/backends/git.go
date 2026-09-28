package backends

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

// GitBackend commits the store and pushes it to this machine's machine
// branch on the store's origin remote.
type GitBackend struct {
	MachineID string
	// TakeOver force-pushes the machine branch even when it holds
	// commits another store with the same machine_id pushed
	// ('swarf push --take-over').
	TakeOver bool
	// loggedRefusal is the push refusal last logged. A daemon whose
	// machine branch another store writes is refused on every cycle
	// until the user acts, so the refusal is logged when it first
	// appears and again only after it clears or changes.
	loggedRefusal string
}

// MachineBranch is the machine branch for machineID: the branch
// machines/<machine_id> on the git remote, which only the machine with
// that machine_id pushes. It is the git counterpart of the rclone
// <remote>/machines/<machine_id>/ subtree (see MachineRemote).
func MachineBranch(machineID string) string {
	return MachineSuffix + "/" + machineID
}

// PushedRef is the store-local ref recording the commit this store last
// pushed to machineID's machine branch. 'swarf status' counts unpushed
// commits against it without asking the remote, and a sync pushes
// whenever HEAD has moved past it, so a push that failed is retried
// even when nothing new was committed.
func PushedRef(machineID string) string {
	return "refs/swarf-pushed/" + machineID
}

func (g *GitBackend) Sync(ctx context.Context, storePath string) SyncResult {
	if g.MachineID == "" {
		g.MachineID = config.EnsureMachineID()
	}
	slog.Info("sync: staging all changes", "store", storePath)
	gitexec.AddAll(storePath)

	nFiles := 0
	status := gitexec.StatusPorcelain(storePath)
	if strings.TrimSpace(status) == "" {
		slog.Info("sync: no changes to commit")
	} else {
		for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
			if strings.TrimSpace(line) != "" {
				slog.Info("sync: staged", "file", strings.TrimSpace(line))
				nFiles++
			}
		}

		s := "s"
		if nFiles == 1 {
			s = ""
		}
		msg := fmt.Sprintf("auto: sync %d file%s", nFiles, s)
		slog.Info("sync: committing", "message", msg)
		if err := gitexec.Commit(storePath, msg); err != nil {
			slog.Error("sync: commit failed", "path", storePath, "err", err)
			return SyncResult{Success: false, Message: "Commit failed: " + err.Error(), FilesChanged: 0}
		}
		stampNow(paths.LastCommitFile)
		slog.Info("sync: committed locally", "files", nFiles)
	}

	head := gitexec.RevParseHEAD(storePath)
	remote := gitexec.RemoteURL(storePath, "")
	switch {
	case head == "":
		return SyncResult{Success: true, Message: "No changes to sync", FilesChanged: 0}
	case remote == "":
		slog.Info("sync: no git remote configured, skipping push")
		return SyncResult{Success: true, Message: fmt.Sprintf("Committed %d files locally", nFiles), FilesChanged: nFiles}
	case !g.TakeOver && gitexec.ReadRef(storePath, PushedRef(g.MachineID)) == head:
		// Already on the machine branch as of the last push.
		return SyncResult{Success: true, Message: "No changes to sync", FilesChanged: nFiles}
	}

	branch := MachineBranch(g.MachineID)
	if g.TakeOver {
		slog.Info("sync: taking over the machine branch", "remote", remote, "branch", branch)
	}
	slog.Info("sync: pushing to remote", "remote", remote, "branch", branch)
	err := gitexec.PushRef(ctx, storePath, "origin", "refs/heads/"+branch, g.TakeOver)
	if errors.Is(err, gitexec.ErrPushRejected) {
		// Never forced: the branch holds commits this store does not
		// have, so another store is writing this machine_id.
		msg := "refusing to push (committed locally only): " + GitBranchMismatch(g.MachineID, remote)
		if msg != g.loggedRefusal {
			slog.Warn("sync: refusing to push", "remote", remote, "branch", branch, "reason", msg)
			g.loggedRefusal = msg
		}
		return SyncResult{Success: false, Message: msg, FilesChanged: nFiles, Logged: true}
	}
	g.loggedRefusal = ""
	if err != nil {
		// Unreachable remote, bad credentials, or a server-side refusal
		// (a hook, receive.denyNonFastForwards) — including of a
		// --take-over force push. Not another store's commits, so no
		// refusal wording; the next sync retries, since HEAD is still
		// past PushedRef.
		slog.Warn("sync: push failed (will retry later)", "remote", remote, "branch", branch, "err", err)
		msg := fmt.Sprintf("push to %s on %s failed (committed locally only; the daemon retries): %v", branch, remote, err)
		return SyncResult{Success: false, Message: msg, FilesChanged: nFiles, Logged: true}
	}
	if err := gitexec.UpdateRef(storePath, PushedRef(g.MachineID), head); err != nil {
		slog.Warn("sync: could not record the pushed commit", "ref", PushedRef(g.MachineID), "err", err)
	}
	stampNow(paths.LastPushFile)
	slog.Info("sync: pushed to remote successfully", "remote", remote, "branch", branch, "files", nFiles)

	return SyncResult{Success: true, Message: fmt.Sprintf("Synced %d files", nFiles), FilesChanged: nFiles, Pushed: true}
}

// GitBranchMismatch is the explanation, with the ways out, for a machine
// branch holding commits this store does not have: a different store
// uses the same machine_id. It is the git counterpart of OwnerMismatch.
//
// 'swarf push --take-over' force-pushes the branch, so the advice is
// explicit that whatever only the other store pushed there is discarded,
// and says how to keep a copy first.
func GitBranchMismatch(machineID, remote string) string {
	branch := MachineBranch(machineID)
	return fmt.Sprintf("branch %s on %s has commits from a different store that also uses machine_id %q.\n"+
		"  If this is a different computer: give it its own id under [machine] id in %s.\n"+
		"  If this computer replaces that one: 'swarf push --take-over' force-pushes this store to %s and discards the commits only the old store pushed there; 'swarf pull' starts a store from that branch only while the store has no commits, so copy out anything you need beforehand (git fetch %s %s).",
		branch, remote, machineID, paths.GlobalConfigTOML, branch, remote, branch)
}

func (g *GitBackend) HasChanges(storePath string) bool {
	return strings.TrimSpace(gitexec.StatusPorcelain(storePath)) != ""
}
