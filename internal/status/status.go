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
	console.Info("\033[1mStore\033[0m")
	console.Infof("  Path:    %s", paths.StoreDir)
	console.Infof("  Backend: %s", gc.Backend)
	if gc.Remote != "" {
		console.Infof("  Remote:  %s", gc.Remote)
	} else {
		console.Info("  Remote:  not set")
	}

	if !paths.IsDir(paths.StoreDir) || !gitexec.IsRepo(paths.StoreDir) {
		console.Info("  \033[31mStore not initialized. Run 'swarf init'.\033[0m")
		return
	}

	status := gitexec.StatusPorcelain(paths.StoreDir)
	pending := countNonEmpty(strings.Split(strings.TrimSpace(status), "\n"))
	console.Infof("  Pending: %d file(s)", pending)

	if out := gitLog(paths.StoreDir); out != "" {
		console.Infof("  Last sync: %s", out)
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
	console.Infof("%-20s %-40s %s", "PROJECT", "HOST PATH", "LINKED")
	console.Infof("%-20s %-40s %s", "-------", "---------", "------")
	home, _ := os.UserHomeDir()
	for _, d := range drawers {
		displayHost := strings.Replace(d.Host, home, "~", 1)
		linked := isSymlink(d.Host + "/.swarf")
		linkedStr := "\033[31mno\033[0m"
		if linked {
			linkedStr = "\033[32myes\033[0m"
		}
		console.Infof("%-20s %-40s %s", d.Slug, displayHost, linkedStr)
	}
}

func printDaemonStatus() {
	console.Info("")
	pid, ok := readPID(paths.PIDFile)
	if !ok {
		console.Info("Daemon: \033[33mnot running\033[0m")
		return
	}
	if err := syscall.Kill(pid, 0); err != nil {
		console.Info("Daemon: \033[31mnot running\033[0m (stale PID file)")
		return
	}
	console.Infof("Daemon: \033[32mrunning\033[0m (PID %d)", pid)
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

func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
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
