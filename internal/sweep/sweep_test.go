package sweep_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/sweep"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestSweepMoveAndSymlink(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	target := filepath.Join(repo, "AGENTS.md")
	os.WriteFile(target, []byte("# Agents\n"), 0o644)

	err := sweep.Run([]string{"AGENTS.md"}, repo)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected original to be replaced with symlink")
	}

	dest := filepath.Join(paths.SwarfDir(repo), "links", "AGENTS.md")
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		t.Fatal("expected file in swarf/links/")
	}
}

func TestSweepNestedPath(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	nested := filepath.Join(repo, "docs", "notes.md")
	os.MkdirAll(filepath.Dir(nested), 0o755)
	os.WriteFile(nested, []byte("# Notes\n"), 0o644)

	sweep.Run([]string{"docs/notes.md"}, repo)

	fi, err := os.Lstat(nested)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink")
	}
}

func TestSweepAlreadySymlink(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.SwarfDir(repo), "links", "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	target := filepath.Join(repo, "AGENTS.md")
	os.Symlink(source, target)

	err := sweep.Run([]string{"AGENTS.md"}, repo)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSweepFileNotFound(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	err := sweep.Run([]string{"nonexistent.md"}, repo)
	if err != nil {
		t.Fatal(err) // individual file errors are logged, not returned
	}
}

func TestSweepInsideSwarf(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	notes := filepath.Join(paths.SwarfDir(repo), "notes", "test.md")
	os.MkdirAll(filepath.Dir(notes), 0o755)
	os.WriteFile(notes, []byte("test"), 0o644)

	err := sweep.Run([]string{paths.SwarfDirName + "/notes/test.md"}, repo)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSweepAlreadyInLinks(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	// Put file in links and in project root, then sweep
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Agents\n"), 0o644)
	os.WriteFile(filepath.Join(paths.SwarfDir(repo), "links", "AGENTS.md"), []byte("# Old\n"), 0o644)

	sweep.Run([]string{"AGENTS.md"}, repo)
	// Should skip — file already in links
}

func TestSweepNoProject(t *testing.T) {
	tmp := t.TempDir()
	os.Chdir(tmp)
	err := sweep.Run([]string{"file.md"}, "")
	if err == nil {
		t.Fatal("expected error for no project")
	}
}

func TestSweepNoLinksDir(t *testing.T) {
	repo := testutil.GitRepo(t)
	// Create .swarf but no links/
	os.MkdirAll(filepath.Join(paths.SwarfDir(repo)), 0o755)
	err := sweep.Run([]string{"file.md"}, repo)
	if err == nil {
		t.Fatal("expected error for no links dir")
	}
}

func TestSweepMultiple(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	os.WriteFile(filepath.Join(repo, "a.md"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(repo, "b.md"), []byte("b"), 0o644)

	err := sweep.Run([]string{"a.md", "b.md"}, repo)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"a.md", "b.md"} {
		fi, err := os.Lstat(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected %s to be symlink", name)
		}
	}
}
