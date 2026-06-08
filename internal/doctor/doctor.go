package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/daemon"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

// Package doctor observes swarf's state and reports it. All functions are
// pure checks — they never modify config, files, services, or symlinks.
// When something is wrong, the Check's Msg points the user at the exact
// command that fixes it (typically 'swarf init' or 'swarf daemon install').
//
// Historical note: earlier versions of doctor auto-fixed things like
// missing global config, missing store, broken symlinks, and uninstalled
// services. That turned out to conflate two responsibilities: doctor's
// job (observe and diagnose) and init's job (set things up). We split
// them: doctor reports, the user (or `swarf init`) acts.

type Check struct {
	Name string
	OK   bool
	Msg  string
}

// Result groups checks into project-local and system-level categories.
type Result struct {
	Project []Check
	System  []Check
	// InJail is true when global config is unavailable and we're not
	// interactive — commonly inside a container.
	InJail bool
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- System checks ---

// CheckGlobalConfig reports the global config state. Returns the config
// (possibly nil) alongside the check so callers that want to drive further
// checks from the config don't have to read it twice.
func CheckGlobalConfig() (*config.GlobalConfig, Check) {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return nil, Check{"global config", false,
			fmt.Sprintf("Global config not found\n    Fix: run 'swarf init' to create %s", paths.GlobalConfigTOML)}
	}
	if gc.Remote == "" {
		return gc, Check{"global config", false,
			fmt.Sprintf("Global config has no remote configured\n    Fix: edit %s and set remote", paths.GlobalConfigTOML)}
	}
	return gc, Check{"global config", true,
		fmt.Sprintf("Global config: backend=%s, remote=%s (%s)", gc.Backend, gc.Remote, paths.GlobalConfigTOML)}
}

// CheckStore reports whether the central store exists as a git repo.
func CheckStore(gc *config.GlobalConfig) Check {
	if paths.IsDir(paths.StoreDir) && gitexec.IsRepo(paths.StoreDir) {
		return Check{"store", true, fmt.Sprintf("Central store exists at %s", paths.StoreDir)}
	}
	if gc == nil {
		return Check{"store", false, "Central store not found (no config)"}
	}
	return Check{"store", false, fmt.Sprintf("Central store missing at %s\n    Fix: run 'swarf init' to create it", paths.StoreDir)}
}

// CheckStoreRemote reports whether the store has a configured remote.
// For rclone backends, the remote lives in global config, not in the
// store's git config.
func CheckStoreRemote() Check {
	if !paths.IsDir(paths.StoreDir) {
		return Check{"store remote", false, "Store does not exist"}
	}
	gc := config.ReadGlobalConfig()
	if gc != nil && gc.Backend == "rclone" {
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

// CheckMachineID reports the configured machine id. Does not write to
// config — the machine id is persisted on first run by the init/setup
// path or by the rclone backend's Sync.
func CheckMachineID() Check {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return Check{"machine id", false, "No global config — cannot determine machine id"}
	}
	if gc.MachineID != "" {
		return Check{"machine id", true, fmt.Sprintf("Machine id: %s", gc.MachineID)}
	}
	// No stored id yet — report the fallback we'd use.
	fallback := config.DefaultMachineID()
	return Check{"machine id", true, fmt.Sprintf(
		"Machine id: %s (default from hostname; set [machine].id in %s to override)",
		fallback, paths.GlobalConfigTOML)}
}

// CheckRcloneLayout warns when the rclone remote is in the legacy flat layout
// (.git/ at root). Swarf refuses to push/pull against it.
func CheckRcloneLayout() Check {
	gc := config.ReadGlobalConfig()
	if gc == nil || gc.Backend != "rclone" {
		return Check{"rclone layout", true, ""} // N/A
	}
	if _, err := exec.LookPath("rclone"); err != nil {
		return Check{"rclone layout", true, ""}
	}
	cmd := exec.Command("rclone", "lsf", "--dirs-only", strings.TrimRight(gc.Remote, "/"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Check{"rclone layout", true, ""}
	}
	hasGit := false
	hasMachines := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimRight(strings.TrimSpace(line), "/")
		if name == ".git" {
			hasGit = true
		}
		if name == "machines" {
			hasMachines = true
		}
	}
	if hasGit && !hasMachines {
		msg := fmt.Sprintf(
			"Rclone remote %s is in the legacy flat layout (.git/ at root).\n"+
				"    Swarf now writes per-machine under machines/<id>/ to avoid concurrent-write corruption.\n"+
				"    Migration (one-time, manual):\n"+
				"      1. Stop the daemon on all machines that use this remote:   swarf daemon stop\n"+
				"      2. Pick a machine id for this host (default: hostname).\n"+
				"      3. On the remote, create the directory: machines/<your-id>/\n"+
				"      4. Move everything currently at the remote root (including .git/) into machines/<your-id>/\n"+
				"         Example (one-shot):   rclone move %s %s/machines/<your-id> --exclude 'machines/**'\n"+
				"      5. Start the daemon again:   swarf daemon start",
			gc.Remote, gc.Remote, gc.Remote)
		return Check{"rclone layout", false, msg}
	}
	return Check{"rclone layout", true, "Rclone remote uses per-machine layout"}
}

// CheckPeerRefs lists the refs/swarf-peers/<id> entries in the local store.
// Informational — surfaces what "last-seen" state the pull path has cached.
func CheckPeerRefs() Check {
	gc := config.ReadGlobalConfig()
	if gc == nil || gc.Backend != "rclone" {
		return Check{"peer refs", true, ""}
	}
	if !paths.IsDir(paths.StoreDir) {
		return Check{"peer refs", true, ""}
	}
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname:short) %(objectname:short)", "refs/swarf-peers/")
	cmd.Dir = paths.StoreDir
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return Check{"peer refs", true, "No peer refs yet (first pull will create them)"}
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		lines = append(lines, "    "+strings.TrimSpace(line))
	}
	return Check{"peer refs", true, "Last-seen peer tips:\n" + strings.Join(lines, "\n")}
}

