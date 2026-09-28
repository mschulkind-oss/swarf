package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reverseFixture is a store (src) and a project (dst) that have been
// through one forward pass, so the manifest lists every project file.
type reverseFixture struct {
	t                        *testing.T
	store, project, manifest string
}

func newReverseFixture(t *testing.T, files ...string) *reverseFixture {
	t.Helper()
	f := &reverseFixture{
		t:        t,
		store:    t.TempDir(),
		project:  t.TempDir(),
		manifest: filepath.Join(t.TempDir(), "m.txt"),
	}
	for _, rel := range files {
		writeFile(t, filepath.Join(f.project, rel), "content of "+rel)
	}
	if _, err := TrackedDir(f.project, f.store, f.manifest, nil, Guard{}); err != nil {
		t.Fatalf("TrackedDir: %v", err)
	}
	return f
}

func (f *reverseFixture) reverse(skip Skip) {
	f.t.Helper()
	if err := TrackedReverse(f.store, f.project, f.manifest, skip, nil); err != nil {
		f.t.Fatalf("TrackedReverse: %v", err)
	}
}

func (f *reverseFixture) projectHas(rel string) bool {
	_, err := os.Lstat(filepath.Join(f.project, rel))
	return err == nil
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A project file the manifest has never listed — saved after the last
// forward pass, e.g. while pull was downloading a peer — is not the
// store's to delete.
func TestTrackedReverseLeavesUntrackedProjectFile(t *testing.T) {
	f := newReverseFixture(t, "a.md")
	writeFile(t, filepath.Join(f.project, "fresh.md"), "just saved")

	f.reverse(nil)

	assertFile(t, filepath.Join(f.project, "fresh.md"), "just saved")
	assertFile(t, filepath.Join(f.project, "a.md"), "content of a.md")
	if _, ok := readManifest(f.manifest)["fresh.md"]; !ok {
		t.Error("the manifest should now list fresh.md: it is in the project pull left behind")
	}
}

// A tracked file that left the store (a peer deleted it) leaves the
// project too, along with any directory that empties.
func TestTrackedReverseDeletesTrackedFileGoneFromStore(t *testing.T) {
	f := newReverseFixture(t, "keep.md", "sub/doomed.md")
	os.Remove(filepath.Join(f.store, "sub", "doomed.md"))

	f.reverse(nil)

	if f.projectHas("sub/doomed.md") {
		t.Error("sub/doomed.md was synced before and is gone from the store: it should be deleted")
	}
	if f.projectHas("sub") {
		t.Error("the emptied sub/ directory should be removed")
	}
	assertFile(t, filepath.Join(f.project, "keep.md"), "content of keep.md")
	if _, ok := readManifest(f.manifest)["sub/doomed.md"]; ok {
		t.Error("the manifest should drop the deleted file")
	}
}

// The reverse direction still copies store content into the project.
func TestTrackedReverseCopiesStoreFiles(t *testing.T) {
	f := newReverseFixture(t, "a.md")
	writeFile(t, filepath.Join(f.store, "peer/new.md"), "from a peer")

	f.reverse(nil)

	assertFile(t, filepath.Join(f.project, "peer/new.md"), "from a peer")
	if _, ok := readManifest(f.manifest)["peer/new.md"]; !ok {
		t.Error("the manifest should list the file pull just copied in")
	}
}

// Excluded paths are never in the store, so they are never copied, never
// deleted, and never recorded — even when an old manifest lists them.
func TestTrackedReverseLeavesExcludedPaths(t *testing.T) {
	f := newReverseFixture(t, "a.md", "home/ssh/keys/k0", "scratch.tmp")
	skip := SkipPatterns([]string{"home/ssh/*", "*.tmp"})
	os.RemoveAll(filepath.Join(f.store, "home"))
	os.Remove(filepath.Join(f.store, "scratch.tmp"))

	f.reverse(skip)

	assertFile(t, filepath.Join(f.project, "home/ssh/keys/k0"), "content of home/ssh/keys/k0")
	assertFile(t, filepath.Join(f.project, "scratch.tmp"), "content of scratch.tmp")
	m := readManifest(f.manifest)
	if _, ok := m["home/ssh/keys/k0"]; ok {
		t.Error("an excluded file should not be recorded in the manifest")
	}
	if _, ok := m["scratch.tmp"]; ok {
		t.Error("an excluded file should not be recorded in the manifest")
	}
}

// With no manifest at all (first pull on this machine), nothing is
// deleted: there is no record of anything swarf synced.
func TestTrackedReverseNoManifestDeletesNothing(t *testing.T) {
	store, project := t.TempDir(), t.TempDir()
	manifest := filepath.Join(t.TempDir(), "m.txt")
	writeFile(t, filepath.Join(project, "local.md"), "mine")
	writeFile(t, filepath.Join(store, "peer.md"), "theirs")

	if err := TrackedReverse(store, project, manifest, nil, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(project, "local.md"), "mine")
	assertFile(t, filepath.Join(project, "peer.md"), "theirs")
}

// A symlink to a directory is never copied into the store, so it is not
// something swarf synced and the reverse mirror must not delete it for
// being absent from the store.
func TestTrackedReverseKeepsSymlinkToDirectory(t *testing.T) {
	f := newReverseFixture(t, "a.md")
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "x.md"), "outside the drawer")
	if err := os.Symlink(target, filepath.Join(f.project, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := TrackedDir(f.project, f.store, f.manifest, nil, Guard{}); err != nil {
		t.Fatalf("TrackedDir: %v", err)
	}
	if _, ok := readManifest(f.manifest)["linked"]; ok {
		t.Error("a link to a directory is never copied, so the manifest should not list it")
	}

	f.reverse(nil)

	if !f.projectHas("linked") {
		t.Error("the link was never synced to the store: the reverse mirror should keep it")
	}
	if _, ok := readManifest(f.manifest)["linked"]; ok {
		t.Error("the rewritten manifest should not list the link either")
	}
}

// failReads makes the mirror's reads of the named files fail for the
// rest of the test, standing in for a file the copy pass cannot read.
func failReads(t *testing.T, names ...string) {
	t.Helper()
	orig := readFile
	t.Cleanup(func() { readFile = orig })
	readFile = func(p string) ([]byte, error) {
		for _, n := range names {
			if filepath.Base(p) == n {
				return nil, fmt.Errorf("read %s: injected failure", p)
			}
		}
		return orig(p)
	}
}

// A file whose forward copy failed never reached the store. The forward
// pass does not record it as synced, so the reverse mirror leaves it.
func TestTrackedReverseKeepsFileWhoseForwardCopyFailed(t *testing.T) {
	f := newReverseFixture(t, "a.md")
	writeFile(t, filepath.Join(f.project, "locked.md"), "cannot be read")
	failReads(t, "locked.md")

	TrackedDir(f.project, f.store, f.manifest, nil, Guard{}) // the copy failure is expected
	if _, ok := readManifest(f.manifest)["locked.md"]; ok {
		t.Error("a file that never reached the store should not be recorded as synced")
	}

	f.reverse(nil)
	assertFile(t, filepath.Join(f.project, "locked.md"), "cannot be read")
}

// A file whose forward copy fails but that the store already has from an
// earlier pass stays recorded, so the forward mirror does not treat it
// as deleted and prune the store's copy.
func TestTrackedDirKeepsSyncedFileWhoseCopyFails(t *testing.T) {
	f := newReverseFixture(t, "a.md", "b.md")
	writeFile(t, filepath.Join(f.project, "b.md"), "an edit the next copy will fail to read")
	failReads(t, "b.md")

	TrackedDir(f.project, f.store, f.manifest, nil, Guard{}) // the copy failure is expected
	if _, ok := readManifest(f.manifest)["b.md"]; !ok {
		t.Error("b.md is still in the project and the store: the manifest should keep it")
	}
	assertFile(t, filepath.Join(f.store, "b.md"), "content of b.md")
}

// The store still has a tracked path when a dangling symlink sits there:
// the path is present, it just resolves to nothing. The reverse mirror
// checks presence without following links, so the project copy stays.
func TestTrackedReverseKeepsFileWhenStoreHasDanglingLink(t *testing.T) {
	f := newReverseFixture(t, "a.md")
	storeCopy := filepath.Join(f.store, "a.md")
	os.Remove(storeCopy)
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), storeCopy); err != nil {
		t.Fatal(err)
	}

	f.reverse(nil)

	assertFile(t, filepath.Join(f.project, "a.md"), "content of a.md")
}

