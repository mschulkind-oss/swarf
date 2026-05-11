package pull

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

// installFakeRclone writes a shim that implements enough of rclone for pull
// and clone tests. It treats a remote of the form "fake:<path>" as the local
// filesystem path <path>, so a scratch directory can stand in for Google Drive.
func installFakeRclone(t *testing.T, fakeRoot string) {
	t.Helper()
	dir := t.TempDir()
	shim := filepath.Join(dir, "rclone")
	// The shim maps "fake:SUBPATH" → "$FAKE_ROOT/SUBPATH". Subcommands implemented:
	//   lsf --dirs-only <path>   → list immediate subdirectories, one per line with trailing /
	//   mkdir <path>             → mkdir -p
	//   sync <src> <dst> [-v]    → rsync --delete semantics via cp + rm
	//   copy <src> <dst> [-v]    → cp -a, no delete
	// Errors are reported to stderr and the shim exits non-zero.
	script := `#!/usr/bin/env bash
set -u
FAKE_ROOT="` + fakeRoot + `"

resolve() {
  case "$1" in
    fake:*) printf '%s' "$FAKE_ROOT/${1#fake:}" ;;
    *) printf '%s' "$1" ;;
  esac
}

cmd="$1"
shift
case "$cmd" in
  lsf)
    # args: --dirs-only <path>
    dirs_only=0
    path=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --dirs-only) dirs_only=1 ;;
        -*) ;;
        *) path="$1" ;;
      esac
      shift
    done
    [ -z "$path" ] && exit 2
    real=$(resolve "$path")
    if [ ! -d "$real" ]; then
      echo "directory not found" >&2
      exit 3
    fi
    # Pure-shell directory listing (no find — blocked in some jails).
    for entry in "$real"/*; do
      [ -e "$entry" ] || continue
      name=$(basename "$entry")
      if [ "$dirs_only" = "1" ]; then
        [ -d "$entry" ] && echo "$name/"
      else
        if [ -d "$entry" ]; then echo "$name/"; else echo "$name"; fi
      fi
    done | sort
    # Also list dotfiles so ".git/" shows up for legacy-layout detection.
    for entry in "$real"/.*; do
      [ -e "$entry" ] || continue
      name=$(basename "$entry")
      case "$name" in .|..) continue ;; esac
      if [ "$dirs_only" = "1" ]; then
        [ -d "$entry" ] && echo "$name/"
      else
        if [ -d "$entry" ]; then echo "$name/"; else echo "$name"; fi
      fi
    done | sort
    exit 0
    ;;
  mkdir)
    path=""
    for a in "$@"; do
      case "$a" in -*) ;; *) path="$a" ;; esac
    done
    real=$(resolve "$path")
    mkdir -p "$real"
    exit 0
    ;;
  sync|copy)
    src=""
    dst=""
    for a in "$@"; do
      case "$a" in
        -*) ;;
        *) if [ -z "$src" ]; then src="$a"; else dst="$a"; fi ;;
      esac
    done
    src=$(resolve "$src")
    dst=$(resolve "$dst")
    mkdir -p "$dst"
    if [ "$cmd" = "sync" ]; then
      # Delete files in dst that are not in src (simple rsync-delete behavior).
      if command -v rsync >/dev/null 2>&1; then
        rsync -a --delete "$src/" "$dst/"
      else
        rm -rf "$dst"
        mkdir -p "$dst"
        cp -a "$src/." "$dst/"
      fi
    else
      cp -a "$src/." "$dst/"
    fi
    exit 0
    ;;
  about|size)
    # Minimal stubs to keep unrelated code paths from panicking.
    echo '{"count":0,"bytes":0}'
    exit 0
    ;;
  lsd)
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// setupStore creates an initialized store with the given machine id. Returns
// the store dir. The store has one seed commit so it shares history with peers
// that are cloned from it.
func setupStore(t *testing.T, machineID string) string {
	t.Helper()
	testutil.InitializedSwarf(t)
	runGit(t, paths.StoreDir, "config", "user.email", "t@t")
	runGit(t, paths.StoreDir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(paths.StoreDir, "seed.txt"), []byte("seed\n"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "seed")
	config.WriteGlobalConfig(&config.GlobalConfig{
		Backend:   "rclone",
		Remote:    "fake:store",
		Debounce:  "5s",
		MachineID: machineID,
	})
	return paths.StoreDir
}

// mirrorStoreToRemote copies a store directory into the fake remote under
// machines/<id>/, simulating what a daemon `Sync` would do.
func mirrorStoreToRemote(t *testing.T, storeDir, fakeRoot, machineID string) {
	t.Helper()
	target := filepath.Join(fakeRoot, "store", "machines", machineID)
	os.MkdirAll(target, 0o755)
	if err := exec.Command("rsync", "-a", "--delete", storeDir+"/", target+"/").Run(); err != nil {
		// Fallback for environments without rsync.
		os.RemoveAll(target)
		os.MkdirAll(target, 0o755)
		if err := exec.Command("cp", "-a", storeDir+"/.", target+"/").Run(); err != nil {
			t.Fatalf("mirror: %v", err)
		}
	}
}

func TestPullMultiPeerFastForward(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	// "Remote" machine: a separate store that commits ahead of ours and
	// mirrors itself to fake://store/machines/peer/.
	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "seed.txt"), []byte("seed\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "seed")

	// Our local store with the same root commit (simulated by copying the
	// peer's tree before it diverges).
	setupStore(t, "self")
	// Align history with peer by resetting to peer's HEAD via a throwaway fetch.
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")

	// Peer commits ahead.
	os.WriteFile(filepath.Join(peerStore, "peer.txt"), []byte("peer wrote this\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer add")

	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if res.PeersSeen != 1 || res.PeersMerged != 1 {
		t.Fatalf("expected 1 peer merged, got seen=%d merged=%d", res.PeersSeen, res.PeersMerged)
	}
	if len(res.ConflictFiles) != 0 {
		t.Fatalf("unexpected conflicts: %v", res.ConflictFiles)
	}

	// Peer's file should now be in our store.
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "peer.txt")); err != nil {
		t.Fatalf("peer.txt should exist after fast-forward: %v", err)
	}
}

func TestPullMultiPeerConflict(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	// Two histories that diverge on the same file.
	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("original\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "seed")

	setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")

	// Divergent edits to shared.txt.
	os.WriteFile(filepath.Join(paths.StoreDir, "shared.txt"), []byte("local wrote this\n"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "local edit")

	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("peer wrote this\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer edit")

	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if res.PeersMerged != 1 {
		t.Fatalf("expected 1 peer merged, got %d", res.PeersMerged)
	}
	if len(res.ConflictFiles) == 0 {
		t.Fatal("expected at least one conflict file")
	}
	// Main file keeps our version.
	got, _ := os.ReadFile(filepath.Join(paths.StoreDir, "shared.txt"))
	if !strings.Contains(string(got), "local wrote this") {
		t.Fatalf("main file should keep local content, got: %q", got)
	}
	// Sidecar exists and carries peer content.
	found := false
	for _, c := range res.ConflictFiles {
		if strings.HasPrefix(c, "shared.txt.conflict.peer.") {
			b, _ := os.ReadFile(filepath.Join(paths.StoreDir, c))
			if strings.Contains(string(b), "peer wrote this") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected a peer-content sidecar, got: %v", res.ConflictFiles)
	}

	// The merge commit should be in history.
	if gitexec.RevParseHEAD(paths.StoreDir) == "" {
		t.Fatal("no HEAD after merge")
	}
}

func TestPullLegacyLayoutDetected(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	// Legacy flat layout: .git/ at remote root, no machines/ dir.
	os.MkdirAll(filepath.Join(fakeRoot, "store", ".git"), 0o755)

	setupStore(t, "self")

	if _, err := RunWithResult(); err == nil || !strings.Contains(err.Error(), "legacy flat layout") {
		t.Fatalf("expected legacy-layout error, got %v", err)
	}
}

func TestPullNoPeersYet(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	// Remote exists but has no machines/ directory.
	os.MkdirAll(filepath.Join(fakeRoot, "store"), 0o755)

	setupStore(t, "self")

	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res.PeersSeen != 0 {
		t.Fatalf("expected 0 peers, got %d", res.PeersSeen)
	}
}

func TestPullBootstrapEmptyStore(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	// Peer has content; our local store has no HEAD yet.
	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "hello.txt"), []byte("hi\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "seed")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	// Set up a store dir that is an empty git repo (no commits).
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{
		Backend:   "rclone",
		Remote:    "fake:store",
		Debounce:  "5s",
		MachineID: "self",
	})

	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if res.PeersMerged != 1 {
		t.Fatalf("expected 1 peer merged, got %d", res.PeersMerged)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "hello.txt")); err != nil {
		t.Fatalf("expected bootstrap to restore hello.txt: %v", err)
	}
}

// TestPullPropagatesDelete: peer deletes a file that local still has;
// since local content matches the pre-delete version, pull removes it locally.
func TestPullPropagatesDelete(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "seed.txt"), []byte("seed\n"), 0o644)
	os.WriteFile(filepath.Join(peerStore, "doomed.txt"), []byte("bye\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "seed")

	setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")

	// First pull establishes refs/swarf-peers/peer at the seed commit.
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")
	if _, err := RunWithResult(); err != nil {
		t.Fatalf("initial pull: %v", err)
	}

	// Peer deletes doomed.txt.
	os.Remove(filepath.Join(peerStore, "doomed.txt"))
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "drop doomed")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if len(res.ConflictFiles) != 0 {
		t.Fatalf("expected no conflicts, got %v", res.ConflictFiles)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "doomed.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected doomed.txt removed, stat err = %v", err)
	}
}

// TestPullDeleteVsLocalModify: peer deletes a file we've locally modified.
// Local change wins, peer's pre-delete content lands as a sidecar.
func TestPullDeleteVsLocalModify(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "seed.txt"), []byte("seed\n"), 0o644)
	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("original\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "seed")

	setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")

	// Establish last-seen.
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")
	if _, err := RunWithResult(); err != nil {
		t.Fatalf("initial pull: %v", err)
	}

	// Local modifies shared.txt.
	os.WriteFile(filepath.Join(paths.StoreDir, "shared.txt"), []byte("local changes\n"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "local edit")

	// Peer deletes shared.txt.
	os.Remove(filepath.Join(peerStore, "shared.txt"))
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer delete")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	// Local copy preserved.
	got, err := os.ReadFile(filepath.Join(paths.StoreDir, "shared.txt"))
	if err != nil {
		t.Fatalf("shared.txt should still exist locally: %v", err)
	}
	if !strings.Contains(string(got), "local changes") {
		t.Fatalf("local content should survive; got %q", got)
	}
	// Sidecar with peer's pre-delete content.
	found := false
	for _, c := range res.ConflictFiles {
		if strings.HasPrefix(c, "shared.txt.conflict.peer.") {
			b, _ := os.ReadFile(filepath.Join(paths.StoreDir, c))
			if strings.Contains(string(b), "original") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected sidecar with peer's pre-delete content, got %v", res.ConflictFiles)
	}
}

// TestPullUnrelatedHistories: local and peer share no commits. Without a
// merge-base, the peer's tree unions into local with conflict rules; no
// deletes happen even if a file is absent on one side.
func TestPullUnrelatedHistories(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	// Peer has its own history.
	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "from-peer.txt"), []byte("peer\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer root")

	// Local has a completely separate history.
	setupStore(t, "self")
	os.WriteFile(filepath.Join(paths.StoreDir, "from-local.txt"), []byte("local\n"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "local root")

	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	// Peer file should land.
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "from-peer.txt")); err != nil {
		t.Fatalf("peer file should be unioned in: %v", err)
	}
	// Local file should NOT be deleted just because peer doesn't have it.
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "from-local.txt")); err != nil {
		t.Fatalf("local file must survive unrelated-histories pull: %v", err)
	}
	// seed.txt (from setupStore) may or may not be touched; either way it
	// must still exist since no deletes happen in union mode.
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "seed.txt")); err != nil {
		t.Fatalf("seed.txt should still exist: %v", err)
	}
	_ = res
}

// TestPullPostUpgradeMergeBase: mimic the pre-redesign state (no peer ref)
// where local and peer share a common ancestor. The first pull must pick
// up only the commits after the ancestor — not replay the whole tree.
func TestPullPostUpgradeMergeBase(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("shared\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "common seed")

	setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")

	// Peer adds a new file after the common point.
	os.WriteFile(filepath.Join(peerStore, "new.txt"), []byte("new\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer add")

	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	headBefore := runGitOut(t, paths.StoreDir, "rev-parse", "HEAD")
	res, err := RunWithResult()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "new.txt")); err != nil {
		t.Fatalf("expected new.txt from peer: %v", err)
	}
	// There should be at most one auto-sync commit on top of headBefore,
	// not a replay of the whole tree. Count commits since headBefore.
	count := runGitOut(t, paths.StoreDir, "rev-list", "--count", headBefore+"..HEAD")
	if strings.TrimSpace(count) != "1" {
		t.Fatalf("expected exactly 1 new commit (got %q, conflicts=%v)", count, res.ConflictFiles)
	}
}

func runGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestListPeersMissingMachinesDir(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)
	os.MkdirAll(filepath.Join(fakeRoot, "store"), 0o755)

	peers, err := listPeers("fake:store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("expected empty peers, got %v", peers)
	}
}

func TestListPeersError(t *testing.T) {
	// rclone is unreachable → expect an error other than "directory not found".
	fakeDir := t.TempDir()
	shim := filepath.Join(fakeDir, "rclone")
	// Use a non-"not found" error message so we take the error path rather
	// than the empty-peers case.
	os.WriteFile(shim, []byte("#!/bin/sh\necho 'network is down' >&2\nexit 1\n"), 0o755)
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	_, err := listPeers("fake:store")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected error classification: %v", err)
	}
}
