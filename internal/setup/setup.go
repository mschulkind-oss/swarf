// Package setup contains the interactive first-run orchestration that
// 'swarf init' uses: prompt for global config, create the central store,
// offer to install the OS service. Lives in its own package to avoid an
// import cycle between `initialize` (which the daemon imports) and
// `daemon` (which this code needs for service install).
package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

// SetupSystem runs the interactive system-level setup: global config,
// machine id, central store, and optional service install. Returns the
// resulting global config, or nil if the user cancelled the config prompt.
//
// This is the init-only counterpart to the doctor package's pure checks.
// Doctor observes; init acts.
func SetupSystem(interactive bool) *config.GlobalConfig {
	gc := ensureGlobalConfig(interactive)
	if gc == nil {
		return nil
	}

	// Persist a default machine id if one isn't set yet. Idempotent.
	config.EnsureMachineID()

	if err := initialize.EnsureStore("", gc); err != nil {
		console.Error(fmt.Sprintf("Failed to create store: %v", err))
		return gc
	}

	if interactive {
		ensureService()
	}
	return gc
}

// ensureGlobalConfig returns the existing global config, or prompts the user
// to create one when interactive. Returns nil if there's no config and we
// couldn't create one.
func ensureGlobalConfig(interactive bool) *config.GlobalConfig {
	if gc := config.ReadGlobalConfig(); gc != nil {
		return gc
	}
	if !interactive {
		return nil
	}

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

	var remote string
	if backend == "rclone" {
		remote = promptRcloneRemote(reader)
		if remote == "" {
			return nil
		}
	} else {
		fmt.Print("  Remote URL (your private backup repo): ")
		remote, _ = reader.ReadString('\n')
		remote = strings.TrimSpace(remote)
	}

	gc := &config.GlobalConfig{Backend: backend, Remote: remote, Debounce: "5s"}
	if err := config.WriteGlobalConfig(gc); err != nil {
		console.Error(fmt.Sprintf("Failed to write config: %v", err))
		return nil
	}
	console.Ok(fmt.Sprintf("Wrote %s", paths.GlobalConfigTOML))
	return gc
}

// ensureService prompts the user to install the OS service if it isn't
// already installed. No-op when the service is present or unavailable.
func ensureService() {
	kind := daemon.ServiceKind()
	if kind == "" {
		return
	}
	if IsServiceInstalled() {
		return
	}
	if inVenv, _ := daemon.IsInVenv(); inVenv {
		console.Warn("Skipping service install — swarf is running from an ephemeral location.")
		console.Hint("Install swarf persistently (brew, go install, pipx, uv tool install), then run 'swarf daemon install'.")
		return
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("  Install %s service for auto-sync? [Y/n] ", kind)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		console.Hint("No problem. Install later: swarf daemon install")
		return
	}
	if err := daemon.InstallService(); err != nil {
		console.Error(fmt.Sprintf("Service install failed: %v", err))
		console.Hint("You can try again later: swarf daemon install")
		return
	}
	console.Ok(fmt.Sprintf("Installed %s service — daemon is running.", titleCase(kind)))
}

// IsServiceInstalled reports whether a swarf OS service unit is on disk.
func IsServiceInstalled() bool {
	kind := daemon.ServiceKind()
	home, _ := os.UserHomeDir()
	switch kind {
	case "systemd":
		_, err := os.Stat(home + "/.config/systemd/user/swarf.service")
		return err == nil
	case "launchd":
		_, err := os.Stat(home + "/Library/LaunchAgents/com.swarf.daemon.plist")
		return err == nil
	default:
		return false
	}
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

// promptRcloneRemote walks the user through picking an rclone remote and path.
// Returns the full remote spec (e.g. "gdrive:swarf-store") or "" if cancelled.
func promptRcloneRemote(reader *bufio.Reader) string {
	remotes := listRcloneRemotes()
	if len(remotes) == 0 {
		console.Info("")
		console.Warn("No rclone remotes found.")
		console.Info("")
		console.Info("  Set one up first:")
		console.Info("    rclone config")
		console.Info("")
		console.Hint("Then re-run this command.")
		return ""
	}

	console.Info("")
	console.Info("  Pick an rclone remote:")
	console.Info("")
	for i, r := range remotes {
		console.Infof("    %d. %s", i+1, r)
	}
	console.Info("")
	fmt.Printf("  Enter a number (1-%d), or q to quit: ", len(remotes))
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(answer)

	if answer == "q" || answer == "" {
		return ""
	}

	var idx int
	if _, err := fmt.Sscanf(answer, "%d", &idx); err != nil || idx < 1 || idx > len(remotes) {
		console.Error(fmt.Sprintf("Invalid choice: %s", answer))
		return ""
	}

	chosen := remotes[idx-1]
	defaultPath := "swarf-store"
	console.Info("")
	fmt.Printf("  Directory path on %s [%s]: ", chosen, defaultPath)
	dirPath, _ := reader.ReadString('\n')
	dirPath = strings.TrimSpace(dirPath)

	if dirPath == "" {
		dirPath = defaultPath
	}
	result := chosen + dirPath
	console.Ok(fmt.Sprintf("Remote: %s", result))
	return result
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
