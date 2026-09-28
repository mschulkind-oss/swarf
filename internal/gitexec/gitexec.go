package gitexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func run(dir string, args ...string) (string, error) {
	return runCtx(context.Background(), dir, args...)
}

// runCtx is the context-aware form of run. A cancelled ctx kills the git
// child process, so a network-bound git command (e.g. push) can be aborted
// promptly on daemon shutdown.
func runCtx(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runStdout(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func Init(dir string) error {
	_, err := run(dir, "init")
	return err
}

func ConfigSet(dir, key, value string) error {
	_, err := run(dir, "config", key, value)
	return err
}

func ConfigGet(dir, key string) string {
	out, err := runStdout(dir, "config", key)
	if err != nil {
		return ""
	}
	return out
}

func AddRemote(dir, name, url string) error {
	_, err := run(dir, "remote", "add", name, url)
	return err
}

func AddAll(dir string) error {
	_, err := run(dir, "add", "--force", "-A")
	return err
}

func Commit(dir, message string) error {
	_, err := run(dir, "commit", "-m", message)
	return err
}

func Push(dir string, remote string) error {
	return PushContext(context.Background(), dir, remote)
}

// PushContext runs `git push` with a cancellable context. On daemon shutdown
// a cancelled ctx kills the push child so reboot isn't blocked on a slow or
// stuck network push.
func PushContext(ctx context.Context, dir string, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := runCtx(ctx, dir, "push", remote)
	return err
}

func Clone(url, dest string) error {
	_, err := run("", "clone", url, dest)
	return err
}

func Pull(dir string) error {
	_, err := run(dir, "pull")
	return err
}

func StatusPorcelain(dir string) string {
	out, _ := runStdout(dir, "status", "--porcelain")
	return out
}

func IsRepo(dir string) bool {
	_, err := runStdout(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func RemoteURL(dir string, name string) string {
	if name == "" {
		name = "origin"
	}
	out, err := runStdout(dir, "remote", "get-url", name)
	if err != nil {
		return ""
	}
	return out
}

func IsInsideWorkTree(dir string) bool {
	return IsRepo(dir)
}

func CheckIgnore(path string, dir string) bool {
	cmd := exec.Command("git", "check-ignore", "-q", path)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Run() == nil
}

// RevParseHEAD returns the full SHA of HEAD in the given repo, or "".
func RevParseHEAD(dir string) string {
	out, err := runStdout(dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// LsRemoteHEAD returns the full SHA of HEAD on the given remote, or "".
func LsRemoteHEAD(dir, remote string) string {
	if remote == "" {
		remote = "origin"
	}
	out, err := runStdout(dir, "ls-remote", remote, "HEAD")
	if err != nil {
		return ""
	}
	// Format: "<sha>\tHEAD"
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

// IsTracked returns true if the file at the given path is tracked by git.
func IsTracked(dir, path string) bool {
	out, err := runStdout(dir, "ls-files", path)
	return err == nil && out != ""
}

// RmCached removes a file from the git index without touching the working tree.
func RmCached(dir, path string) error {
	_, err := run(dir, "rm", "--cached", "--", path)
	return err
}

func GetRepoRoot(dir string) string {
	out, err := runStdout(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return out
}

// RemoteExists reports whether a named remote is configured in the given repo.
func RemoteExists(dir, name string) bool {
	out, err := runStdout(dir, "remote")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// SetRemoteURL sets the URL for an existing remote, replacing any prior value.
func SetRemoteURL(dir, name, url string) error {
	_, err := run(dir, "remote", "set-url", name, url)
	return err
}

// Fetch runs `git fetch <remote>` in the given repo.
func Fetch(dir, remote string) error {
	_, err := run(dir, "fetch", remote)
	return err
}

// FetchHead runs `git fetch <remote> HEAD` in the given repo, which writes
// the peer's current tip to FETCH_HEAD as a merge-eligible ref. A bare
// `git fetch <remote>` marks FETCH_HEAD as not-for-merge, which would cause
// `git merge FETCH_HEAD` to be a no-op.
func FetchHead(dir, remote string) error {
	out, err := run(dir, "fetch", remote, "HEAD")
	if err != nil {
		if out != "" {
			return fmt.Errorf("%s: %w", out, err)
		}
		return err
	}
	return nil
}

// AddPath stages a single path.
func AddPath(dir, path string) error {
	_, err := run(dir, "add", "--", path)
	return err
}

// RemovePath removes a path from the index and working tree. Used when a peer
// has deleted a file and we want to mirror that locally.
func RemovePath(dir, path string) error {
	_, err := run(dir, "rm", "-f", "--", path)
	return err
}

// MergeBase returns the SHA of the best common ancestor of a and b, or ""
// if they share no history.
func MergeBase(dir, a, b string) (string, error) {
	out, err := runStdout(dir, "merge-base", a, b)
	if err != nil {
		// git merge-base exits 1 when there is no common ancestor. That's
		// not an error from our perspective; return "" and nil.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ReadRef returns the SHA the given ref points at, or "" if it doesn't exist.
// The ref is expected in the form accepted by `git rev-parse` (e.g.
// "refs/swarf-peers/<name>").
func ReadRef(dir, ref string) string {
	out, err := runStdout(dir, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// UpdateRef sets the named ref to the given SHA, creating or moving it.
func UpdateRef(dir, ref, sha string) error {
	_, err := run(dir, "update-ref", ref, sha)
	return err
}

// DiffEntry is one file-level change between two tree-ish refs, as produced
// by `git diff --name-status -z`.
type DiffEntry struct {
	// Status is the single-character status code: "A" (added), "M" (modified),
	// "D" (deleted), "T" (type changed). Renames and copies are expanded into
	// a delete + add pair, so the caller never has to handle "R"/"C".
	Status string
	// Path is the path of the changed file, relative to the repo root.
	Path string
}

// DiffNameStatus runs `git diff --name-status` between the two refs and
// returns the parsed entries. Renames are expanded into delete+add pairs so
// every entry carries exactly one path. Passing "" for oldRef returns entries
// relative to the empty tree (i.e. every file in newRef as an "A" entry).
func DiffNameStatus(dir, oldRef, newRef string) ([]DiffEntry, error) {
	args := []string{"diff", "--name-status", "--no-renames", "-z"}
	if oldRef == "" {
		// Empty tree SHA is well-known. Lets us diff "from nothing" to newRef.
		args = append(args, "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	} else {
		args = append(args, oldRef)
	}
	args = append(args, newRef)
	out, err := runStdout(dir, args...)
	if err != nil {
		return nil, err
	}
	return parseDiffNameStatus(out), nil
}

// parseDiffNameStatus parses the NUL-separated output of
// `git diff --name-status --no-renames -z`. Each record is
//
//	<status>NUL<path>NUL
//
// so we split on NUL and walk pairs. --no-renames guarantees we never see R/C.
func parseDiffNameStatus(out string) []DiffEntry {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	var entries []DiffEntry
	for i := 0; i+1 < len(parts); i += 2 {
		status := strings.TrimSpace(parts[i])
		path := parts[i+1]
		if status == "" || path == "" {
			continue
		}
		entries = append(entries, DiffEntry{Status: status[:1], Path: path})
	}
	return entries
}

// ShowFile returns the content of the file at the given ref. The ref can be
// any tree-ish (a commit SHA, a ref name, etc.). Returns an error if the file
// did not exist at that ref.
func ShowFile(dir, ref, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref+":"+path)
	cmd.Dir = dir
	return cmd.Output()
}

// ErrPushRejected is returned by PushRef when the remote refused the
// update as a non-fast-forward: the remote branch has commits the pushed
// commit does not contain.
var ErrPushRejected = errors.New("push rejected: the remote branch has commits this repository does not")

// PushRef pushes HEAD to ref (e.g. "refs/heads/machines/laptop") on
// remote. It never forces unless force is set. A non-fast-forward
// refusal returns an error wrapping ErrPushRejected; any other failure
// returns git's output. A cancelled ctx kills the push.
func PushRef(ctx context.Context, dir, remote, ref string, force bool) error {
	refspec := "HEAD:" + ref
	if force {
		refspec = "+" + refspec
	}
	out, err := runCtx(ctx, dir, "push", "--porcelain", remote, refspec)
	if err == nil {
		return nil
	}
	// --porcelain prints one line per ref; a refused update reads
	// "!\tHEAD:<ref>\t[rejected] (non-fast-forward)" or "(fetch first)"
	// when the remote tip is not known here. A hook's refusal is
	// "[remote rejected]", which is a different failure.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "!") && strings.Contains(line, "[rejected]") {
			return fmt.Errorf("%w: %s", ErrPushRejected, strings.TrimSpace(line))
		}
	}
	if out != "" {
		return fmt.Errorf("%s: %w", out, err)
	}
	return err
}

// LsRemoteRef returns the SHA ref points at on remote. ok is false when
// the remote has no such ref; err is set only when the remote could not
// be read, so the caller cannot tell either way.
func LsRemoteRef(dir, remote, ref string) (sha string, ok bool, err error) {
	cmd := exec.Command("git", "ls-remote", "--exit-code", remote, ref)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// --exit-code makes a missing ref exit 2.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 {
			return "", false, nil
		}
		msg := err.Error()
		if exit != nil && len(exit.Stderr) > 0 {
			msg = strings.TrimSpace(string(exit.Stderr))
		}
		return "", false, fmt.Errorf("git ls-remote %s %s: %s", remote, ref, msg)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
			return f[0], true, nil
		}
	}
	return "", false, nil
}

// IsAncestor reports whether commit a is an ancestor of (or equal to)
// commit b. A commit this repository does not have is not an ancestor
// of anything in it.
func IsAncestor(dir, a, b string) bool {
	_, err := run(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}
