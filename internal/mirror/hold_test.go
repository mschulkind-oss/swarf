package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/deletehold"
)

// holdFixture is a project (src) and store (dst) that have been through
// one tracked pass, so the manifest lists every file in files.
type holdFixture struct {
	t                  *testing.T
	src, dst, manifest string
	marker             string
}

func newHoldFixture(t *testing.T, files ...string) *holdFixture {
	t.Helper()
	f := &holdFixture{
		t:        t,
		src:      t.TempDir(),
		dst:      t.TempDir(),
		manifest: filepath.Join(t.TempDir(), "m.txt"),
		marker:   filepath.Join(t.TempDir(), "holds", "p.json"),
	}
	for _, rel := range files {
		f.write(rel, "content of "+rel)
	}
	f.pass(Guard{}, nil)
	return f
}

func numbered(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("dir/f%02d.md", i))
	}
	return out
}

func (f *holdFixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.src, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *holdFixture) remove(rels ...string) {
	for _, rel := range rels {
		os.Remove(filepath.Join(f.src, rel))
	}
}

func (f *holdFixture) pass(g Guard, skip Skip) *deletehold.Hold {
	f.t.Helper()
	if g.Marker == "" {
		g.Marker = f.marker
	}
	h, err := TrackedDir(f.src, f.dst, f.manifest, skip, g)
	if err != nil {
		f.t.Fatalf("TrackedDir: %v", err)
	}
	return h
}

func (f *holdFixture) storeHas(rel string) bool {
	_, err := os.Stat(filepath.Join(f.dst, rel))
	return err == nil
}

func (f *holdFixture) manifestHas(rel string) bool {
	_, ok := readManifest(f.manifest)[rel]
	return ok
}

func (f *holdFixture) markerExists() bool {
	_, err := os.Stat(f.marker)
	return err == nil
}

// Scenario 6 in miniature: the manifest lists every file and the project
// has lost all of them.
func TestTrackedDirHoldsWhenProjectLosesEveryFile(t *testing.T) {
	f := newHoldFixture(t, "a.md", "b.md", "sub/c.md")
	f.remove("a.md", "b.md", "sub/c.md")

	h := f.pass(Guard{}, nil)
	if h == nil {
		t.Fatal("expected a delete hold")
	}
	if h.Count != 3 || !h.New {
		t.Errorf("unexpected hold: %+v", h)
	}
	for _, rel := range []string{"a.md", "b.md", "sub/c.md"} {
		if !f.storeHas(rel) {
			t.Errorf("held delete was applied: store lost %s", rel)
		}
		if !f.manifestHas(rel) {
			t.Errorf("held path %s must stay in the manifest", rel)
		}
	}
	if !f.markerExists() {
		t.Error("a hold must write the hold marker")
	}
}

// One tracked file deleted is an ordinary delete, even though it is the
// whole project.
func TestTrackedDirSingleFileDeleteDoesNotHold(t *testing.T) {
	f := newHoldFixture(t, "only.md")
	f.remove("only.md")

	if h := f.pass(Guard{}, nil); h != nil {
		t.Fatalf("a single delete should not hold, got %+v", h)
	}
	if f.storeHas("only.md") {
		t.Error("the delete should have been applied")
	}
	if f.markerExists() {
		t.Error("no hold, no marker")
	}
}

func TestTrackedDirMassDeleteThreshold(t *testing.T) {
	// Ten of twenty is at the threshold on both counts: hold.
	f := newHoldFixture(t, numbered(20)...)
	f.remove(numbered(10)...)
	if h := f.pass(Guard{}, nil); h == nil || h.Count != 10 {
		t.Fatalf("ten of twenty should hold, got %+v", h)
	}
	if !f.storeHas("dir/f00.md") {
		t.Error("held delete was applied")
	}

	// Nine of twenty is under the absolute minimum: apply.
	g := newHoldFixture(t, numbered(20)...)
	g.remove(numbered(9)...)
	if h := g.pass(Guard{}, nil); h != nil {
		t.Fatalf("nine deletes should not hold, got %+v", h)
	}
	if g.storeHas("dir/f00.md") {
		t.Error("the deletes should have been applied")
	}

	// Twelve of twenty-five is under half: apply.
	k := newHoldFixture(t, numbered(25)...)
	k.remove(numbered(12)...)
	if h := k.pass(Guard{}, nil); h != nil {
		t.Fatalf("under half the manifest should not hold, got %+v", h)
	}
}

