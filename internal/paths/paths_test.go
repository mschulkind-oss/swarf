package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwarfDir(t *testing.T) {
	got := SwarfDir("/home/user/project")
	want := filepath.Join("/home/user/project", "swarf")
	if got != want {
		t.Fatalf("SwarfDir = %s, want %s", got, want)
	}
}

func TestLinksDir(t *testing.T) {
	got := LinksDir("/home/user/project")
	want := filepath.Join("/home/user/project", "swarf", ".links")
	if got != want {
		t.Fatalf("LinksDir = %s, want %s", got, want)
	}
}

func TestProjectSlug(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "myproject")
	os.MkdirAll(project, 0o755)
	slug := ProjectSlug(project)
	if slug != "myproject" {
		t.Fatalf("ProjectSlug = %s, want myproject", slug)
	}
}

func TestStoreProjectDir(t *testing.T) {
	old := StoreDir
	StoreDir = "/tmp/test-store"
	defer func() { StoreDir = old }()

	tmp := t.TempDir()
	project := filepath.Join(tmp, "myproject")
	os.MkdirAll(project, 0o755)
	got := StoreProjectDir(project)
	if got != "/tmp/test-store/myproject" {
		t.Fatalf("StoreProjectDir = %s", got)
	}
}

func TestFindHostRoot(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "project")
	os.MkdirAll(filepath.Join(project, "swarf"), 0o755)

	got := FindHostRoot(project)
	if got == "" {
		t.Fatal("expected to find host root")
	}
}

func TestFindHostRootNotFound(t *testing.T) {
	tmp := t.TempDir()
	got := FindHostRoot(tmp)
	if got != "" {
		t.Fatalf("expected empty, got %s", got)
	}
}

func TestFindHostRootSymlink(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "project")
	os.MkdirAll(project, 0o755)
	// Create .swarf as a symlink
	target := filepath.Join(tmp, "store")
	os.MkdirAll(target, 0o755)
	os.Symlink(target, filepath.Join(project, "swarf"))

	got := FindHostRoot(project)
	if got == "" {
		t.Fatal("expected to find host root via symlink")
	}
}

func TestIsDir(t *testing.T) {
	tmp := t.TempDir()
	if !IsDir(tmp) {
		t.Fatal("expected dir")
	}
	if IsDir(filepath.Join(tmp, "nonexistent")) {
		t.Fatal("expected false for nonexistent")
	}
	f := filepath.Join(tmp, "file")
	os.WriteFile(f, []byte("x"), 0o644)
	if IsDir(f) {
		t.Fatal("expected false for file")
	}
}
