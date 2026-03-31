package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

func DoStart(foreground bool) {
	// Check if already running
	if data, err := os.ReadFile(paths.PIDFile); err == nil {
		var pid int
		if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil {
			if err := syscall.Kill(pid, 0); err == nil {
				return // already running
			}
		}
		os.Remove(paths.PIDFile)
	}

	if foreground {
		setupLogging()
		os.MkdirAll(paths.ConfigDir, 0o755)
		os.WriteFile(paths.PIDFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
		defer os.Remove(paths.PIDFile)

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()
		Run(ctx)
	} else {
		daemonize()
	}
}

func setupLogging() {
	os.MkdirAll(paths.ConfigDir, 0o755)
	f, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
}

func daemonize() {
	// Re-exec ourselves with --foreground in background
	exe, _ := os.Executable()
	attr := &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	proc, err := os.StartProcess(exe, []string{exe, "daemon", "start", "--foreground"}, attr)
	if err != nil {
		slog.Error("Failed to start daemon", "err", err)
		return
	}
	proc.Release()
}
