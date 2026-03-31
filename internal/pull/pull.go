package pull

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	ErrNoConfig      = errors.New("no global config found — run 'swarf init' first")
	ErrNoStore       = errors.New("store does not exist — run 'swarf clone' first")
	ErrNotGitRepo    = errors.New("store is not a git repository")
	ErrUnknownBackend = errors.New("unknown backend")
)

func Run() error {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return ErrNoConfig
	}
	if !paths.IsDir(paths.StoreDir) {
		return ErrNoStore
	}

	switch gc.Backend {
	case "git":
		return pullGit()
	case "rclone":
		return pullRclone(gc.Remote)
	default:
		return fmt.Errorf("%w: %s", ErrUnknownBackend, gc.Backend)
	}
}

func pullGit() error {
	if !gitexec.IsRepo(paths.StoreDir) {
		return ErrNotGitRepo
	}
	if err := gitexec.Pull(paths.StoreDir); err != nil {
		return fmt.Errorf("git pull: %w", err)
	}
	console.Ok("Pulled latest changes.")
	return nil
}

func pullRclone(remote string) error {
	if _, err := exec.LookPath("rclone"); err != nil {
		return errors.New("rclone is not installed")
	}
	cmd := exec.Command("rclone", "copy", remote, paths.StoreDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rclone copy: %s", string(out))
	}
	console.Ok("Pulled latest changes via rclone.")
	return nil
}
