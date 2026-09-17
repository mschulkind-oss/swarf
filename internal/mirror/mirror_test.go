package mirror

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

	if err := Dir(src, dst); err != nil {
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
	Dir(src, dst)

	// Remove one file from source.
	os.Remove(filepath.Join(src, "delete.txt"))
	Dir(src, dst)

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
	Dir(src, dst)

	// Remove entire subdirectory from source.
	os.RemoveAll(filepath.Join(src, "sub"))
	Dir(src, dst)

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

	Dir(src, dst)

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
	Dir(src, dst)

	// Record mtime of destination file.
	info1, _ := os.Stat(filepath.Join(dst, "a.txt"))

	// Mirror again without changes — destination should not be rewritten.
	Dir(src, dst)
	info2, _ := os.Stat(filepath.Join(dst, "a.txt"))

	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("expected destination file to be unchanged on second mirror")
	}
}

// A broken symlink in src (target no longer exists) must not block the
// mirror. Before the fix this emitted 'stat failed: no such file' and
// left the dst in an incomplete state.
func TestMirrorHandlesBrokenSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	os.WriteFile(filepath.Join(src, "good.txt"), []byte("good"), 0o644)
	// Symlink pointing at a file that will never exist.
	os.Symlink(filepath.Join(src, "nonexistent"), filepath.Join(src, "broken"))

	if err := Dir(src, dst); err != nil {
		t.Fatalf("Dir: %v", err)
	}
	assertFile(t, filepath.Join(dst, "good.txt"), "good")
	if _, err := os.Lstat(filepath.Join(dst, "broken")); err == nil {
		t.Fatal("broken symlink should be skipped, not copied")
	}
}

// When src has a subdirectory but dst already has a REGULAR FILE at
// that path, the mirror must replace it with a directory so copies can
// succeed. Before the fix this aborted with 'mkdir X: not a directory'.
func TestMirrorReplacesFileWithDirectory(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	os.MkdirAll(filepath.Join(src, "conflict"), 0o755)
	os.WriteFile(filepath.Join(src, "conflict", "inner.txt"), []byte("v"), 0o644)

	// dst has 'conflict' as a REGULAR FILE, not a directory.
	os.WriteFile(filepath.Join(dst, "conflict"), []byte("stale"), 0o644)

	if err := Dir(src, dst); err != nil {
		t.Fatalf("Dir should recover from file-where-dir-wanted: %v", err)
	}
	assertFile(t, filepath.Join(dst, "conflict", "inner.txt"), "v")
}

// Same as above but via TrackedDir (the forward-mirror path).
func TestTrackedDirReplacesFileWithDirectory(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.MkdirAll(filepath.Join(src, "conflict"), 0o755)
	os.WriteFile(filepath.Join(src, "conflict", "inner.txt"), []byte("v"), 0o644)

	os.WriteFile(filepath.Join(dst, "conflict"), []byte("stale"), 0o644)

	if err := TrackedDir(src, dst, manifest); err != nil {
		t.Fatalf("TrackedDir should recover from file-where-dir-wanted: %v", err)
	}
	assertFile(t, filepath.Join(dst, "conflict", "inner.txt"), "v")
}

func TestTrackedDirHandlesBrokenSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	os.Symlink(filepath.Join(src, "nope"), filepath.Join(src, "broken"))

	if err := TrackedDir(src, dst, manifest); err != nil {
		t.Fatalf("TrackedDir: %v", err)
	}
	assertFile(t, filepath.Join(dst, "a.txt"), "a")
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

// PendingFiles must answer for the copy pass, not for a lookalike walk of
// its own. Every case below is one where a hand-rolled size/mtime compare
// disagrees with what the mirror actually does, and each disagreement is
// permanent: the reported file never becomes un-pending, because no copy
// is ever coming.

func TestPendingFilesZeroAfterMirror(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	external := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.WriteFile(filepath.Join(src, "plain.txt"), []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested"), 0o644)
	// A symlink to a file outside src — the mirror copies the target's
	// bytes, so dst holds a file far bigger than the link's own size.
	os.WriteFile(filepath.Join(external, "real.txt"), []byte("linked content, much longer than the link"), 0o644)
	os.Symlink(filepath.Join(external, "real.txt"), filepath.Join(src, "link.txt"))

	if err := TrackedDir(src, dst, manifest); err != nil {
		t.Fatalf("TrackedDir: %v", err)
	}
	if got := PendingFiles(src, dst); got != 0 {
		t.Fatalf("nothing is pending right after a mirror pass, got %d", got)
	}
}

func TestPendingFilesCountsUnmirroredWork(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	TrackedDir(src, dst, manifest)

	os.WriteFile(filepath.Join(src, "new.txt"), []byte("brand new"), 0o644)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a, now edited"), 0o644)

	if got := PendingFiles(src, dst); got != 2 {
		t.Fatalf("expected the new file and the edit to be pending, got %d", got)
	}

	TrackedDir(src, dst, manifest)
	if got := PendingFiles(src, dst); got != 0 {
		t.Fatalf("expected 0 pending after the mirror caught up, got %d", got)
	}
}

func TestPendingFilesIgnoresBrokenSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.WriteFile(filepath.Join(src, "good.txt"), []byte("good"), 0o644)
	os.Symlink(filepath.Join(src, "nonexistent"), filepath.Join(src, "broken"))
	TrackedDir(src, dst, manifest)

	// The mirror skips a broken link permanently, so pending would never
	// clear if we counted it.
	if got := PendingFiles(src, dst); got != 0 {
		t.Fatalf("a broken symlink is not pending work, got %d", got)
	}
}

func TestPendingFilesIgnoresSymlinkedDirectory(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	external := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.WriteFile(filepath.Join(external, "inner.txt"), []byte("x"), 0o644)
	os.Symlink(external, filepath.Join(src, "dirlink"))
	TrackedDir(src, dst, manifest)

	// A link to a directory resolves to a directory, which the copy pass
	// cannot write as a file. Not pending work either.
	if got := PendingFiles(src, dst); got != 0 {
		t.Fatalf("a symlinked directory is not pending work, got %d", got)
	}
}

func TestPendingFilesMissingSource(t *testing.T) {
	if got := PendingFiles(filepath.Join(t.TempDir(), "nope"), t.TempDir()); got != 0 {
		t.Fatalf("a missing source has nothing pending, got %d", got)
	}
}