// CheckRemoteReachable tries to reach the configured remote. Safe to call
// when no config exists — returns a failure check in that case.
func CheckRemoteReachable() Check {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		return Check{"remote", false, "No global config — cannot check remote"}
	}

	if gc.Backend == "git" {
		cmd := exec.Command("git", "ls-remote", gc.Remote)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return Check{"remote", false, fmt.Sprintf(
				"Git remote not reachable: %s\n    Error: %s\n    Fix: edit %s and set remote to a valid git URL",
				gc.Remote, strings.TrimSpace(string(out)), paths.GlobalConfigTOML)}
		}
		return Check{"remote", true, fmt.Sprintf("Git remote reachable: %s", gc.Remote)}
	}

	if gc.Backend == "rclone" {
		if _, err := exec.LookPath("rclone"); err != nil {
			return Check{"remote", false, "rclone not installed — install it: https://rclone.org/install/"}
		}

		if !strings.Contains(gc.Remote, ":") {
			return Check{"remote", false, fmt.Sprintf(
				"Invalid rclone remote: %q — expected format like gdrive:swarf-store\n    Fix: edit %s and set remote to remotename:path",
				gc.Remote, paths.GlobalConfigTOML)}
		}

		remoteName := strings.Split(gc.Remote, ":")[0]
		knownRemotes := rcloneRemoteNames()
		found := false
		for _, r := range knownRemotes {
			if strings.TrimSuffix(r, ":") == remoteName {
				found = true
				break
			}
		}
		if !found {
			available := "none"
			if len(knownRemotes) > 0 {
				available = strings.Join(knownRemotes, ", ")
			}
			return Check{"remote", false, fmt.Sprintf(
				"Rclone remote %q not found in rclone config\n    Available remotes: %s\n    Fix: run 'rclone config' to add it, or edit %s",
				remoteName+":", available, paths.GlobalConfigTOML)}
		}

		cmd := exec.Command("rclone", "lsd", gc.Remote)
		out, err := cmd.CombinedOutput()
		if err != nil {
			cmd2 := exec.Command("rclone", "about", remoteName+":")
			out2, err2 := cmd2.CombinedOutput()
			if err2 != nil {
				return Check{"remote", false, fmt.Sprintf(
					"Rclone remote not reachable: %s\n    rclone lsd: %s\n    rclone about: %s\n    Fix: check your rclone config with 'rclone config show %s'",
					gc.Remote, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)), remoteName)}
			}
			return Check{"remote", true, fmt.Sprintf("Rclone remote reachable (path will be created on first sync): %s", gc.Remote)}
		}
		return Check{"remote", true, fmt.Sprintf("Rclone remote reachable: %s", gc.Remote)}
	}

	return Check{"remote", false, fmt.Sprintf("Unknown backend: %s", gc.Backend)}
}

