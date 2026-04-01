package backends

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

type RcloneBackend struct {
	Remote string
}

func (r *RcloneBackend) Sync(storePath string) SyncResult {
	slog.Info("sync: staging all changes", "store", storePath, "backend", "rclone", "remote", r.Remote)

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

	// Ensure the remote directory exists before syncing.
	slog.Info("sync: ensuring remote directory exists", "remote", r.Remote)
	mkdirCmd := exec.Command("rclone", "mkdir", r.Remote)
	if mkOut, mkErr := mkdirCmd.CombinedOutput(); mkErr != nil {
		slog.Warn("sync: rclone mkdir failed (may be ok)", "remote", r.Remote, "err", mkErr, "output", strings.TrimSpace(string(mkOut)))
	}

	// Sync the entire store including .git/ so history is preserved on the remote.
	slog.Info("sync: rclone sync starting", "from", storePath, "to", r.Remote)
	cmd := exec.Command("rclone", "sync", storePath, r.Remote, "-v")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("rclone sync failed: %s", strings.TrimSpace(string(out)))
		slog.Warn("sync: "+msg, "remote", r.Remote)
		return SyncResult{Success: false, Message: msg, FilesChanged: nFiles}
	}
	stampNow(paths.LastPushFile)
	slog.Info("sync: rclone sync completed successfully", "remote", r.Remote, "files", nFiles, "output", strings.TrimSpace(string(out)))

	return SyncResult{Success: true, Message: fmt.Sprintf("Synced %d files via rclone", nFiles), FilesChanged: nFiles}
}

func (r *RcloneBackend) HasChanges(_ string) bool {
	return true // rclone can't cheaply diff
}
