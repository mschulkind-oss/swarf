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
    # Always do a full rm+cp for sync. We deliberately avoid rsync --delete
    # here because on some platforms it diverges from rclone's behavior —
    # specifically around updating a peer cache that already contains a .git/
    # directory from a prior sync, where rsync's same-inode optimizations
    # can leave stale ref packs or loose objects behind and make the next
    # pull see an older HEAD than the peer just pushed.
    if [ "$cmd" = "sync" ]; then
      rm -rf "$dst"
    fi
    mkdir -p "$dst"
    cp -a "$src/." "$dst/"
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
// machines/<id>/, simulating what a daemon `Sync` would do. Always does a
// full wipe-and-copy to match the fake rclone shim's semantics — see the
// sync|copy branch in installFakeRclone for why rsync is avoided.
func mirrorStoreToRemote(t *testing.T, storeDir, fakeRoot, machineID string) {
	t.Helper()
	target := filepath.Join(fakeRoot, "store", "machines", machineID)
	os.RemoveAll(target)
	os.MkdirAll(target, 0o755)
	if err := exec.Command("cp", "-a", storeDir+"/.", target+"/").Run(); err != nil {
		t.Fatalf("mirror: %v", err)
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

	res, err := RunWithResult(Options{})
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

	res, err := RunWithResult(Options{})
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

	if _, err := RunWithResult(Options{}); err == nil || !strings.Contains(err.Error(), "legacy flat layout") {
		t.Fatalf("expected legacy-layout error, got %v", err)
	}
}

func TestPullNoPeersYet(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	// Remote exists but has no machines/ directory.
	os.MkdirAll(filepath.Join(fakeRoot, "store"), 0o755)

	setupStore(t, "self")

	res, err := RunWithResult(Options{})
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

	res, err := RunWithResult(Options{})
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
	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("initial pull: %v", err)
	}

	// Peer deletes doomed.txt.
	os.Remove(filepath.Join(peerStore, "doomed.txt"))
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "drop doomed")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	res, err := RunWithResult(Options{})
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
	if _, err := RunWithResult(Options{}); err != nil {
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

	res, err := RunWithResult(Options{})
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

	res, err := RunWithResult(Options{})
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
	res, err := RunWithResult(Options{})
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

// TestPullReverseMirrorsIntoProject: after pull applies peer changes to
// the store, it should also mirror the store subtree back into the
// registered project's swarf/ directory, so the user sees pulled content
// and conflict sidecars where they actually work.
func TestPullReverseMirrorsIntoProject(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	// Place a file under a per-project subdirectory, matching the store's
	// layout (store/<slug>/file).
	os.MkdirAll(filepath.Join(peerStore, "forms"), 0o755)
	os.WriteFile(filepath.Join(peerStore, "forms", "peer.txt"), []byte("from peer\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer seed")

	repo := setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")

	// Register a drawer whose slug matches the per-project subdir in the
	// store, and create the project's swarf/ dir so the reverse mirror has
	// somewhere to write.
	slug := "forms"
	projectRoot := filepath.Join(repo, "..", "forms-project")
	os.MkdirAll(filepath.Join(projectRoot, "swarf"), 0o755)
	config.RegisterDrawer(slug, projectRoot)

	// Peer adds a new file that should reach the project.
	os.WriteFile(filepath.Join(peerStore, "forms", "new.txt"), []byte("newly added\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "add new.txt")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// Reverse-mirrored into the project?
	got, err := os.ReadFile(filepath.Join(projectRoot, "swarf", "new.txt"))
	if err != nil {
		t.Fatalf("expected new.txt in project swarf/, got: %v", err)
	}
	if string(got) != "newly added\n" {
		t.Fatalf("expected 'newly added\\n', got %q", got)
	}
}

// TestPullFlagsUnregisteredProjects: after pull, any slug in the store
// that has no drawer entry shows up in result/log as "unregistered" so
// the user knows to 'swarf init' it. The mirror itself skips those slugs
// (we don't know where to put their files without a drawer).
func TestPullFlagsUnregisteredProjects(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.MkdirAll(filepath.Join(peerStore, "forms"), 0o755)
	os.WriteFile(filepath.Join(peerStore, "forms", "peer.txt"), []byte("peer\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer seed")

	repo := setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")

	// Note: no drawer registered for 'forms'. The pull must not silently
	// drop its content on the floor.
	_ = repo

	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	// We can't introspect the Result for unregistered slugs directly —
	// the check is that the mirror helper returns them correctly.
	synced, unregistered := mirrorStoreBackToProjects(nil)
	if len(synced) != 0 {
		t.Fatalf("no drawers registered, expected nothing synced; got %v", synced)
	}
	foundForms := false
	for _, s := range unregistered {
		if s == "forms" {
			foundForms = true
		}
	}
	if !foundForms {
		t.Fatalf("expected 'forms' in unregistered, got %v", unregistered)
	}
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

// TestPullResync: after a pull has recorded the peer's HEAD, a plain pull
// with no new peer commit applies nothing, so a file the local store
// lost stays lost. Resync unions the peer's whole tree back in: missing
// files return, a file both sides changed (local content matches no
// version the peer ever had, and the peer's current content is not one
// the local store moved on from) keeps the local content and gets a
// conflict sidecar, and a local-only file is never deleted.
func TestPullResync(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "seed.txt"), []byte("seed\n"), 0o644)
	os.WriteFile(filepath.Join(peerStore, "lost.txt"), []byte("lost\n"), 0o644)
	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("peer\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "seed")

	setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")
	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("initial pull: %v", err)
	}

	// Local store diverges: loses lost.txt, rewrites shared.txt, adds
	// local.txt. The peer does not change.
	os.Remove(filepath.Join(paths.StoreDir, "lost.txt"))
	os.WriteFile(filepath.Join(paths.StoreDir, "shared.txt"), []byte("local\n"), 0o644)
	os.WriteFile(filepath.Join(paths.StoreDir, "local.txt"), []byte("mine\n"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "diverge")

	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("plain pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "lost.txt")); !os.IsNotExist(err) {
		t.Fatalf("a plain pull with no new peer commit should not restore lost.txt (err=%v)", err)
	}

	// The peer now changes shared.txt too, so both sides have diverged.
	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("peer2\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "peer edit")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")

	peerHead := gitexec.RevParseHEAD(peerStore)
	res, err := RunWithResult(Options{Resync: true})
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(paths.StoreDir, "lost.txt")); string(got) != "lost\n" {
		t.Errorf("resync should restore lost.txt, got %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(paths.StoreDir, "shared.txt")); string(got) != "local\n" {
		t.Errorf("resync should keep the diverged local shared.txt, got %q", got)
	}
	if len(res.ConflictFiles) != 1 || !strings.HasPrefix(res.ConflictFiles[0], "shared.txt.conflict.peer.") {
		t.Errorf("expected one conflict sidecar for shared.txt, got %v", res.ConflictFiles)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "local.txt")); err != nil {
		t.Errorf("resync never deletes: local.txt should survive: %v", err)
	}
	if got := gitexec.ReadRef(paths.StoreDir, peerRefName("peer")); got != peerHead {
		t.Errorf("resync should leave the last-seen ref at the peer's HEAD %s, got %s", peerHead, got)
	}
	if status := strings.TrimSpace(gitexec.StatusPorcelain(paths.StoreDir)); status != "" {
		t.Errorf("resync should commit what it applied, store is dirty:\n%s", status)
	}
}

// TestPullResyncAppliesPendingPeerDeletes: when the peer has committed
// since the last pull, resync first applies the deletes in that range as a
// plain pull would, then advances the last-seen ref to the peer's new
// HEAD. Skipping them would move the ref past the deletes for good: no
// later pull would apply them, and the next push would hand the file back
// to a peer that removed it.
func TestPullResyncAppliesPendingPeerDeletes(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "seed.txt"), []byte("seed\n"), 0o644)
	os.WriteFile(filepath.Join(peerStore, "gone.txt"), []byte("gone\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "seed")

	setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")
	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("initial pull: %v", err)
	}

	// Peer moves forward: deletes gone.txt, adds new.txt.
	runGit(t, peerStore, "rm", "-q", "gone.txt")
	os.WriteFile(filepath.Join(peerStore, "new.txt"), []byte("new\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "drop gone, add new")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")
	peerHead := gitexec.RevParseHEAD(peerStore)

	res, err := RunWithResult(Options{Resync: true})
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if len(res.ConflictFiles) != 0 {
		t.Errorf("expected no conflicts, got %v", res.ConflictFiles)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "gone.txt")); !os.IsNotExist(err) {
		t.Errorf("resync should apply the peer's delete of gone.txt since the last pull (stat err=%v)", err)
	}
	if got, _ := os.ReadFile(filepath.Join(paths.StoreDir, "new.txt")); string(got) != "new\n" {
		t.Errorf("resync should bring in new.txt, got %q", got)
	}
	if got := gitexec.ReadRef(paths.StoreDir, peerRefName("peer")); got != peerHead {
		t.Errorf("resync should advance the last-seen ref to the peer's new HEAD %s, got %s", peerHead, got)
	}

	headBefore := gitexec.RevParseHEAD(paths.StoreDir)
	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("follow-up plain pull: %v", err)
	}
	if got := gitexec.RevParseHEAD(paths.StoreDir); got != headBefore {
		t.Errorf("a plain pull after resync should be a no-op, HEAD moved %s -> %s", headBefore, got)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, "gone.txt")); !os.IsNotExist(err) {
		t.Errorf("gone.txt should stay deleted after the follow-up pull (stat err=%v)", err)
	}
}

// TestPullResyncUpdatesOlderPeerVersion: resync compares a local file with
// every version in the peer's history, not only the last-seen one. A local
// copy at an older peer version is replaced with the peer's current content
// and gets no conflict sidecar.
func TestPullResyncUpdatesOlderPeerVersion(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("v1\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "v1")
	os.WriteFile(filepath.Join(peerStore, "shared.txt"), []byte("v2\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "v2")

	setupStore(t, "self")
	runGit(t, paths.StoreDir, "fetch", peerStore)
	runGit(t, paths.StoreDir, "reset", "--hard", "FETCH_HEAD")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")
	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("initial pull: %v", err)
	}

	// Local store goes back to v1, a version the peer had before lastSeen.
	os.WriteFile(filepath.Join(paths.StoreDir, "shared.txt"), []byte("v1\n"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "back to v1")

	res, err := RunWithResult(Options{Resync: true})
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if len(res.ConflictFiles) != 0 {
		t.Errorf("a local copy at an older peer version should not conflict, got %v", res.ConflictFiles)
	}
	if got, _ := os.ReadFile(filepath.Join(paths.StoreDir, "shared.txt")); string(got) != "v2\n" {
		t.Errorf("resync should update shared.txt to the peer's current v2, got %q", got)
	}
}

// TestPullResyncKeepsLocalOnlyEdit: a file the local store edited after
// pulling it, while the peer left it alone, is an ordinary local edit, not
// divergent content. Resync keeps the local copy and writes no conflict
// sidecar, as a plain pull would, and running it again changes nothing.
func TestPullResyncKeepsLocalOnlyEdit(t *testing.T) {
	fakeRoot := t.TempDir()
	installFakeRclone(t, fakeRoot)

	peerTmp := t.TempDir()
	peerStore := filepath.Join(peerTmp, "peer-store")
	os.MkdirAll(peerStore, 0o755)
	runGit(t, peerStore, "init")
	runGit(t, peerStore, "config", "user.email", "t@t")
	runGit(t, peerStore, "config", "user.name", "t")
	os.WriteFile(filepath.Join(peerStore, "notes.txt"), []byte("v1\n"), 0o644)
	runGit(t, peerStore, "add", "-A")
	runGit(t, peerStore, "commit", "-m", "v1")

	setupStore(t, "self")
	mirrorStoreToRemote(t, peerStore, fakeRoot, "peer")
	if _, err := RunWithResult(Options{}); err != nil {
		t.Fatalf("initial pull: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(paths.StoreDir, "notes.txt")); string(got) != "v1\n" {
		t.Fatalf("initial pull should bring in notes.txt, got %q", got)
	}

	// Local store edits notes.txt; the peer does not change.
	os.WriteFile(filepath.Join(paths.StoreDir, "notes.txt"), []byte("v2 local edit\n"), 0o644)
	runGit(t, paths.StoreDir, "add", "-A")
	runGit(t, paths.StoreDir, "commit", "-m", "local edit")

	for i := 0; i < 2; i++ {
		res, err := RunWithResult(Options{Resync: true})
		if err != nil {
			t.Fatalf("resync %d: %v", i, err)
		}
		if len(res.ConflictFiles) != 0 {
			t.Errorf("resync %d: a local-only edit should not conflict, got %v", i, res.ConflictFiles)
		}
		if got, _ := os.ReadFile(filepath.Join(paths.StoreDir, "notes.txt")); string(got) != "v2 local edit\n" {
			t.Errorf("resync %d: should keep the local edit, got %q", i, got)
		}
	}
	if status := strings.TrimSpace(gitexec.StatusPorcelain(paths.StoreDir)); status != "" {
		t.Errorf("store should be clean, got:\n%s", status)
	}
	if out := runGitOut(t, paths.StoreDir, "ls-files"); strings.Contains(out, ".conflict.") {
		t.Errorf("no conflict sidecar should be committed, ls-files:\n%s", out)
	}
}
