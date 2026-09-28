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

// FetchRefspec runs `git fetch --prune` of refspecs (e.g.
// "+refs/heads/machines/*:refs/swarf-machines/*") from remote. Only
// the refspecs' destination refs change: --refmap= stops git from also
// updating the remote's remote-tracking branches, and no tags are
// fetched. --prune removes each destination ref whose source is gone.
// A cancelled ctx kills the fetch.
func FetchRefspec(ctx context.Context, dir, remote string, refspecs ...string) error {
	args := append([]string{"fetch", "--prune", "--no-tags", "--refmap=", remote}, refspecs...)
	out, err := runCtx(ctx, dir, args...)
	if err != nil && out != "" {
		return fmt.Errorf("%s: %w", out, err)
	}
	return err
}

// ListRefs returns the refs under prefix (e.g. "refs/swarf-machines/"),
// keyed by the ref name with prefix removed, each with the SHA it
// points at.
func ListRefs(dir, prefix string) (map[string]string, error) {
	out, err := runStdout(dir, "for-each-ref", "--format=%(objectname) %(refname)", prefix)
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" {
			refs[rest] = sha
		}
	}
	return refs, nil
}

// ResetHard moves the current branch to commit and makes the index and
// working tree match it (`git reset --hard`). It also works on a
// repository with no commits yet, where it starts the branch at commit.
func ResetHard(dir, commit string) error {
	out, err := run(dir, "reset", "--hard", "--quiet", commit)
	if err != nil && out != "" {
		return fmt.Errorf("%s: %w", out, err)
	}
	return err
}

// LsRemoteHeads returns every branch on remote, keyed by branch name
// (refs/heads/ removed), each with the SHA it points at. A cancelled ctx
// kills the ls-remote.
func LsRemoteHeads(ctx context.Context, dir, remote string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", remote)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			msg = strings.TrimSpace(string(exit.Stderr))
		}
		return nil, fmt.Errorf("git ls-remote --heads %s: %s", remote, msg)
	}
	heads := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if name, ok := strings.CutPrefix(f[1], "refs/heads/"); ok && name != "" {
			heads[name] = f[0]
		}
	}
	return heads, nil
}

// ErrStaleLease is returned by PushLease when the remote ref no longer
// points at the commit the caller expected, so nothing was changed.
var ErrStaleLease = errors.New("push rejected: the remote branch moved")

// PushLease sets ref on remote to src (a commit, or "" to delete ref),
// but only while the remote ref still points at expect (git push
// --force-with-lease). The update may discard commits, which the lease
// limits to exactly expect's. A remote ref that moved returns an error
// wrapping ErrStaleLease; any other failure, such as a hook refusing
// the update, returns git's output. A cancelled ctx kills the push.
func PushLease(ctx context.Context, dir, remote, src, ref, expect string) error {
	out, err := runCtx(ctx, dir, "push", "--porcelain", "--force-with-lease="+ref+":"+expect, remote, src+":"+ref)
	if err == nil {
		return nil
	}
	// A lease that no longer holds reads "!\t<src>:<ref>\t[rejected]
	// (stale info)"; a hook's refusal is "[remote rejected]".
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "!") && strings.Contains(line, "[rejected]") {
			return fmt.Errorf("%w: %s", ErrStaleLease, strings.TrimSpace(line))
		}
	}
	if out != "" {
		return fmt.Errorf("%s: %w", out, err)
	}
	return err
}

// DeleteRef removes ref itself from the repository; a symbolic ref is
// removed, not the ref it points at. A ref that does not exist is not an
// error.
func DeleteRef(dir, ref string) error {
	out, err := run(dir, "update-ref", "--no-deref", "-d", ref)
	if err != nil && out != "" {
		return fmt.Errorf("%s: %w", out, err)
	}
	return err
}

// ConfigUnset removes key from the repository's config. A key that is
// not set is not an error.
func ConfigUnset(dir, key string) error {
	if ConfigGet(dir, key) == "" {
		return nil
	}
	_, err := run(dir, "config", "--unset-all", key)
	return err
}

// WriteTree writes the index as a tree object and returns its SHA.
func WriteTree(dir string) (string, error) {
	return runStdout(dir, "write-tree")
}

// TreeOf returns the SHA of commit's tree, or "" if commit is not in the
// repository.
func TreeOf(dir, commit string) string {
	return ReadRef(dir, commit+"^{tree}")
}

// CommitTree creates a commit of tree with message and the given parents
// (none makes a root commit) without touching any branch, the index, or
// the working tree, and returns its SHA.
func CommitTree(dir, tree, message string, parents ...string) (string, error) {
	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	out, err := runStdout(dir, args...)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("git commit-tree: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return out, nil
}

// HashBlob writes content as a blob object and returns its SHA.
func HashBlob(dir string, content []byte) (string, error) {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(string(content))
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// MkTreeFile writes a tree holding one regular file, name, whose content
// is the blob blob, and returns the tree's SHA.
func MkTreeFile(dir, name, blob string) (string, error) {
	cmd := exec.Command("git", "mktree")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("100644 blob " + blob + "\t" + name + "\n")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// CommitInfo returns the parents and full message of commit. ok is
// false when the repository does not have commit.
func CommitInfo(dir, commit string) (parents []string, message string, ok bool) {
	// The "parents:" label keeps a root commit's empty parent line from
	// being trimmed away with the output's leading whitespace.
	out, err := runStdout(dir, "show", "-s", "--format=parents:%P%n%B", commit+"^{commit}", "--")
	if err != nil {
		return nil, "", false
	}
	first, rest, _ := strings.Cut(out, "\n")
	return strings.Fields(strings.TrimPrefix(first, "parents:")), rest, true
}

// UntrackUpstreams stops every local branch from tracking a branch of
// remote (it unsets each one's upstream), removes remote's fetch
// refspec (remote.<remote>.fetch), and deletes remote's remote-tracking
// branches (refs/remotes/<remote>/). Without the refspec, which a clone
// sets, a later push to or fetch from remote no longer recreates them.
func UntrackUpstreams(dir, remote string) error {
	if err := ConfigUnset(dir, "remote."+remote+".fetch"); err != nil {
		return fmt.Errorf("remove the fetch refspec of %s: %w", remote, err)
	}
	out, err := runStdout(dir, "for-each-ref", "--format=%(refname:short) %(upstream:remotename)", "refs/heads/")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		branch, upstreamRemote, _ := strings.Cut(strings.TrimSpace(line), " ")
		if branch == "" || upstreamRemote != remote {
			continue
		}
		if _, err := run(dir, "branch", "--unset-upstream", branch); err != nil {
			return fmt.Errorf("unset upstream of %s: %w", branch, err)
		}
	}
	tracking, err := ListRefs(dir, "refs/remotes/"+remote+"/")
	if err != nil {
		return err
	}
	for name := range tracking {
		if err := DeleteRef(dir, "refs/remotes/"+remote+"/"+name); err != nil {
			return err
		}
	}
	return nil
}
