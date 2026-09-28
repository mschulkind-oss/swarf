package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/pull"
)

// The daemon schedules pulls only when [sync] pull_interval is set, on
// the rclone and git backends alike. A backend that cannot pull says so
// once, and a value that does not parse is reported rather than guessed
// at.
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
		{"git set", "git", "10m", 10 * time.Minute, ""},
		{"unknown backend set", "ftp", "10m", 0, "not supported"},
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
		runScheduledPulls(ctx, 50*time.Millisecond, func(context.Context) (*pull.Result, error) {
			pulls.Add(1)
			return &pull.Result{}, nil
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

// A scheduled pull's outcome reaches the daemon log, which is all a
// daemon started without a supervisor has: when it starts and ends, each
// peer that failed, each conflict sidecar it wrote, and each store
// project with no drawer here. pull reports these on the console, which
// such a daemon sends to /dev/null.
func TestRunScheduledPullsLogsOutcome(t *testing.T) {
	var buf lockedBuffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runScheduledPulls(ctx, 20*time.Millisecond, func(context.Context) (*pull.Result, error) {
			cancel()
			return &pull.Result{
				PeersSeen:     3,
				PeersMerged:   1,
				ConflictFiles: []string{"forms/notes.md.conflict.a.20260101T000000Z"},
				PeerFailures:  []pull.PeerFailure{{Peer: "a", Err: errors.New("token expired")}},
				Unregistered:  []string{"orphan"},
			}, nil
		})
		close(done)
	}()
	<-done

	out := buf.String()
	for _, want := range []string{"scheduled pull: starting", "scheduled pull: done", "peers_merged=1",
		"token expired", "notes.md.conflict.a", "orphan"} {
		if !strings.Contains(out, want) {
			t.Errorf("daemon log should mention %q\n--- log ---\n%s", want, out)
		}
	}
}

// lockedBuffer is a bytes.Buffer safe to write from one goroutine while
// another reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Stopping scheduled pulls ends the pull in progress and waits for it to
// return, so the daemon never exits out from under a pull that is still
// unwinding (releasing the sync lock, mirroring back what it applied).
func TestStopScheduledPullsWaitsForPullInProgress(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool
	stop := startScheduledPulls(context.Background(), 10*time.Millisecond, func(ctx context.Context) (*pull.Result, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		time.Sleep(200 * time.Millisecond) // the pull unwinding
		finished.Store(true)
		return nil, ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no scheduled pull started")
	}

	returned := make(chan struct{})
	go func() { stop(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("stop should end the pull in progress and return")
	}
	if !finished.Load() {
		t.Fatal("stop returned before the pull in progress did")
	}
}
