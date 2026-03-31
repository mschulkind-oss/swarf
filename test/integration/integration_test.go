package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run the compiled swarf binary end-to-end.

var swarfBin string

func TestMain(m *testing.M) {
	// Build the binary once for all tests
	tmp, _ := os.MkdirTemp("", "swarf-integration-*")
	swarfBin = filepath.Join(tmp, "swarf")
	cmd := exec.Command("go", "build", "-o", swarfBin, "github.com/mschulkind-oss/swarf")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build failed: " + string(out))
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

type env struct {
	repo      string
	configDir string
	dataDir   string
	t         *testing.T
}

func setup(t *testing.T) *env {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "project")
	configDir := filepath.Join(tmp, "config")
	dataDir := filepath.Join(tmp, "data")

	os.MkdirAll(repo, 0o755)
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(dataDir, 0o755)

	run(t, repo, "git", "init")
	run(t, repo, "git", "config", "user.email", "test@test.com")
	run(t, repo, "git", "config", "user.name", "Test")

	// Write global config
	swarfConfig := filepath.Join(configDir, "swarf")
	os.MkdirAll(swarfConfig, 0o755)
	os.WriteFile(filepath.Join(swarfConfig, "config.toml"), []byte(`[sync]
backend = "git"
remote = ""
debounce = "5s"
`), 0o644)

	return &env{repo: repo, configDir: configDir, dataDir: dataDir, t: t}
}

func (e *env) swarf(args ...string) (string, error) {
	e.t.Helper()
	cmd := exec.Command(swarfBin, args...)
	cmd.Dir = e.repo
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+e.configDir,
		"XDG_DATA_HOME="+e.dataDir,
		"HOME="+e.t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %s\n%s", name, args, err, out)
	}
}

func TestE2EVersion(t *testing.T) {
	e := setup(t)
	out, err := e.swarf("--version")
	if err != nil {
		t.Fatalf("--version failed: %s", out)
	}
	if !strings.Contains(out, "swarf version") {
		t.Fatalf("unexpected version output: %s", out)
	}
}

func TestE2EHelp(t *testing.T) {
	e := setup(t)
	out, err := e.swarf("--help")
	if err != nil {
		t.Fatalf("--help failed: %s", out)
	}
	if !strings.Contains(out, "init") || !strings.Contains(out, "doctor") {
		t.Fatalf("help missing commands: %s", out)
	}
}

