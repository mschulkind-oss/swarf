package clone

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	ErrNoConfig      = errors.New("no global config found — run 'swarf init' first")
	ErrNoRemote      = errors.New("no remote configured in global config")
	ErrStoreExists   = errors.New("store already exists — use 'swarf pull' to update")
	ErrUnknownBackend = errors.New("unknown backend")
)

func Run() error {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return ErrNoConfig
	}
	if gc.Remote == "" {
		return ErrNoRemote
	}
	if paths.IsDir(paths.StoreDir) && gitexec.IsRepo(paths.StoreDir) {
		return ErrStoreExists
	}

	switch gc.Backend {
	case "git":
		if err := cloneGit(gc.Remote); err != nil {
			return err
		}
	case "rclone":
		if err := cloneRclone(gc.Remote); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownBackend, gc.Backend)
	}

	listProjects()
	return nil
}

func cloneGit(remote string) error {
	os.RemoveAll(paths.StoreDir)
	if err := gitexec.Clone(remote, paths.StoreDir); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	console.Ok(fmt.Sprintf("Cloned from %s", remote))
	return nil
}

func cloneRclone(remote string) error {
	if _, err := exec.LookPath("rclone"); err != nil {
		return errors.New("rclone is not installed")
	}
	os.MkdirAll(paths.StoreDir, 0o755)
	cmd := exec.Command("rclone", "copy", remote, paths.StoreDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rclone copy: %s", string(out))
	}

	// The remote contains a full git repo (.git/ directory). After copying it
	// down, reconstruct the working tree from the latest commit.
	if gitexec.IsRepo(paths.StoreDir) {
		gitexec.ResetHard(paths.StoreDir)
	}

	console.Ok(fmt.Sprintf("Copied from %s", remote))
	return nil
}

func listProjects() {
	entries, err := os.ReadDir(paths.StoreDir)
	if err != nil {
		return
	}
	var projects []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			projects = append(projects, e.Name())
		}
	}
	if len(projects) > 0 {
		sort.Strings(projects)
		console.Ok(fmt.Sprintf("Cloned store with %d project(s):", len(projects)))
		for _, name := range projects {
			console.Infof("  %s", name)
		}
		console.Info("\nRun 'swarf init' inside each project to link it.")
	} else {
		console.Ok("Cloned store (empty — no projects yet).")
	}
}
