package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMirrorDirCopiesFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world"), 0o644)

	if err := mirrorDir(src, dst); err != nil {
		t.Fatal(err)
	}

	assertFile(t, filepath.Join(dst, "a.txt"), "hello")
	assertFile(t, filepath.Join(dst, "sub", "b.txt"), "world")
}

func TestMirrorDirDeletesStaleFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Initial mirror with two files.
	os.WriteFile(filepath.Join(src, "keep.txt"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(src, "delete.txt"), []byte("gone"), 0o644)
	mirrorDir(src, dst)

	// Remove one file from source.
	os.Remove(filepath.Join(src, "delete.txt"))
	mirrorDir(src, dst)

	assertFile(t, filepath.Join(dst, "keep.txt"), "keep")
	if _, err := os.Stat(filepath.Join(dst, "delete.txt")); !os.IsNotExist(err) {
		t.Fatal("expected delete.txt to be removed from dst")
	}
}

func TestMirrorDirDeletesStaleDirectories(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "f.txt"), []byte("x"), 0o644)
	mirrorDir(src, dst)

	// Remove entire subdirectory from source.
	os.RemoveAll(filepath.Join(src, "sub"))
	mirrorDir(src, dst)

	if _, err := os.Stat(filepath.Join(dst, "sub")); !os.IsNotExist(err) {
		t.Fatal("expected sub/ to be removed from dst")
	}
}

func TestMirrorDirFollowsSymlinks(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	external := t.TempDir()

	// Create a file outside src and symlink to it.
	os.WriteFile(filepath.Join(external, "real.txt"), []byte("linked"), 0o644)
	os.Symlink(filepath.Join(external, "real.txt"), filepath.Join(src, "link.txt"))

	mirrorDir(src, dst)

	// dst should have a regular file with the content, not a symlink.
	assertFile(t, filepath.Join(dst, "link.txt"), "linked")
	fi, err := os.Lstat(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected regular file in dst, got symlink")
	}
}

func TestMirrorDirSkipsUnchanged(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644)
	mirrorDir(src, dst)

	// Record mtime of destination file.
	info1, _ := os.Stat(filepath.Join(dst, "a.txt"))

	// Mirror again without changes — destination should not be rewritten.
	mirrorDir(src, dst)
	info2, _ := os.Stat(filepath.Join(dst, "a.txt"))

	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("expected destination file to be unchanged on second mirror")
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s: got %q, want %q", path, string(data), want)
	}
}
