package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

// The daemon runs under systemd as Type=simple with no StandardOutput
// override, and under launchd with StandardErrorPath set — both capture
// stderr. Logging to the file alone left `journalctl --user -u swarf`
// (and the repo's own `just logs`) showing nothing but systemd's start
// and stop lines, on a daemon that had plenty to say about every sync.
func TestSetupLoggingTeesToFileAndStderr(t *testing.T) {
	tmp := t.TempDir()
	origConfigDir, origLogFile := paths.ConfigDir, paths.LogFile
	origLogger, origStderr := slog.Default(), os.Stderr
	paths.ConfigDir = tmp
	paths.LogFile = filepath.Join(tmp, "daemon.log")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = origStderr
		slog.SetDefault(origLogger)
		paths.ConfigDir, paths.LogFile = origConfigDir, origLogFile
	})

	setupLogging()
	slog.Info("sync: rclone sync completed successfully")
	w.Close()

	captured, _ := io.ReadAll(r)
	if !strings.Contains(string(captured), "rclone sync completed successfully") {
		t.Errorf("expected the line on stderr for the supervisor to capture, got %q", captured)
	}

	onDisk, err := os.ReadFile(paths.LogFile)
	if err != nil {
		t.Fatalf("read %s: %v", paths.LogFile, err)
	}
	if !strings.Contains(string(onDisk), "sync: rclone sync completed successfully") {
		t.Errorf("expected the line in %s as well, got %q", paths.LogFile, onDisk)
	}
}
