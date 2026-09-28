package daemon

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/config"
)

// The daemon schedules pulls only when [sync] pull_interval is set and
// the backend is rclone. The git backend says once that it cannot, and
// a value that does not parse is reported rather than guessed at.
func TestPullSchedule(t *testing.T) {
	cases := []struct {
		name, backend, interval string
		want                    time.Duration
		note                    string // substring of the log note; "" means none
	}{
		{"rclone unset", "rclone", "", 0, ""},
		{"rclone zero", "rclone", "0", 0, ""},
		{"rclone set", "rclone", "10m", 10 * time.Minute, ""},
		{"git unset", "git", "", 0, ""},
		{"git set", "git", "10m", 0, "not supported"},
		{"rclone invalid", "rclone", "soon", 0, "pull_interval"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, note := pullSchedule(&config.GlobalConfig{Backend: c.backend, PullInterval: c.interval})
			if got != c.want {
				t.Errorf("interval = %v, want %v", got, c.want)
			}
			if c.note == "" && note != "" {
				t.Errorf("expected no note, got %q", note)
			}
			if c.note != "" && !strings.Contains(note, c.note) {
				t.Errorf("note %q should mention %q", note, c.note)
			}
		})
	}
}

// Scheduled pulls run on the interval until the daemon shuts down, and
// runScheduledPulls returns once they stop.
func TestRunScheduledPulls(t *testing.T) {
	var pulls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runScheduledPulls(ctx, 50*time.Millisecond, func(context.Context) error {
			pulls.Add(1)
			return nil
		})
		close(done)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for pulls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pulls.Load() < 2 {
		t.Fatalf("expected repeated scheduled pulls, got %d", pulls.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled pulls should stop on shutdown")
	}
}
