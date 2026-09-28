package push

import (
	"bytes"
	"context"
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
func gitPushSetup(t *testing.T, remote string) {
	t.Helper()
	testutil.InitializedSwarf(t)
	if out, err := exec.Command("git", "-C", paths.StoreDir, "remote", "add", "origin", remote).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	config.WriteGlobalConfig(&config.GlobalConfig{Backend: "git", Remote: remote, Debounce: "5s", MachineID: "box"})
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
