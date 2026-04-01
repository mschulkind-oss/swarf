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
	"github.com/mschulkind-oss/swarf/internal/docs"
	"github.com/mschulkind-oss/swarf/internal/enter"
	"github.com/mschulkind-oss/swarf/internal/initialize"
	"github.com/mschulkind-oss/swarf/internal/paths"
	"github.com/mschulkind-oss/swarf/internal/pull"
	"github.com/mschulkind-oss/swarf/internal/status"
	"github.com/mschulkind-oss/swarf/internal/sweep"
	"github.com/mschulkind-oss/swarf/internal/version"
)

const (
	groupCore   = "core"
	groupSync   = "sync"
	groupSystem = "system"
	groupInfo   = "info"
)

func main() {
	root := &cobra.Command{
		Use:           "swarf",
		Short:         "Invisible, auto-syncing personal storage for any git repo",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Swarf gives your side-files a durable home alongside any project,
without touching the project repo.

Agent research, design docs, personal notes, skill files — swept into
a private store that auto-syncs in the background. Invisible to git,
durable across machines.

Get started:
  swarf init              Set up swarf in the current project
  swarf docs quickstart   Full walkthrough

Learn more:
  swarf docs              List all documentation topics
  swarf docs architecture How swarf works under the hood`,
	}

	root.SetUsageTemplate(usageTemplate())
	root.SetHelpTemplate(helpTemplate())
	root.SetVersionTemplate("swarf version {{.Version}}\n")

	root.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core Commands:"},
		&cobra.Group{ID: groupSync, Title: "Sync & Remote:"},
		&cobra.Group{ID: groupSystem, Title: "System:"},
		&cobra.Group{ID: groupInfo, Title: "Info & Diagnostics:"},
	)

	root.AddCommand(
		initCmd(),
		sweepCmd(),
		enterCmd(),
		cloneCmd(),
		pullCmd(),
		daemonCmd(),
		statusCmd(),
		doctorCmd(),
		docsCmd(),
	)

	if err := root.Execute(); err != nil {
		console.Error(err.Error())
		os.Exit(1)
	}
}

// --- Core Commands ---

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "init",
		Short:   "Initialize swarf for the current project",
		GroupID: groupCore,
		Args:    cobra.NoArgs,
		Long: `Initialize swarf in the current git repository.

Creates the central store (if it doesn't exist), registers this project,
sets up .swarf/ directory, configures .git/info/exclude, re-links any
swept files, and creates the mise enter hook. On first run, walks you
through global config setup.`,
		Example: `  swarf init              # interactive setup (first time)
  cd ~/other-project && swarf init   # instant (reuses config)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			gc := config.ReadGlobalConfig()
			if gc == nil {
				gc = promptGlobalConfig()
			}
			return initialize.Run(gc)
		},
	}
}

func sweepCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "sweep <file>...",
		Short:   "Move files into .swarf/links/ and symlink them back",
		GroupID: groupCore,
		Args:    cobra.MinimumNArgs(1),
		Long: `Sweep moves files from the host repo into the swarf store and replaces
them with symlinks. This lets files like AGENTS.md appear in the project
tree while living in swarf's private, synced storage.

The original path is automatically excluded from git via .git/info/exclude.
Run 'swarf docs sweep' for the full guide.`,
		Example: `  swarf sweep AGENTS.md
  swarf sweep CLAUDE.md .copilot/skills/SKILL.md
  swarf sweep docs/design.md`,
		RunE: func(cmd *cobra.Command, args []string) error { return sweep.Run(args, "") },
	}
}

func enterCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "enter",
		Short:  "Re-link on project enter (internal, called by mise hook)",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run:    func(cmd *cobra.Command, args []string) { enter.Run() },
	}
}

// --- Sync & Remote ---

func cloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "clone",
		Short:   "Clone the central store from your configured remote",
		GroupID: groupSync,
		Args:    cobra.NoArgs,
		Long: `Clones the central store from the remote configured in
~/.config/swarf/config.toml. Use this when setting up swarf on a
new machine where the store doesn't exist yet.

After cloning, run 'swarf init' in each project directory to re-link.`,
		Example: `  swarf clone             # clone store from remote
  swarf init              # then init each project`,
		RunE: func(cmd *cobra.Command, args []string) error { return clone.Run() },
	}
}

func pullCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "pull",
		Short:   "Pull latest changes from the remote into the store",
		GroupID: groupSync,
		Args:    cobra.NoArgs,
		Long: `Pulls the latest changes from the configured remote into the local
store. Useful if another machine pushed changes and you want them
immediately (the daemon also pulls periodically).`,
		Example: `  swarf pull`,
		RunE:    func(cmd *cobra.Command, args []string) error { return pull.Run() },
	}
}

// --- System ---

