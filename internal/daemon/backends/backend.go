package backends

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

type SyncResult struct {
	Success      bool
	Message      string
	FilesChanged int
}

type SyncBackend interface {
	// Sync stages, commits, and pushes the store. The context governs the
	// network portion of the sync (rclone / git push): cancelling it kills
	// the in-flight transfer so the daemon can shut down promptly. Local git
	// operations (add/commit) are fast and run to completion regardless.
	Sync(ctx context.Context, storePath string) SyncResult
	HasChanges(storePath string) bool
}

// stampNow writes the current time to a file for status reporting.
func stampNow(path string) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644)
}

// ReadStamp reads a timestamp previously written by stampNow.
func ReadStamp(path string) (time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, string(data[:len(data)-1]))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
