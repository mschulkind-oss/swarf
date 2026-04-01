package status

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/gitexec"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var (
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	cyanStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
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

	rows := [][]string{
		{"Path", dimStyle.Render(paths.StoreDir)},
		{"Backend", cyanStyle.Render(gc.Backend)},
	}

	if gc.Remote != "" {
		rows = append(rows, []string{"Remote", gc.Remote})
	} else {
		rows = append(rows, []string{"Remote", dimStyle.Render("not set")})
	}

	if paths.IsDir(paths.StoreDir) && gitexec.IsRepo(paths.StoreDir) {
		status := gitexec.StatusPorcelain(paths.StoreDir)
		pending := countNonEmpty(strings.Split(strings.TrimSpace(status), "\n"))
		if pending > 0 {
			rows = append(rows, []string{"Pending", yellowStyle.Render(fmt.Sprintf("%d file(s)", pending))})
		} else {
			rows = append(rows, []string{"Pending", greenStyle.Render("0 file(s)")})
		}
		if out := gitLog(paths.StoreDir); out != "" {
			rows = append(rows, []string{"Last sync", dimStyle.Render(out)})
		}
		if url := gitexec.RemoteURL(paths.StoreDir, ""); url != "" {
			rows = append(rows, []string{"Git remote", url})
		}
	} else {
		rows = append(rows, []string{"Status", redStyle.Render("Store not initialized. Run 'swarf init'.")})
	}

	t := table.New().
		Border(lipgloss.HiddenBorder()).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if col == 0 {
				return lipgloss.NewStyle().Bold(true).Width(12)
			}
			return lipgloss.NewStyle()
		})
	fmt.Println(t)
}

func printProjects() {
	drawers := config.ReadDrawers()
	if len(drawers) == 0 {
		return
	}
	fmt.Println()
	console.Header("Projects")

	home, _ := os.UserHomeDir()
	var rows [][]string
	for _, d := range drawers {
		displayHost := strings.Replace(d.Host, home, "~", 1)
		hasSwarfDir := paths.IsDir(d.Host + "/.swarf")
		statusStr := redStyle.Render("✗ missing")
		if hasSwarfDir {
			statusStr = greenStyle.Render("✓ ok")
		}
		rows = append(rows, []string{d.Slug, displayHost, statusStr})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderStyle).
		Headers("PROJECT", "PATH", "STATUS").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
			if row == table.HeaderRow {
				return s.Bold(true).Foreground(lipgloss.Color("15"))
			}
			return s
		})
	fmt.Println(t)
}

func printDaemonStatus() {
	fmt.Println()
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
