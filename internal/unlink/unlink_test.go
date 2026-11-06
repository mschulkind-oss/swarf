package unlink_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/sweep"
	"github.com/mschulkind-oss/swarf/internal/testutil"
	"github.com/mschulkind-oss/swarf/internal/unlink"
)

func TestUnlinkRestoresFile(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	content := []byte("# Agents\nsome content\n")

	// Create and sweep a file.
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), content, 0o644)
	os.Chdir(repo)
	if err := sweep.Run([]string{"AGENTS.md"}, repo); err != nil {
		t.Fatal(err)
	}

	// Verify it's a symlink.
	fi, _ := os.Lstat(filepath.Join(repo, "AGENTS.md"))
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected symlink after sweep")
	}

	// Unlink it.
	if err := unlink.Run([]string{"AGENTS.md"}, repo); err != nil {
		t.Fatal(err)
	}

	// Should be a regular file now.
	fi, err := os.Lstat(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected regular file after unlink")
	}

	// Content should match.
	got, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q", got)
	}

	// Should be removed from swarf/.links/.
	if _, err := os.Stat(filepath.Join(paths.LinksDir(repo), "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("expected file removed from swarf/.links/")
	}
}

func TestUnlinkRemovesExclude(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Agents\n"), 0o644)
	os.Chdir(repo)
	sweep.Run([]string{"AGENTS.md"}, repo)

	// Verify exclude entry exists.
	entries := exclude.ReadManagedExcludes(repo)
	found := false
	for _, e := range entries {
		if e == "/AGENTS.md" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected /AGENTS.md in excludes after sweep")
	}

	// Unlink.
	unlink.Run([]string{"AGENTS.md"}, repo)

	// Exclude entry should be gone.
	entries = exclude.ReadManagedExcludes(repo)
	for _, e := range entries {
		if e == "/AGENTS.md" {
			t.Fatal("/AGENTS.md should be removed from excludes after unlink")
		}
	}
}

func TestUnlinkNestedPath(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	nested := filepath.Join(repo, "docs", "design.md")
	os.MkdirAll(filepath.Dir(nested), 0o755)
	os.WriteFile(nested, []byte("# Design\n"), 0o644)
	os.Chdir(repo)
	sweep.Run([]string{"docs/design.md"}, repo)

	if err := unlink.Run([]string{"docs/design.md"}, repo); err != nil {
		t.Fatal(err)
	}

	fi, _ := os.Lstat(nested)
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected regular file")
	}

	// Empty parent in links/ should be cleaned up.
	if _, err := os.Stat(filepath.Join(paths.LinksDir(repo), "docs")); !os.IsNotExist(err) {
		t.Fatal("expected empty docs/ dir cleaned up in links/")
	}
}

