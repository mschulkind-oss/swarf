package mirror

import (
	"os"
	"path/filepath"
	"testing"
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
	if err := TrackedReverse(f.store, f.project, f.manifest, skip); err != nil {
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

	if err := TrackedReverse(store, project, manifest, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(project, "local.md"), "mine")
	assertFile(t, filepath.Join(project, "peer.md"), "theirs")
}
