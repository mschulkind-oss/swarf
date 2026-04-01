package doctor

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

type Check struct {
	Name string
	OK   bool
	Msg  string
}

// Result groups checks into project-local and system-level categories.
type Result struct {
	Project []Check
	System  []Check
	// InJail is true when global config is unavailable and we're not interactive
	// (e.g. inside a container).
	InJail bool
}

// --- System checks (and fixes) ---

// CheckAndFixGlobalConfig checks for global config and offers to create it
// when interactive. Returns the config (possibly newly created) or nil.
func CheckAndFixGlobalConfig(interactive bool) (*config.GlobalConfig, Check) {
	gc := config.ReadGlobalConfig()
	if gc != nil {
		if gc.Remote == "" {
			return gc, Check{"global config", false, "Global config has no remote configured"}
		}
		return gc, Check{"global config", true, fmt.Sprintf("Global config: backend=%s, remote=%s", gc.Backend, gc.Remote)}
	}

	if !interactive {
		return nil, Check{"global config", false, fmt.Sprintf("Global config not found — create %s", paths.GlobalConfigTOML)}
	}

	// Offer to create.
	console.Info("")
	console.Header("No global config found. Let's set one up.")
	console.Info("")
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("  Backend [git/rclone] (git): ")
	backend, _ := reader.ReadString('\n')
	backend = strings.TrimSpace(backend)
	if backend == "" {
		backend = "git"
	}

	var remotePrompt string
	if backend == "rclone" {
		// Check for available rclone remotes.
		if remotes := listRcloneRemotes(); len(remotes) > 0 {
			console.Info("")
			console.Info("  Available rclone remotes:")
			for i, r := range remotes {
				console.Infof("    %d. %s", i+1, r)
			}
			console.Info("")
			fmt.Print("  Remote path (e.g. gdrive:swarf-store): ")
		} else {
			remotePrompt = "rclone"
			fmt.Print("  Remote path (e.g. gdrive:swarf-store): ")
		}
	} else {
		remotePrompt = "git"
		fmt.Print("  Remote URL (your private backup repo): ")
	}
	_ = remotePrompt
	remote, _ := reader.ReadString('\n')
	remote = strings.TrimSpace(remote)

	gc = &config.GlobalConfig{Backend: backend, Remote: remote, Debounce: "5s"}
	config.WriteGlobalConfig(gc)
	console.Ok(fmt.Sprintf("Wrote %s", paths.GlobalConfigTOML))
	return gc, Check{"global config", true, fmt.Sprintf("Global config: backend=%s, remote=%s", gc.Backend, gc.Remote)}
}

func listRcloneRemotes() []string {
	if _, err := exec.LookPath("rclone"); err != nil {
		return nil
	}
	out, err := exec.Command("rclone", "listremotes").Output()
	if err != nil {
		return nil
	}
	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			remotes = append(remotes, line)
		}
	}
	return remotes
}

// CheckAndFixStore checks for the central store and creates it if missing.
func CheckAndFixStore(gc *config.GlobalConfig) Check {
	if paths.IsDir(paths.StoreDir) && gitexec.IsRepo(paths.StoreDir) {
		return Check{"store", true, fmt.Sprintf("Central store exists at %s", paths.StoreDir)}
	}

	if gc == nil {
		return Check{"store", false, "Central store not found (no config)"}
	}

	// Create it.
	if err := initialize.EnsureStore("", gc); err != nil {
		return Check{"store", false, fmt.Sprintf("Failed to create store: %v", err)}
	}
	return Check{"store", true, fmt.Sprintf("Created central store at %s", paths.StoreDir)}
}

func CheckStoreRemote() Check {
	if !paths.IsDir(paths.StoreDir) {
		return Check{"store remote", false, "Store does not exist"}
	}
	gc := config.ReadGlobalConfig()
	if gc != nil && gc.Backend == "rclone" {
		// Rclone stores don't have a git remote — the rclone remote is in the config.
		if gc.Remote != "" {
			return Check{"store remote", true, fmt.Sprintf("Rclone remote: %s", gc.Remote)}
		}
		return Check{"store remote", false, "No rclone remote configured"}
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
			// For rclone, the remote path might not exist yet (first sync creates it).
			// lsd failing isn't necessarily an error — the remote itself might be fine.
			cmd2 := exec.Command("rclone", "about", strings.Split(gc.Remote, ":")[0]+":")
			if err2 := cmd2.Run(); err2 != nil {
				return Check{"remote", false, fmt.Sprintf("Rclone remote not reachable: %s", gc.Remote)}
			}
			return Check{"remote", true, fmt.Sprintf("Rclone remote reachable (path will be created on first sync): %s", gc.Remote)}
		}
		return Check{"remote", true, fmt.Sprintf("Rclone remote reachable: %s", gc.Remote)}
	}

	return Check{"remote", false, fmt.Sprintf("Unknown backend: %s", gc.Backend)}
}

func CheckBinaryLocation() Check {
	inVenv, path := daemon.IsInVenv()
	if inVenv {
		return Check{"binary location", false, fmt.Sprintf("Swarf is running from an ephemeral location (%s) — the daemon service will break when this environment is removed. Use pipx, brew, or uv tool install for a persistent install.", path)}
	}
	return Check{"binary location", true, "Binary is at a stable location"}
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

// IsServiceInstalled checks whether a swarf service is installed.
func IsServiceInstalled() bool {
	kind := daemon.ServiceKind()
	home, _ := os.UserHomeDir()
	switch kind {
	case "systemd":
		_, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "swarf.service"))
		return err == nil
	case "launchd":
		_, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.swarf.daemon.plist"))
		return err == nil
	default:
		return false
	}
}