// TestUnlinkPurgesStoreCopy locks in the "half-unswept" fix. unlink used to
// remove swarf/.links/<rel> and the exclude entry but leave the store's copy
// at store/<slug>/.links/<rel> behind. Since init seeds project/swarf/
// additively from the store, the next 'swarf init' (or a reverse mirror on
// pull) copied .links/<rel> straight back — resurrecting the sweep while the
// host file stayed a regular file. doctor then reported the file as
// not-gitignored, still-tracked, and "not a symlink" forever.
func TestUnlinkPurgesStoreCopy(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Agents\n"), 0o644)
	os.Chdir(repo)
	if err := sweep.Run([]string{"AGENTS.md"}, repo); err != nil {
		t.Fatal(err)
	}

	// Simulate the daemon's forward mirror: project/swarf/ → store/<slug>/.
	storeLinks := filepath.Join(paths.StoreDir, slug, ".links")
	os.MkdirAll(storeLinks, 0o755)
	storeCopy := filepath.Join(storeLinks, "AGENTS.md")
	if err := os.WriteFile(storeCopy, []byte("# Agents\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := unlink.Run([]string{"AGENTS.md"}, repo); err != nil {
		t.Fatal(err)
	}

	// The store copy must be gone too, or init re-seeds it.
	if _, err := os.Stat(storeCopy); !os.IsNotExist(err) {
		t.Fatal("expected store copy removed from store/<slug>/.links/ after unlink")
	}

	// And the manifest entry must be dropped, so the forward mirror doesn't
	// treat the store copy as a tracked file it should keep resurrecting.
	manifest, err := os.ReadFile(paths.ProjectManifest(slug))
	if err == nil && strings.Contains(string(manifest), ".links/AGENTS.md") {
		t.Fatal("expected .links/AGENTS.md dropped from the forward-mirror manifest")
	}
}

// TestUnlinkPurgesStoreCopyNested is the nested-path variant: the store's
// now-empty parent dirs should be cleaned up as well, matching what unlink
// already does inside swarf/.links/.
func TestUnlinkPurgesStoreCopyNested(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	nested := filepath.Join(repo, "docs", "design.md")
	os.MkdirAll(filepath.Dir(nested), 0o755)
	os.WriteFile(nested, []byte("# Design\n"), 0o644)
	os.Chdir(repo)
	if err := sweep.Run([]string{"docs/design.md"}, repo); err != nil {
		t.Fatal(err)
	}

	storeDocs := filepath.Join(paths.StoreDir, slug, ".links", "docs")
	os.MkdirAll(storeDocs, 0o755)
	os.WriteFile(filepath.Join(storeDocs, "design.md"), []byte("# Design\n"), 0o644)

	if err := unlink.Run([]string{"docs/design.md"}, repo); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(storeDocs); !os.IsNotExist(err) {
		t.Fatal("expected empty docs/ dir cleaned up in the store's .links/")
	}
}

func TestUnlinkNotSwept(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# README\n"), 0o644)
	os.Chdir(repo)

	err := unlink.Run([]string{"README.md"}, repo)
	if err != nil {
		t.Fatal(err) // no error, just prints a warning
	}
}

func TestUnlinkNoProject(t *testing.T) {
	tmp := t.TempDir()
	os.Chdir(tmp)
	err := unlink.Run([]string{"AGENTS.md"}, "")
	if err == nil {
		t.Fatal("expected error outside project")
	}
}

// TestUnlinkRemoveDeletesFile covers 'swarf unlink --rm': the swept file is
// deleted outright — no regular file left behind, no .links/ copy, no store
// copy, no exclude entry — instead of being restored in place.
func TestUnlinkRemoveDeletesFile(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	slug := filepath.Base(repo)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Agents\n"), 0o644)
	os.Chdir(repo)
	if err := sweep.Run([]string{"AGENTS.md"}, repo); err != nil {
		t.Fatal(err)
	}
	storeLinks := filepath.Join(paths.StoreDir, slug, ".links")
	os.MkdirAll(storeLinks, 0o755)
	storeCopy := filepath.Join(storeLinks, "AGENTS.md")
	os.WriteFile(storeCopy, []byte("# Agents\n"), 0o644)

	if err := unlink.RunWithRemove([]string{"AGENTS.md"}, repo, true); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("expected AGENTS.md gone from the project tree")
	}
	if _, err := os.Stat(filepath.Join(paths.LinksDir(repo), "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("expected file removed from swarf/.links/")
	}
	if _, err := os.Stat(storeCopy); !os.IsNotExist(err) {
		t.Fatal("expected store copy removed")
	}
	for _, e := range exclude.ReadManagedExcludes(repo) {
		if e == "/AGENTS.md" {
			t.Fatal("/AGENTS.md should be removed from excludes after unlink --rm")
		}
	}
}

// TestUnlinkRemoveKeepsRegularFile: if the host path is no longer the
// symlink sweep made (a tool replaced it with a regular file), --rm must
// not delete it — that content isn't in swarf/.links/ and would be lost.
func TestUnlinkRemoveKeepsRegularFile(t *testing.T) {
	repo := testutil.InitializedSwarf(t)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Agents\n"), 0o644)
	os.Chdir(repo)
	if err := sweep.Run([]string{"AGENTS.md"}, repo); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(repo, "AGENTS.md")
	os.Remove(host)
	os.WriteFile(host, []byte("# Rewritten by a tool\n"), 0o644)

	if err := unlink.RunWithRemove([]string{"AGENTS.md"}, repo, true); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(host)
	if err != nil || string(got) != "# Rewritten by a tool\n" {
		t.Fatalf("expected the regular file kept untouched, got %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(paths.LinksDir(repo), "AGENTS.md")); err != nil {
		t.Fatal("expected swarf/.links/ copy kept when the path is skipped")
	}
}
