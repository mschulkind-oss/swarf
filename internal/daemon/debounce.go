package daemon

import (
	"sync"
	"time"
)

type Debouncer struct {
	duration time.Duration
	callback func()
	timer    *time.Timer
	mu       sync.Mutex
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
	d.timer = time.AfterFunc(d.duration, d.callback)
}

func (d *Debouncer) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
