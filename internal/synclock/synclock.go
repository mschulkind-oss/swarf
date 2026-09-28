// Package synclock implements the sync lock: an advisory file lock at
// ~/.cache/swarf/sync.lock held for the duration of any push, pull, or
// daemon cycle, so two of them never touch the store at the same time.
//
// The lock is flock(2) on that file. The kernel drops it when the holder
// exits, so a crashed push or a killed daemon never leaves it stuck.
package synclock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

// pollInterval is how often a waiter retries the lock. flock has no
// cancellable blocking form, so waiting is a non-blocking try on a timer,
// which lets a Ctrl-C or daemon shutdown end the wait.
const pollInterval = 100 * time.Millisecond

// WaitMessage is what a CLI command prints when it finds the lock held.
const WaitMessage = "Another swarf push, pull, or daemon cycle holds the sync lock; waiting for it to finish..."

// Acquire takes the sync lock, waiting for as long as another holder has
// it. waiting, if non-nil, is called once, only when the lock is already
// held, before the wait begins. The returned release drops the lock. If
// ctx ends while waiting, Acquire returns ctx's error.
func Acquire(ctx context.Context, waiting func()) (release func(), err error) {
	file := paths.SyncLockFile()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, err
		}
		if waiting != nil {
			waiting()
			waiting = nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
