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

	if err := Dir(src, dst, nil); err != nil {
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
	Dir(src, dst, nil)

	// Remove one file from source.
	os.Remove(filepath.Join(src, "delete.txt"))
	Dir(src, dst, nil)

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
	Dir(src, dst, nil)

	// Remove entire subdirectory from source.
	os.RemoveAll(filepath.Join(src, "sub"))
	Dir(src, dst, nil)

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

	Dir(src, dst, nil)

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
	Dir(src, dst, nil)

	// Record mtime of destination file.
	info1, _ := os.Stat(filepath.Join(dst, "a.txt"))

	// Mirror again without changes — destination should not be rewritten.
	Dir(src, dst, nil)
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

	if err := Dir(src, dst, nil); err != nil {
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

	if err := Dir(src, dst, nil); err != nil {
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

	if _, err := TrackedDir(src, dst, manifest, nil, Guard{}); err != nil {
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

	if _, err := TrackedDir(src, dst, manifest, nil, Guard{}); err != nil {
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

	if _, err := TrackedDir(src, dst, manifest, nil, Guard{}); err != nil {
		t.Fatalf("TrackedDir: %v", err)
	}
	if got := PendingFiles(src, dst, nil); got != 0 {
		t.Fatalf("nothing is pending right after a mirror pass, got %d", got)
	}
}

func TestPendingFilesCountsUnmirroredWork(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	TrackedDir(src, dst, manifest, nil, Guard{})

	os.WriteFile(filepath.Join(src, "new.txt"), []byte("brand new"), 0o644)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a, now edited"), 0o644)

	if got := PendingFiles(src, dst, nil); got != 2 {
		t.Fatalf("expected the new file and the edit to be pending, got %d", got)
	}

	TrackedDir(src, dst, manifest, nil, Guard{})
	if got := PendingFiles(src, dst, nil); got != 0 {
		t.Fatalf("expected 0 pending after the mirror caught up, got %d", got)
	}
}

func TestPendingFilesIgnoresBrokenSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.WriteFile(filepath.Join(src, "good.txt"), []byte("good"), 0o644)
	os.Symlink(filepath.Join(src, "nonexistent"), filepath.Join(src, "broken"))
	TrackedDir(src, dst, manifest, nil, Guard{})

	// The mirror skips a broken link permanently, so pending would never
	// clear if we counted it.
	if got := PendingFiles(src, dst, nil); got != 0 {
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
	TrackedDir(src, dst, manifest, nil, Guard{})

	// A link to a directory resolves to a directory, which the copy pass
	// cannot write as a file. Not pending work either.
	if got := PendingFiles(src, dst, nil); got != 0 {
		t.Fatalf("a symlinked directory is not pending work, got %d", got)
	}
}

func TestPendingFilesMissingSource(t *testing.T) {
	if got := PendingFiles(filepath.Join(t.TempDir(), "nope"), t.TempDir(), nil); got != 0 {
		t.Fatalf("a missing source has nothing pending, got %d", got)
	}
}

func TestSkipPatterns(t *testing.T) {
	skip := SkipPatterns([]string{".yolo/", "*.tmp", "home/ssh/*"})
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{".yolo", true, true},                    // the directory itself
		{".yolo/home/bash_history", false, true}, // anything under it
		{"forms/.yolo/state.db", false, true},    // at any depth
		{"notes/.yolonot/keep.md", false, false}, // not a prefix match
		{"build/out.tmp", false, true},           // base-name glob, any depth
		{"out.tmpx", false, false},
		{"home/ssh/id_rsa", false, true}, // path glob
		{"home/ssh", true, false},        // the glob needs a segment after it
		{"home/notes.md", false, false},
		{"", false, false},
		{".", false, false},
	}
	for _, c := range cases {
		if got := skip(c.rel, c.isDir); got != c.want {
			t.Errorf("skip(%q, isDir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}

	if SkipPatterns(nil) != nil {
		t.Error("no patterns must yield a nil Skip (mirror everything)")
	}
	if SkipPatterns([]string{"", "   "}) != nil {
		t.Error("blank patterns must yield a nil Skip")
	}
}

// An excluded path already in the store has to leave it, or excluding it
// does nothing for the remote — which is the whole reason to exclude a
// 300 MB directory of jail state. The manifest already knows the path was
// ours, so the existing delete pass does it.
func TestTrackedDirPrunesNewlyExcludedContent(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.MkdirAll(filepath.Join(src, ".yolo", "home"), 0o755)
	os.WriteFile(filepath.Join(src, "notes.md"), []byte("keep me"), 0o644)
	os.WriteFile(filepath.Join(src, ".yolo", "home", "bash_history"), []byte("secrets"), 0o644)

	// Synced before anyone thought to exclude it.
	if _, err := TrackedDir(src, dst, manifest, nil, Guard{}); err != nil {
		t.Fatalf("TrackedDir: %v", err)
	}
	assertFile(t, filepath.Join(dst, ".yolo", "home", "bash_history"), "secrets")

	// Now excluded.
	skip := SkipPatterns([]string{".yolo/"})
	if _, err := TrackedDir(src, dst, manifest, skip, Guard{}); err != nil {
		t.Fatalf("TrackedDir with skip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".yolo", "home", "bash_history")); !os.IsNotExist(err) {
		t.Error("an excluded file already in the store must be pruned from it")
	}
	assertFile(t, filepath.Join(dst, "notes.md"), "keep me")

	// And it stays in the project, which is where it belongs.
	assertFile(t, filepath.Join(src, ".yolo", "home", "bash_history"), "secrets")

	if got := PendingFiles(src, dst, skip); got != 0 {
		t.Errorf("an excluded file is not pending work, got %d", got)
	}
}

// The reverse mirror deletes whatever the store lacks, and the store
// lacks every excluded path by design. Without the Skip on this side,
// excluding a directory would delete it out of the user's project on the
// next pull — the exact opposite of what they asked for.
func TestDirLeavesExcludedDestinationContentAlone(t *testing.T) {
	store := t.TempDir()
	project := t.TempDir()

	os.WriteFile(filepath.Join(store, "shared.md"), []byte("from a peer"), 0o644)
	os.MkdirAll(filepath.Join(project, ".yolo"), 0o755)
	os.WriteFile(filepath.Join(project, ".yolo", "state.db"), []byte("local jail state"), 0o644)
	os.WriteFile(filepath.Join(project, "stale.md"), []byte("deleted on the peer"), 0o644)

	if err := Dir(store, project, SkipPatterns([]string{".yolo/"})); err != nil {
		t.Fatalf("Dir: %v", err)
	}

	assertFile(t, filepath.Join(project, ".yolo", "state.db"), "local jail state")
	assertFile(t, filepath.Join(project, "shared.md"), "from a peer")
	if _, err := os.Stat(filepath.Join(project, "stale.md")); !os.IsNotExist(err) {
		t.Error("a non-excluded file missing from the store should still be deleted")
	}
}

func TestDirDoesNotCopyExcludedContent(t *testing.T) {
	store := t.TempDir()
	project := t.TempDir()

	// A peer that had no exclude pushed its jail state into the store.
	os.MkdirAll(filepath.Join(store, ".yolo"), 0o755)
	os.WriteFile(filepath.Join(store, ".yolo", "peer.db"), []byte("peer jail state"), 0o644)
	os.WriteFile(filepath.Join(store, "shared.md"), []byte("ok"), 0o644)

	if err := Dir(store, project, SkipPatterns([]string{".yolo/"})); err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".yolo", "peer.db")); !os.IsNotExist(err) {
		t.Error("an excluded path must not be written into the project either")
	}
	assertFile(t, filepath.Join(project, "shared.md"), "ok")
}

// Scenario 3 (Sync Scenarios): the first forward pass for a project has
// no manifest yet, so it cannot tell a store file the project never had
// from one the user deleted. It copies the project in, deletes nothing
// from the store even when the project is empty, and writes the
// manifest so later passes can tell.
func TestTrackedDirWithoutManifestDeletesNothing(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")

	os.MkdirAll(filepath.Join(dst, "docs"), 0o755)
	os.WriteFile(filepath.Join(dst, "docs", "peer.md"), []byte("from a peer"), 0o644)

	h, err := TrackedDir(src, dst, manifest, nil, Guard{})
	if err != nil {
		t.Fatalf("TrackedDir from an empty project: %v", err)
	}
	if h != nil {
		t.Errorf("nothing is at stake without a manifest, got hold %+v", h)
	}
	assertFile(t, filepath.Join(dst, "docs", "peer.md"), "from a peer")

	os.WriteFile(filepath.Join(src, "mine.md"), []byte("mine"), 0o644)
	if _, err := TrackedDir(src, dst, manifest, nil, Guard{}); err != nil {
		t.Fatalf("TrackedDir: %v", err)
	}
	assertFile(t, filepath.Join(dst, "docs", "peer.md"), "from a peer")
	assertFile(t, filepath.Join(dst, "mine.md"), "mine")
	if _, ok := readManifest(manifest)["mine.md"]; !ok {
		t.Error("the pass should record mine.md in the manifest")
	}
}