func rcloneRemoteNames() []string {
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

// CheckBinaryLocation flags ephemeral install paths that would make the
// daemon service unreliable.
func CheckBinaryLocation() Check {
	inVenv, path := daemon.IsInVenv()
	if inVenv {
		return Check{"binary location", false, fmt.Sprintf("Swarf is running from an ephemeral location (%s) — the daemon service will break when this environment is removed. Use pipx, brew, or uv tool install for a persistent install.", path)}
	}
	return Check{"binary location", true, "Binary is at a stable location"}
}

// CheckService reports whether the OS service unit is on disk. It does not
// touch service state — the daemon check covers runtime health.
func CheckService() Check {
	kind := daemon.ServiceKind()
	if kind == "" {
		return Check{"service", false, "No supported service manager found (need systemd or launchd)"}
	}
	if isServiceInstalled(kind) {
		return Check{"service", true, fmt.Sprintf("%s service installed", titleCase(kind))}
	}
	return Check{"service", false, fmt.Sprintf("%s service not installed\n    Fix: run 'swarf daemon install'", titleCase(kind))}
}

func isServiceInstalled(kind string) bool {
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

// CheckDaemonRunning reports whether the daemon process is alive. When it
// isn't, we look at systemd for hints (e.g. status=203/EXEC from a stale
// ExecStart after a cross-source reinstall).
func CheckDaemonRunning() Check {
	data, err := os.ReadFile(paths.PIDFile)
	if err != nil {
		return Check{"daemon", false, diagnoseDaemonNotRunning("Daemon is not running (no PID file)")}
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return Check{"daemon", false, diagnoseDaemonNotRunning("Daemon is not running (stale PID file)")}
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return Check{"daemon", false, diagnoseDaemonNotRunning("Daemon is not running (stale PID file)")}
	}
	return Check{"daemon", true, fmt.Sprintf("Daemon is running (PID %d)", pid)}
}

// diagnoseDaemonNotRunning augments a "not running" message with whatever
// systemd tells us. 203/EXEC means the unit's ExecStart points at a binary
// that no longer exists — common after switching install sources.
func diagnoseDaemonNotRunning(base string) string {
	if daemon.ServiceKind() != "systemd" {
		return base
	}
	cmd := exec.Command("systemctl", "--user", "show", "swarf.service",
		"--property=ExecMainStatus", "--property=ExecStart")
	out, err := cmd.Output()
	if err != nil {
		return base
	}
	var execMainStatus, execStart string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch {
		case strings.HasPrefix(line, "ExecMainStatus="):
			execMainStatus = strings.TrimPrefix(line, "ExecMainStatus=")
		case strings.HasPrefix(line, "ExecStart="):
			execStart = strings.TrimPrefix(line, "ExecStart=")
		}
	}
	if execMainStatus != "203" {
		return base
	}
	path := extractExecStartPath(execStart)
	msg := base + "\n    systemd reports status=203/EXEC — the service unit's ExecStart points at a binary that cannot be executed."
	if path != "" {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			msg += fmt.Sprintf("\n    Unit ExecStart: %s (missing)", path)
		} else {
			msg += fmt.Sprintf("\n    Unit ExecStart: %s", path)
		}
	}
	if currentExe, err := os.Executable(); err == nil {
		if resolved, rErr := filepath.EvalSymlinks(currentExe); rErr == nil {
			currentExe = resolved
		}
		msg += fmt.Sprintf("\n    Current swarf binary: %s", currentExe)
	}
	msg += "\n    Fix: re-run 'swarf daemon install' to rewrite the unit, then 'systemctl --user restart swarf'."
	return msg
}

// extractExecStartPath pulls the binary path out of a systemd ExecStart
// property value. `systemctl show` emits these in a verbose form like
//
//	{ path=/home/me/.local/bin/swarf ; argv[]=/home/me/.local/bin/swarf daemon start --foreground ; ignore_errors=no ; ... }
//
// with a simpler fallback shape for older versions.
func extractExecStartPath(s string) string {
	if i := strings.Index(s, "path="); i >= 0 {
		rest := s[i+len("path="):]
		if j := strings.IndexAny(rest, " ;"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "/") {
			return tok
		}
	}
	return ""
}

// --- Project checks ---

// CheckProject returns the set of project-level checks for the given
// working directory. No side effects — drift is reported, not repaired.
func CheckProject(cwd string) []Check {
	if !gitexec.IsInsideWorkTree(cwd) {
		return []Check{{"project", true, "Not inside a git repository."}}
	}
	hostRoot := gitexec.GetRepoRoot(cwd)
	if hostRoot == "" {
		hostRoot = cwd
	}

	var checks []Check
	sd := paths.SwarfDir(hostRoot)
	if fi, err := os.Lstat(sd); err != nil || !(fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
		return []Check{{paths.SwarfDirName + "/", true,
			fmt.Sprintf("No %s/ here. Run 'swarf init' to set up this project.", paths.SwarfDirName)}}
	}
	checks = append(checks, Check{paths.SwarfDirName + "/", true,
		fmt.Sprintf("%s/ directory exists", paths.SwarfDirName)})

	checks = append(checks, CheckGitignore(hostRoot)...)
	checks = append(checks, CheckLinks(hostRoot))
	checks = append(checks, CheckSymlinksRelative(hostRoot))
	return checks
}