// During a hold the rest of the pass still happens: new files and edits
// reach the store.
func TestTrackedDirHoldStillCopiesAddsAndEdits(t *testing.T) {
	f := newHoldFixture(t, numbered(12)...)
	f.remove(numbered(11)...)
	f.write("dir/f11.md", "edited during the hold, and longer")
	f.write("new.md", "brand new")

	if h := f.pass(Guard{}, nil); h == nil {
		t.Fatal("expected a hold")
	}
	assertFile(t, filepath.Join(f.dst, "dir", "f11.md"), "edited during the hold, and longer")
	assertFile(t, filepath.Join(f.dst, "new.md"), "brand new")
	if !f.manifestHas("new.md") {
		t.Error("the new file belongs in the manifest")
	}
}

// Dropping excluded paths is deliberate, so they neither count toward a
// hold nor get held.
func TestTrackedDirExcludedPathsDoNotCountTowardHold(t *testing.T) {
	var files []string
	for i := 0; i < 20; i++ {
		files = append(files, fmt.Sprintf(".yolo/state%02d", i))
	}
	files = append(files, "notes.md")
	f := newHoldFixture(t, files...)

	skip := SkipPatterns([]string{".yolo/"})
	if h := f.pass(Guard{}, skip); h != nil {
		t.Fatalf("excluding a directory is not a mass delete, got %+v", h)
	}
	if f.storeHas(".yolo/state00") {
		t.Error("the excluded path should have been pruned from the store")
	}
	assertFile(t, filepath.Join(f.dst, "notes.md"), "content of notes.md")
}

// The exclude prune still happens during a hold on the other paths, and
// the excluded paths are not part of the hold.
func TestTrackedDirHoldStillPrunesExcludedPaths(t *testing.T) {
	f := newHoldFixture(t, "a.md", "b.md", ".yolo/state")
	f.remove("a.md", "b.md")

	h := f.pass(Guard{}, SkipPatterns([]string{".yolo/"}))
	if h == nil || h.Count != 2 {
		t.Fatalf("expected a hold on the two tracked files, got %+v", h)
	}
	if f.storeHas(".yolo/state") || f.manifestHas(".yolo/state") {
		t.Error("the excluded path should be pruned from store and manifest regardless")
	}
}

// Files already gone from the store are not at stake: there is nothing
// left to delete, so there is nothing to hold.
func TestTrackedDirFilesMissingFromStoreAreNotAtStake(t *testing.T) {
	f := newHoldFixture(t, "a.md", "b.md", "c.md")
	f.remove("a.md", "b.md", "c.md")
	os.Remove(filepath.Join(f.dst, "a.md"))
	os.Remove(filepath.Join(f.dst, "b.md"))

	if h := f.pass(Guard{}, nil); h != nil {
		t.Fatalf("one file at stake should not hold, got %+v", h)
	}
	if f.storeHas("c.md") {
		t.Error("the one remaining delete should apply")
	}
}

func TestTrackedDirAllowDeletesAppliesHeldDeletes(t *testing.T) {
	f := newHoldFixture(t, "a.md", "b.md")
	f.remove("a.md", "b.md")
	if h := f.pass(Guard{}, nil); h == nil {
		t.Fatal("setup: expected a hold")
	}

	if h := f.pass(Guard{AllowDeletes: true}, nil); h != nil {
		t.Fatalf("AllowDeletes must not hold, got %+v", h)
	}
	if f.storeHas("a.md") || f.storeHas("b.md") {
		t.Error("AllowDeletes should apply the deletes")
	}
	if f.manifestHas("a.md") {
		t.Error("confirmed deletes leave the manifest")
	}
	if f.markerExists() {
		t.Error("a pass that does not hold removes the marker")
	}
}

