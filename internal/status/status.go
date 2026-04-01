package status

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

func Run() {
	gc := config.ReadGlobalConfig()
	if gc == nil {
		console.Info("No global config found. Run 'swarf init' in a project.")
		return
	}

	printStoreInfo(gc)
	printProjects()
	printDaemonStatus()
}

func printStoreInfo(gc *config.GlobalConfig) {
	console.Header("Store")
	console.Infof("  Path:    %s", console.Color(console.Dim, paths.StoreDir))
	console.Infof("  Backend: %s", console.Color(console.Cyan, gc.Backend))
	if gc.Remote != "" {
		console.Infof("  Remote:  %s", gc.Remote)
	} else {
		console.Infof("  Remote:  %s", console.Color(console.Dim, "not set"))
	}

	if !paths.IsDir(paths.StoreDir) || !gitexec.IsRepo(paths.StoreDir) {
		console.Infof("  %s", console.Color(console.Red, "Store not initialized. Run 'swarf init'."))
		return
	}

	status := gitexec.StatusPorcelain(paths.StoreDir)
	pending := countNonEmpty(strings.Split(strings.TrimSpace(status), "\n"))
	if pending > 0 {
		console.Infof("  Pending: %s", console.Color(console.Yellow, fmt.Sprintf("%d file(s)", pending)))
	} else {
		console.Infof("  Pending: %s", console.Color(console.Green, "0 file(s)"))
	}

	if out := gitLog(paths.StoreDir); out != "" {
		console.Infof("  Last sync: %s", console.Color(console.Dim, out))
	}
	if url := gitexec.RemoteURL(paths.StoreDir, ""); url != "" {
		console.Infof("  Git remote: %s", url)
	}
}

func printProjects() {
	drawers := config.ReadDrawers()
	if len(drawers) == 0 {
		return
	}
	console.Info("")
	console.Infof("%-20s %-40s %s", console.Color(console.Bold, "PROJECT"),
		console.Color(console.Bold, "HOST PATH"),
		console.Color(console.Bold, "STATUS"))
	console.Infof("%-20s %-40s %s", "-------", "---------", "------")
	home, _ := os.UserHomeDir()
	for _, d := range drawers {
		displayHost := strings.Replace(d.Host, home, "~", 1)
		hasSwarfDir := paths.IsDir(d.Host + "/.swarf")
		statusStr := console.Color(console.Red, "missing")
		if hasSwarfDir {
			statusStr = console.Color(console.Green, "ok")
		}
		console.Infof("%-20s %-40s %s", d.Slug, displayHost, statusStr)
	}
}

func printDaemonStatus() {
	console.Info("")
	pid, ok := readPID(paths.PIDFile)
	if !ok {
		console.Infof("Daemon: %s", console.Color(console.Yellow, "not running"))
		return
	}
	if err := syscall.Kill(pid, 0); err != nil {
		console.Infof("Daemon: %s (stale PID file)", console.Color(console.Red, "not running"))
		return
	}
	console.Infof("Daemon: %s (PID %d)", console.Color(console.Green, "running"), pid)
}

func countNonEmpty(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func gitLog(dir string) string {
	cmd := exec.Command("git", "log", "-1", "--format=%cr — %s")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return 0, false
	}
	return pid, true
}
