package backends

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/testutil"
)

func TestEnsureOwnerTokenCreatesOnceAndKeepsIt(t *testing.T) {
	testutil.InitializedSwarf(t)
	if _, ok := ReadOwnerToken(paths.StoreDir); ok {
		t.Fatal("a fresh store should have no owner token yet")
	}
	first, err := EnsureOwnerToken(paths.StoreDir)
	if err != nil || first == "" {
		t.Fatalf("EnsureOwnerToken: %q, %v", first, err)
	}
	if !strings.HasPrefix(OwnerTokenPath(paths.StoreDir), filepath.Join(paths.StoreDir, ".git")+string(filepath.Separator)) {
		t.Errorf("owner token must live in the store's .git/, got %s", OwnerTokenPath(paths.StoreDir))
	}
	second, err := EnsureOwnerToken(paths.StoreDir)
	if err != nil || second != first {
		t.Fatalf("second EnsureOwnerToken = %q, %v; want the first token %q", second, err, first)
	}
	if got, ok := ReadOwnerToken(paths.StoreDir); !ok || got != first {
		t.Errorf("ReadOwnerToken = %q, %v; want %q", got, ok, first)
	}

	// Another store gets its own token.
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, ".git"), 0o755)
	tok, err := EnsureOwnerToken(other)
	if err != nil || tok == first {
		t.Errorf("a second store must get a different token, got %q (%v)", tok, err)
	}
}

// ownerFixture is a store with one file and an empty local-filesystem
// remote, synced with real rclone.
type ownerFixture struct {
	remote string
	target string // this machine's subtree on the remote, as a path
}

func newOwnerFixture(t *testing.T) ownerFixture {
	t.Helper()
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not installed")
	}
	testutil.InitializedSwarf(t)
	os.WriteFile(filepath.Join(paths.StoreDir, "note.md"), []byte("mine"), 0o644)
	remote := t.TempDir()
	return ownerFixture{remote: remote, target: filepath.Join(remote, MachineSuffix, "box")}
}

// claimRemote makes the remote subtree look like another store with the
// same machine_id already wrote it.
func (f ownerFixture) claimRemote(t *testing.T, token string) {
	t.Helper()
	os.MkdirAll(filepath.Join(f.target, ".git"), 0o755)
	os.WriteFile(filepath.Join(f.target, ".git", OwnerTokenFile), []byte(token+"\n"), 0o644)
	os.WriteFile(filepath.Join(f.target, "note.md"), []byte("theirs"), 0o644)
}

func (f ownerFixture) remoteToken(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.target, ".git", OwnerTokenFile))
	if err != nil {
		t.Fatalf("no owner token on the remote: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestSyncClaimsAbsentOwnerToken(t *testing.T) {
	f := newOwnerFixture(t)
	b := &RcloneBackend{Remote: f.remote, MachineID: "box"}
	if res := b.Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("sync to an unclaimed subtree failed: %s", res.Message)
	}
	local, ok := ReadOwnerToken(paths.StoreDir)
	if !ok {
		t.Fatal("sync should create the store's owner token")
	}
	if got := f.remoteToken(t); got != local {
		t.Errorf("remote owner token = %q, want this store's %q", got, local)
	}
}

func TestSyncProceedsWhenOwnerTokenMatches(t *testing.T) {
	f := newOwnerFixture(t)
	tok, _ := EnsureOwnerToken(paths.StoreDir)
	f.claimRemote(t, tok)
	b := &RcloneBackend{Remote: f.remote, MachineID: "box"}
	if res := b.Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("sync to our own subtree failed: %s", res.Message)
	}
	if data, _ := os.ReadFile(filepath.Join(f.target, "note.md")); string(data) != "mine" {
		t.Errorf("remote note.md = %q, want the upload", data)
	}
}

func TestSyncRefusesSubtreeOwnedByAnotherStore(t *testing.T) {
	f := newOwnerFixture(t)
	f.claimRemote(t, "someone-else")
	b := &RcloneBackend{Remote: f.remote, MachineID: "box"}
	res := b.Sync(context.Background(), paths.StoreDir)
	if res.Success {
		t.Fatal("sync must refuse a subtree another store owns")
	}
	for _, want := range []string{"box", "--take-over", "swarf pull", "machine"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal should mention %q:\n%s", want, res.Message)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(f.target, "note.md")); string(data) != "theirs" {
		t.Errorf("remote note.md = %q; the refused sync must not touch it", data)
	}
	if got := f.remoteToken(t); got != "someone-else" {
		t.Errorf("remote owner token = %q; the refused sync must not touch it", got)
	}
	// Still committed locally.
	if gitexec.RevParseHEAD(paths.StoreDir) == "" || strings.TrimSpace(gitexec.StatusPorcelain(paths.StoreDir)) != "" {
		t.Error("a refused sync should still commit the store locally")
	}
}

