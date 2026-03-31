package backends

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/gitexec"
)

type GitBackend struct{}

func (g *GitBackend) Sync(storePath string) SyncResult {
	gitexec.AddAll(storePath)

	status := gitexec.StatusPorcelain(storePath)
	if strings.TrimSpace(status) == "" {
		return SyncResult{Success: true, Message: "No changes to sync", FilesChanged: 0}
	}

	nFiles := 0
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
		return SyncResult{Success: false, Message: "Commit failed", FilesChanged: 0}
	}

	if gitexec.RemoteURL(storePath, "") != "" {
		if err := gitexec.Push(storePath, "origin"); err != nil {
			slog.Warn("Push failed (will retry later)", "path", storePath)
		}
	}

	return SyncResult{Success: true, Message: fmt.Sprintf("Synced %d files", nFiles), FilesChanged: nFiles}
}

func (g *GitBackend) HasChanges(storePath string) bool {
	return strings.TrimSpace(gitexec.StatusPorcelain(storePath)) != ""
}
