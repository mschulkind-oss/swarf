package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mschulkind-oss/swarf/internal/config"
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

// runScheduledPulls calls pullOnce every interval until ctx ends. Each
// pull takes the sync lock itself (see pull.Run), so it never overlaps a
// debounced sync cycle or a CLI push or pull; a slow pull delays the next
// tick rather than stacking up behind it.
func runScheduledPulls(ctx context.Context, interval time.Duration, pullOnce func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := pullOnce(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("scheduled pull failed", "err", err)
			}
		}
	}
}