// Marker lifecycle: written on the first held pass, kept (with its
// first-seen time) while the hold lasts, removed by the first pass that
// does not hold.
func TestTrackedDirHoldMarkerLifecycle(t *testing.T) {
	f := newHoldFixture(t, "a.md", "b.md")
	f.remove("a.md", "b.md")

	first := f.pass(Guard{}, nil)
	if first == nil || !first.New {
		t.Fatalf("first held pass should report a new hold, got %+v", first)
	}
	again := f.pass(Guard{}, nil)
	if again == nil || again.New {
		t.Fatalf("second held pass should hold, not as new, got %+v", again)
	}
	if !again.FirstSeen.Equal(first.FirstSeen) {
		t.Errorf("first-seen should carry over: %v vs %v", again.FirstSeen, first.FirstSeen)
	}
	onDisk, ok := deletehold.Read(f.marker)
	if !ok || onDisk.Count != 2 || len(onDisk.Sample) != 2 {
		t.Fatalf("marker should record count and sample, got %+v (ok=%v)", onDisk, ok)
	}

	// The files come back: no hold, marker gone.
	f.write("a.md", "content of a.md")
	f.write("b.md", "content of b.md")
	if h := f.pass(Guard{}, nil); h != nil {
		t.Fatalf("restored project should not hold, got %+v", h)
	}
	if f.markerExists() {
		t.Error("marker should be removed once a pass completes without holding")
	}
}

// ClearResolvedHold is how pull ends a hold after its reverse mirror has
// put the files back, without a full forward pass.
func TestClearResolvedHold(t *testing.T) {
	f := newHoldFixture(t, "a.md", "b.md")
	f.remove("a.md", "b.md")
	f.pass(Guard{}, nil)

	if ClearResolvedHold(f.src, f.dst, f.manifest, nil, f.marker) {
		t.Fatal("files still missing: the hold is not resolved")
	}
	if !f.markerExists() {
		t.Fatal("an unresolved hold keeps its marker")
	}

	if err := Dir(f.dst, f.src, nil); err != nil {
		t.Fatal(err)
	}
	if !ClearResolvedHold(f.src, f.dst, f.manifest, nil, f.marker) {
		t.Fatal("files are back: the hold is resolved")
	}
	if f.markerExists() {
		t.Error("a resolved hold loses its marker")
	}
}

// An exclude that matches a directory — a slash glob like "home/ssh/*" or
// a bare name like "keys" — covers everything beneath it, as the walk's
// prune does, even though the pattern alone does not match the files'
// own paths. Those files are excluded, not missing: no hold, and pruned.
func TestTrackedDirDirectoryExcludeCoversNestedFiles(t *testing.T) {
	for _, pattern := range []string{"home/ssh/*", "keys"} {
		t.Run(pattern, func(t *testing.T) {
			var files []string
			for i := 0; i < 12; i++ {
				files = append(files, fmt.Sprintf("home/ssh/keys/k%02d", i))
			}
			files = append(files, "notes.md")
			f := newHoldFixture(t, files...)

			skip := SkipPatterns([]string{pattern})
			if h := f.pass(Guard{}, skip); h != nil {
				t.Fatalf("excluding a directory is not a mass delete, got %+v", h)
			}
			if f.storeHas("home/ssh/keys/k00") || f.manifestHas("home/ssh/keys/k00") {
				t.Error("the excluded files should be pruned from store and manifest")
			}
			assertFile(t, filepath.Join(f.dst, "notes.md"), "content of notes.md")
		})
	}
}

// A hold on real deletes that also sees directory-excluded files clears
// once pull brings the real files back: the excluded files never return
// to the project and must not keep the hold alive.
func TestClearResolvedHoldIgnoresDirectoryExcludedFiles(t *testing.T) {
	files := numbered(10)
	files = append(files, "home/ssh/keys/k00", "home/ssh/keys/k01")
	f := newHoldFixture(t, files...)
	f.remove(numbered(10)...)

	skip := SkipPatterns([]string{"home/ssh/*"})
	h := f.pass(Guard{}, skip)
	if h == nil || h.Count != 10 {
		t.Fatalf("expected a hold on the ten tracked files, got %+v", h)
	}
	if err := Dir(f.dst, f.src, skip); err != nil {
		t.Fatal(err)
	}
	if !ClearResolvedHold(f.src, f.dst, f.manifest, skip, f.marker) {
		t.Fatal("tracked files are back: the hold is resolved")
	}
}
