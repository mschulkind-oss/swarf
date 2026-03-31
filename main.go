package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mschulkind-oss/swarf/internal/clone"
	"github.com/mschulkind-oss/swarf/internal/config"
	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/daemon"
	"github.com/mschulkind-oss/swarf/internal/doctor"
	"github.com/mschulkind-oss/swarf/internal/enter"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/link"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/pull"
	"github.com/mschulkind-oss/swarf/internal/status"
	"github.com/mschulkind-oss/swarf/internal/sweep"
	"github.com/mschulkind-oss/swarf/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:           "swarf",
		Short:         "Invisible, auto-syncing personal storage for any git repo",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		initCmd(),
		cloneCmd(),
		pullCmd(),
		statusCmd(),
		doctorCmd(),
		linkCmd(),
		enterCmd(),
		sweepCmd(),
		daemonCmd(),
	)

	if err := root.Execute(); err != nil {
		console.Error(err.Error())
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize swarf for the current project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			gc := config.ReadGlobalConfig()
			if gc == nil {
				gc = promptGlobalConfig()
			}
			return initialize.Run(gc)
		},
	}
}

func promptGlobalConfig() *config.GlobalConfig {
	console.Info("\nNo global config found. Let's set one up.\n")
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Backend [git/rclone] (git): ")
	backend, _ := reader.ReadString('\n')
	backend = strings.TrimSpace(backend)
	if backend == "" {
		backend = "git"
	}

	fmt.Print("Remote URL: ")
	remote, _ := reader.ReadString('\n')
	remote = strings.TrimSpace(remote)

	gc := &config.GlobalConfig{Backend: backend, Remote: remote, Debounce: "5s"}
	config.WriteGlobalConfig(gc)
	console.Ok(fmt.Sprintf("Wrote %s", paths.GlobalConfigTOML))
	return gc
}

func cloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone",
		Short: "Clone the central store from your configured remote",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return clone.Run() },
	}
}

func pullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Pull the latest changes from the remote into the store",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return pull.Run() },
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync status for the central store and all projects",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { status.Run() },
	}
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate swarf setup is healthy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			allOk := true
			for _, c := range doctor.RunAllChecks("") {
				if c.OK {
					console.Ok(c.Msg)
				} else {
					console.Error(c.Msg)
					allOk = false
				}
			}
			if !allOk {
				return fmt.Errorf("some checks failed")
			}
			fmt.Println("\nAll checks passed.")
			return nil
		},
	}
}

func linkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Create symlinks from .swarf/links/ into the host repo tree",
		Args:  cobra.NoArgs,
	}
	quiet := cmd.Flags().BoolP("quiet", "q", false, "Only show warnings")
	cmd.RunE = func(c *cobra.Command, args []string) error {
		_, err := link.Run("", *quiet)
		return err
	}
	return cmd
}

func enterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enter",
		Short: "Run on project enter (mise hook)",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { enter.Run() },
	}
}

func sweepCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sweep [files...]",
		Short: "Sweep files into .swarf/links/ and symlink them back",
		Args:  cobra.MinimumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return sweep.Run(args, "") },
	}
}

func daemonCmd() *cobra.Command {
	d := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the swarf background sync daemon",
	}

	startCmd := &cobra.Command{Use: "start", Short: "Start the background sync daemon", Args: cobra.NoArgs}
	fg := startCmd.Flags().Bool("foreground", false, "Run in the foreground")
	startCmd.Run = func(cmd *cobra.Command, args []string) {
		daemon.DoStart(*fg)
		if !*fg {
			console.Ok("Daemon started.")
		}
	}

	stopCmd := &cobra.Command{
		Use: "stop", Short: "Stop the background sync daemon", Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			pid, ok := readPID()
			if !ok {
				console.Info("Daemon is not running.")
				return
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				console.Info("Daemon is not running (stale PID file).")
			} else {
				console.Ok(fmt.Sprintf("Sent SIGTERM to daemon (PID %d)", pid))
			}
			os.Remove(paths.PIDFile)
		},
	}

	statusCmd := &cobra.Command{
		Use: "status", Short: "Check if the daemon is running", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, ok := readPID()
			if !ok {
				return fmt.Errorf("daemon is not running")
			}
			if err := syscall.Kill(pid, 0); err != nil {
				os.Remove(paths.PIDFile)
				return fmt.Errorf("daemon is not running (stale PID file)")
			}
			console.Ok(fmt.Sprintf("Daemon is running (PID %d)", pid))
			return nil
		},
	}

	installCmd := &cobra.Command{
		Use: "install", Short: "Install systemd user service", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.InstallSystemdService(); err != nil {
				return err
			}
			console.Ok("Systemd user service installed and started.")
			return nil
		},
	}

	d.AddCommand(startCmd, stopCmd, statusCmd, installCmd)
	return d
}

func readPID() (int, bool) {
	data, err := os.ReadFile(paths.PIDFile)
	if err != nil {
		return 0, false
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return 0, false
	}
	return pid, true
}
