package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const systemdUnit = `[Unit]
Description=Swarf Auto-Sync Daemon
After=network.target

[Service]
Type=simple
ExecStart=%s daemon start --foreground
Restart=on-failure
RestartSec=5
Environment=PATH=%s

[Install]
WantedBy=default.target
`

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.swarf.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
        <string>start</string>
        <string>--foreground</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>%s/swarf.out.log</string>
    <key>StandardErrorPath</key>
    <string>%s/swarf.err.log</string>
</dict>
</plist>
`

// IsInVenv returns true if the swarf binary appears to be running from
// inside a Python virtual environment or other ephemeral location.
func IsInVenv() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		return false, ""
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// Check common ephemeral patterns.
	for _, marker := range []string{
		"/.venv/",
		"/venv/",
		"/.virtualenvs/",
		"/site-packages/",
		"/tmp/",
	} {
		if strings.Contains(exe, marker) {
			return true, exe
		}
	}

	// Check VIRTUAL_ENV environment variable.
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		if strings.HasPrefix(exe, venv) {
			return true, exe
		}
	}

	return false, ""
}

// ServiceKind reports what service manager is available.
// Returns "systemd", "launchd", or "".
func ServiceKind() string {
	if runtime.GOOS == "darwin" {
		return "launchd"
	}
	// Check for systemd.
	if _, err := exec.LookPath("systemctl"); err == nil {
		return "systemd"
	}
	return ""
}

func InstallService() error {
	kind := ServiceKind()
	switch kind {
	case "systemd":
		return installSystemd()
	case "launchd":
		return installLaunchd()
	default:
		return fmt.Errorf("no supported service manager found (need systemd or launchd).\n  You can still run the daemon manually: swarf daemon start")
	}
}

// InstallSystemdService installs and starts a systemd user service.
// Kept as a public alias for backward compatibility.
func InstallSystemdService() error {
	return installSystemd()
}

func installSystemd() error {
	swarfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine swarf binary path: %w", err)
	}
	swarfPath, _ = filepath.EvalSymlinks(swarfPath)

	content := fmt.Sprintf(systemdUnit, swarfPath, os.Getenv("PATH"))

	home, _ := os.UserHomeDir()
	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0o755); err != nil {
		return err
	}
	serviceFile := filepath.Join(serviceDir, "swarf.service")
	if err := os.WriteFile(serviceFile, []byte(content), 0o644); err != nil {
		return err
	}

	for _, args := range [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "swarf"},
		{"systemctl", "--user", "start", "swarf"},
	} {
		if err := exec.Command(args[0], args[1:]...).Run(); err != nil {
			return fmt.Errorf("%s failed: %w", args[0], err)
		}
	}
	return nil
}

func installLaunchd() error {
	swarfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine swarf binary path: %w", err)
	}
	swarfPath, _ = filepath.EvalSymlinks(swarfPath)

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, "Library", "Logs", "swarf")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}

	content := fmt.Sprintf(launchdPlist, swarfPath, logDir, logDir)

	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return err
	}
	plistFile := filepath.Join(agentsDir, "com.swarf.daemon.plist")

	// Unload first if it already exists (ignore errors).
	if _, err := os.Stat(plistFile); err == nil {
		exec.Command("launchctl", "unload", plistFile).Run()
	}

	if err := os.WriteFile(plistFile, []byte(content), 0o644); err != nil {
		return err
	}

	if err := exec.Command("launchctl", "load", plistFile).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}
	return nil
}
