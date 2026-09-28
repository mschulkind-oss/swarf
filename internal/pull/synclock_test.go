package pull

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/synclock"
)

// lockedBuffer is a bytes.Buffer safe to write from a pull's goroutine
// while the test reads it.
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

// A pull that finds the sync lock held says so and waits for the holder
// to release it, rather than failing or running alongside it.
func TestPullWaitsForSyncLockWithMessage(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)
	os.MkdirAll(filepath.Join(fakeRoot, "store"), 0o755)
	setupStore(t, "self")

	out := &lockedBuffer{}
	saved := console.Stdout
	console.Stdout = out
	t.Cleanup(func() { console.Stdout = saved })

	release, err := synclock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("hold the sync lock: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), Options{}) }()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), synclock.WaitMessage) {
		if time.Now().After(deadline) {
			release()
			t.Fatalf("pull should print the wait message while the lock is held; output:\n%s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("pull finished while another holder had the sync lock (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pull after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pull did not finish after the sync lock was released")
	}
}

// A pull holds the sync lock until it is done, downloads included, so a
// push or daemon cycle started mid-pull waits for it.
func TestPullHoldsSyncLockWhileDownloading(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)
	setupStore(t, "self")
	newUnrelatedPeer(t, fakeRoot, "peer")
	gate := gateRcloneSync(t, "")

	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), Options{}) }()
	gate.waitStarted(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if release, err := synclock.Acquire(ctx, nil); err == nil {
		release()
		gate.release(t)
		<-done
		t.Fatal("the sync lock was free while the pull was downloading")
	}

	gate.release(t)
	if err := <-done; err != nil {
		t.Fatalf("pull: %v", err)
	}
	release, err := synclock.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatalf("the sync lock should be free once the pull is done: %v", err)
	}
	release()
}
