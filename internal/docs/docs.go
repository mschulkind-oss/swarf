package docs

import "fmt"

// Topic represents a documentation topic accessible via `swarf docs <topic>`.
type Topic struct {
	Name        string
	Title       string
	Description string
	Content     string
}

// Topics returns all available documentation topics.
func Topics() []Topic {
	return []Topic{
		{
			Name:        "architecture",
			Title:       "Architecture",
			Description: "How swarf works under the hood",
			Content:     architecture,
		},
		{
			Name:        "quickstart",
			Title:       "Quick Start",
			Description: "Get up and running in 60 seconds",
			Content:     quickstart,
		},
		{
			Name:        "config",
			Title:       "Configuration",
			Description: "Global config, auto-sweep, and per-project settings",
			Content:     configDoc,
		},
		{
			Name:        "sweep",
			Title:       "Sweeping Files",
			Description: "How sweep works",
			Content:     sweepDoc,
		},
		{
			Name:        "daemon",
			Title:       "Background Daemon",
			Description: "How the sync daemon works",
			Content:     daemonDoc,
		},
		{
			Name:        "backends",
			Title:       "Storage Backends",
			Description: "Git and rclone backend configuration",
			Content:     backendsDoc,
		},
	}
}

// Get returns a topic by name, or nil if not found.
func Get(name string) *Topic {
	for _, t := range Topics() {
		if t.Name == name {
			return &t
		}
	}
	return nil
}

// ListTopics returns a formatted list of available topics.
func ListTopics() string {
	topics := Topics()
	s := ""
	for _, t := range topics {
		s += fmt.Sprintf("  %-14s %s\n", t.Name, t.Description)
	}
	return s
}

const architecture = `
  ARCHITECTURE

  Files live as regular files inside each project's swarf/ directory.
  A background daemon mirrors changes to a central backup store, which
  syncs to your configured remote (git or rclone).

  Per-project layout
  ------------------
  my-project/
  ├── src/
  ├── swarf/                     Real directory (auto-excluded from git)
  │   ├── .links/                Files projected into the host tree
  │   │   ├── AGENTS.md
  │   │   └── CLAUDE.md
  │   └── docs/                  Free-form storage (research, design, etc.)
  ├── AGENTS.md -> swarf/.links/AGENTS.md  (symlink)
  └── .git/info/exclude          Auto-managed by swarf

  Central backup store
  --------------------
  ~/.local/share/swarf/          Mirror of all projects (a git repo)
  ├── my-project/                Mirrored from my-project/swarf/
  ├── another-project/
  └── .git/

  ~/.config/swarf/
  ├── config.toml                Global config (backend, remote, debounce)
  └── drawers.toml               Registry: maps project slugs to host paths

  Key design decisions:

  1. Local-first: Files live in each project's swarf/ as regular files.
     No symlinks to a central store — your data is right where you work.

  2. Mirror + sync: The daemon watches all project swarf/ dirs, mirrors
     changes to ~/.local/share/swarf/ (a git repo), then pushes to remote.
     One daemon, one remote, one backup for all projects.

  3. Swept links: Files in swarf/.links/ are symlinked into the host tree.
     This lets AGENTS.md appear in the project root while living in swarf/.

  4. .git/info/exclude: Swarf manages exclude entries in a fenced section.
     This is per-clone and never committed, so it works on shared repos
     and monorepos without touching .gitignore.

  5. Auto-sweep: The daemon watches for configured files (e.g. AGENTS.md)
     and automatically sweeps them when they appear. No manual step needed.

  6. Auto-link: Missing symlinks are re-created at daemon startup and
     whenever a sync fires. 'swarf doctor' also fixes missing links.
`

const quickstart = `
  QUICK START

  1. Install swarf

     brew install swarf                                     # macOS / Linux
     pipx install swarf                                     # via PyPI
     go install github.com/mschulkind-oss/swarf@latest      # via Go

     Don't use 'pip install' in a venv or 'uvx' — the daemon breaks
     when the venv disappears.

  2. Initialize your first project

     cd ~/projects/my-app
     swarf init

     On first run, init walks you through global config (backend, remote)
     and offers to install the daemon service. This only happens once —
     subsequent projects reuse your config.

  3. Add content to swarf/

     echo "# Design Notes" > swarf/docs/design.md

     The daemon auto-commits and syncs after a 5-second quiet period.

  4. Project files that should appear in the repo tree (like AGENTS.md)

     swarf sweep AGENTS.md

     This moves the file into swarf/.links/ and replaces it with a symlink.
     The original path is auto-excluded from git.

  5. Verify everything

     swarf status       # show sync state and remote verification
     swarf doctor       # check config, store, remote, daemon, links

  6. Set up another project (reuses existing config and store)

     cd ~/projects/other-app
     swarf init          # instant — no prompts
`