func daemonCmd() *cobra.Command {
	d := &cobra.Command{
		Use:     "daemon <command>",
		Short:   "Manage the background sync daemon",
		GroupID: groupSystem,
		Long: `The swarf daemon watches the central store for file changes and
auto-syncs to your configured backend. It handles all projects from
a single process.

Run 'swarf docs daemon' for the full guide.`,
	}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the background sync daemon",
		Args:  cobra.NoArgs,
		Long: `Starts the daemon process. By default it daemonizes (detaches from
the terminal). Use --foreground for debugging.`,
		Example: `  swarf daemon start
  swarf daemon start --foreground`,
	}
	fg := startCmd.Flags().Bool("foreground", false, "Run in the foreground (don't daemonize)")
	startCmd.Run = func(cmd *cobra.Command, args []string) {
		daemon.DoStart(*fg)
		if !*fg {
			console.Ok("Daemon started.")
		}
	}

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the background sync daemon",
		Args:  cobra.NoArgs,
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

	daemonStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check if the daemon is running",
		Args:  cobra.NoArgs,
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
		Use:   "install",
		Short: "Install systemd user service (auto-start on login)",
		Args:  cobra.NoArgs,
		Long: `Installs a systemd user service that starts the daemon automatically
on login. View logs with: journalctl --user -u swarf -f`,
		Example: `  swarf daemon install
  systemctl --user status swarf
  journalctl --user -u swarf -f`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.InstallSystemdService(); err != nil {
				return err
			}
			console.Ok("Systemd user service installed and started.")
			return nil
		},
	}

	d.AddCommand(startCmd, stopCmd, daemonStatusCmd, installCmd)
	return d
}

// --- Info & Diagnostics ---

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show sync status for the central store and all projects",
		GroupID: groupInfo,
		Args:    cobra.NoArgs,
		Long: `Displays the current state of the central store, registered projects,
sync status, and daemon health. A quick way to see everything at a glance.`,
		Example: `  swarf status`,
		Run:     func(cmd *cobra.Command, args []string) { status.Run() },
	}
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Validate that swarf is set up correctly",
		GroupID: groupInfo,
		Args:    cobra.NoArgs,
		Long: `Runs a series of health checks: global config, store existence,
remote connectivity, daemon status, .swarf/ directory, gitignore
entries, mise hooks, and symlink integrity.

Green checks pass, red checks need attention.`,
		Example: `  swarf doctor`,
		RunE: func(cmd *cobra.Command, args []string) error {
			result := doctor.RunAllChecks("")
			allOk := true

			if result.InJail {
				console.Header("Project checks (no global config — running in container?)")
				console.Hint("Only sweep works here — write files to .swarf/ directly.")
				console.Hint("The host daemon handles sync and backup.")
				console.Info("")
			} else {
				console.Header("System")
				for _, c := range result.System {
					if c.OK {
						console.Ok(c.Msg)
					} else {
						console.Error(c.Msg)
						allOk = false
					}
				}
				console.Info("")
				console.Header("Project")
			}

			for _, c := range result.Project {
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
			console.Info("")
			console.Ok("All checks passed.")
			return nil
		},
	}
}

func docsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "docs [topic]",
		Short:   "Browse built-in documentation",
		GroupID: groupInfo,
		Args:    cobra.MaximumNArgs(1),
		Long: `Access swarf's built-in documentation. Run without arguments to see
all available topics, or specify a topic name for details.`,
		Example: `  swarf docs                 # list all topics
  swarf docs quickstart      # getting started guide
  swarf docs architecture    # how swarf works
  swarf docs config          # configuration reference
  swarf docs sweep           # how sweep works
  swarf docs daemon          # daemon operations
  swarf docs backends        # git and rclone setup`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				console.Header("Available documentation topics:")
				console.Info("")
				console.Info(docs.ListTopics())
				console.Hint("  Run 'swarf docs <topic>' to read a topic.")
				return nil
			}
			topic := docs.Get(args[0])
			if topic == nil {
				return fmt.Errorf("unknown topic %q — run 'swarf docs' to see available topics", args[0])
			}
			console.Info(topic.Content)
			return nil
		},
	}
	return cmd
}

// --- Helpers ---

func promptGlobalConfig() *config.GlobalConfig {
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

	fmt.Print("  Remote URL: ")
	remote, _ := reader.ReadString('\n')
	remote = strings.TrimSpace(remote)

	gc := &config.GlobalConfig{Backend: backend, Remote: remote, Debounce: "5s"}
	config.WriteGlobalConfig(gc)
	console.Ok(fmt.Sprintf("Wrote %s", paths.GlobalConfigTOML))
	return gc
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

func usageTemplate() string {
	// Based on cobra's default template with ANSI colors injected.
	return `{{"\033[1m"}}Usage:{{"\033[0m"}}{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

{{"\033[1m"}}Aliases:{{"\033[0m"}}
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{"\033[1m"}}Examples:{{"\033[0m"}}
{{"\033[2m"}}{{.Example}}{{"\033[0m"}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

{{"\033[1m"}}Available Commands:{{"\033[0m"}}{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{"\033[36m"}}{{rpad .Name .NamePadding }}{{"\033[0m"}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{"\033[1;36m"}}{{.Title}}{{"\033[0m"}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{"\033[36m"}}{{rpad .Name .NamePadding }}{{"\033[0m"}} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

{{"\033[1m"}}Additional Commands:{{"\033[0m"}}{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{"\033[36m"}}{{rpad .Name .NamePadding }}{{"\033[0m"}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{"\033[1m"}}Flags:{{"\033[0m"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{"\033[1m"}}Global Flags:{{"\033[0m"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
}

func helpTemplate() string {
	return `{{if or .Runnable .HasSubCommands}}{{if .Long}}{{.Long | trimTrailingWhitespaces}}

{{end}}` + usageTemplate() + `{{end}}`
}
