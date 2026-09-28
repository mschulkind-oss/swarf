package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/pull"
)

// pullSchedule decides whether the daemon runs scheduled pulls, from
// [sync] pull_interval and the backend. It returns the interval (0 means
// no scheduled pulls) and, when the setting cannot be honored, a note for
// the daemon to log once at startup. Only the rclone backend pulls on a
// schedule: a git-backend pull does not mirror into projects, and the
// git backend is single-machine.
func pullSchedule(gc *config.GlobalConfig) (time.Duration, string) {
	interval, err := config.ParsePullInterval(gc.PullInterval)
	if err != nil {
		return 0, fmt.Sprintf("[sync] pull_interval %q is not a duration such as \"10m\"; scheduled pull is off", gc.PullInterval)
	}
	if interval <= 0 {
		return 0, ""
	}
	if gc.Backend != "rclone" {
		return 0, fmt.Sprintf("[sync] pull_interval is set, but scheduled pull is not supported with the %s backend; not scheduling it", gc.Backend)
	}
	return interval, ""
}

// startScheduledPulls runs runScheduledPulls in the background until
// ctx ends or stop is called. stop ends the pull in progress, if any,
// and returns once it has.
func startScheduledPulls(ctx context.Context, interval time.Duration, pullOnce func(context.Context) (*pull.Result, error)) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runScheduledPulls(ctx, interval, pullOnce)
	}()
	return func() {
		cancel()
		<-done
	}
}

// runScheduledPulls calls pullOnce every interval until ctx ends. Each
// pull takes the sync lock itself (see pull.Run), so it never overlaps a
// debounced sync cycle or a CLI push or pull; a slow pull delays the next
// tick rather than stacking up behind it.
//
// Each pull's outcome goes to the daemon log: pull reports failed peers,
// conflict sidecars, and unregistered projects on the console, which a
// daemon started without a supervisor sends to /dev/null, and a pull
// whose every peer failed still returns no error.
func runScheduledPulls(ctx context.Context, interval time.Duration, pullOnce func(context.Context) (*pull.Result, error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			slog.Info("scheduled pull: starting")
			result, err := pullOnce(ctx)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("scheduled pull failed", "err", err)
				}
				continue
			}
			logPullResult(result)
		}
	}
}

// logPullResult writes one finished scheduled pull's outcome to the
// daemon log.
func logPullResult(r *pull.Result) {
	if r == nil {
		slog.Info("scheduled pull: done")
		return
	}
	slog.Info("scheduled pull: done", "peers_seen", r.PeersSeen, "peers_merged", r.PeersMerged,
		"conflicts", len(r.ConflictFiles))
	for _, f := range r.PeerFailures {
		slog.Warn("scheduled pull: peer failed", "peer", f.Peer, "err", f.Err)
	}
	if len(r.ConflictFiles) > 0 {
		slog.Warn("scheduled pull: wrote conflict sidecars; see 'swarf docs conflicts'", "files", r.ConflictFiles)
	}
	if len(r.Unregistered) > 0 {
		slog.Warn("scheduled pull: store projects not registered on this machine; run 'swarf init' in each", "projects", r.Unregistered)
	}
}