func CheckAndFixService(interactive bool) Check {
	kind := daemon.ServiceKind()

	if IsServiceInstalled() {
		return Check{"service", true, fmt.Sprintf("%s service installed", titleCase(kind))}
	}

	if kind == "" {
		return Check{"service", false, "No supported service manager found (need systemd or launchd)"}
	}

	if inVenv, _ := daemon.IsInVenv(); inVenv {
		return Check{"service", false, fmt.Sprintf("%s service not installed — fix binary location first", titleCase(kind))}
	}

	if !interactive {
		return Check{"service", false, fmt.Sprintf("%s service not installed — run 'swarf doctor' to fix", titleCase(kind))}
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("  Install %s service for auto-sync? [Y/n] ", kind)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer == "" || answer == "y" || answer == "yes" {
		if err := daemon.InstallService(); err != nil {
			console.Error(fmt.Sprintf("Service install failed: %v", err))
			console.Hint("You can try again later: swarf daemon install")
			return Check{"service", false, fmt.Sprintf("%s service install failed", titleCase(kind))}
		}
		return Check{"service", true, fmt.Sprintf("Installed %s service — daemon is running", titleCase(kind))}
	}

	console.Hint("No problem. Install later: swarf daemon install")
	return Check{"service", false, fmt.Sprintf("%s service not installed (skipped)", titleCase(kind))}
}

// --- Project checks (and fixes) ---

// CheckAndFixProject checks whether swarf is initialized for the current project
// and initializes it if interactive. This replaces the standalone `swarf init` logic.
func CheckAndFixProject(cwd string, gc *config.GlobalConfig, interactive bool) []Check {
	if !gitexec.IsInsideWorkTree(cwd) {
		if interactive {
			return []Check{{"project", false, "Not inside a git repository — cd into a project first"}}
		}
		return []Check{{"project", false, "Not inside a git repository"}}
	}

	hostRoot := gitexec.GetRepoRoot(cwd)
	if hostRoot == "" {
		hostRoot = cwd
	}

	sd := paths.SwarfDir(hostRoot)
	var checks []Check

	// Check/create swarf/ directory.
	if fi, err := os.Lstat(sd); err != nil || !(fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
		if !interactive || gc == nil {
			return []Check{{paths.SwarfDirName + "/", false, fmt.Sprintf("%s/ not found — run 'swarf init'", paths.SwarfDirName)}}
		}

		// Initialize the project.
		if err := initialize.Run(gc); err != nil {
			return []Check{{paths.SwarfDirName + "/", false, fmt.Sprintf("Init failed: %v", err)}}
		}
		checks = append(checks, Check{paths.SwarfDirName + "/", true, fmt.Sprintf("Initialized %s/ for %s", paths.SwarfDirName, paths.ProjectSlug(hostRoot))})
	} else {
		checks = append(checks, Check{paths.SwarfDirName + "/", true, fmt.Sprintf("%s/ directory exists", paths.SwarfDirName)})
	}

	// Gitignore checks.
	checks = append(checks, CheckGitignore(hostRoot)...)

	// Link and symlink checks (with auto-fix).
	checks = append(checks, CheckAndFixLinks(hostRoot))
	checks = append(checks, CheckSymlinksRelative(hostRoot))

	return checks
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

// --- RunAllChecks ---

// RunAllChecks is the universal health check and fix-it function.
// When interactive is true, it acts as both doctor AND init:
//   - Missing global config → prompts to create
//   - Missing store → creates it
//   - Missing project registration → initializes the project
//   - Missing service → offers to install
//
// When interactive is false, it only reports problems.
// When global config is absent and non-interactive, InJail is set.
func RunAllChecks(cwd string, interactive bool) Result {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	var r Result

	// Step 1: Global config. Without this, everything else depends on context.
	gc, configCheck := CheckAndFixGlobalConfig(interactive)
	if gc == nil {
		// No config and not interactive — jail mode.
		r.InJail = true
		// Still run project-local checks if we're in a git repo with swarf/.
		if gitexec.IsInsideWorkTree(cwd) {
			hostRoot := gitexec.GetRepoRoot(cwd)
			if hostRoot == "" {
				hostRoot = cwd
			}
			if paths.IsDir(paths.SwarfDir(hostRoot)) {
				r.Project = append(r.Project, CheckGitignore(hostRoot)...)
				r.Project = append(r.Project, CheckAndFixLinks(hostRoot))
				r.Project = append(r.Project, CheckSymlinksRelative(hostRoot))
			}
		}
		return r
	}

	// Step 2: System checks — config exists (or was just created).
	r.System = append(r.System, configCheck)
	r.System = append(r.System, CheckBinaryLocation())
	r.System = append(r.System, CheckAndFixStore(gc))
	r.System = append(r.System, CheckStoreRemote())
	r.System = append(r.System, CheckRemoteReachable())
	r.System = append(r.System, CheckAndFixService(interactive))
	r.System = append(r.System, CheckDaemonRunning())

	// Step 3: Project checks — initialize if needed.
	r.Project = CheckAndFixProject(cwd, gc, interactive)

	return r
}
