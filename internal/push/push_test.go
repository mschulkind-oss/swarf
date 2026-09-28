package push

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

// TestPushGitTakeOver: with the git backend, --take-over force-pushes
// this machine's machine branch over commits a different store with the
// same machine_id pushed, where a plain push is refused.
func TestPushGitTakeOver(t *testing.T) {
	testutil.InitializedSwarf(t)
	bare := testutil.BareRemote(t)
	other := t.TempDir()
	for _, args := range [][]string{
		{"-C", paths.StoreDir, "remote", "add", "origin", bare},
		{"-C", other, "init"},
		{"-C", other, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "--allow-empty", "-m", "other store"},
		{"-C", other, "push", bare, "HEAD:refs/heads/machines/box"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "box"})

	if err := Run(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "--take-over") {
		t.Fatalf("a plain push over another store's machine branch should be refused naming --take-over, got %v", err)
	}
	if err := Run(context.Background(), Options{TakeOver: true}); err != nil {
		t.Fatalf("push --take-over: %v", err)
	}
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "refs/heads/machines/box").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, head := strings.TrimSpace(string(out)), gitexec.RevParseHEAD(paths.StoreDir); got != head {
		t.Errorf("after --take-over remote machines/box = %q, want the store's HEAD %q", got, head)
	}
}
