package daemon

import (
	"context"
	"fmt"
	"io"
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

// setupLogging sends the daemon's log to the file under ConfigDir and to
// stderr at the same time.
//
// Stderr is what a supervisor reads: the systemd unit is Type=simple with
// no StandardOutput override, so stderr is the journal, and the launchd
// plist names a StandardErrorPath. Writing only to the file meant
// `journalctl --user -u swarf` and `just logs` showed nothing but
// systemd's own start/stop lines, so a daemon that was syncing normally
// was indistinguishable from one doing nothing at all. The file stays: it
// is what `swarf docs` documents, and it outlives journal rotation.
func setupLogging() {
	os.MkdirAll(paths.ConfigDir, 0o755)
	var out io.Writer = os.Stderr
	if f, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		out = io.MultiWriter(f, os.Stderr)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, nil)))
}

func daemonize() {
	// Re-exec ourselves with --foreground in background
	exe, _ := os.Executable()

	// The detached child must not inherit the launching terminal's stdio.
	// It outlives the shell that started it, and now that its log also
	// goes to stderr it would spray sync chatter across whatever the user
	// does next in that terminal. Nothing supervises this path, so the log
	// file is the only reader, and setupLogging still writes it.
	stdio := devNullOrStd()
	attr := &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: stdio,
	}
	proc, err := os.StartProcess(exe, []string{exe, "daemon", "start", "--foreground"}, attr)
	if err != nil {
		slog.Error("Failed to start daemon", "err", err)
		return
	}
	proc.Release()
}

// devNullOrStd returns the stdio triple for the detached child: /dev/null
// where we can open it, and our own descriptors if we cannot, since
// inheriting a terminal is better than starting no daemon at all.
func devNullOrStd() []*os.File {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return []*os.File{os.Stdin, os.Stdout, os.Stderr}
	}
	return []*os.File{null, null, null}
}
