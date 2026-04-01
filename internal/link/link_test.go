package link_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestLinkCreatesSymlinks(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.SwarfDir(repo), "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(result.Created))
	}

	target := filepath.Join(repo, "AGENTS.md")
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink")
	}

	// Symlink must be relative for jail/remapped-dir portability.
	linkTarget, _ := os.Readlink(target)
	if filepath.IsAbs(linkTarget) {
		t.Fatalf("expected relative symlink, got absolute: %s", linkTarget)
	}
}

func TestLinkIdempotent(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.SwarfDir(repo), "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	link.Run(repo, false)
	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 0 {
		t.Fatalf("expected 0 created on second run, got %d", len(result.Created))
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %d", len(result.Skipped))
	}
}

func TestLinkNestedDirs(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	nested := filepath.Join(paths.SwarfDir(repo), "links", "docs", "notes.md")
	os.MkdirAll(filepath.Dir(nested), 0o755)
	os.WriteFile(nested, []byte("# Notes\n"), 0o644)

	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(result.Created))
	}

	target := filepath.Join(repo, "docs", "notes.md")
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink")
	}

	// Nested symlinks must also be relative.
	linkTarget, _ := os.Readlink(target)
	if filepath.IsAbs(linkTarget) {
		t.Fatalf("expected relative symlink, got absolute: %s", linkTarget)
	}
}

func TestLinkWarnsOnRealFile(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.SwarfDir(repo), "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("real file\n"), 0o644)

	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(result.Warnings))
	}
	if len(result.Created) != 0 {
		t.Fatal("should not create link when real file exists")
	}
}

func TestLinkEmptyLinksDir(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 0 {
		t.Fatalf("expected 0 created for empty links, got %d", len(result.Created))
	}
}

func TestLinkNoProject(t *testing.T) {
	tmp := t.TempDir()
	os.Chdir(tmp)
	_, err := link.Run("", false)
	if err == nil {
		t.Fatal("expected error for no project")
	}
}

func TestLinkFixesStaleSymlink(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.SwarfDir(repo), "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	// Create stale symlink pointing to wrong location
	target := filepath.Join(repo, "AGENTS.md")
	os.Symlink("/nonexistent/old/path", target)

	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 1 {
		t.Fatalf("expected 1 created (stale fix), got %d", len(result.Created))
	}
}
