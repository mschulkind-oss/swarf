package gitexec

import (
	"fmt"
	"os/exec"
	"strings"
)

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
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
	if remote == "" {
		remote = "origin"
	}
	_, err := run(dir, "push", remote)
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

// ResetHard resets the working tree to match HEAD. Used after rclone clone/pull
// to reconstruct working files from the downloaded .git/ directory.
func ResetHard(dir string) error {
	_, err := run(dir, "reset", "--hard", "HEAD")
	return err
}

// ResetHardTo resets the working tree and HEAD to match the given ref. Used
// when bootstrapping a store that has no HEAD yet (e.g. right after first
// contact with a peer).
func ResetHardTo(dir, ref string) error {
	_, err := run(dir, "reset", "--hard", ref)
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
	_, err := run(dir, "fetch", remote, "HEAD")
	return err
}

// MergeFF attempts a fast-forward-only merge of the given ref into HEAD.
// Returns an error if the merge would require a real merge commit (i.e. the
// branches have diverged), which is the caller's signal to fall back to the
// conflict-aware merge path.
func MergeFF(dir, ref string) error {
	_, err := run(dir, "merge", "--ff-only", ref)
	return err
}

// MergeNoCommit runs a regular merge with --no-commit so the caller can
// inspect the result (including any conflicts) before deciding what to do.
// Returns (cleanIndex, err): cleanIndex is true when no conflicts arose.
// An unexpected error (not a conflict) returns err!=nil.
func MergeNoCommit(dir, ref, message string) (bool, error) {
	// -m supplies the message in case we later finalize with `git commit`.
	_, err := run(dir, "merge", "--no-commit", "--no-ff", "-m", message, ref)
	if err == nil {
		return true, nil
	}
	// Git returns non-zero on conflicts; detect that vs. a real error by
	// checking for unmerged paths.
	unmerged, lsErr := runStdout(dir, "diff", "--name-only", "--diff-filter=U")
	if lsErr != nil {
		return false, err
	}
	if strings.TrimSpace(unmerged) == "" {
		return false, err
	}
	return false, nil
}

// UnmergedPaths lists paths that currently have unresolved merge conflicts.
func UnmergedPaths(dir string) []string {
	out, err := runStdout(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			paths = append(paths, s)
		}
	}
	return paths
}

// ShowStage returns the content of the given file at the given merge stage:
// 1 = common ancestor, 2 = ours, 3 = theirs. Returns empty string if the file
// did not exist at that stage (e.g. add/add conflicts have no stage 1).
func ShowStage(dir string, stage int, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", fmt.Sprintf(":%d:%s", stage, path))
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CheckoutOurs resolves the conflict on the given path by keeping our version.
func CheckoutOurs(dir, path string) error {
	_, err := run(dir, "checkout", "--ours", "--", path)
	return err
}

// AddPath stages a single path (used after conflict resolution).
func AddPath(dir, path string) error {
	_, err := run(dir, "add", "--", path)
	return err
}

// AbortMerge aborts an in-progress merge, restoring the working tree.
func AbortMerge(dir string) error {
	_, err := run(dir, "merge", "--abort")
	return err
}
