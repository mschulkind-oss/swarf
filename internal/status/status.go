package status

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon/backends"
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
		// Pending = files that haven't reached the remote yet.
		// This includes: unmirrored project files + uncommitted store
		// changes + committed-but-not-pushed commits.
		pending := countPendingFiles()
		storeStatus := gitexec.StatusPorcelain(paths.StoreDir)
		storeDirty := countNonEmpty(strings.Split(strings.TrimSpace(storeStatus), "\n"))
		pending += storeDirty
		unpushed := countUnpushedCommits(gc)
		if pending > 0 || unpushed > 0 {
			parts := []string{}
			if pending > 0 {
				parts = append(parts, fmt.Sprintf("%d file(s) not yet committed", pending))
			}
			if unpushed > 0 {
				parts = append(parts, fmt.Sprintf("%d commit(s) not yet pushed", unpushed))
			}
			rows = append(rows, []string{"Pending", yellowStyle.Render(strings.Join(parts, ", "))})
		} else {
			rows = append(rows, []string{"Pending", greenStyle.Render("all synced to remote")})
		}
		if out := gitLog(paths.StoreDir); out != "" {
			rows = append(rows, []string{"Last commit", dimStyle.Render(out)})
		}

		// Separate timestamps for local commit vs remote push.
		if t, ok := backends.ReadStamp(paths.LastCommitFile); ok {
			rows = append(rows, []string{"Local save", greenStyle.Render(timeAgo(t))})
		} else {
			rows = append(rows, []string{"Local save", dimStyle.Render("never")})
		}
		if t, ok := backends.ReadStamp(paths.LastPushFile); ok {
			rows = append(rows, []string{"Remote push", greenStyle.Render(timeAgo(t))})
		} else {
			rows = append(rows, []string{"Remote push", yellowStyle.Render("never — data has not reached the remote yet")})
		}

		// Remote verification — did data actually land?
		rows = append(rows, remoteVerifyRows(gc)...)
	} else {
		rows = append(rows, []string{"Status", redStyle.Render("Store not initialized. Run 'swarf init'.")})
	}

	t := table.New().
		Border(lipgloss.HiddenBorder()).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if col == 0 {
				return lipgloss.NewStyle().Bold(true).Width(14)
			}
			return lipgloss.NewStyle()
		})
	fmt.Println(t)
}

func remoteVerifyRows(gc *config.GlobalConfig) [][]string {
	if gc.Remote == "" {
		return [][]string{{"Remote sync", dimStyle.Render("no remote configured")}}
	}

	if gc.Backend == "git" {
		return verifyGitRemote()
	}
	if gc.Backend == "rclone" {
		return verifyRcloneRemote(gc.Remote)
	}
	return [][]string{{"Remote sync", redStyle.Render("unknown backend: " + gc.Backend)}}
}

func verifyGitRemote() [][]string {
	localHEAD := gitexec.RevParseHEAD(paths.StoreDir)
	if localHEAD == "" {
		return [][]string{{"Remote sync", dimStyle.Render("no local commits yet")}}
	}

	remoteHEAD := gitexec.LsRemoteHEAD(paths.StoreDir, "origin")
	if remoteHEAD == "" {
		return [][]string{{"Remote sync", redStyle.Render("cannot reach remote — data may not be pushed")}}
	}

	short := localHEAD[:8]
	if localHEAD == remoteHEAD {
		return [][]string{{"Remote sync", greenStyle.Render(fmt.Sprintf("verified — local and remote match (%s)", short))}}
	}
	return [][]string{
		{"Remote sync", yellowStyle.Render(fmt.Sprintf("out of sync — local %s, remote %s", short, remoteHEAD[:8]))},
		{"", dimStyle.Render("Local commits haven't been pushed yet. The daemon will retry.")},
	}
}

