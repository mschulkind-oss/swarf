package status

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestCountNonEmpty(t *testing.T) {
	tests := []struct {
		input []string
		want  int
	}{
		{[]string{}, 0},
		{[]string{"", "  "}, 0},
		{[]string{"a", "b"}, 2},
		{[]string{"a", "", "b"}, 2},
	}
	for _, tt := range tests {
		if got := countNonEmpty(tt.input); got != tt.want {
			t.Errorf("countNonEmpty(%v) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestIsSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	os.WriteFile(target, []byte("x"), 0o644)
	link := filepath.Join(tmp, "link")
	os.Symlink(target, link)

	if !isSymlink(link) {
		t.Fatal("expected true for symlink")
	}
	if isSymlink(target) {
		t.Fatal("expected false for regular file")
	}
	if isSymlink(filepath.Join(tmp, "nonexistent")) {
		t.Fatal("expected false for nonexistent")
	}
}

func TestReadPID(t *testing.T) {
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "pid")
	os.WriteFile(pidFile, []byte("12345"), 0o644)
	pid, ok := readPID(pidFile)
	if !ok || pid != 12345 {
		t.Fatalf("expected 12345, got %d (ok=%v)", pid, ok)
	}

	os.WriteFile(pidFile, []byte("notanumber"), 0o644)
	_, ok = readPID(pidFile)
	if ok {
		t.Fatal("expected not ok for invalid PID")
	}

	_, ok = readPID(filepath.Join(tmp, "nonexistent"))
	if ok {
		t.Fatal("expected not ok for missing file")
	}
}

func TestRunNoConfig(t *testing.T) {
	testutil.GitRepo(t)
	Run()
}

func TestRunWithStore(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	Run()
}

func TestRunWithStoreAndDrawers(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	config.RegisterDrawer("testproject", repo)
	// Make a commit so git log works
	exec.Command("git", "-C", paths.StoreDir, "add", "-A").Run()
	exec.Command("git", "-C", paths.StoreDir, "commit", "-m", "test").Run()
	Run()
}

func TestPrintDaemonStatusNotRunning(t *testing.T) {
	tmp := t.TempDir()
	paths.PIDFile = filepath.Join(tmp, "nonexistent.pid")
	printDaemonStatus()
}

func TestPrintDaemonStatusStalePid(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "daemon.pid")
	os.WriteFile(pidFile, []byte("999999999"), 0o644)
	paths.PIDFile = pidFile
	printDaemonStatus()
}

func TestPrintStoreInfoNoStore(t *testing.T) {
	testutil.GitRepo(t)
	gc := &config.GlobalConfig{Backend: "git", Remote: "test", Debounce: "5s"}
	printStoreInfo(gc)
}

func TestPrintStoreInfoNoRemote(t *testing.T) {
	testutil.InitializedSwarf(t)
	gc := &config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"}
	printStoreInfo(gc)
}

func TestPrintProjectsEmpty(t *testing.T) {
	testutil.GitRepo(t)
	printProjects()
}

func TestGitLogNonRepo(t *testing.T) {
	tmp := t.TempDir()
	got := gitLog(tmp)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestGitLogRepo(t *testing.T) {
	testutil.InitializedSwarf(t)
	// Create a file so git add has something to commit
	os.WriteFile(filepath.Join(paths.StoreDir, "dummy.txt"), []byte("x"), 0o644)
	if out, err := exec.Command("git", "-C", paths.StoreDir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", paths.StoreDir, "commit", "-m", "test commit").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s %v", out, err)
	}
	got := gitLog(paths.StoreDir)
	if got == "" {
		t.Fatal("expected non-empty git log")
	}
}
