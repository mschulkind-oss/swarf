package backends

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/gitexec"
)

type RcloneBackend struct {
	Remote string
}

func (r *RcloneBackend) Sync(storePath string) SyncResult {
	// Local git commit for version history
	gitexec.AddAll(storePath)
	status := gitexec.StatusPorcelain(storePath)
	nFiles := 0
	if strings.TrimSpace(status) != "" {
		for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
			if strings.TrimSpace(line) != "" {
				nFiles++
			}
		}
		s := "s"
		if nFiles == 1 {
			s = ""
		}
		if err := gitexec.Commit(storePath, fmt.Sprintf("auto: sync %d file%s", nFiles, s)); err != nil {
			slog.Error("Failed to commit", "path", storePath, "err", err)
		}
	}

	if _, err := exec.LookPath("rclone"); err != nil {
		return SyncResult{Success: false, Message: "rclone not installed", FilesChanged: nFiles}
	}

	// Sync the entire store including .git/ so history is preserved on the remote.
	// The remote is not human-browseable — it's a git repo backup.
	cmd := exec.Command("rclone", "sync", storePath, r.Remote)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("rclone sync failed: %s", strings.TrimSpace(string(out)))
		slog.Warn(msg)
		return SyncResult{Success: false, Message: msg, FilesChanged: nFiles}
	}

	return SyncResult{Success: true, Message: fmt.Sprintf("Synced %d files via rclone", nFiles), FilesChanged: nFiles}
}

func (r *RcloneBackend) HasChanges(_ string) bool {
	return true // rclone can't cheaply diff
}
