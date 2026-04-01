package gitexec

import (
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

func GetRepoRoot(dir string) string {
	out, err := runStdout(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return out
}
