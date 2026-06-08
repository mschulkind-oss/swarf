package link_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestLinkCreatesSymlinks(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
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

	linkTarget, _ := os.Readlink(target)
	if filepath.IsAbs(linkTarget) {
		t.Fatalf("expected relative symlink, got absolute: %s", linkTarget)
	}
}

func TestLinkIdempotent(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
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
	nested := filepath.Join(paths.LinksDir(repo), "docs", "notes.md")
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

	linkTarget, _ := os.Readlink(target)
	if filepath.IsAbs(linkTarget) {
		t.Fatalf("expected relative symlink, got absolute: %s", linkTarget)
	}
}

func TestLinkHealsIdenticalRegularFile(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	content := []byte("# Agents\n")
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, content, 0o644)

	// Create symlink first, then replace with identical regular file.
	link.Run(repo, true)
	target := filepath.Join(repo, "AGENTS.md")
	os.Remove(target)
	os.WriteFile(target, content, 0o644)

	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Healed) != 1 {
		t.Fatalf("expected 1 healed, got %d", len(result.Healed))
	}

	// Target should be a symlink again.
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after heal")
	}

	// .links/ content should be unchanged.
	data, _ := os.ReadFile(source)
	if string(data) != string(content) {
		t.Fatalf("expected .links/ content to be unchanged, got %q", string(data))
	}
}

func TestLinkHealsDivergentRegularFile(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	oldContent := []byte("# Agents v1\n")
	newContent := []byte("# Agents v2 — with extra rules\n")
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, oldContent, 0o644)

	// Create symlink first, then replace with divergent regular file
	// (simulates an atomic-save editor clobbering the symlink).
	link.Run(repo, true)
	target := filepath.Join(repo, "AGENTS.md")
	os.Remove(target)
	os.WriteFile(target, newContent, 0o644)

	result, err := link.Run(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Healed) != 1 {
		t.Fatalf("expected 1 healed, got %d", len(result.Healed))
	}

	// Target should be a symlink again.
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after heal")
	}

	// .links/ content should now match the newer host content.
	data, _ := os.ReadFile(source)
	if string(data) != string(newContent) {
		t.Fatalf("expected .links/ to have new content, got %q", string(data))
	}

	// Reading through the symlink should return new content.
	data, _ = os.ReadFile(target)
	if string(data) != string(newContent) {
		t.Fatalf("expected target to read new content through symlink, got %q", string(data))
	}
}

func TestLinkHealsMissingHostPath(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	// Don't create the host file — just run link. It should create the symlink.
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
}

// TestLinkQuietSuppressesConsole locks in the spam fix: with quiet=true the
// warning must be returned to the caller (so the daemon can decide whether to
// log it) but nothing may be written to the console. Previously quiet=true
// still printed every warning, so the daemon re-dumped the same unresolved
// conflicts to the journal on every relink cycle.
func TestLinkQuietSuppressesConsole(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("real file\n"), 0o644)

	var out, errOut bytes.Buffer
	oldOut, oldErr := console.Stdout, console.Stderr
	console.Stdout, console.Stderr = &out, &errOut
	defer func() { console.Stdout, console.Stderr = oldOut, oldErr }()

	result, err := link.Run(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning in result, got %d", len(result.Warnings))
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("quiet=true wrote to console: stdout=%q stderr=%q", out.String(), errOut.String())
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
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
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

func TestLinkHealIsIdempotent(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	// First run creates symlink.
	r1, _ := link.Run(repo, true)
	if len(r1.Created) != 1 {
		t.Fatalf("expected 1 created on first run, got %d", len(r1.Created))
	}

	// Second run should skip (already healthy).
	r2, _ := link.Run(repo, true)
	if len(r2.Skipped) != 1 {
		t.Fatalf("expected 1 skipped on second run, got %d", len(r2.Skipped))
	}
	if len(r2.Created) != 0 || len(r2.Healed) != 0 {
		t.Fatal("expected no created or healed on second run")
	}
}

func TestRunWithFixUntracksSweptFile(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	source := filepath.Join(paths.LinksDir(repo), "AGENTS.md")
	os.WriteFile(source, []byte("# Agents\n"), 0o644)

	// Stage and commit the file so it's tracked.
	target := filepath.Join(repo, "AGENTS.md")
	os.WriteFile(target, []byte("# Agents\n"), 0o644)
	testutil.GitAdd(t, repo, "AGENTS.md")
	testutil.GitCommit(t, repo, "add AGENTS.md")

	// RunWithFix should untrack it and heal the symlink.
	result, err := link.RunWithFix(repo, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Healed) != 1 {
		t.Fatalf("expected 1 healed, got %d", len(result.Healed))
	}

	// Verify it's a symlink now.
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after fix")
	}
}
