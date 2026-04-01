package exclude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git", "info"), 0o755)
	return tmp
}

func TestReadManagedExcludesEmpty(t *testing.T) {
	tmp := makeRepo(t)
	entries := ReadManagedExcludes(tmp)
	if len(entries) != 0 {
		t.Fatalf("expected empty, got %v", entries)
	}
}

func TestWriteAndReadManagedExcludes(t *testing.T) {
	tmp := makeRepo(t)
	WriteManagedExcludes(tmp, []string{"/swarf/", "/.mise.local.toml"})

	entries := ReadManagedExcludes(tmp)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %v", entries)
	}
}

func TestWritePreservesUserContent(t *testing.T) {
	tmp := makeRepo(t)
	path := filepath.Join(tmp, ".git", "info", "exclude")
	os.WriteFile(path, []byte("# my custom ignores\n*.log\n"), 0o644)

	WriteManagedExcludes(tmp, []string{"/swarf/"})

	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "*.log") {
		t.Fatal("user content was lost")
	}
	if !strings.Contains(string(content), "/swarf/") {
		t.Fatal("managed entry not found")
	}
}

func TestUpdateExcludes(t *testing.T) {
	tmp := makeRepo(t)
	UpdateExcludes(tmp, []string{"/AGENTS.md"})

	entries := ReadManagedExcludes(tmp)
	found := map[string]bool{}
	for _, e := range entries {
		found[e] = true
	}
	if !found["/swarf/"] || !found["/.mise.local.toml"] || !found["/AGENTS.md"] {
		t.Fatalf("missing expected entries: %v", entries)
	}
}

func TestAddLinkedExcludes(t *testing.T) {
	tmp := makeRepo(t)
	UpdateExcludes(tmp, nil) // base
	AddLinkedExcludes(tmp, []string{"AGENTS.md", "docs/README.md"})

	entries := ReadManagedExcludes(tmp)
	found := map[string]bool{}
	for _, e := range entries {
		found[e] = true
	}
	if !found["/AGENTS.md"] || !found["/docs/README.md"] {
		t.Fatalf("missing linked entries: %v", entries)
	}
}