const configDoc = `
  CONFIGURATION

  Global config: ~/.config/swarf/config.toml
  (Created automatically by 'swarf init' on first run)

  [sync]
  backend = "git"          # "git" or "rclone"
  remote = ""              # git remote URL or rclone remote:path
  debounce = "5s"          # wait time after last change before syncing

  [auto_sweep]
  paths = ["AGENTS.md"]    # files to sweep automatically when they appear

  See docs/CONFIGURATION.md for the full reference (all options, all
  file locations, troubleshooting).

  Drawer registry: ~/.config/swarf/drawers.toml
  (Managed automatically — one entry per initialized project)

  [[drawer]]
  slug = "my-project"
  host = "/home/you/projects/my-project"

  Per-project files (auto-created by 'swarf init'):

  swarf/                   local storage directory
  swarf/.links/            files projected into the host tree
  .git/info/exclude        fenced section hiding swarf/ and swept files

  Environment variables:

  XDG_CONFIG_HOME          Override config directory (default: ~/.config)
  XDG_DATA_HOME            Override data/store directory (default: ~/.local/share)
`

const sweepDoc = `
  SWEEPING FILES

  'swarf sweep' moves a file from the host repo into swarf/.links/ and
  replaces it with a symlink. This lets files like AGENTS.md appear in
  the repo tree while actually living in swarf's store.

  Usage:

    swarf sweep AGENTS.md CLAUDE.md .copilot/skills/SKILL.md

  What happens:

    1. File is moved to swarf/.links/<path>
    2. A symlink is created: <path> -> swarf/.links/<path>
    3. An exclude entry is added to .git/info/exclude
    4. The daemon picks up the change and syncs

  Re-linking:

    If symlinks break (e.g., after a fresh clone), they are restored
    automatically by 'swarf init', the daemon, or 'swarf doctor'.

  Notes:

  - Sweep is idempotent — running it on an already-swept file is a no-op
  - Nested paths work: 'swarf sweep docs/design.md' creates swarf/.links/docs/design.md
  - The symlink target is relative, so it works across machines
`

const daemonDoc = `
  BACKGROUND DAEMON

  The swarf daemon watches ~/.local/share/swarf/ for file changes and
  syncs to your configured backend after a debounce period.

  Starting:

    swarf daemon start             # background (daemonizes)
    swarf daemon start --foreground # foreground (for debugging)
    swarf daemon install           # install as systemd user service

  Stopping:

    swarf daemon stop
    systemctl --user stop swarf    # if installed as service

  Checking:

    swarf daemon status
    systemctl --user status swarf
    journalctl --user -u swarf -f  # follow logs

  How it works:

    1. Reads drawers.toml to find all registered project swarf/ dirs
    2. Watches each project's swarf/ directory recursively (fsnotify)
    3. Also watches project roots for auto-sweep target files
    4. On any file change, resets a debounce timer (default 5s)
    5. When the timer fires:
       a. Re-links any missing symlinks from swarf/.links/
       b. Mirrors each project's swarf/ to ~/.local/share/swarf/<project>/
       c. Commits and pushes the central store (git or rclone)
    6. New projects are picked up automatically (polled every 30s)
    7. Auto-sweep targets are checked at startup and on file creation

  The daemon is a single process for all projects — one watcher,
  one backup, one remote.

  PID file:   ~/.config/swarf/daemon.pid
  Log file:   ~/.config/swarf/daemon.log
`

const backendsDoc = `
  STORAGE BACKENDS

  Swarf supports two backends for syncing the central store.

  Git (recommended)
  -----------------
  The store is a git repo. The daemon commits and pushes changes.
  Use any git remote: GitHub, GitLab, a bare repo on a server, etc.

    Backend: git
    Remote:  git@github.com:you/my-swarf.git

  Setup:
    1. Create a private git repo (e.g., on GitHub)
    2. Run 'swarf init' and enter the remote URL
    3. Or set it manually in ~/.config/swarf/config.toml

  To clone on a new machine:
    swarf clone    # clones the store from your remote

  To pull updates:
    swarf pull     # pulls latest from remote

  Rclone (Google Drive, Dropbox, S3, etc.)
  -----------------------------------------
  Uses rclone to copy the store to any cloud provider.

    Backend: rclone
    Remote:  gdrive:swarf

  Setup:
    1. Install rclone: brew install rclone
    2. Configure a remote: rclone config
    3. Run 'swarf init' with backend=rclone

  For Google Drive, use the 'drive.file' scope — this restricts the
  OAuth token to files rclone created, so it can't read your other docs.

    rclone config
    > n (new remote), name: gdrive, type: drive
    > scope: drive.file (option 2)
    > auto config: y (opens browser)

  Then use 'gdrive:swarf' as your remote.
`
