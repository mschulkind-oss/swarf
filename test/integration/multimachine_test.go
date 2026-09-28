package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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
// fakeRemoteRoot. The shim implements lsf / mkdir / sync / copy / cat /
// about / size / lsd with filesystem paths. Remote refs of the form fake:<path>
// resolve to fakeRemoteRoot/<path>. When FAKE_RCLONE_AFTER_DOWNLOAD is set,
// sync/copy runs it with bash after every copy from a fake: source, and
// FAKE_RCLONE_AFTER_UPLOAD likewise after every copy to a fake: destination.
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
    src_ref="$src"
    dst_ref="$dst"
    src=$(resolve "$src")
    dst=$(resolve "$dst")
    mkdir -p "$dst"

    # Detect path conflicts: any directory under src whose matching path
    # in dst exists as a non-directory. Real rclone hits 'mkdir X: not a
    # directory' in this case; emulate it so pull's self-heal path is
    # exercised by tests.
    conflict=""
    check_conflicts() {
      local base_src="$1" base_dst="$2" entry rel tgt
      for entry in "$base_src"/* "$base_src"/.*; do
        [ -e "$entry" ] || continue
        case "$(basename "$entry")" in .|..) continue ;; esac
        rel="${entry#"$base_src"/}"
        tgt="$base_dst/$rel"
        if [ -d "$entry" ]; then
          if [ -e "$tgt" ] && [ ! -d "$tgt" ]; then
            conflict="$rel"
            return
          fi
          check_conflicts "$entry" "$tgt"
          [ -n "$conflict" ] && return
        fi
      done
    }
    check_conflicts "$src" "$dst"
    if [ -n "$conflict" ]; then
      echo "ERROR : $conflict: Failed to copy: mkdir $dst/$conflict: not a directory" >&2
      exit 1
    fi

    if [ "$cmd" = "sync" ]; then
      case "$dst_ref" in
        fake:*)
          # An upload copies into a staging tree beside the remote and
          # swaps it into place, so a machine pulling this subtree
          # meanwhile never copies a half-written peer.
          stage="$FAKE_ROOT/.staging"
          mkdir -p "$stage"
          tmp=$(mktemp -d "$stage/new.XXXXXX")
          cp -a "$src/." "$tmp/"
          old=$(mktemp -d "$stage/old.XXXXXX")
          mv "$dst" "$old/tree"
          mv "$tmp" "$dst"
          rm -rf "$old"
          ;;
        *)
          rm -rf "$dst"
          mkdir -p "$dst"
          cp -a "$src/." "$dst/"
          ;;
      esac
    else
      cp -a "$src/." "$dst/"
    fi
    # Test hook: a command to run once a download from the remote lands,
    # standing in for whatever the user does while a slow pull is still
    # fetching peers.
    case "$src_ref" in
      fake:*) [ -n "${FAKE_RCLONE_AFTER_DOWNLOAD:-}" ] && bash -c "$FAKE_RCLONE_AFTER_DOWNLOAD" ;;
    esac
    # Test hook: the same for an upload, standing in for a slow push.
    case "$dst_ref" in
      fake:*) [ -n "${FAKE_RCLONE_AFTER_UPLOAD:-}" ] && bash -c "$FAKE_RCLONE_AFTER_UPLOAD" ;;
    esac
    exit 0
    ;;
  cat)
    path=""
    for a in "$@"; do
      case "$a" in -*) ;; *) path="$a" ;; esac
    done
    real=$(resolve "$path")
    if [ ! -f "$real" ]; then
      echo "directory not found" >&2
      exit 3
    fi
    cat "$real"
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
	shimDir string   // directory holding the rclone shim on PATH
	env     []string // extra KEY=VALUE entries for every swarf run
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

// newGitMachine is newMachine for the git backend: its store's remote is
// bare, a local bare repository (see newBareRemote) standing in for a
// hosted git remote. The rclone shim is still on PATH but unused.
func newGitMachine(t *testing.T, id, bare string) *machine {
	t.Helper()
	m := newMachine(t, id, t.TempDir())
	m.setSyncConfig(fmt.Sprintf("backend = \"git\"\nremote = %q\ndebounce = \"1s\"\n", bare))
	return m
}

// newBareRemote creates an empty bare git repository to serve as the
// shared git remote.
func newBareRemote(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "remote.git")
	mustRun(t, "", "git", "init", "--bare", bare)
	return bare
}

// bareRef returns the SHA ref points at in the bare repository, or "".
func bareRef(bare, ref string) string {
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "--verify", "--quiet", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// bareBranches lists the bare repository's branches, one per line.
func bareBranches(t *testing.T, bare string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", bare, "for-each-ref", "--format=%(refname)", "refs/heads/").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// storeHEAD returns the commit this machine's store is at.
func (m *machine) storeHEAD() string {
	m.t.Helper()
	out, err := exec.Command("git", "-C", filepath.Join(m.home, ".local", "share", "swarf"), "rev-parse", "HEAD").Output()
	if err != nil {
		m.t.Fatalf("store HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// swarf runs `swarf args...` as this machine. The project dir is the cwd.
func (m *machine) swarf(args ...string) (string, error) {
	m.t.Helper()
	return m.swarfIn(m.project, args...)
}

func (m *machine) swarfIn(dir string, args ...string) (string, error) {
	m.t.Helper()
	out, err := m.command(dir, args...).CombinedOutput()
	return string(out), err
}

// command is `swarf args...` run as this machine from dir, not started.
func (m *machine) command(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command(swarfBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+m.home,
		"XDG_CONFIG_HOME="+filepath.Join(m.home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(m.home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(m.home, ".cache"),
		"PATH="+m.shimDir+":"+os.Getenv("PATH"),
	)
	cmd.Env = append(cmd.Env, m.env...)
	return cmd
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

// REGRESSION: if the peer cache on disk has a stale layout (a file at
// a path that the incoming sync wants to be a directory), rclone fails
// with 'not a directory'. Pull should detect this, wipe the cache, and
// retry cleanly instead of leaving the user with a perpetually-broken
// sync.
func TestMultiMachine_StalePeerCacheSelfHeals(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	// A pushes a nested directory structure.
	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("deep/nested/file.md", "deep content\n")
	a.push()

	// B comes online. Before any pull, plant a stale peer cache that has
	// a FILE where one of the dirs should be — simulating a leftover
	// from an older layout.
	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	peerCache := filepath.Join(b.home, ".cache", "swarf", "peers", "a")
	os.MkdirAll(filepath.Join(peerCache, "deep"), 0o755)
	// 'nested' should be a directory but exists as a file.
	os.WriteFile(filepath.Join(peerCache, "deep", "nested"), []byte("stale\n"), 0o644)

	// Pull must heal the cache and complete successfully.
	out, err := b.swarf("pull")
	if err != nil {
		t.Fatalf("pull should self-heal stale peer cache: %s\nerr: %v", out, err)
	}

	// After pull, B's store should have A's file.
	if !b.storeHas(a.slug(), "deep/nested/file.md") {
		t.Fatalf("store missing A's file after self-healed pull\n%s", out)
	}
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

// A file saved while pull is still downloading peers — after pull's
// forward mirror, before its reverse mirror — has never been synced, so
// the reverse mirror must leave it alone even though the store lacks it.
// Deletes a peer made in the same pull still land.
func TestMultiMachine_FileSavedDuringPullSurvives(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "v1\n")
	a.write("doomed.md", "bye\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.pull() // records B's project in the manifest

	os.Remove(filepath.Join(a.project, "swarf", "doomed.md"))
	a.push()

	saved := filepath.Join(b.project, "swarf", "saved-mid-pull.md")
	b.env = []string{"FAKE_RCLONE_AFTER_DOWNLOAD=printf 'new work\\n' > '" + saved + "'"}
	out := mustSwarf(t, b, "pull")
	b.env = nil

	if got := b.read("saved-mid-pull.md"); got != "new work\n" {
		t.Fatalf("a file saved during pull should survive it, got %q\n--- pull output ---\n%s", got, out)
	}
	if b.exists("doomed.md") {
		t.Fatalf("A's delete should still reach B's project\n--- pull output ---\n%s", out)
	}
	if got := b.read("shared.md"); got != "v1\n" {
		t.Fatalf("shared.md: got %q, want %q", got, "v1\n")
	}

	// The next push carries the saved file to the store.
	b.push()
	if !b.storeHas(a.slug(), "saved-mid-pull.md") {
		t.Fatal("B's push should copy the file saved during pull into the store")
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

// wipeProject empties project/swarf/ of the given files while leaving
// the directory itself in place: the Scenario 6 shape, where something
// outside swarf removed the files between passes.
func (m *machine) wipeProject(rels ...string) {
	m.t.Helper()
	for _, rel := range rels {
		if err := os.RemoveAll(filepath.Join(m.project, "swarf", rel)); err != nil {
			m.t.Fatal(err)
		}
	}
}

// holdMarker returns the path of this machine's hold marker for slug.
func (m *machine) holdMarker(slug string) string {
	return filepath.Join(m.home, ".cache", "swarf", "delete-holds", slug+".json")
}

// Scenario 6 (Sync Scenarios): B's manifest already lists every file,
// then the files vanish from B's project between passes. The forward
// mirror used to read that as "the user deleted all of them" and pruned
// the store, which the next upload carried to the remote. With the
// delete hold the store keeps every file, the hold is visible in
// status, and a pull puts the files back.
func TestMultiMachine_WipedProjectHoldsDeletes(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	files := []string{"README.md", "docs/design.md", "src/mod/notes.md"}
	for _, f := range files {
		a.write(f, "A's "+f+"\n")
	}
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	// This push records every file in B's manifest: the populated
	// manifest the earlier empty-project test never reached.
	b.push()

	b.wipeProject("README.md", "docs", "src")
	out := mustSwarf(t, b, "push")

	for _, f := range files {
		if !b.storeHas(a.slug(), f) {
			t.Errorf("store lost %s after a push from a wiped project\n--- push output ---\n%s", f, out)
		}
		remote := filepath.Join(fakeRemote, "store", "machines", "b", a.slug(), f)
		if _, err := os.Stat(remote); err != nil {
			t.Errorf("B's remote subtree lost %s", f)
		}
	}
	if !strings.Contains(out, "--allow-deletes") {
		t.Errorf("push should explain the hold and how to confirm it, got:\n%s", out)
	}
	if _, err := os.Stat(b.holdMarker(a.slug())); err != nil {
		t.Fatalf("expected a hold marker after the held push: %v", err)
	}

	status := mustSwarf(t, b, "status")
	if !strings.Contains(status, "delete hold") || !strings.Contains(status, "swarf pull") {
		t.Errorf("status should show the hold and the ways out, got:\n%s", status)
	}

	// The first way out: pull copies the files back from the store, and
	// the hold is gone because nothing is missing any more.
	b.pull()
	for _, f := range files {
		if !b.exists(f) {
			t.Errorf("pull should have restored %s into the project", f)
		}
	}
	if _, err := os.Stat(b.holdMarker(a.slug())); !os.IsNotExist(err) {
		t.Errorf("hold marker should be removed once the files are back (err=%v)", err)
	}
	b.push()
	if _, err := os.Stat(b.holdMarker(a.slug())); !os.IsNotExist(err) {
		t.Errorf("a push after the restore must not hold again (err=%v)", err)
	}
}

// The second way out of a delete hold: the user really did mean to
// delete everything, and says so with --allow-deletes.
func TestMultiMachine_AllowDeletesAppliesHeldDeletes(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("one.md", "1\n")
	a.write("two.md", "2\n")
	a.push()
	a.push() // second pass: the manifest now lists both files

	a.wipeProject("one.md", "two.md")
	a.push()
	if !a.storeHas(a.slug(), "one.md") || !a.storeHas(a.slug(), "two.md") {
		t.Fatal("setup: a plain push should have held the deletes")
	}

	mustSwarf(t, a, "push", "--allow-deletes")
	if a.storeHas(a.slug(), "one.md") || a.storeHas(a.slug(), "two.md") {
		t.Error("--allow-deletes should apply the held deletes to the store")
	}
	if _, err := os.Stat(a.holdMarker(a.slug())); !os.IsNotExist(err) {
		t.Errorf("hold marker should be removed after the confirmed pass (err=%v)", err)
	}
	remote := filepath.Join(fakeRemote, "store", "machines", "a", a.slug(), "one.md")
	if _, err := os.Stat(remote); !os.IsNotExist(err) {
		t.Error("the confirmed delete should reach the remote")
	}

	// And the hold does not come back: the manifest no longer lists them.
	out := mustSwarf(t, a, "push")
	if strings.Contains(out, "--allow-deletes") {
		t.Errorf("no hold expected after confirming, got:\n%s", out)
	}
}

// A delete hold can also end because the files are gone everywhere: B's
// project lost them, and a peer deleted them on purpose. B's pull merges
// the peer's deletes into the store, so the reverse mirror has nothing
// to copy back. The hold is over, but pull must not claim it restored
// the files.
func TestMultiMachine_HoldClearedByPeerDeleteIsNotRestored(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	files := []string{"one.md", "two.md", "three.md"}
	for _, f := range files {
		a.write(f, "A's "+f+"\n")
	}
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.push()

	// A deletes the files on purpose and confirms it.
	a.push() // the manifest now lists every file
	a.wipeProject(files...)
	mustSwarf(t, a, "push", "--allow-deletes")

	// B loses them too, and its push holds.
	b.wipeProject(files...)
	b.push()
	if _, err := os.Stat(b.holdMarker(a.slug())); err != nil {
		t.Fatalf("setup: expected B's push to hold: %v", err)
	}

	out := mustSwarf(t, b, "pull")
	for _, f := range files {
		if b.exists(f) {
			t.Errorf("%s was deleted on A and should stay gone on B", f)
		}
	}
	if _, err := os.Stat(b.holdMarker(a.slug())); !os.IsNotExist(err) {
		t.Errorf("hold marker should be removed once no held file is left in the store (err=%v)", err)
	}
	if strings.Contains(out, "restored") {
		t.Errorf("pull restored nothing and must not say it did, got:\n%s", out)
	}
	if !strings.Contains(out, "hold cleared") {
		t.Errorf("pull should say the hold was cleared, got:\n%s", out)
	}
}

// A machine whose store already lost files (here B confirmed a mass
// delete; before the delete hold, a wiped project did the same) never
// gets them back from a plain pull: A has not changed, so there is no
// new peer commit to apply. `swarf pull --resync` unions every file in
// A's current tree back into B's store and project, and deletes
// nothing.
func TestMultiMachine_ResyncRestoresDivergedStore(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	files := []string{"README.md", "docs/design.md", "src/mod/notes.md"}
	for _, f := range files {
		a.write(f, "A's "+f+"\n")
	}
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.push() // the manifest now lists every file

	// B's store loses the files and commits the loss; A stays unchanged.
	b.wipeProject("README.md", "docs", "src")
	mustSwarf(t, b, "push", "--allow-deletes")
	for _, f := range files {
		if b.storeHas(a.slug(), f) {
			t.Fatalf("setup: B's store should have lost %s", f)
		}
	}
	b.write("b-only.md", "B's own file\n")
	b.push()

	// A plain pull has nothing new from A to apply, so nothing comes back.
	out := mustSwarf(t, b, "pull")
	for _, f := range files {
		if b.storeHas(a.slug(), f) || b.exists(f) {
			t.Fatalf("a plain pull is not expected to restore %s\n--- pull output ---\n%s", f, out)
		}
	}

	out = mustSwarf(t, b, "pull", "--resync")
	for _, f := range files {
		if !b.storeHas(a.slug(), f) {
			t.Errorf("--resync should restore %s into B's store\n--- pull output ---\n%s", f, out)
		}
		if got := b.read(f); got != "A's "+f+"\n" {
			t.Errorf("--resync should repopulate B's project with %s, got %q", f, got)
		}
	}
	if got := b.read("b-only.md"); got != "B's own file\n" {
		t.Errorf("--resync never deletes: B's own file should survive, got %q", got)
	}
	if sidecars := findSidecars(t, b.project, "README.md"); len(sidecars) != 0 {
		t.Errorf("restoring a missing file is not a conflict, got sidecars %v", sidecars)
	}

	// The restored files go back up on B's next push.
	b.push()
	remote := filepath.Join(fakeRemote, "store", "machines", "b", a.slug(), "README.md")
	if _, err := os.Stat(remote); err != nil {
		t.Errorf("B's push after --resync should upload the restored files: %v", err)
	}
}

// Scenario 5: a push started while a pull is running must not touch the
// store until the pull is done. The push finds the sync lock held, says
// so, waits, and then runs to completion.
func TestMultiMachine_PushWaitsForRunningPull(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "v1\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.push()

	// While B's pull is downloading A, start a push in the background
	// (all descriptors redirected, so the pull does not wait on its
	// output), give it time to reach the lock, and record whether it
	// finished while the pull was still running.
	work := t.TempDir()
	pushOut := filepath.Join(work, "push.out")
	early := filepath.Join(work, "finished-early")
	hook := filepath.Join(work, "hook.sh")
	script := fmt.Sprintf(`
( '%[1]s' push > '%[2]s' 2>&1 < /dev/null; echo "EXIT $?" >> '%[2]s' ) > /dev/null 2>&1 < /dev/null &
for i in $(seq 50); do grep -q 'sync lock' '%[2]s' 2>/dev/null && break; sleep 0.1; done
sleep 0.5
if grep -q EXIT '%[2]s'; then touch '%[3]s'; fi
`, swarfBin, pushOut, early)
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b.env = []string{"FAKE_RCLONE_AFTER_DOWNLOAD=bash '" + hook + "'"}
	pullOut := mustSwarf(t, b, "pull")
	b.env = nil

	var got string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(pushOut)
		if got = string(data); strings.Contains(got, "EXIT") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(got, "EXIT 0") {
		t.Fatalf("the waiting push should finish once the pull is done\n--- push output ---\n%s\n--- pull output ---\n%s", got, pullOut)
	}
	if _, err := os.Stat(early); err == nil {
		t.Fatalf("the push ran while the pull held the store\n--- push output ---\n%s", got)
	}
	if !strings.Contains(got, "sync lock") {
		t.Fatalf("the push should say it is waiting for the sync lock\n--- push output ---\n%s", got)
	}
}

// Scenario 5, the other way round: a pull started while a push is
// uploading must not touch the store until the push is done. The pull
// finds the sync lock held, says so, waits, and then runs to completion.
func TestMultiMachine_PullWaitsForRunningPush(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "v1\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.write("from-b.md", "b's edit\n")

	// While B's push is uploading, start a pull in the background (all
	// descriptors redirected, so the push does not wait on its output),
	// give it time to reach the lock, and record whether it finished
	// while the push was still running.
	work := t.TempDir()
	pullOut := filepath.Join(work, "pull.out")
	early := filepath.Join(work, "finished-early")
	hook := filepath.Join(work, "hook.sh")
	script := fmt.Sprintf(`
( '%[1]s' pull > '%[2]s' 2>&1 < /dev/null; echo "EXIT $?" >> '%[2]s' ) > /dev/null 2>&1 < /dev/null &
for i in $(seq 50); do grep -q 'sync lock' '%[2]s' 2>/dev/null && break; sleep 0.1; done
sleep 0.5
if grep -q EXIT '%[2]s'; then touch '%[3]s'; fi
`, swarfBin, pullOut, early)
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b.env = []string{"FAKE_RCLONE_AFTER_UPLOAD=bash '" + hook + "'"}
	pushOut := mustSwarf(t, b, "push")
	b.env = nil

	var got string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(pullOut)
		if got = string(data); strings.Contains(got, "EXIT") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(got, "EXIT 0") {
		t.Fatalf("the waiting pull should finish once the push is done\n--- pull output ---\n%s\n--- push output ---\n%s", got, pushOut)
	}
	if _, err := os.Stat(early); err == nil {
		t.Fatalf("the pull ran while the push held the store\n--- pull output ---\n%s", got)
	}
	if !strings.Contains(got, "sync lock") {
		t.Fatalf("the pull should say it is waiting for the sync lock\n--- pull output ---\n%s", got)
	}
}

// setSyncConfig replaces this machine's config.toml [sync] section keys
// with the given ones (the [machine] id stays).
func (m *machine) setSyncConfig(keys string) {
	m.t.Helper()
	body := "[sync]\n" + keys + "\n[machine]\nid = \"" + m.id + "\"\n"
	if err := os.WriteFile(filepath.Join(m.home, ".config", "swarf", "config.toml"), []byte(body), 0o644); err != nil {
		m.t.Fatal(err)
	}
}

// daemonProc is a 'swarf daemon start --foreground' running in the
// background as one machine.
type daemonProc struct {
	t       *testing.T
	cmd     *exec.Cmd
	logPath string
	exited  chan struct{}
	once    sync.Once
}

// startDaemon starts this machine's daemon in the background. It is
// stopped with SIGTERM when the test ends, if the test has not already.
func (m *machine) startDaemon() *daemonProc {
	m.t.Helper()
	logPath := filepath.Join(m.t.TempDir(), "daemon.log")
	f, err := os.Create(logPath)
	if err != nil {
		m.t.Fatal(err)
	}
	cmd := m.command(m.project, "daemon", "start", "--foreground")
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		m.t.Fatalf("start daemon: %v", err)
	}
	d := &daemonProc{t: m.t, cmd: cmd, logPath: logPath, exited: make(chan struct{})}
	go func() { cmd.Wait(); f.Close(); close(d.exited) }()
	m.t.Cleanup(func() { d.stop(10 * time.Second) })
	return d
}

// log returns the daemon's log so far.
func (d *daemonProc) log() string {
	data, _ := os.ReadFile(d.logPath)
	return string(data)
}

// stop sends the daemon SIGTERM and waits up to timeout for it to exit.
// A daemon still running then fails the test, and is killed. Returns
// how long the daemon took to exit.
func (d *daemonProc) stop(timeout time.Duration) time.Duration {
	var took time.Duration
	d.once.Do(func() {
		start := time.Now()
		d.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-d.exited:
		case <-time.After(timeout):
			d.t.Errorf("the daemon did not exit within %v of SIGTERM\n--- daemon log ---\n%s", timeout, d.log())
			d.cmd.Process.Kill()
			<-d.exited
		}
		took = time.Since(start)
	})
	return took
}

// With [sync] pull_interval set, the daemon pulls on its own: a file A
// pushes reaches B's project with no 'swarf pull' on B.
func TestMultiMachine_DaemonScheduledPull(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "v1\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.push()

	b.setSyncConfig("backend = \"rclone\"\nremote = \"fake:store\"\ndebounce = \"1s\"\npull_interval = \"1s\"\n")
	d := b.startDaemon()

	a.write("from-a.md", "pushed by A\n")
	a.push()

	deadline := time.Now().Add(30 * time.Second)
	for !b.exists("from-a.md") {
		if time.Now().After(deadline) {
			t.Fatalf("B's daemon should pull A's new file on its own\n--- daemon log ---\n%s", d.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := b.read("from-a.md"); got != "pushed by A\n" {
		t.Errorf("B's project from-a.md = %q", got)
	}
	if !strings.Contains(d.log(), "Scheduled pull enabled") {
		t.Errorf("the daemon should log that scheduled pull is on\n--- daemon log ---\n%s", d.log())
	}
}

// A daemon told to stop (SIGTERM) while a scheduled pull is downloading
// ends the download and exits promptly, leaving no rclone behind.
func TestMultiMachine_DaemonStopsDuringScheduledPull(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)

	a := newMachine(t, "a", fakeRemote)
	a.init()
	a.write("shared.md", "v1\n")
	a.push()

	b := newMachineWithSlug(t, "b", fakeRemote, a.slug())
	b.pull()
	b.init()
	b.push()

	// Every download from the remote stalls once its copy lands: the
	// hook records the rclone's pid, and its own, then sleeps in place.
	work := t.TempDir()
	rclonePid, hookPid := filepath.Join(work, "rclone.pid"), filepath.Join(work, "hook.pid")
	b.env = []string{fmt.Sprintf("FAKE_RCLONE_AFTER_DOWNLOAD=echo $PPID > '%s'; echo $$ > '%s'; exec sleep 60", rclonePid, hookPid)}
	t.Cleanup(func() {
		if pid := readPid(hookPid); pid > 0 {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	b.setSyncConfig("backend = \"rclone\"\nremote = \"fake:store\"\ndebounce = \"1s\"\npull_interval = \"1s\"\n")
	d := b.startDaemon()

	deadline := time.Now().Add(20 * time.Second)
	for readPid(rclonePid) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no scheduled pull started a download\n--- daemon log ---\n%s", d.log())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if took := d.stop(5 * time.Second); took > 5*time.Second {
		return // stop has already failed the test
	}
	if pid := readPid(rclonePid); processRunning(pid) {
		t.Errorf("the scheduled pull's rclone (pid %d) is still running after the daemon exited", pid)
	}
}

// readPid returns the pid written in file, or 0 if there is none yet.
func readPid(file string) int {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	var pid int
	fmt.Sscanf(string(data), "%d", &pid)
	return pid
}

// processRunning reports whether pid is a live process, not a zombie
// waiting to be reaped.
func processRunning(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// The state follows the parenthesized command name.
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}

// Two machines configured with the same machine_id (a shared hostname,
// or a config.toml copied with dotfiles) must not take turns overwriting
// one remote subtree. The second one to push is refused and commits
// locally only, until it moves to its own id or takes over with
// --take-over; 'swarf doctor' reports the mismatch meanwhile.
func TestMultiMachine_SharedMachineIDRefusesSecondPush(t *testing.T) {
	fakeRemote := t.TempDir()
	os.MkdirAll(filepath.Join(fakeRemote, "store"), 0o755)
	subtree := filepath.Join(fakeRemote, "store", "machines", "same")

	a := newMachine(t, "same", fakeRemote)
	a.init()
	a.write("a.md", "from A\n")
	a.push()
	before := listTree(subtree)
	if !strings.Contains(before, "a.md") {
		t.Fatalf("A's push should land a.md in its subtree:\n%s", before)
	}

	b := newMachineWithSlug(t, "same", fakeRemote, a.slug())
	b.init()
	b.write("b.md", "from B\n")
	out, err := b.swarf("push")
	if err == nil {
		t.Fatalf("B's push into A's subtree should be refused:\n%s", out)
	}
	for _, want := range []string{"machine_id", "--take-over", "swarf pull"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal should mention %q:\n%s", want, out)
		}
	}
	if got := listTree(subtree); got != before {
		t.Errorf("A's subtree changed under a refused push:\nbefore:\n%s\nafter:\n%s", before, got)
	}
	if data, _ := os.ReadFile(filepath.Join(subtree, a.slug(), "a.md")); string(data) != "from A\n" {
		t.Errorf("A's a.md on the remote = %q", data)
	}
	if !b.storeHas(a.slug(), "b.md") {
		t.Error("the refused push should still mirror and commit into B's store")
	}

	doc, _ := b.swarf("doctor")
	if !strings.Contains(doc, "different store") || !strings.Contains(doc, "--take-over") {
		t.Errorf("doctor should report the owner token mismatch:\n%s", doc)
	}

	// Taking over replaces the subtree with B's store.
	mustSwarf(t, b, "push", "--take-over")
	if _, err := os.Stat(filepath.Join(subtree, a.slug(), "b.md")); err != nil {
		t.Errorf("--take-over should upload B's store: %v", err)
	}
	if out, err := b.swarf("push"); err != nil {
		t.Errorf("after taking over, B pushes normally: %v\n%s", err, out)
	}
	if out, err := a.swarf("push"); err == nil {
		t.Errorf("after B took over, A is the one refused:\n%s", out)
	}
}

// With the git backend, each machine pushes its store to its own machine
// branch, machines/<machine_id>, so two machines sharing one remote both
// push without either being rejected, and neither writes any other
// branch.
func TestMultiMachineGit_EachMachinePushesItsOwnBranch(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	a.write("a.md", "from A\n")
	a.push()

	b := newGitMachine(t, "b", bare)
	b.init()
	b.write("b.md", "from B\n")
	b.push()

	if got := bareRef(bare, "refs/heads/machines/a"); got != a.storeHEAD() {
		t.Errorf("machines/a = %q, want A's store HEAD %q", got, a.storeHEAD())
	}
	if got := bareRef(bare, "refs/heads/machines/b"); got != b.storeHEAD() {
		t.Errorf("machines/b = %q, want B's store HEAD %q", got, b.storeHEAD())
	}
	if got, want := bareBranches(t, bare), "refs/heads/machines/a\nrefs/heads/machines/b"; got != want {
		t.Errorf("the remote should hold only the machine branches:\n%s", got)
	}

	st := mustSwarf(t, a, "status")
	for _, want := range []string{"all synced to remote", "verified"} {
		if !strings.Contains(st, want) {
			t.Errorf("A's status should say %q after its push:\n%s", want, st)
		}
	}
}

// Two machines configured with the same machine_id must not take turns
// overwriting one machine branch. The second push is refused as a
// non-fast-forward (never forced) and commits locally only, until the
// machine moves to its own id or takes over with --take-over; 'swarf
// doctor' reports the mismatch meanwhile. The git counterpart of
// TestMultiMachine_SharedMachineIDRefusesSecondPush.
func TestMultiMachineGit_SharedMachineIDRefusesSecondPush(t *testing.T) {
	bare := newBareRemote(t)
	const branch = "refs/heads/machines/same"

	a := newGitMachine(t, "same", bare)
	a.init()
	a.write("a.md", "from A\n")
	a.push()
	before := bareRef(bare, branch)
	if before == "" || before != a.storeHEAD() {
		t.Fatalf("A's push should land its store on machines/same, got %q", before)
	}

	// Same id, so the same project slug too.
	b := newGitMachine(t, "same", bare)
	b.init()
	b.write("b.md", "from B\n")
	out, err := b.swarf("push")
	if err == nil {
		t.Fatalf("B's push onto A's machine branch should be refused:\n%s", out)
	}
	for _, want := range []string{"machine_id", "--take-over", "discards"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal should mention %q:\n%s", want, out)
		}
	}
	if got := bareRef(bare, branch); got != before {
		t.Errorf("machines/same moved under a refused push: %q, want %q", got, before)
	}
	if !b.storeHas(a.slug(), "b.md") {
		t.Error("the refused push should still mirror and commit into B's store")
	}

	doc, _ := b.swarf("doctor")
	if !strings.Contains(doc, "different store") || !strings.Contains(doc, "--take-over") {
		t.Errorf("doctor should report the machine branch mismatch:\n%s", doc)
	}
	if st := mustSwarf(t, b, "status"); !strings.Contains(st, "not yet pushed") {
		t.Errorf("B's status should count its refused commit as not pushed:\n%s", st)
	}

	// Taking over force-pushes B's store to the machine branch.
	mustSwarf(t, b, "push", "--take-over")
	if got := bareRef(bare, branch); got != b.storeHEAD() {
		t.Errorf("--take-over should push B's store HEAD %q, machines/same = %q", b.storeHEAD(), got)
	}
	if out, err := b.swarf("push"); err != nil {
		t.Errorf("after taking over, B pushes normally: %v\n%s", err, out)
	}
	a.write("a2.md", "more from A\n")
	if out, err := a.swarf("push"); err == nil {
		t.Errorf("after B took over, A is the one refused:\n%s", out)
	}
}

// newGitMachineWithSlug is newGitMachine with the project directory
// named slug, so two machines share one project (see newMachineWithSlug).
func newGitMachineWithSlug(t *testing.T, id, bare, slug string) *machine {
	t.Helper()
	m := newMachineWithSlug(t, id, t.TempDir(), slug)
	m.setSyncConfig(fmt.Sprintf("backend = \"git\"\nremote = %q\ndebounce = \"1s\"\n", bare))
	return m
}

// storeRefs lists the refs under prefix in this machine's store, one
// per line.
func (m *machine) storeRefs(prefix string) string {
	m.t.Helper()
	out, err := exec.Command("git", "-C", filepath.Join(m.home, ".local", "share", "swarf"),
		"for-each-ref", "--format=%(refname)", prefix).Output()
	if err != nil {
		m.t.Fatalf("store refs: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// With the git backend, pull reads every other machine's machine branch:
// a fresh B bootstraps from A's branch with no clone, A's later edit
// reaches B's project, and B's edit reaches A's. The branches are
// fetched into the store's refs/swarf-machines/ namespace, never as
// remote-tracking branches. The git counterpart of
// TestMultiMachine_RoundTrip.
func TestMultiMachineGit_RoundTrip(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	a.write("shared.md", "v1 from A\n")
	a.push()

	b := newGitMachineWithSlug(t, "b", bare, a.slug())
	b.pull()
	if !b.storeHas(a.slug(), "shared.md") {
		t.Fatal("B's bootstrap pull should bring A's shared.md into B's store")
	}
	b.init()
	if got := b.read("shared.md"); got != "v1 from A\n" {
		t.Fatalf("B's shared.md after init = %q, want A's v1", got)
	}
	if got := b.storeRefs("refs/remotes/"); got != "" {
		t.Errorf("pull must not create remote-tracking branches:\n%s", got)
	}
	if got := b.storeRefs("refs/swarf-machines/"); !strings.Contains(got, "refs/swarf-machines/a") {
		t.Errorf("pull should fetch A's machine branch into refs/swarf-machines/:\n%s", got)
	}

	b.write("shared.md", "v2 from B\n")
	b.push()
	a.pull()
	if got := a.read("shared.md"); got != "v2 from B\n" {
		t.Fatalf("A after round-trip pull: got %q, want %q", got, "v2 from B\n")
	}
	if sidecars := findSidecars(t, a.project, "shared.md"); len(sidecars) != 0 {
		t.Errorf("a clean round trip writes no sidecars, got %v", sidecars)
	}
}

// Both machines edit one file without seeing each other's edit: the
// puller keeps its own version and gets the peer's as a conflict sidecar.
func TestMultiMachineGit_ConflictingEdits(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	a.write("shared.md", "seed\n")
	a.push()

	b := newGitMachineWithSlug(t, "b", bare, a.slug())
	b.pull()
	b.init()

	a.write("shared.md", "A wins\n")
	a.push()
	b.write("shared.md", "B wins\n")
	b.push()

	a.pull()
	if got := a.read("shared.md"); got != "A wins\n" {
		t.Fatalf("A's shared.md should keep A's text, got %q", got)
	}
	sidecars := findSidecars(t, a.project, "shared.md")
	if len(sidecars) != 1 || !strings.Contains(sidecars[0], ".conflict.b.") {
		t.Fatalf("A should have one .conflict.b.* sidecar after pull, got %v", sidecars)
	}
	if got := a.read(sidecars[0]); got != "B wins\n" {
		t.Errorf("the sidecar should hold B's version, got %q", got)
	}
	if strings.Contains(a.read("shared.md"), "<<<<<<<") {
		t.Error("pull must never leave merge markers")
	}
}

// A file A deletes is deleted from B's project by B's next pull.
func TestMultiMachineGit_DeletePropagates(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	a.write("doomed.md", "bye\n")
	a.write("kept.md", "stay\n")
	a.push()

	b := newGitMachineWithSlug(t, "b", bare, a.slug())
	b.pull()
	b.init()
	if !b.exists("doomed.md") {
		t.Fatal("B should have doomed.md after the first sync")
	}

	os.Remove(filepath.Join(a.project, "swarf", "doomed.md"))
	a.push()

	out := mustSwarf(t, b, "pull")
	if b.exists("doomed.md") {
		t.Fatalf("B's doomed.md should be gone after pull\n--- pull output ---\n%s", out)
	}
	if !b.exists("kept.md") {
		t.Error("B's kept.md should survive A's unrelated delete")
	}
}

// B set up its own store before ever pulling, so its history and A's
// share no commit. The pull unions A's files in (content-union mode)
// and deletes nothing of B's.
func TestMultiMachineGit_UnrelatedHistoriesUnion(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	a.write("from-a.md", "A\n")
	a.push()

	b := newGitMachineWithSlug(t, "b", bare, a.slug())
	b.init()
	b.write("from-b.md", "B\n")
	b.push()

	out := mustSwarf(t, b, "pull")
	if got := b.read("from-a.md"); got != "A\n" {
		t.Errorf("B should get A's file, got %q\n--- pull output ---\n%s", got, out)
	}
	if got := b.read("from-b.md"); got != "B\n" {
		t.Errorf("B's own file should survive the union, got %q", got)
	}
	b.push()
	a.pull()
	if got := a.read("from-b.md"); got != "B\n" {
		t.Errorf("A should get B's file back, got %q", got)
	}
}

// A reinstalled machine (same machine_id, fresh home) gets its own data
// back from its machine branch, plus every peer's, from one 'swarf
// pull'; its next push fast-forwards the machine branch instead of
// being refused as another store's.
func TestMultiMachineGit_ReinstallRecoversOwnBranch(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	a.write("mine.md", "A's own\n")
	a.push()

	b := newGitMachineWithSlug(t, "b", bare, a.slug())
	b.pull()
	b.init()
	b.write("theirs.md", "B's\n")
	b.push()

	oldTip := bareRef(bare, "refs/heads/machines/a")
	a2 := newGitMachineWithSlug(t, "a", bare, a.slug())
	out := mustSwarf(t, a2, "pull")
	if !a2.storeHas(a.slug(), "mine.md") || !a2.storeHas(a.slug(), "theirs.md") {
		t.Fatalf("the reinstalled store should hold its own and B's files\n--- pull output ---\n%s\n--- store ---\n%s",
			out, listTree(a2.storeRoot(a.slug())))
	}
	if !strings.Contains(out, "machines/a") {
		t.Errorf("pull should say it started the store from this machine's branch:\n%s", out)
	}
	a2.init()
	if got := a2.read("mine.md"); got != "A's own\n" {
		t.Errorf("reinstalled project mine.md = %q", got)
	}
	a2.write("new.md", "after reinstall\n")
	if out, err := a2.swarf("push"); err != nil {
		t.Fatalf("the reinstalled machine's push should fast-forward its machine branch: %v\n%s", err, out)
	}
	if !isAncestorIn(bare, oldTip, bareRef(bare, "refs/heads/machines/a")) {
		t.Error("machines/a should have moved forward from its old tip, not been replaced")
	}
}

// isAncestorIn reports whether commit a is an ancestor of commit b in
// the bare repository.
func isAncestorIn(bare, a, b string) bool {
	return exec.Command("git", "--git-dir", bare, "merge-base", "--is-ancestor", a, b).Run() == nil
}

// The git counterpart of TestMultiMachine_ResyncRestoresDivergedStore:
// files B's store lost stay lost on a plain pull, since A has not
// changed, and 'swarf pull --resync' brings them back without deleting
// anything of B's.
func TestMultiMachineGit_ResyncRestoresDivergedStore(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	files := []string{"README.md", "docs/design.md"}
	for _, f := range files {
		a.write(f, "A's "+f+"\n")
	}
	a.push()

	b := newGitMachineWithSlug(t, "b", bare, a.slug())
	b.pull()
	b.init()
	b.push() // the manifest now lists every file

	b.wipeProject("README.md", "docs")
	mustSwarf(t, b, "push", "--allow-deletes")
	b.write("b-only.md", "B's own file\n")
	b.push()

	out := mustSwarf(t, b, "pull")
	for _, f := range files {
		if b.storeHas(a.slug(), f) {
			t.Fatalf("a plain pull is not expected to restore %s\n--- pull output ---\n%s", f, out)
		}
	}

	out = mustSwarf(t, b, "pull", "--resync")
	for _, f := range files {
		if got := b.read(f); got != "A's "+f+"\n" {
			t.Errorf("--resync should restore %s into B's project, got %q\n--- pull output ---\n%s", f, got, out)
		}
	}
	if got := b.read("b-only.md"); got != "B's own file\n" {
		t.Errorf("--resync never deletes: B's own file should survive, got %q", got)
	}
}

// A delete hold on the git backend ends the same way as on rclone: a
// pull copies the held files back from the store into the project and
// clears the hold, and status names 'swarf pull' as the way to do it.
func TestMultiMachineGit_PullReleasesDeleteHold(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	files := []string{"README.md", "docs/design.md"}
	for _, f := range files {
		a.write(f, "A's "+f+"\n")
	}
	a.push()
	a.push() // the manifest now lists every file

	a.wipeProject("README.md", "docs")
	out := mustSwarf(t, a, "push")
	if _, err := os.Stat(a.holdMarker(a.slug())); err != nil {
		t.Fatalf("setup: expected the push to hold: %v\n%s", err, out)
	}
	if status := mustSwarf(t, a, "status"); !strings.Contains(status, "swarf pull") {
		t.Errorf("status should name 'swarf pull' as a way out of the hold:\n%s", status)
	}

	out = mustSwarf(t, a, "pull")
	for _, f := range files {
		if got := a.read(f); got != "A's "+f+"\n" {
			t.Errorf("pull should restore %s, got %q\n--- pull output ---\n%s", f, got, out)
		}
	}
	if _, err := os.Stat(a.holdMarker(a.slug())); !os.IsNotExist(err) {
		t.Errorf("the hold should be cleared once the files are back (err=%v)\n%s", err, out)
	}
}

// With [sync] pull_interval set, a git-backend daemon pulls on its own:
// a file A pushes reaches B's project with no 'swarf pull' on B.
func TestMultiMachineGit_DaemonScheduledPull(t *testing.T) {
	bare := newBareRemote(t)
	a := newGitMachine(t, "a", bare)
	a.init()
	a.write("shared.md", "v1\n")
	a.push()

	b := newGitMachineWithSlug(t, "b", bare, a.slug())
	b.pull()
	b.init()
	b.push()

	b.setSyncConfig(fmt.Sprintf("backend = \"git\"\nremote = %q\ndebounce = \"1s\"\npull_interval = \"1s\"\n", bare))
	d := b.startDaemon()

	a.write("from-a.md", "pushed by A\n")
	a.push()

	deadline := time.Now().Add(30 * time.Second)
	for !b.exists("from-a.md") {
		if time.Now().After(deadline) {
			t.Fatalf("B's daemon should pull A's new file on its own\n--- daemon log ---\n%s", d.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := b.read("from-a.md"); got != "pushed by A\n" {
		t.Errorf("B's project from-a.md = %q", got)
	}
	if strings.Contains(d.log(), "not supported") {
		t.Errorf("the git backend supports scheduled pull\n--- daemon log ---\n%s", d.log())
	}
}