// Project files written after the stamps were taken — a save while pull
// was downloading peers — are neither overwritten with the store's copy
// nor deleted for leaving the store, and stay in the manifest; files the
// stamps still match take the store's changes as usual.
func TestTrackedReverseSparesFilesChangedSinceStamps(t *testing.T) {
	f := newReverseFixture(t, "edited.md", "doomed.md", "stale.md", "gone.md")
	stamps := TakeStamps(f.project, nil)

	// The pull's peers change the store...
	writeFile(t, filepath.Join(f.store, "edited.md"), "peer version")
	writeFile(t, filepath.Join(f.store, "stale.md"), "peer version")
	writeFile(t, filepath.Join(f.store, "made.md"), "peer version")
	os.Remove(filepath.Join(f.store, "doomed.md"))
	os.Remove(filepath.Join(f.store, "gone.md"))
	// ...while the user saves in the project.
	writeFile(t, filepath.Join(f.project, "edited.md"), "the user's longer save")
	writeFile(t, filepath.Join(f.project, "doomed.md"), "the user's longer save")
	writeFile(t, filepath.Join(f.project, "made.md"), "the user's new file")

	if err := TrackedReverse(f.store, f.project, f.manifest, nil, stamps); err != nil {
		t.Fatalf("TrackedReverse: %v", err)
	}

	assertFile(t, filepath.Join(f.project, "edited.md"), "the user's longer save")
	assertFile(t, filepath.Join(f.project, "doomed.md"), "the user's longer save")
	assertFile(t, filepath.Join(f.project, "made.md"), "the user's new file")
	assertFile(t, filepath.Join(f.project, "stale.md"), "peer version")
	if f.projectHas("gone.md") {
		t.Error("gone.md is unchanged since the stamps and gone from the store: it should be deleted")
	}
	m := readManifest(f.manifest)
	for _, rel := range []string{"edited.md", "doomed.md", "made.md"} {
		if _, ok := m[rel]; !ok {
			t.Errorf("the manifest should still list %s", rel)
		}
	}
}