func TestSyncTakeOverUploadsAnyway(t *testing.T) {
	f := newOwnerFixture(t)
	f.claimRemote(t, "someone-else")
	b := &RcloneBackend{Remote: f.remote, MachineID: "box", TakeOver: true}
	if res := b.Sync(context.Background(), paths.StoreDir); !res.Success {
		t.Fatalf("take-over sync failed: %s", res.Message)
	}
	if data, _ := os.ReadFile(filepath.Join(f.target, "note.md")); string(data) != "mine" {
		t.Errorf("remote note.md = %q, want the upload", data)
	}
	local, _ := ReadOwnerToken(paths.StoreDir)
	if got := f.remoteToken(t); got != local {
		t.Errorf("after take-over the remote owner token = %q, want this store's %q", got, local)
	}
}

func TestRemoteOwnerToken(t *testing.T) {
	f := newOwnerFixture(t)
	ctx := context.Background()
	if tok, ok, err := RemoteOwnerToken(ctx, f.target); err != nil || ok {
		t.Fatalf("missing subtree: got %q, %v, %v; want absent", tok, ok, err)
	}
	os.MkdirAll(filepath.Join(f.target, ".git"), 0o755)
	if tok, ok, err := RemoteOwnerToken(ctx, f.target); err != nil || ok {
		t.Fatalf("subtree without a token: got %q, %v, %v; want absent", tok, ok, err)
	}
	f.claimRemote(t, "abc")
	if tok, ok, err := RemoteOwnerToken(ctx, f.target); err != nil || !ok || tok != "abc" {
		t.Fatalf("claimed subtree: got %q, %v, %v; want abc", tok, ok, err)
	}
}

// shimRcloneCat puts an rclone first on PATH that runs catBody for
// 'rclone cat' and hands every other subcommand to the real rclone.
func shimRcloneCat(t *testing.T, catBody string) {
	t.Helper()
	real, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not installed")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = cat ]; then\n" + catBody + "\nfi\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "rclone"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRemoteOwnerTokenReadFailure(t *testing.T) {
	f := newOwnerFixture(t)
	f.claimRemote(t, "abc")
	ctx := context.Background()

	// Any exit other than 3 or 4 means the remote could not be read,
	// not that the token is missing.
	shimRcloneCat(t, "echo 'unauthorized' >&2; exit 1")
	tok, ok, err := RemoteOwnerToken(ctx, f.target)
	if err == nil || ok {
		t.Fatalf("unreadable remote: got %q, %v, %v; want an error", tok, ok, err)
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error should carry rclone's stderr, got %v", err)
	}
}

func TestRemoteOwnerTokenFileNotFoundExit(t *testing.T) {
	f := newOwnerFixture(t)
	shimRcloneCat(t, "exit 4")
	if tok, ok, err := RemoteOwnerToken(context.Background(), f.target); err != nil || ok {
		t.Fatalf("exit 4 (file not found): got %q, %v, %v; want absent", tok, ok, err)
	}
}

func TestRemoteOwnerTokenEmptyOutputIsAbsent(t *testing.T) {
	// Object stores answer a missing path with nothing and exit 0.
	f := newOwnerFixture(t)
	shimRcloneCat(t, "exit 0")
	if tok, ok, err := RemoteOwnerToken(context.Background(), f.target); err != nil || ok {
		t.Fatalf("empty output: got %q, %v, %v; want absent", tok, ok, err)
	}
}

func TestSyncSkipsUploadWhenOwnerTokenUnreadable(t *testing.T) {
	f := newOwnerFixture(t)
	f.claimRemote(t, "someone-else")
	shimRcloneCat(t, "echo 'rate limited' >&2; exit 1")
	b := &RcloneBackend{Remote: f.remote, MachineID: "box"}
	res := b.Sync(context.Background(), paths.StoreDir)
	if res.Success {
		t.Fatal("sync must not upload when the remote owner token cannot be read")
	}
	if !strings.Contains(res.Message, "could not read the remote owner token") {
		t.Errorf("refusal should say the owner token could not be read:\n%s", res.Message)
	}
	if data, _ := os.ReadFile(filepath.Join(f.target, "note.md")); string(data) != "theirs" {
		t.Errorf("remote note.md = %q; the skipped sync must not touch it", data)
	}
	if got := f.remoteToken(t); got != "someone-else" {
		t.Errorf("remote owner token = %q; the skipped sync must not touch it", got)
	}
}