// CheckGitignore reports per-path gitignore status for swarf/ and any
// swept files under swarf/.links/.
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
			if gitexec.IsTracked(cwd, rel) {
				checks = append(checks, Check{rel + " (tracked)", false,
					fmt.Sprintf("%s is swept but still tracked by git — run 'swarf doctor --fix' to heal", rel)})
			}
			return nil
		})
	}

	return checks
}

// CheckLinks reports whether every file under swarf/.links/ has a
// corresponding symlink back in the host tree. Missing symlinks are a
// common drift mode — the daemon re-links automatically on each sync, so
// here we just describe the situation and defer to the daemon.
func CheckLinks(cwd string) Check {
	linksDir := paths.LinksDir(cwd)
	if fi, err := os.Stat(linksDir); err != nil || !fi.IsDir() {
		return Check{"links", true, "No linked files"}
	}

	var missing []string
	filepath.Walk(linksDir, func(source string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(linksDir, source)
		target := filepath.Join(cwd, rel)
		fi, lErr := os.Lstat(target)
		if lErr != nil {
			missing = append(missing, rel)
			return nil
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			missing = append(missing, rel+" (not a symlink)")
		}
		return nil
	})

	if len(missing) > 0 {
		return Check{"links", false, fmt.Sprintf(
			"Missing or wrong-type symlinks: %s\n    Fix: run 'swarf doctor --fix' to heal, or 'swarf init' to re-initialize.",
			strings.Join(missing, ", "))}
	}
	return Check{"links", true, "All symlinks present"}
}

// CheckSymlinksRelative reports any absolute symlinks under the project.
// Absolute symlinks break across machines/mounts; pure-report, no rewrite.
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
			absolute = append(absolute, rel)
		}
		return nil
	})

	if len(absolute) > 0 {
		return Check{"symlink paths", false, fmt.Sprintf(
			"Absolute symlinks found: %s\n    Fix: run 'swarf init' to rewrite them as relative paths.",
			strings.Join(absolute, ", "))}
	}
	return Check{"symlink paths", true, "All symlinks are relative"}
}

// --- Orchestration ---

// RunChecks returns the complete doctor report for the given working
// directory. Pure observation: no files created, no services installed,
// no symlinks rewritten.
//
// When global config is missing (the usual "running inside a container"
// case), InJail is set and only project-local checks are returned.
// FixProject runs the content-aware reconcile for the current project:
// heals clobbered symlinks, copies divergent content into .links/,
// untracks swept files still in the git index, and ensures excludes.
func FixProject(cwd string) (link.Result, error) {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	hostRoot := gitexec.GetRepoRoot(cwd)
	if hostRoot == "" {
		return link.Result{}, fmt.Errorf("not inside a git repository")
	}
	if !paths.IsDir(paths.SwarfDir(hostRoot)) {
		return link.Result{}, fmt.Errorf("no %s/ directory — run 'swarf init' first", paths.SwarfDirName)
	}
	return link.RunWithFix(hostRoot, false, true)
}

func RunChecks(cwd string) Result {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	var r Result

	gc, configCheck := CheckGlobalConfig()
	if gc == nil {
		r.InJail = true
		if gitexec.IsInsideWorkTree(cwd) {
			hostRoot := gitexec.GetRepoRoot(cwd)
			if hostRoot == "" {
				hostRoot = cwd
			}
			if paths.IsDir(paths.SwarfDir(hostRoot)) {
				r.Project = append(r.Project, CheckGitignore(hostRoot)...)
				r.Project = append(r.Project, CheckLinks(hostRoot))
				r.Project = append(r.Project, CheckSymlinksRelative(hostRoot))
			}
		}
		return r
	}

	r.System = append(r.System, configCheck)
	r.System = append(r.System, CheckBinaryLocation())
	r.System = append(r.System, CheckMachineID())
	r.System = append(r.System, CheckStore(gc))
	r.System = append(r.System, CheckStoreRemote())
	r.System = append(r.System, CheckRemoteReachable())
	if layout := CheckRcloneLayout(); layout.Msg != "" {
		r.System = append(r.System, layout)
	}
	if peers := CheckPeerRefs(); peers.Msg != "" {
		r.System = append(r.System, peers)
	}
	r.System = append(r.System, CheckService())
	r.System = append(r.System, CheckDaemonRunning())

	r.Project = CheckProject(cwd)
	return r
}