func TestE2EInitAndDoctor(t *testing.T) {
	e := setup(t)

	// Init
	out, err := e.swarf("init")
	if err != nil {
		t.Fatalf("init failed: %s", out)
	}
	if !strings.Contains(out, "Initialized swarf") {
		t.Fatalf("unexpected init output: %s", out)
	}

	// .swarf should be a symlink
	fi, err := os.Lstat(filepath.Join(e.repo, ".swarf"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected .swarf to be symlink")
	}

	// .mise.local.toml should exist
	data, err := os.ReadFile(filepath.Join(e.repo, ".mise.local.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "swarf enter") {
		t.Fatal("expected enter hook in .mise.local.toml")
	}

	// Doctor (some checks will fail — remote, daemon — but should not crash)
	out, _ = e.swarf("doctor")
	if !strings.Contains(out, "store") {
		t.Fatalf("doctor output missing store check: %s", out)
	}
}

func TestE2EInitAlreadyInitialized(t *testing.T) {
	e := setup(t)
	e.swarf("init")
	out, err := e.swarf("init")
	if err == nil {
		t.Fatal("expected error for double init")
	}
	if !strings.Contains(out, "already initialized") {
		t.Fatalf("unexpected error: %s", out)
	}
}

func TestE2EInitNotGitRepo(t *testing.T) {
	e := setup(t)
	e.repo = t.TempDir() // not a git repo
	out, err := e.swarf("init")
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(out, "not inside a git repository") {
		t.Fatalf("unexpected error: %s", out)
	}
}

func TestE2ESweepAndLink(t *testing.T) {
	e := setup(t)
	e.swarf("init")

	// Create a file and sweep it
	os.WriteFile(filepath.Join(e.repo, "AGENTS.md"), []byte("# Agents\n"), 0o644)
	out, err := e.swarf("sweep", "AGENTS.md")
	if err != nil {
		t.Fatalf("sweep failed: %s", out)
	}

	// Should be a symlink now
	fi, err := os.Lstat(filepath.Join(e.repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after sweep")
	}

	// Should exist in .swarf/links/
	if _, err := os.Stat(filepath.Join(e.repo, ".swarf", "links", "AGENTS.md")); err != nil {
		t.Fatal("expected file in .swarf/links/")
	}

	// Remove the symlink to test re-linking
	os.Remove(filepath.Join(e.repo, "AGENTS.md"))
	out, err = e.swarf("link")
	if err != nil {
		t.Fatalf("link failed: %s", out)
	}

	fi, err = os.Lstat(filepath.Join(e.repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after link")
	}
}

func TestE2EStatus(t *testing.T) {
	e := setup(t)
	e.swarf("init")

	out, err := e.swarf("status")
	if err != nil {
		t.Fatalf("status failed: %s", out)
	}
	if !strings.Contains(out, "Store") {
		t.Fatalf("status missing Store section: %s", out)
	}
	if !strings.Contains(out, "git") {
		t.Fatalf("status missing backend: %s", out)
	}
}

func TestE2EEnter(t *testing.T) {
	e := setup(t)
	e.swarf("init")

	// Create a link source
	source := filepath.Join(e.repo, ".swarf", "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	out, err := e.swarf("enter")
	if err != nil {
		t.Fatalf("enter failed: %s", out)
	}

	fi, err := os.Lstat(filepath.Join(e.repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after enter")
	}
}

func TestE2EDocs(t *testing.T) {
	e := setup(t)
	// List topics
	out, err := e.swarf("docs")
	if err != nil {
		t.Fatalf("docs failed: %s", out)
	}
	if !strings.Contains(out, "architecture") || !strings.Contains(out, "quickstart") {
		t.Fatalf("docs missing topics: %s", out)
	}
	// Read a specific topic
	out, err = e.swarf("docs", "architecture")
	if err != nil {
		t.Fatalf("docs architecture failed: %s", out)
	}
	if !strings.Contains(out, "central store") {
		t.Fatalf("architecture doc missing content: %s", out)
	}
	// Unknown topic
	out, err = e.swarf("docs", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown topic")
	}
}

func TestE2EDaemonStatus(t *testing.T) {
	e := setup(t)
	out, _ := e.swarf("daemon", "status")
	if !strings.Contains(out, "not running") {
		t.Fatalf("expected 'not running': %s", out)
	}
}

func TestE2EFullLifecycle(t *testing.T) {
	e := setup(t)

	// 1. Init
	out, err := e.swarf("init")
	if err != nil {
		t.Fatalf("init: %s", out)
	}

	// 2. Create and sweep a file
	os.WriteFile(filepath.Join(e.repo, "CLAUDE.md"), []byte("# Claude\n"), 0o644)
	out, err = e.swarf("sweep", "CLAUDE.md")
	if err != nil {
		t.Fatalf("sweep: %s", out)
	}

	// 3. Verify it's swept
	fi, _ := os.Lstat(filepath.Join(e.repo, "CLAUDE.md"))
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("CLAUDE.md should be symlink")
	}

	// 4. Remove symlink, re-enter
	os.Remove(filepath.Join(e.repo, "CLAUDE.md"))
	e.swarf("enter")
	fi, _ = os.Lstat(filepath.Join(e.repo, "CLAUDE.md"))
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("CLAUDE.md should be re-linked after enter")
	}

	// 5. Status
	out, err = e.swarf("status")
	if err != nil {
		t.Fatalf("status: %s", out)
	}

	// 6. Doctor (some will fail — that's ok)
	out, _ = e.swarf("doctor")
	if !strings.Contains(out, "Central store exists") {
		t.Fatalf("doctor should find store: %s", out)
	}
}

func TestE2ESecondProjectReusesStore(t *testing.T) {
	e := setup(t)
	e.swarf("init")

	// Create a second project
	proj2 := filepath.Join(t.TempDir(), "proj2")
	os.MkdirAll(proj2, 0o755)
	run(t, proj2, "git", "init")
	run(t, proj2, "git", "config", "user.email", "test@test.com")
	run(t, proj2, "git", "config", "user.name", "Test")

	e2 := &env{repo: proj2, configDir: e.configDir, dataDir: e.dataDir, t: t}
	out, err := e2.swarf("init")
	if err != nil {
		t.Fatalf("init proj2: %s", out)
	}

	fi, err := os.Lstat(filepath.Join(proj2, ".swarf"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected .swarf symlink in proj2")
	}
}
