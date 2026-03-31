package enter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/enter"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestEnterLinksFiles(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})

	source := filepath.Join(repo, ".swarf", "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	enter.Run()

	fi, err := os.Lstat(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after enter")
	}
}

func TestEnterAutoSweep(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{
		Backend:   "git",
		Remote:    "",
		Debounce:  "5s",
		AutoSweep: []string{"AGENTS.md"},
	})

	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Agents\n"), 0o644)

	enter.Run()

	fi, err := os.Lstat(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected auto-swept file to be symlink")
	}
}

func TestEnterNoSwarfDir(t *testing.T) {
	tmp := t.TempDir()
	os.Chdir(tmp)
	// Should not panic
	enter.Run()
}

func TestEnterSkipAlreadySwept(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{
		Backend:   "git",
		Remote:    "",
		Debounce:  "5s",
		AutoSweep: []string{"AGENTS.md"},
	})

	// Already swept — create as symlink
	source := filepath.Join(repo, ".swarf", "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	os.Symlink(source, filepath.Join(repo, "AGENTS.md"))

	// Should not panic or error
	enter.Run()
}

func TestEnterSkipMissingAutoSweep(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{
		Backend:   "git",
		Remote:    "",
		Debounce:  "5s",
		AutoSweep: []string{"nonexistent.md"},
	})
	_ = repo
	// Should not error
	enter.Run()
}

func TestEnterNoAutoSweepConfig(t *testing.T) {
	testutil.InitializedSwarf(t)
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: "", Debounce: "5s"})
	// Should not error
	enter.Run()
}
