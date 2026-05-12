package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests simulate two swarf machines sharing one rclone "remote"
// (a local directory standing in for Google Drive). Each machine gets
// its own XDG dirs, its own machine id, and its own project checkouts.
// The only contact point between them is the shared fakeRemote directory.
//
// The goal is to pin down exactly what happens in every ordering of
// init / edit / push (daemon Sync) / pull across two machines, because
// that's the state space users actually hit.

// fakeRemoteShim writes an rclone stand-in into dir, pointing at
// fakeRemoteRoot. The shim implements lsf / mkdir / sync / copy / about /
// size / lsd with filesystem paths. Remote refs of the form fake:<path>
// resolve to fakeRemoteRoot/<path>.
func fakeRemoteShim(t *testing.T, dir, fakeRemoteRoot string) string {
	t.Helper()
	shim := filepath.Join(dir, "rclone")
	script := `#!/usr/bin/env bash
set -u
FAKE_ROOT="` + fakeRemoteRoot + `"

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
    for entry in "$real"/*; do
      [ -e "$entry" ] || continue
      name=$(basename "$entry")
      if [ "$dirs_only" = "1" ]; then
        [ -d "$entry" ] && echo "$name/"
      else
        if [ -d "$entry" ]; then echo "$name/"; else echo "$name"; fi
      fi
    done | sort
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
    if [ "$cmd" = "sync" ]; then
      rm -rf "$dst"
    fi
    mkdir -p "$dst"
    cp -a "$src/." "$dst/"
    exit 0
    ;;
  about|size)
    echo '{"count":0,"bytes":0}'
    exit 0
    ;;
  lsd|listremotes)
    echo 'fake:'
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
	return shim
}

// machine represents one "host": a project dir, an isolated swarf config/
// data/cache tree, and a path to the swarf binary with that machine's env.
type machine struct {
	t       *testing.T
	id      string
	home    string
	project string
	shimDir string // directory holding the rclone shim on PATH
}

// newMachine sets up a fresh swarf machine pointed at the given shared
// fakeRemote. It creates the project as a git repo, writes a global
// config, and prepares the rclone shim on PATH. Does NOT run 'swarf init'.
func newMachine(t *testing.T, id, fakeRemote string) *machine {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project-"+id)
	os.MkdirAll(project, 0o755)

	mustRun(t, project, "git", "init")
	mustRun(t, project, "git", "config", "user.email", "t@t")
	mustRun(t, project, "git", "config", "user.name", "t")

	// Global config points all machines at the same fake: remote.
	configDir := filepath.Join(home, ".config", "swarf")
	os.MkdirAll(configDir, 0o755)
	configBody := fmt.Sprintf(`[sync]
backend = "rclone"
remote = "fake:store"
debounce = "1s"

[machine]
id = "%s"
`, id)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configBody), 0o644)

	shimDir := t.TempDir()
	fakeRemoteShim(t, shimDir, fakeRemote)

	return &machine{t: t, id: id, home: home, project: project, shimDir: shimDir}
}

// swarf runs `swarf args...` as this machine. The project dir is the cwd.
func (m *machine) swarf(args ...string) (string, error) {
	m.t.Helper()
	return m.swarfIn(m.project, args...)
}

func (m *machine) swarfIn(dir string, args ...string) (string, error) {
	m.t.Helper()
	cmd := exec.Command(swarfBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+m.home,
		"XDG_CONFIG_HOME="+filepath.Join(m.home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(m.home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(m.home, ".cache"),
		"PATH="+m.shimDir+":"+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// write writes content into project/swarf/<rel>. Creates parent dirs.
func (m *machine) write(rel, content string) {
	m.t.Helper()
	full := filepath.Join(m.project, "swarf", rel)
	os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		m.t.Fatal(err)
	}
}

// read returns the content of project/swarf/<rel>. Returns "" and marks
// the test failed if the file doesn't exist.
func (m *machine) read(rel string) string {
	m.t.Helper()
	full := filepath.Join(m.project, "swarf", rel)
	data, err := os.ReadFile(full)
	if err != nil {
		m.t.Errorf("read %s: %v", full, err)
		return ""
	}
	return string(data)
}

// exists reports whether project/swarf/<rel> exists (as a file, symlink, or dir).
func (m *machine) exists(rel string) bool {
	m.t.Helper()
	_, err := os.Lstat(filepath.Join(m.project, "swarf", rel))
	return err == nil
}

// storeHas reports whether ~/.local/share/swarf/<slug>/<rel> exists.
func (m *machine) storeHas(slug, rel string) bool {
	m.t.Helper()
	_, err := os.Stat(filepath.Join(m.home, ".local", "share", "swarf", slug, rel))
	return err == nil
}

// slug returns the project's slug as swarf would compute it.
func (m *machine) slug() string {
	return filepath.Base(m.project)
}

// push runs 'swarf push': mirrors project/swarf/ → store/<slug>/ and
// syncs to the remote. Exactly what the daemon does on each debounce,
// but synchronous so tests don't race.
func (m *machine) push() {
	m.t.Helper()
	out, err := m.swarf("push")
	if err != nil {
		m.t.Fatalf("push: %s\nerr: %v", out, err)
	}
}

// pull runs `swarf pull`.
func (m *machine) pull() {
	m.t.Helper()
	out, err := m.swarf("pull")
	if err != nil {
		m.t.Fatalf("pull failed: %s\nerr: %v", out, err)
	}
}

// init runs `swarf init`. Non-fatal: the doctor portion may exit
// nonzero (remote unreachable etc.) even when the init succeeded.
func (m *machine) init() {
	m.t.Helper()
	out, _ := m.swarf("init")
	// We deliberately don't fail on error: `swarf init` returns nonzero if
	// any doctor check fails (service missing, etc.), but the setup work
	// still happened. Let the test's own assertions pin down what matters.
	_ = out
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %s\n%s", name, args, err, out)
	}
}

// --- The actual tests ---

// Sanity: after init on machine A with no store content, project/swarf/
// is empty and the store has an empty project dir.
func TestMultiMachine_InitEmpty(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)
	a := newMachine(t, "a", fakeRemote)

	a.init()

	if a.exists("anything.txt") {
		t.Error("expected empty project/swarf/")
	}
}

// A edits, A pushes; B inits and should see A's files. This is the
// exact scenario the user hit.
func TestMultiMachine_AEditsPushes_BInits(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("README.md", "from A\n")
	a.write("docs/notes.md", "A's notes\n")
	a.push()

	// Sanity: A's push made its store contents show up on the fake remote.
	if _, err := os.Stat(filepath.Join(fakeRemote, "store", "machines", "a", a.slug(), "README.md")); err != nil {
		t.Fatalf("A's push should have landed README.md on the fake remote: %v", err)
	}

	// B starts up, creates a project with the same slug, pulls, then inits.
	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	out, err := b.swarf("pull")
	if err != nil {
		t.Fatalf("B pull: %s\nerr: %v", out, err)
	}
	// Store on B should have A's files by now.
	if !b.storeHas(a.slug(), "README.md") {
		t.Fatalf("B's store should contain README.md after pull\n--- pull output ---\n%s", out)
	}
	// Pull should also have reverse-mirrored for unregistered projects
	// that match an existing project dir on disk — but B hasn't init'd
	// yet, so reverse-mirror skips. That's expected.
	if b.exists("README.md") {
		t.Fatalf("B's project/swarf/README.md shouldn't exist before init (pull has no drawer yet)")
	}

	// After init, B should see A's files.
	b.init()
	if !b.exists("README.md") {
		t.Fatalf("B's project/swarf/README.md MISSING after init\n--- latest swarf init output ---\n%s", mustSwarf(t, b, "init"))
	}
	if got := b.read("README.md"); got != "from A\n" {
		t.Fatalf("B's README.md: got %q, want %q", got, "from A\n")
	}
	if got := b.read("docs/notes.md"); got != "A's notes\n" {
		t.Fatalf("B's docs/notes.md: got %q, want %q", got, "A's notes\n")
	}
}

// Reverse order: B inits first (empty), then A's content arrives via pull.
func TestMultiMachine_BInitsFirst_ThenPullsA(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("README.md", "from A\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.init() // B has its own empty store for this slug
	b.pull() // now B pulls A's content into store AND reverse-mirrors into project/swarf/
	if !b.exists("README.md") {
		t.Fatal("B's project/swarf/README.md MISSING after init-then-pull — the reverse mirror should have placed it")
	}
}

// A pushes, B pulls then inits; then B edits and pushes; A pulls. A
// should see B's edits. This is the basic round-trip.
func TestMultiMachine_RoundTrip(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "v1 from A\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	if b.read("shared.md") != "v1 from A\n" {
		t.Fatal("B didn't receive A's v1")
	}

	b.write("shared.md", "v2 from B\n")
	b.push()

	a.pull()
	if got := a.read("shared.md"); got != "v2 from B\n" {
		t.Fatalf("A after round-trip pull: got %q, want %q", got, "v2 from B\n")
	}
}

// Conflict: both edit shared.md without seeing each other first.
func TestMultiMachine_ConflictingEdits(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "seed\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()

	a.write("shared.md", "A wins\n")
	a.push()

	b.write("shared.md", "B wins\n")
	b.push()

	// A pulls and should see B's version as a sidecar, A's own as the main file.
	a.pull()
	if got := a.read("shared.md"); got != "A wins\n" {
		t.Fatalf("A's main shared.md should keep A's text, got %q", got)
	}
	sidecars := findSidecars(t, a.project, "shared.md")
	if len(sidecars) == 0 {
		t.Fatal("A should have a .conflict.* sidecar after pull")
	}
}

// Delete propagation: A deletes, B had the file; after B pulls it should be gone.
func TestMultiMachine_DeletePropagates(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("doomed.md", "bye\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	if !b.exists("doomed.md") {
		t.Fatal("B should have doomed.md after initial sync")
	}

	os.Remove(filepath.Join(a.project, "swarf", "doomed.md"))
	a.push()

	out, _ := b.swarf("pull")
	if b.exists("doomed.md") {
		// Collect diagnostics.
		storeList := listTree(b.storeRoot(a.slug()))
		projectList := listTree(filepath.Join(b.project, "swarf"))
		t.Fatalf("B's doomed.md should be gone after pull\n--- pull output ---\n%s\n--- store/%s ---\n%s\n--- project/swarf ---\n%s",
			out, a.slug(), storeList, projectList)
	}
}

// storeRoot returns the path to this machine's store/<slug>/ directory.
func (m *machine) storeRoot(slug string) string {
	return filepath.Join(m.home, ".local", "share", "swarf", slug)
}

// listTree returns a recursive file listing (one per line) rooted at dir.
func listTree(dir string) string {
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, rel)
		return nil
	})
	return strings.Join(out, "\n")
}

// newMachineWithSlug lets us force two machines to share a project slug
// even though their home dirs differ. The slug is derived from the
// project directory's basename.
func newMachineWithSlug(t *testing.T, id, fakeRemote, slug string) *machine {
	t.Helper()
	m := newMachine(t, id, fakeRemote)
	// Rename the project dir so the slug matches.
	newPath := filepath.Join(filepath.Dir(m.project), slug)
	if m.project != newPath {
		if err := os.Rename(m.project, newPath); err != nil {
			t.Fatal(err)
		}
		m.project = newPath
	}
	return m
}

// findSidecars returns the list of .conflict.* filenames for the given
// base path under project/swarf/.
func findSidecars(t *testing.T, projectRoot, base string) []string {
	t.Helper()
	dir := filepath.Join(projectRoot, "swarf", filepath.Dir(base))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	baseName := filepath.Base(base)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), baseName+".conflict.") {
			out = append(out, e.Name())
		}
	}
	return out
}

// mustSwarf runs a swarf command on machine m and returns its combined
// output. Fails the test on any exec error.
func mustSwarf(t *testing.T, m *machine, args ...string) string {
	t.Helper()
	out, err := m.swarf(args...)
	if err != nil {
		t.Fatalf("swarf %v: %v\n%s", args, err, out)
	}
	return out
}

// --- More interleavings ---

// Three machines: A pushes, B pulls+pushes a change, C pulls and must
// see B's content. Covers propagation past the first hop.
func TestMultiMachine_ThreeMachineChain(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("chain.md", "A's initial\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.write("chain.md", "B's update\n")
	b.push()

	c := newMachineWithSlug(t, "c", fakeRemote, a.slug())
	c.pull()
	c.init()
	// C should see B's update. It'll pull from both A and B. B's version
	// is newer in wall-clock terms but that's not what pull reasons about —
	// pull applies each peer's file-level changes in turn. Depending on
	// iteration order we either pick A's then B overwrites, or B's first
	// (no-op for C) then A conflicts. Accept either A-wins-with-sidecar
	// or B-wins-cleanly as long as B's content is reachable.
	if got := c.read("chain.md"); got != "B's update\n" && got != "A's initial\n" {
		t.Fatalf("C should see A or B version, got %q", got)
	}
	if got := c.read("chain.md"); got != "B's update\n" {
		// A-wins path — the sidecar should contain B's version.
		sidecars := findSidecars(t, c.project, "chain.md")
		if len(sidecars) == 0 {
			t.Fatal("if A wins, B's update must appear as a sidecar")
		}
		foundB := false
		for _, s := range sidecars {
			data, _ := os.ReadFile(filepath.Join(c.project, "swarf", s))
			if string(data) == "B's update\n" {
				foundB = true
			}
		}
		if !foundB {
			t.Fatal("B's update not found in any sidecar")
		}
	}
}

// Independent edits on different files: each machine edits a distinct
// file, they push, each pulls; both should end up with both files.
func TestMultiMachine_ParallelIndependentEdits(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("seed.md", "shared seed\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()

	a.write("from_a.md", "A's file\n")
	b.write("from_b.md", "B's file\n")
	a.push()
	b.push()

	a.pull()
	b.pull()

	if !a.exists("from_b.md") {
		t.Fatal("A should have B's file after pulling")
	}
	if !b.exists("from_a.md") {
		t.Fatal("B should have A's file after pulling")
	}
	if a.read("from_b.md") != "B's file\n" {
		t.Fatal("A's copy of B's file has wrong content")
	}
	if b.read("from_a.md") != "A's file\n" {
		t.Fatal("B's copy of A's file has wrong content")
	}
}

// Pull against an empty remote (no peers) should be a no-op.
func TestMultiMachine_PullEmptyRemote(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	// No push, no peers.
	out, err := a.swarf("pull")
	if err != nil {
		t.Fatalf("pull on empty remote should succeed: %s\nerr: %v", out, err)
	}
	if !strings.Contains(out, "No peers found") && !strings.Contains(out, "cleanly") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// Idempotent pull: pulling twice with no new remote changes should be a
// clean no-op each time.
func TestMultiMachine_IdempotentPull(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("x.md", "x\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()

	// Second pull should be a no-op.
	out1, err := b.swarf("pull")
	if err != nil {
		t.Fatalf("pull: %s\nerr: %v", out1, err)
	}
	out2, err := b.swarf("pull")
	if err != nil {
		t.Fatalf("pull: %s\nerr: %v", out2, err)
	}
	if strings.Contains(out2, "conflict") {
		t.Fatalf("second pull should not produce conflicts: %s", out2)
	}
	if b.read("x.md") != "x\n" {
		t.Fatal("x.md content changed during idempotent pull")
	}
}

// Local edit after pull, before next push: the local change should
// survive a subsequent pull that brings nothing new from the peer.
func TestMultiMachine_LocalEditSurvivesPull(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "v1\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()

	b.write("shared.md", "B's local v2\n")
	// B pulls before pushing — nothing new from A, local change must survive.
	b.pull()
	if got := b.read("shared.md"); got != "B's local v2\n" {
		t.Fatalf("B's unpushed local change should survive pull, got %q", got)
	}
}

// Rename on peer (delete + add) should apply cleanly if local hasn't
// touched either path.
func TestMultiMachine_PeerRename(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("old-name.md", "content\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	if !b.exists("old-name.md") {
		t.Fatal("B should have old-name.md after initial pull")
	}

	// A renames the file.
	os.Rename(filepath.Join(a.project, "swarf", "old-name.md"),
		filepath.Join(a.project, "swarf", "new-name.md"))
	a.push()

	b.pull()
	if b.exists("old-name.md") {
		t.Fatal("B should have lost old-name.md after pull")
	}
	if !b.exists("new-name.md") {
		t.Fatal("B should have new-name.md after pull")
	}
	if got := b.read("new-name.md"); got != "content\n" {
		t.Fatalf("wrong content for new-name.md: %q", got)
	}
}

// Same content added on both sides (e.g. two machines both sweep the
// same AGENTS.md) — should NOT produce a conflict because the content
// is identical.
func TestMultiMachine_SameAddNoConflict(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.init()

	// Both write identical content before either has synced.
	a.write("twin.md", "identical\n")
	b.write("twin.md", "identical\n")
	a.push()
	b.push()

	out, err := a.swarf("pull")
	if err != nil {
		t.Fatalf("A pull: %s\nerr: %v", out, err)
	}
	if strings.Contains(out, "conflict") {
		t.Fatalf("A should not see a conflict (same content), got: %s", out)
	}
	sidecars := findSidecars(t, a.project, "twin.md")
	if len(sidecars) != 0 {
		t.Fatalf("no sidecars expected for identical add, got %v", sidecars)
	}
}

// Fresh-machine bootstrap: no prior config or store. 'swarf pull' alone
// (with config pre-written) must create the store and populate it from
// peers.
func TestMultiMachine_FreshMachineBootstrapsViaPull(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("hello.md", "hello from A\n")
	a.push()

	// B: config is pre-written by newMachineWithSlug, but no init yet.
	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	// Pull alone should set up the store and bring hello.md down.
	b.pull()
	if !b.storeHas(a.slug(), "hello.md") {
		t.Fatal("B's store should have hello.md after a bootstrap pull")
	}
	// Project/swarf isn't populated yet (no drawer registered) — that's
	// the 'unregistered' case; user must 'swarf init' next.
	if b.exists("hello.md") {
		t.Fatal("B's project shouldn't have hello.md until init registers the drawer")
	}

	b.init()
	if !b.exists("hello.md") {
		t.Fatal("after init, project should have hello.md")
	}
}

// REGRESSION: the exact scenario the user hit.
// matt-schulkind-3wny6v has a full project with README.md and several
// subdirectories. matt-dev2 pulls (store gets populated), then a daemon
// cycle on matt-dev2 runs forward-mirror from its project/swarf/
// (which is empty, since init hasn't seeded anything locally yet) and
// destructively wipes the store's content.
func TestMultiMachine_EmptyProjectDoesNotWipeStore(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	// A has lots of content.
	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("README.md", "A's README\n")
	a.write("docs/design.md", "design notes\n")
	a.write("src/mod/notes.md", "nested content\n")
	a.push()

	// B does the bare "pull then init"; init has already registered the
	// drawer and seeded project/swarf/ from the store.
	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()

	// Sanity: after pull+init, B has A's content locally.
	if !b.exists("README.md") {
		t.Fatal("setup: B should have README.md after pull+init")
	}

	// SIMULATE: the user's project/swarf/ got cleared (e.g. by a prior
	// buggy daemon cycle, or the user manually running rm). Before the
	// fix, a subsequent push would destructively mirror this empty
	// project into the store, losing A's content forever.
	os.RemoveAll(filepath.Join(b.project, "swarf", "docs"))
	os.RemoveAll(filepath.Join(b.project, "swarf", "src"))
	os.Remove(filepath.Join(b.project, "swarf", "README.md"))

	// Push: forward mirror runs. Pre-fix this wiped the store.
	b.push()

	// The store MUST still have A's content. If forward mirror was
	// destructive we'd lose it; the safe rule is "don't delete store
	// content when project/swarf/ is plainly incomplete."
	if !b.storeHas(a.slug(), "README.md") {
		t.Fatal("REGRESSION: forward mirror wiped README.md out of the store")
	}
	if !b.storeHas(a.slug(), "docs/design.md") {
		t.Fatal("REGRESSION: forward mirror wiped docs/design.md out of the store")
	}
	if !b.storeHas(a.slug(), "src/mod/notes.md") {
		t.Fatal("REGRESSION: forward mirror wiped src/mod/notes.md out of the store")
	}
}

// Repeated push+pull with no changes should be stable — no new commits,
// no new files appearing/disappearing.
func TestMultiMachine_StablePushPullLoop(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("x.md", "x\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()

	// Run five idle pull/push cycles on both sides — nothing should drift.
	for range 5 {
		a.push()
		b.push()
		a.pull()
		b.pull()
	}
	if a.read("x.md") != "x\n" {
		t.Fatal("A's x.md drifted in idle loop")
	}
	if b.read("x.md") != "x\n" {
		t.Fatal("B's x.md drifted in idle loop")
	}
}
