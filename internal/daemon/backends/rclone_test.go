package backends

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestMachineRemote(t *testing.T) {
	tests := []struct {
		remote, id, want string
	}{
		{"gdrive:swarf-store", "laptop", "gdrive:swarf-store/machines/laptop"},
		{"gdrive:swarf-store/", "laptop", "gdrive:swarf-store/machines/laptop"},
		{"local:/var/swarf", "a-b_c", "local:/var/swarf/machines/a-b_c"},
	}
	for _, tt := range tests {
		got := MachineRemote(tt.remote, tt.id)
		if got != tt.want {
			t.Errorf("MachineRemote(%q, %q) = %q, want %q", tt.remote, tt.id, got, tt.want)
		}
	}
}

func TestListingFlags(t *testing.T) {
	if got := ListingFlags(false); len(got) != 1 || got[0] != "--fast-list" {
		t.Errorf("fast listing is the default, got %v", got)
	}
	if got := ListingFlags(true); len(got) != 0 {
		t.Errorf("no_fast_list = true must drop the flag, got %v", got)
	}
}

// TestSyncPushesStoreToMachineSubtree runs a real rclone sync — rclone
// treats a filesystem path as a remote — so the flags we hand it are
// exercised rather than only asserted on. It is also the standing check
// that --fast-list does not break a backend that cannot do recursive
// listing: the local backend ignores it, as any such backend must.
func TestSyncPushesStoreToMachineSubtree(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}
	testutil.InitializedSwarf(t)
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("hello"), 0o644)

	remote := t.TempDir()
	for _, noFastList := range []bool{false, true} {
		b := &RcloneBackend{Remote: remote, MachineID: "thisbox", NoFastList: noFastList}
		res := b.Sync(context.Background(), paths.StoreDir)
		if !res.Success {
			t.Fatalf("no_fast_list=%v: sync failed: %s", noFastList, res.Message)
		}
		landed := filepath.Join(remote, MachineSuffix, "thisbox", "note.md")
		data, err := os.ReadFile(landed)
		if err != nil {
			t.Fatalf("no_fast_list=%v: expected %s on the remote: %v", noFastList, landed, err)
		}
		if string(data) != "hello" {
			t.Fatalf("no_fast_list=%v: got %q on the remote", noFastList, data)
		}
	}
}
