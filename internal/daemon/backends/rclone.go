package backends

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

type RcloneBackend struct {
	Remote     string
	MachineID  string
	NoFastList bool
	// TakeOver uploads even when the machine subtree's owner token
	// belongs to a different store ('swarf push --take-over').
	TakeOver    bool
	remoteMkdir bool // true after we've confirmed/created our machine dir
	// loggedRefusal is the owner-check refusal last logged. A daemon
	// whose subtree another store owns is refused on every cycle until
	// the user acts, so the refusal is logged when it first appears and
	// again only after it clears or changes.
	loggedRefusal string
}

// ListingFlags returns the rclone flags a full store sync needs for its
// listing phase.
//
// A store sync stat-checks every file in the store, and without
// --fast-list rclone issues one listing request per directory: ~28k
// round-trips for a 12k-file store, which measured 148s per no-op cycle
// against 10s with the flag on the same remote. Bytes were never the
// bottleneck — dropping 10 GB of packfile out of that store bought 12
// seconds; the flag bought 138.
//
// The cost is memory in the rclone child process, and it scales with the
// number of objects listed rather than their size (tens of MB at 15k
// objects). A remote holding millions of objects can turn it off with
// no_fast_list = true under [sync]. Backends with no recursive listing
// ignore the flag rather than failing on it.
func ListingFlags(noFastList bool) []string {
	if noFastList {
		return nil
	}
	return []string{"--fast-list"}
}

// MachineSuffix is the path inside the rclone remote under which each machine
// writes exclusively. The full push target is "<remote>/<MachineSuffix>/<machine_id>".
// Keeping each machine in its own subtree avoids concurrent-write corruption
// of the store's .git/ directory — the single rule that makes multi-machine
// rclone safe.
const MachineSuffix = "machines"

// MachineRemote returns the rclone remote path that this machine owns, e.g.
// "gdrive:swarf-store/machines/laptop".
func MachineRemote(remote, machineID string) string {
	return strings.TrimRight(remote, "/") + "/" + MachineSuffix + "/" + machineID
}

func (r *RcloneBackend) Sync(ctx context.Context, storePath string) SyncResult {
	if r.MachineID == "" {
		r.MachineID = config.EnsureMachineID()
	}
	target := MachineRemote(r.Remote, r.MachineID)

	slog.Info("sync: staging all changes", "store", storePath, "backend", "rclone", "target", target)

	// Local git commit for version history
	gitexec.AddAll(storePath)
	status := gitexec.StatusPorcelain(storePath)
	nFiles := 0
	if strings.TrimSpace(status) != "" {
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
		slog.Info("sync: committing locally", "message", msg)
		if err := gitexec.Commit(storePath, msg); err != nil {
			slog.Error("sync: commit failed", "path", storePath, "err", err)
		} else {
			stampNow(paths.LastCommitFile)
			slog.Info("sync: committed locally", "files", nFiles)
		}
	} else {
		slog.Info("sync: no new changes to commit")
	}

	if _, err := exec.LookPath("rclone"); err != nil {
		slog.Error("sync: rclone not installed")
		return SyncResult{Success: false, Message: "rclone not installed", FilesChanged: nFiles}
	}

	// Refuse to push into a legacy flat layout (files at the remote root with
	// no machines/ prefix). Silently writing into a flat layout would delete
	// whatever was there — see docs on migration.
	if legacy, reason := hasLegacyLayout(ctx, r.Remote); legacy {
		msg := fmt.Sprintf("remote is in legacy flat layout (%s); see 'swarf doctor' for migration steps", reason)
		slog.Warn("sync: refusing to push", "remote", r.Remote, "reason", reason)
		return SyncResult{Success: false, Message: msg, FilesChanged: nFiles}
	}

	// Refuse to overwrite a machine subtree another store wrote: two
	// machines sharing a machine_id would otherwise take turns wiping each
	// other's upload. The store's owner token (see OwnerTokenFile) rides
	// along with the upload, so an unclaimed subtree becomes ours.
	if msg := r.checkOwner(ctx, storePath, target); msg != "" {
		if msg != r.loggedRefusal {
			slog.Warn("sync: refusing to push", "remote", target, "reason", msg)
			r.loggedRefusal = msg
		}
		return SyncResult{Success: false, Message: msg, FilesChanged: nFiles, Logged: true}
	}
	r.loggedRefusal = ""

	// Create our machine's remote directory on first sync only.
	if !r.remoteMkdir {
		slog.Info("sync: ensuring remote directory exists (first sync)", "remote", target)
		mkdirCmd := exec.CommandContext(ctx, "rclone", "mkdir", target)
		if mkOut, mkErr := mkdirCmd.CombinedOutput(); mkErr != nil {
			slog.Warn("sync: rclone mkdir failed (may be ok)", "remote", target, "err", mkErr, "output", strings.TrimSpace(string(mkOut)))
		}
		r.remoteMkdir = true
	}

	// Sync our store to our machine's folder. Because only this machine writes
	// here, `rclone sync` (destructive) is safe — nothing on the remote could
	// have legitimate content from another writer.
	slog.Info("sync: rclone sync starting", "from", storePath, "to", target)
	args := append([]string{"sync", storePath, target, "-v"}, ListingFlags(r.NoFastList)...)
	cmd := exec.CommandContext(ctx, "rclone", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("rclone sync failed: %s", strings.TrimSpace(string(out)))
		slog.Warn("sync: "+msg, "remote", target)
		return SyncResult{Success: false, Message: msg, FilesChanged: nFiles}
	}
	stampNow(paths.LastPushFile)
	slog.Info("sync: rclone sync completed successfully", "remote", target, "files", nFiles, "output", strings.TrimSpace(string(out)))

	return SyncResult{Success: true, Message: fmt.Sprintf("Synced %d files via rclone", nFiles), FilesChanged: nFiles}
}

// checkOwner compares this store's owner token with the one already in
// the machine subtree target and returns why the upload must not go ahead,
// or "" when it may: the subtree is unclaimed, already ours, or TakeOver
// is set.
func (r *RcloneBackend) checkOwner(ctx context.Context, storePath, target string) string {
	local, err := EnsureOwnerToken(storePath)
	if err != nil {
		return fmt.Sprintf("owner token: %v", err)
	}
	if r.TakeOver {
		slog.Info("sync: taking over the machine subtree", "remote", target)
		return ""
	}
	remote, ok, err := RemoteOwnerToken(ctx, target)
	if err != nil {
		return fmt.Sprintf("could not read the remote owner token, not uploading: %v", err)
	}
	if ok && remote != local {
		return "refusing to upload (committed locally only): " + OwnerMismatch(r.MachineID, target)
	}
	return ""
}

func (r *RcloneBackend) HasChanges(_ string) bool {
	return true // rclone can't cheaply diff
}

// hasLegacyLayout returns (true, reason) when the rclone remote appears to
// already contain a store at its root (i.e. a .git/ directory), which would
// indicate a pre-multi-machine flat layout. A remote that has only a
// machines/ directory, or is entirely empty, returns false.
func hasLegacyLayout(ctx context.Context, remote string) (bool, string) {
	// `rclone lsf` with --dirs-only is cheap and doesn't list files.
	cmd := exec.CommandContext(ctx, "rclone", "lsf", "--dirs-only", strings.TrimRight(remote, "/"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Network/config error: can't tell, treat as not-legacy to avoid
		// blocking a user whose remote is simply unreachable.
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
