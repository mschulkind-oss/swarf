package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func InstallSystemdService() error {
	swarfPath, err := exec.LookPath("swarf")
	if err != nil {
		return fmt.Errorf("swarf not found on PATH. Install it first")
	}

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
