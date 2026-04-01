package backends

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

type GitBackend struct{}

func (g *GitBackend) Sync(storePath string) SyncResult {
	slog.Info("sync: staging all changes", "store", storePath)
	gitexec.AddAll(storePath)

	status := gitexec.StatusPorcelain(storePath)
	if strings.TrimSpace(status) == "" {
		slog.Info("sync: no changes to commit")
		return SyncResult{Success: true, Message: "No changes to sync", FilesChanged: 0}
	}

	nFiles := 0
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

	remote := gitexec.RemoteURL(storePath, "")
	if remote != "" {
		slog.Info("sync: pushing to remote", "remote", remote)
		if err := gitexec.Push(storePath, "origin"); err != nil {
			slog.Warn("sync: push failed (will retry later)", "remote", remote, "err", err)
			return SyncResult{Success: true, Message: fmt.Sprintf("Committed %d files locally, push failed", nFiles), FilesChanged: nFiles}
		}
		stampNow(paths.LastPushFile)
		slog.Info("sync: pushed to remote successfully", "remote", remote, "files", nFiles)
	} else {
		slog.Info("sync: no git remote configured, skipping push")
	}

	return SyncResult{Success: true, Message: fmt.Sprintf("Synced %d files", nFiles), FilesChanged: nFiles}
}

func (g *GitBackend) HasChanges(storePath string) bool {
	return strings.TrimSpace(gitexec.StatusPorcelain(storePath)) != ""
}
