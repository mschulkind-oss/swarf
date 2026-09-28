package push

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

// TestPushGitTakeOver: with the git backend, --take-over force-pushes
// this machine's machine branch over commits a different store with the
// same machine_id pushed, where a plain push is refused.
func TestPushGitTakeOver(t *testing.T) {
	testutil.InitializedSwarf(t)
	bare := testutil.BareRemote(t)
	other := t.TempDir()
	for _, args := range [][]string{
		{"-C", paths.StoreDir, "remote", "add", "origin", bare},
		{"-C", other, "init"},
		{"-C", other, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "--allow-empty", "-m", "other store"},
		{"-C", other, "push", bare, "HEAD:refs/heads/machines/box"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: bare, Debounce: "5s", MachineID: "box"})

	if err := Run(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "--take-over") {
		t.Fatalf("a plain push over another store's machine branch should be refused naming --take-over, got %v", err)
	}
	if err := Run(context.Background(), Options{TakeOver: true}); err != nil {
		t.Fatalf("push --take-over: %v", err)
	}
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "refs/heads/machines/box").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, head := strings.TrimSpace(string(out)), gitexec.RevParseHEAD(paths.StoreDir); got != head {
		t.Errorf("after --take-over remote machines/box = %q, want the store's HEAD %q", got, head)
	}
}

// gitPushSetup is a store configured for the git backend as machine
// "box", with origin at remote.
func gitPushSetup(t *testing.T, remote string) string {
	t.Helper()
	repo := testutil.InitializedSwarf(t)
	if out, err := exec.Command("git", "-C", paths.StoreDir, "remote", "add", "origin", remote).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: remote, Debounce: "5s", MachineID: "box"})
	return repo
}

// TestPushGitUnreachableRemoteFails: a git push that did not reach the
// remote is an error, both when the push had new files and when it only
// retried earlier commits; it is never "Nothing to push."
func TestPushGitUnreachableRemoteFails(t *testing.T) {
	gitPushSetup(t, filepath.Join(t.TempDir(), "missing.git"))
	var out bytes.Buffer
	oldOut := console.Stdout
	console.Stdout = &out
	defer func() { console.Stdout = oldOut }()

	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("x"), 0o644)
	if err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("a push that could not reach the remote should fail")
	}
	if err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("a retry that could not reach the remote should fail")
	}
	if strings.Contains(out.String(), "Nothing to push") || strings.Contains(out.String(), "Pushed") {
		t.Errorf("a failed push must not be reported as done:\n%s", out.String())
	}
}

// TestPushGitTakeOverRefusedByRemote: --take-over the remote refuses
// (receive.denyNonFastForwards) is an error, not a success.
func TestPushGitTakeOverRefusedByRemote(t *testing.T) {
	bare := testutil.BareRemote(t)
	gitPushSetup(t, bare)
	other := t.TempDir()
	for _, args := range [][]string{
		{"-C", other, "init"},
		{"-C", other, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "--allow-empty", "-m", "other store"},
		{"-C", other, "push", bare, "HEAD:refs/heads/machines/box"},
		{"--git-dir", bare, "config", "receive.denyNonFastForwards", "true"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := Run(context.Background(), Options{TakeOver: true}); err == nil {
		t.Fatal("push --take-over the remote refused should fail")
	}
}

// TestPushGitRetryReportsPush: a push that only sends commits an earlier
// failed push left behind says it pushed, not "Nothing to push."
func TestPushGitRetryReportsPush(t *testing.T) {
	bare := testutil.BareRemote(t)
	gitPushSetup(t, filepath.Join(t.TempDir(), "missing.git"))
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("x"), 0o644)
	Run(context.Background(), Options{}) // fails: committed locally only

	gitexec.SetRemoteURL(paths.StoreDir, "origin", bare)
	var out bytes.Buffer
	oldOut := console.Stdout
	console.Stdout = &out
	defer func() { console.Stdout = oldOut }()
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("retry push: %v", err)
	}
	if strings.Contains(out.String(), "Nothing to push") || !strings.Contains(out.String(), "Pushed") {
		t.Errorf("a retry that pushed should say so:\n%s", out.String())
	}
}

