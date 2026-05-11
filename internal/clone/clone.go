package clone

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	ErrNoConfig       = errors.New("no global config found — run 'swarf init' first")
	ErrNoRemote       = errors.New("no remote configured in global config")
	ErrStoreExists    = errors.New("store already exists — use 'swarf pull' to update")
	ErrUnknownBackend = errors.New("unknown backend")
	ErrNoPeers        = errors.New("no peer machines found on remote — push from another machine first")
	ErrAmbiguousPeer  = errors.New("multiple peers found on remote — pass --from-peer <id> to pick one")
	ErrLegacyLayout   = errors.New("remote is in legacy flat layout — run 'swarf doctor' for migration steps")
)

// Run clones the store from the configured remote. For the rclone backend,
// fromPeer optionally selects which peer to seed from when multiple exist.
// An empty fromPeer auto-selects when there's exactly one peer.
func Run() error {
	return RunWithPeer("")
}

func RunWithPeer(fromPeer string) error {
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
		if err := cloneRclone(gc.Remote, fromPeer); err != nil {
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

func cloneRclone(remote, fromPeer string) error {
	if _, err := exec.LookPath("rclone"); err != nil {
		return errors.New("rclone is not installed")
	}

	peers, err := listRemotePeers(remote)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		if legacy, reason := peekLegacyLayout(remote); legacy {
			return fmt.Errorf("%w (%s)", ErrLegacyLayout, reason)
		}
		return ErrNoPeers
	}

	peer := fromPeer
	if peer == "" {
		if len(peers) == 1 {
			peer = peers[0]
		} else {
			return fmt.Errorf("%w: peers=%s", ErrAmbiguousPeer, strings.Join(peers, ","))
		}
	} else {
		found := false
		for _, p := range peers {
			if p == peer {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("peer %q not found on remote (available: %s)", peer, strings.Join(peers, ", "))
		}
	}

	peerRemote := strings.TrimRight(remote, "/") + "/" + backends.MachineSuffix + "/" + peer
	os.MkdirAll(paths.StoreDir, 0o755)
	cmd := exec.Command("rclone", "copy", peerRemote, paths.StoreDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rclone copy from %s: %s", peerRemote, string(out))
	}

	// The peer's folder is a full git repo (.git/ included). Reconstruct the
	// working tree from the latest commit.
	if gitexec.IsRepo(paths.StoreDir) {
		gitexec.ResetHard(paths.StoreDir)
	}

	console.Ok(fmt.Sprintf("Cloned from peer %q at %s", peer, peerRemote))
	return nil
}

func listRemotePeers(remote string) ([]string, error) {
	machinesPath := strings.TrimRight(remote, "/") + "/" + backends.MachineSuffix
	cmd := exec.Command("rclone", "lsf", "--dirs-only", machinesPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(msg, "directory not found") || strings.Contains(msg, "not found") || strings.Contains(msg, "doesn't exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("rclone lsf %s: %s", machinesPath, strings.TrimSpace(string(out)))
	}
	var peers []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimRight(strings.TrimSpace(line), "/")
		if name == "" {
			continue
		}
		peers = append(peers, name)
	}
	sort.Strings(peers)
	return peers, nil
}

func peekLegacyLayout(remote string) (bool, string) {
	cmd := exec.Command("rclone", "lsf", "--dirs-only", strings.TrimRight(remote, "/"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimRight(strings.TrimSpace(line), "/")
		if name == ".git" {
			return true, "found .git/ at remote root"
		}
	}
	return false, ""
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