// Stamps of a missing directory are empty, not nil, so every file that
// appears there later counts as changed.
func TestTakeStampsMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	s := TakeStamps(root, nil)
	if s == nil {
		t.Fatal("TakeStamps of a missing root should be empty, not nil")
	}
	writeFile(t, filepath.Join(root, "new.md"), "x")
	if !s.changed(root, "new.md") {
		t.Error("a file created after the stamps should count as changed")
	}
	if s.changed(root, "never.md") {
		t.Error("a file that does not exist is not changed")
	}
}

// Protected picks out, from the given paths, the ones TrackedReverse
// would spare: changed in the project since the stamps and not excluded.
func TestProtected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "same.md"), "as stamped")
	writeFile(t, filepath.Join(root, "saved.md"), "as stamped")
	writeFile(t, filepath.Join(root, "cache", "saved.md"), "as stamped")
	stamps := TakeStamps(root, nil)
	writeFile(t, filepath.Join(root, "saved.md"), "saved since the stamps")
	writeFile(t, filepath.Join(root, "cache", "saved.md"), "saved since the stamps")
	writeFile(t, filepath.Join(root, "created.md"), "new")

	skip := SkipPatterns([]string{"cache/"})
	got := Protected(root, skip, stamps, []string{"same.md", "saved.md", "cache/saved.md", "created.md", "absent.md"})
	if strings.Join(got, ",") != "saved.md,created.md" {
		t.Errorf("Protected = %v, want [saved.md created.md]", got)
	}
	if got := Protected(root, nil, nil, []string{"saved.md"}); got != nil {
		t.Errorf("nil stamps protect nothing, got %v", got)
	}
}

// A save counts as a change since the stamps when either the size or the
// modification time differs: an edit that keeps the size, or one whose
// modification time is set back to what it was.
func TestStampsChangedBySizeOrModTime(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "same-size.md"), "aaaa")
	writeFile(t, filepath.Join(root, "same-time.md"), "aaaa")
	writeFile(t, filepath.Join(root, "untouched.md"), "aaaa")
	stamped := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, rel := range []string{"same-size.md", "same-time.md", "untouched.md"} {
		if err := os.Chtimes(filepath.Join(root, rel), stamped, stamped); err != nil {
			t.Fatal(err)
		}
	}
	s := TakeStamps(root, nil)

	writeFile(t, filepath.Join(root, "same-size.md"), "bbbb")
	writeFile(t, filepath.Join(root, "same-time.md"), "a longer save")
	if err := os.Chtimes(filepath.Join(root, "same-time.md"), stamped, stamped); err != nil {
		t.Fatal(err)
	}

	if !s.changed(root, "same-size.md") {
		t.Error("an edit that keeps the size but moves the modification time is a change")
	}
	if !s.changed(root, "same-time.md") {
		t.Error("an edit that changes the size under the old modification time is a change")
	}
	if s.changed(root, "untouched.md") {
		t.Error("a file as stamped is not a change")
	}
}

// TrackedReverseKeeping leaves each kept path alone — a file the project
// deleted is not copied back from the store — and keeps it in the
// manifest, so the next forward pass still sees the delete (and holds
// it again); every other path is mirrored as TrackedReverse would.
func TestTrackedReverseKeepingLeavesKeptPaths(t *testing.T) {
	f := newReverseFixture(t, "held.md", "other.md")
	os.Remove(filepath.Join(f.project, "held.md"))
	os.Remove(filepath.Join(f.project, "other.md"))
	writeFile(t, filepath.Join(f.store, "new.md"), "from a peer")

	if err := TrackedReverseKeeping(f.store, f.project, f.manifest, nil, nil, []string{"held.md"}); err != nil {
		t.Fatalf("TrackedReverseKeeping: %v", err)
	}
	if f.projectHas("held.md") {
		t.Error("a kept path must not be copied back into the project")
	}
	if !f.projectHas("other.md") || !f.projectHas("new.md") {
		t.Error("paths not kept should be mirrored back as TrackedReverse does")
	}
	if _, ok := readManifest(f.manifest)["held.md"]; !ok {
		t.Error("a kept path should stay in the manifest")
	}
}