// TestPushGitFoldsAndRetiresLegacyBranch: a git-backend push folds the
// remote's legacy branch (main, pushed before machine branches) into the
// store, pushes it to the machine branch, and then retires the legacy
// branch by deleting it.
func TestPushGitFoldsAndRetiresLegacyBranch(t *testing.T) {
	bare := testutil.BareRemote(t)
	gitPushSetup(t, bare)
	staging := t.TempDir()
	os.WriteFile(filepath.Join(staging, "legacy.md"), []byte("from before"), 0o644)
	for _, args := range [][]string{
		{"-C", staging, "init"},
		{"-C", staging, "add", "-A"},
		{"-C", staging, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "-m", "older swarf"},
		{"-C", staging, "push", bare, "HEAD:refs/heads/main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	legacyTip, err := exec.Command("git", "--git-dir", bare, "rev-parse", "refs/heads/main").Output()
	if err != nil {
		t.Fatal(err)
	}

	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(paths.StoreDir, "legacy.md")); err != nil || string(got) != "from before" {
		t.Errorf("the push should fold main's legacy.md into the store, got %q, %v", got, err)
	}
	if err := exec.Command("git", "--git-dir", bare, "merge-base", "--is-ancestor", strings.TrimSpace(string(legacyTip)), "refs/heads/machines/box").Run(); err != nil {
		t.Errorf("machines/box should hold main's old tip: %v", err)
	}
	if out, _ := exec.Command("git", "--git-dir", bare, "for-each-ref", "--format=%(refname)", "refs/heads/").Output(); strings.TrimSpace(string(out)) != "refs/heads/machines/box" {
		t.Errorf("main should be retired, leaving only the machine branch:\n%s", out)
	}
}

// pushLegacyBranch pushes files, as one commit on a fresh history, to
// the legacy branch main on bare: what an older swarf left there.
func pushLegacyBranch(t *testing.T, bare string, files map[string]string) {
	t.Helper()
	staging := t.TempDir()
	for rel, content := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(staging, rel)), 0o755)
		os.WriteFile(filepath.Join(staging, rel), []byte(content), 0o644)
	}
	for _, args := range [][]string{
		{"-C", staging, "init"},
		{"-C", staging, "add", "-A"},
		{"-C", staging, "-c", "user.email=o@o", "-c", "user.name=o", "commit", "-m", "older swarf"},
		{"-C", staging, "push", "--force", bare, "HEAD:refs/heads/main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// heldDrawer registers the project repo as a drawer with 20 files,
// pushes them, deletes 12 and pushes again, so a delete hold keeps the
// 12 in the store. Returns the drawer's slug.
func heldDrawer(t *testing.T, repo string) string {
	t.Helper()
	slug := filepath.Base(repo)
	if err := config.RegisterDrawer(slug, repo); err != nil {
		t.Fatal(err)
	}
	swarfDir := paths.SwarfDir(repo)
	for i := 0; i < 20; i++ {
		os.WriteFile(filepath.Join(swarfDir, fmt.Sprintf("f%02d.md", i)), []byte("x"), 0o644)
	}
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("push: %v", err)
	}
	for i := 0; i < 12; i++ {
		os.Remove(filepath.Join(swarfDir, fmt.Sprintf("f%02d.md", i)))
	}
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := os.Stat(paths.DeleteHoldMarker(slug)); err != nil {
		t.Fatalf("deleting 12 of 20 files should hold the deletes: %v", err)
	}
	return slug
}

// TestPushAllowDeletesWithLegacyBranchApplies: 'swarf push
// --allow-deletes' applies the held deletes even when the push first
// folds in a legacy branch; folding in must not restore the held files
// into the project or the store.
func TestPushAllowDeletesWithLegacyBranchApplies(t *testing.T) {
	bare := testutil.BareRemote(t)
	repo := gitPushSetup(t, bare)
	slug := heldDrawer(t, repo)
	pushLegacyBranch(t, bare, map[string]string{slug + "/legacy.md": "from before"})

	if err := Run(context.Background(), Options{AllowDeletes: true}); err != nil {
		t.Fatalf("push --allow-deletes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.SwarfDir(repo), "f00.md")); err == nil {
		t.Error("push --allow-deletes restored f00.md into the project")
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, slug, "f00.md")); err == nil {
		t.Error("push --allow-deletes left f00.md in the store")
	}
	if _, err := os.Stat(paths.DeleteHoldMarker(slug)); err == nil {
		t.Error("push --allow-deletes should clear the hold")
	}
	if got, err := os.ReadFile(filepath.Join(paths.SwarfDir(repo), "legacy.md")); err != nil || string(got) != "from before" {
		t.Errorf("the push should fold main's legacy.md into the project, got %q, %v", got, err)
	}
}

// TestPushWithLegacyBranchKeepsHold: a plain push that folds in a legacy
// branch leaves an active delete hold as it is: the held files stay out
// of the project, stay in the store, and the hold stays for the user to
// resolve.
func TestPushWithLegacyBranchKeepsHold(t *testing.T) {
	bare := testutil.BareRemote(t)
	repo := gitPushSetup(t, bare)
	slug := heldDrawer(t, repo)
	pushLegacyBranch(t, bare, map[string]string{slug + "/legacy.md": "from before"})

	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.SwarfDir(repo), "f00.md")); err == nil {
		t.Error("a push restored held f00.md into the project")
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, slug, "f00.md")); err != nil {
		t.Errorf("a push without --allow-deletes deleted held f00.md from the store: %v", err)
	}
	if _, err := os.Stat(paths.DeleteHoldMarker(slug)); err != nil {
		t.Errorf("a push should leave the hold for the user: %v", err)
	}
	// The hold still applies to the next push, too.
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.StoreDir, slug, "f00.md")); err != nil {
		t.Errorf("the next push deleted held f00.md from the store: %v", err)
	}
	if _, err := os.Stat(paths.DeleteHoldMarker(slug)); err != nil {
		t.Errorf("the next push should keep the hold: %v", err)
	}
}