func verifyRcloneRemote(remote string) [][]string {
	if _, err := exec.LookPath("rclone"); err != nil {
		return [][]string{{"Remote sync", redStyle.Render("rclone not installed — cannot verify")}}
	}

	// Count local files (excluding .git internals).
	localCount, localSize := countLocalStore()

	// Count remote files.
	cmd := exec.Command("rclone", "size", remote, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		errMsg := strings.TrimSpace(string(out))
		if errMsg == "" {
			errMsg = err.Error()
		}
		return [][]string{
			{"Remote sync", redStyle.Render("cannot reach remote")},
			{"", dimStyle.Render(errMsg)},
		}
	}

	type sizeResult struct {
		Count int64 `json:"count"`
		Bytes int64 `json:"bytes"`
	}
	var rs sizeResult
	if err := json.Unmarshal(out, &rs); err != nil {
		return [][]string{{"Remote sync", yellowStyle.Render("remote reachable but cannot parse size")}}
	}

	if rs.Count == 0 {
		return [][]string{
			{"Remote sync", redStyle.Render("remote is empty — no data has been uploaded yet")},
			{"", dimStyle.Render("Check daemon logs. The daemon may not have synced yet.")},
		}
	}

	remoteStr := fmt.Sprintf("%d files, %s", rs.Count, humanSize(rs.Bytes))
	localStr := fmt.Sprintf("%d files, %s", localCount, humanSize(localSize))

	if localCount == rs.Count {
		return [][]string{
			{"Remote sync", greenStyle.Render(fmt.Sprintf("verified — %s on remote", remoteStr))},
			{"", dimStyle.Render(fmt.Sprintf("local: %s", localStr))},
		}
	}
	return [][]string{
		{"Remote sync", yellowStyle.Render(fmt.Sprintf("mismatch — remote has %s, local has %s", remoteStr, localStr))},
		{"", dimStyle.Render("The daemon may still be syncing. Check again shortly.")},
	}
}

// countUnpushedCommits counts local commits not yet on the remote.
func countUnpushedCommits(gc *config.GlobalConfig) int {
	if gc.Backend == "git" {
		// For git: count commits ahead of origin.
		cmd := exec.Command("git", "rev-list", "--count", "HEAD", "--not", "--remotes=origin")
		cmd.Dir = paths.StoreDir
		out, err := cmd.Output()
		if err != nil {
			return 0
		}
		var n int
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
		return n
	}
	if gc.Backend == "rclone" {
		// For rclone: if last-push is before last-commit, there are unpushed changes.
		commitTime, commitOk := backends.ReadStamp(paths.LastCommitFile)
		pushTime, pushOk := backends.ReadStamp(paths.LastPushFile)
		if !commitOk {
			return 0 // nothing committed
		}
		if !pushOk || pushTime.Before(commitTime) {
			// Count commits since last push.
			if !pushOk {
				// Never pushed — count all commits.
				cmd := exec.Command("git", "rev-list", "--count", "HEAD")
				cmd.Dir = paths.StoreDir
				out, err := cmd.Output()
				if err != nil {
					return 1 // at least signal something is pending
				}
				var n int
				fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
				return n
			}
			return 1 // at least 1 unpushed
		}
	}
	return 0
}

// countPendingFiles counts files in project swarf/ dirs that haven't been
// mirrored to the store yet (new files or files with different size/mtime).
func countPendingFiles() int {
	drawers := config.ReadDrawers()
	pending := 0
	for _, d := range drawers {
		src := paths.SwarfDir(d.Host)
		dst := filepath.Join(paths.StoreDir, d.Slug)
		if !paths.IsDir(src) {
			continue
		}
		filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(src, path)
			target := filepath.Join(dst, rel)
			dstInfo, dstErr := os.Stat(target)
			if dstErr != nil {
				// File doesn't exist in store yet.
				pending++
				return nil
			}
			if info.Size() != dstInfo.Size() || info.ModTime().After(dstInfo.ModTime()) {
				pending++
			}
			return nil
		})
	}
	return pending
}

func countLocalStore() (int64, int64) {
	var count, size int64
	filepath.Walk(paths.StoreDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		count++
		size += info.Size()
		return nil
	})
	return count, size
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm ago", int(d.Hours()), int(d.Minutes())%60)
	default:
		return t.Format("2006-01-02 15:04")
	}
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
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
		hasSwarfDir := paths.IsDir(paths.SwarfDir(d.Host))
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
