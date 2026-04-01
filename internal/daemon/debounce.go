package daemon

import (
	"sync"
	"time"
)

// Debouncer coalesces rapid triggers into a single callback invocation
// after a quiet period. The callback is guaranteed to never run concurrently
// with itself — if the timer fires while a previous callback is still running,
// the new invocation waits.
type Debouncer struct {
	duration time.Duration
	callback func()
	timer    *time.Timer
	mu       sync.Mutex
	running  sync.Mutex // held while callback executes
}

func NewDebouncer(duration time.Duration, callback func()) *Debouncer {
	return &Debouncer{duration: duration, callback: callback}
}

func (d *Debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.duration, d.execute)
}

// execute runs the callback with mutual exclusion so two callbacks never
// overlap. If a previous callback is still running, this blocks until it
// finishes then runs.
func (d *Debouncer) execute() {
	d.running.Lock()
	defer d.running.Unlock()
	d.callback()
}

// Flush cancels any pending timer and runs the callback synchronously.
// Used during graceful shutdown to ensure in-flight changes are synced.
func (d *Debouncer) Flush() {
	d.mu.Lock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.mu.Unlock()

	// Run callback under the running lock to serialize with any
	// in-flight execution.
	d.running.Lock()
	defer d.running.Unlock()
	d.callback()
}

func (d *Debouncer) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
