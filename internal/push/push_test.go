package push

import (
	"context"
	"errors"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

// TestPushGitTakeOverRejected: owner tokens are a rclone-backend
// concept, so --take-over with the git backend is refused rather than
// silently ignored.
func TestPushGitTakeOverRejected(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	if err := Run(context.Background(), Options{TakeOver: true}); !errors.Is(err, ErrTakeOverNeedsRclone) {
		t.Fatalf("expected ErrTakeOverNeedsRclone, got %v", err)
	}
}
