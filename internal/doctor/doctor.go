package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

type Check struct {
	Name string
	OK   bool
	Msg  string
}

func CheckGlobalConfig() Check {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return Check{"global config", false, fmt.Sprintf("Global config not found — create %s", paths.GlobalConfigTOML)}
	}
	if gc.Remote == "" {
		return Check{"global config", false, "Global config has no remote configured"}
	}
	return Check{"global config", true, fmt.Sprintf("Global config: backend=%s, remote=%s", gc.Backend, gc.Remote)}
}

func CheckStoreExists() Check {
	fi, err := os.Stat(paths.StoreDir)
	if err != nil || !fi.IsDir() {
		return Check{"store", false, fmt.Sprintf("Central store not found at %s", paths.StoreDir)}
	}
	if !gitexec.IsRepo(paths.StoreDir) {
		return Check{"store", false, fmt.Sprintf("Store at %s is not a git repository", paths.StoreDir)}
	}
	return Check{"store", true, fmt.Sprintf("Central store exists at %s", paths.StoreDir)}
}

func CheckStoreRemote() Check {
	fi, err := os.Stat(paths.StoreDir)
	if err != nil || !fi.IsDir() {
		return Check{"store remote", false, "Store does not exist"}
	}
	url := gitexec.RemoteURL(paths.StoreDir, "")
	if url != "" {
		return Check{"store remote", true, fmt.Sprintf("Store remote: %s", url)}
	}
	return Check{"store remote", false, "Store has no git remote configured"}
}

func CheckRemoteReachable() Check {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return Check{"remote", false, "No global config — cannot check remote"}
	}

	if gc.Backend == "git" {
		cmd := exec.Command("git", "ls-remote", gc.Remote)
		if err := cmd.Run(); err != nil {
			return Check{"remote", false, fmt.Sprintf("Git remote not reachable: %s", gc.Remote)}
		}
		return Check{"remote", true, fmt.Sprintf("Git remote reachable: %s", gc.Remote)}
	}

	if gc.Backend == "rclone" {
		if _, err := exec.LookPath("rclone"); err != nil {
			return Check{"remote", false, "rclone not installed"}
		}
		cmd := exec.Command("rclone", "lsd", gc.Remote)
		if err := cmd.Run(); err != nil {
			return Check{"remote", false, fmt.Sprintf("Rclone remote not reachable: %s", gc.Remote)}
		}
		return Check{"remote", true, fmt.Sprintf("Rclone remote reachable: %s", gc.Remote)}
	}

	return Check{"remote", false, fmt.Sprintf("Unknown backend: %s", gc.Backend)}
}

func CheckDaemonRunning() Check {
	data, err := os.ReadFile(paths.PIDFile)
	if err != nil {
		return Check{"daemon", false, "Daemon is not running (no PID file)"}
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return Check{"daemon", false, "Daemon is not running (stale PID file)"}
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return Check{"daemon", false, "Daemon is not running (stale PID file)"}
	}
	return Check{"daemon", true, fmt.Sprintf("Daemon is running (PID %d)", pid)}
}

func CheckSwarfDirExists(cwd string) Check {
	sd := filepath.Join(cwd, ".swarf")
	fi, err := os.Lstat(sd)
	if err != nil {
		return Check{".swarf/", false, ".swarf/ directory not found — run 'swarf init'"}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, _ := filepath.EvalSymlinks(sd)
		return Check{".swarf/", true, fmt.Sprintf(".swarf/ linked to %s", target)}
	}
	if fi.IsDir() {
		return Check{".swarf/", true, ".swarf/ directory exists (not a symlink — consider migrating)"}
	}
	return Check{".swarf/", false, ".swarf/ directory not found — run 'swarf init'"}
}

func CheckGitignore(cwd string) []Check {
	if !gitexec.IsInsideWorkTree(cwd) {
		return []Check{{"git", false, "Not inside a git repository"}}
	}

	managed := exclude.ReadManagedExcludes(cwd)
	managedSet := make(map[string]bool)
	for _, m := range managed {
		managedSet[m] = true
	}

	var checks []Check

	required := map[string]string{
		".swarf/":         "/.swarf/",
		".mise.local.toml": "/.mise.local.toml",
	}
	for path, excludeEntry := range required {
		if managedSet[excludeEntry] || gitexec.CheckIgnore(path, cwd) {
			checks = append(checks, Check{path, true, fmt.Sprintf("%s is gitignored", path)})
		} else {
			checks = append(checks, Check{path, false, fmt.Sprintf("%s is NOT gitignored — run 'swarf init' to fix", path)})
		}
	}

	// Check linked files
	linksDir := filepath.Join(cwd, ".swarf", "links")
	if fi, err := os.Stat(linksDir); err == nil && fi.IsDir() {
		filepath.Walk(linksDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(linksDir, path)
			excludeEntry := "/" + rel
			if managedSet[excludeEntry] || gitexec.CheckIgnore(rel, cwd) {
				checks = append(checks, Check{rel, true, fmt.Sprintf("%s is gitignored", rel)})
			} else {
				checks = append(checks, Check{rel, false, fmt.Sprintf("%s is NOT gitignored — run 'swarf link' to fix", rel)})
			}
			return nil
		})
	}

	return checks
}

func CheckMiseLocal(cwd string) Check {
	path := filepath.Join(cwd, ".mise.local.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{".mise.local.toml", false, ".mise.local.toml not found — run 'swarf init'"}
	}
	content := string(data)
	if strings.Contains(content, "swarf link") || strings.Contains(content, "swarf enter") {
		return Check{".mise.local.toml", true, ".mise.local.toml has swarf enter hook"}
	}
	return Check{".mise.local.toml", false, ".mise.local.toml missing swarf enter hook"}
}

func CheckLinksHealthy(cwd string) Check {
	linksDir := filepath.Join(cwd, ".swarf", "links")
	if fi, err := os.Stat(linksDir); err != nil || !fi.IsDir() {
		return Check{"links", true, "No links directory"}
	}

	var broken []string
	filepath.Walk(linksDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(linksDir, path)
		target := filepath.Join(cwd, rel)
		fi, lerr := os.Lstat(target)
		if lerr != nil {
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if _, err := os.Stat(target); os.IsNotExist(err) {
				broken = append(broken, rel)
			}
		}
		return nil
	})

	if len(broken) > 0 {
		return Check{"links", false, fmt.Sprintf("Broken symlinks: %s", strings.Join(broken, ", "))}
	}
	return Check{"links", true, "All links healthy"}
}

func RunAllChecks(cwd string) []Check {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	var results []Check
	results = append(results, CheckGlobalConfig())
	results = append(results, CheckStoreExists())
	results = append(results, CheckStoreRemote())
	results = append(results, CheckRemoteReachable())
	results = append(results, CheckDaemonRunning())
	results = append(results, CheckSwarfDirExists(cwd))
	results = append(results, CheckGitignore(cwd)...)
	results = append(results, CheckMiseLocal(cwd))
	results = append(results, CheckLinksHealthy(cwd))
	return results
}
