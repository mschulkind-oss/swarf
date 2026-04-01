package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

type Check struct {
	Name string
	OK   bool
	Msg  string
}

// Result groups checks into project-local and system-level categories.
type Result struct {
	Project []Check
	System  []Check
	// InJail is true when global config is unavailable (e.g. inside a container).
	InJail bool
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
	name := paths.SwarfDirName + "/"
	sd := paths.SwarfDir(cwd)
	fi, err := os.Lstat(sd)
	if err != nil {
		return Check{name, false, fmt.Sprintf("%s directory not found — run 'swarf init'", name)}
	}
	if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return Check{name, true, fmt.Sprintf("%s directory exists", name)}
	}
	return Check{name, false, fmt.Sprintf("%s directory not found — run 'swarf init'", name)}
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

	swarfEntry := paths.SwarfDirName + "/"
	swarfExclude := "/" + paths.SwarfDirName + "/"
	if managedSet[swarfExclude] || gitexec.CheckIgnore(swarfEntry, cwd) {
		checks = append(checks, Check{swarfEntry, true, fmt.Sprintf("%s is gitignored", swarfEntry)})
	} else {
		checks = append(checks, Check{swarfEntry, false, fmt.Sprintf("%s is NOT gitignored — run 'swarf init' to fix", swarfEntry)})
	}

	// Check linked files
	linksDir := paths.LinksDir(cwd)
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
				checks = append(checks, Check{rel, false, fmt.Sprintf("%s is NOT gitignored — run 'swarf sweep' to fix", rel)})
			}
			return nil
		})
	}

	return checks
}

// CheckAndFixLinks checks that all files in swarf/.links/ have corresponding
// symlinks in the project root. Missing symlinks are created automatically.
func CheckAndFixLinks(cwd string) Check {
	linksDir := paths.LinksDir(cwd)
	if fi, err := os.Stat(linksDir); err != nil || !fi.IsDir() {
		return Check{"links", true, "No linked files"}
	}

	result, err := link.Run(cwd, true)
	if err != nil {
		return Check{"links", false, fmt.Sprintf("Link error: %v", err)}
	}

	if len(result.Created) > 0 {
		console.Ok(fmt.Sprintf("Fixed %d missing link(s): %s", len(result.Created), strings.Join(result.Created, ", ")))
	}

	if len(result.Warnings) > 0 {
		return Check{"links", false, fmt.Sprintf("Link warnings: %s", strings.Join(result.Warnings, "; "))}
	}
	return Check{"links", true, "All links healthy"}
}

// CheckSymlinksRelative verifies that all swept symlinks use relative targets.
// Absolute symlinks break in jails and containers with remapped directories.
// Any absolute symlinks found are automatically fixed.
func CheckSymlinksRelative(cwd string) Check {
	linksDir := paths.LinksDir(cwd)
	if fi, err := os.Stat(linksDir); err != nil || !fi.IsDir() {
		return Check{"symlink paths", true, "No links directory"}
	}

	var absolute []string
	filepath.Walk(linksDir, func(source string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(linksDir, source)
		target := filepath.Join(cwd, rel)
		fi, lErr := os.Lstat(target)
		if lErr != nil || fi.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		linkDest, rErr := os.Readlink(target)
		if rErr != nil {
			return nil
		}
		if filepath.IsAbs(linkDest) {
			// Auto-fix: rewrite as relative symlink.
			relDest, err := filepath.Rel(filepath.Dir(target), source)
			if err != nil {
				return nil
			}
			os.Remove(target)
			if err := os.Symlink(relDest, target); err == nil {
				absolute = append(absolute, rel)
			}
		}
		return nil
	})

	if len(absolute) > 0 {
		return Check{"symlink paths", true, fmt.Sprintf("Fixed %d absolute symlink(s): %s", len(absolute), strings.Join(absolute, ", "))}
	}
	return Check{"symlink paths", true, "All symlinks are relative"}
}

// RunAllChecks returns project and system checks grouped in a Result.
// When global config is missing (e.g. inside a container/jail), system
// checks are skipped and InJail is set to true.
// Missing links are automatically fixed.
func RunAllChecks(cwd string) Result {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	var r Result

	// Project-local checks — always run.
	r.Project = append(r.Project, CheckSwarfDirExists(cwd))
	r.Project = append(r.Project, CheckGitignore(cwd)...)
	r.Project = append(r.Project, CheckAndFixLinks(cwd))
	r.Project = append(r.Project, CheckSymlinksRelative(cwd))

	// System checks — skip if no global config (jail/container).
	gc := config.ReadGlobalConfig()
	if gc == nil {
		r.InJail = true
		return r
	}

	r.System = append(r.System, CheckGlobalConfig())
	r.System = append(r.System, CheckStoreExists())
	r.System = append(r.System, CheckStoreRemote())
	r.System = append(r.System, CheckRemoteReachable())
	r.System = append(r.System, CheckDaemonRunning())

	return r
}
