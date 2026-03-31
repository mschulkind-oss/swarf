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
			Description: "How sweep and link work together",
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

  Swarf uses a single central store shared across all your projects.

  Layout
  ------
  ~/.local/share/swarf/         The central store (a git repo)
  ├── my-project/               One directory per project (called a "drawer")
  │   ├── links/                Files projected into the host tree as symlinks
  │   │   ├── AGENTS.md
  │   │   └── CLAUDE.md
  │   └── docs/                 Free-form storage (research, design, etc.)
  ├── another-project/
  └── .git/                     The store is itself a git repo

  ~/.config/swarf/
  ├── config.toml               Global config (backend, remote, debounce)
  └── drawers.toml              Registry: maps project slugs to host paths

  Per-project
  -----------
  my-project/
  ├── .swarf/ -> ~/.local/share/swarf/my-project   (symlink)
  ├── AGENTS.md -> .swarf/links/AGENTS.md          (symlink)
  └── .git/info/exclude         Auto-managed by swarf (hides .swarf/, links)

  Key design decisions:

  1. Single store: All projects share one git repo. This means one remote,
     one daemon, one backup. No per-project git submodules or nested repos.

  2. Symlinks, not copies: Files in .swarf/links/ are symlinked into the
     host tree. Edits go straight to the store. No sync lag.

  3. .git/info/exclude: Swarf manages exclude entries in a fenced section.
     This is per-clone and never committed, so it works on shared repos
     and monorepos without touching .gitignore.

  4. Daemon watches the store: A single background process (fsnotify)
     watches ~/.local/share/swarf/ for changes, debounces, and syncs
     to the configured backend.

  5. mise integration: 'swarf enter' runs on directory enter (via mise
     hooks) to re-link any files that were deleted or not yet created.
`

const quickstart = `
  QUICK START

  1. Install swarf

     pip install swarf          # or: uvx swarf
     go install github.com/mschulkind-oss/swarf@latest   # if you have Go

  2. Initialize your first project

     cd ~/projects/my-app
     swarf init

     On first run, swarf walks you through global config (backend, remote).
     This only happens once — subsequent projects reuse your config.

  3. Add content to .swarf/

     echo "# Design Notes" > .swarf/docs/design.md

     The daemon auto-commits and syncs after a 5-second quiet period.

  4. Project files that should appear in the repo tree (like AGENTS.md)

     swarf sweep AGENTS.md

     This moves the file into .swarf/links/ and replaces it with a symlink.
     The original path is auto-excluded from git.

  5. Verify everything

     swarf doctor       # check config, store, remote, daemon, links
     swarf status       # show all projects and sync state

  6. Set up another project (reuses existing config and store)

     cd ~/projects/other-app
     swarf init          # instant — no prompts

  7. Start the daemon as a system service (auto-start on login)

     swarf daemon install
`

const configDoc = `
  CONFIGURATION

  Global config: ~/.config/swarf/config.toml
  (Created automatically by 'swarf init' on first run)

  [sync]
  backend = "git"          # "git" or "rclone"
  remote = ""              # git remote URL or rclone remote:path
  debounce = "5s"          # wait time after last change before syncing

  Drawer registry: ~/.config/swarf/drawers.toml
  (Managed automatically — one entry per initialized project)

  [[drawer]]
  slug = "my-project"
  host = "/home/you/projects/my-project"

  [[drawer]]
  slug = "other-project"
  host = "/home/you/work/other-project"

  Per-project files (auto-created by 'swarf init'):

  .mise.local.toml         mise hook that runs 'swarf enter' on cd
  .git/info/exclude        fenced section hiding .swarf/ and swept files

  Environment variables:

  XDG_CONFIG_HOME          Override config directory (default: ~/.config)
  XDG_DATA_HOME            Override data/store directory (default: ~/.local/share)
`

const sweepDoc = `
  SWEEPING FILES

  'swarf sweep' moves a file from the host repo into .swarf/links/ and
  replaces it with a symlink. This lets files like AGENTS.md appear in
  the repo tree while actually living in swarf's store.

  Usage:

    swarf sweep AGENTS.md CLAUDE.md .copilot/skills/SKILL.md

  What happens:

    1. File is moved to .swarf/links/<path>
    2. A symlink is created: <path> -> .swarf/links/<path>
    3. An exclude entry is added to .git/info/exclude
    4. The daemon picks up the change and syncs

  Re-linking:

    If symlinks break (e.g., after a fresh clone), restore them with:

      swarf link           # re-create all symlinks from .swarf/links/
      swarf enter          # same thing (runs automatically via mise hook)

  Notes:

  - Sweep is idempotent — running it on an already-swept file is a no-op
  - Nested paths work: 'swarf sweep docs/design.md' creates .swarf/links/docs/design.md
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

    1. Watches the store directory recursively (fsnotify)
    2. On any file change, resets a debounce timer (default 5s)
    3. When the timer fires: git add -A && git commit && git push
       (or rclone copy, depending on backend)
    4. New subdirectories are automatically watched

  The daemon is a single process for all projects (since they share
  one store). It starts watching immediately if the store exists,
  or waits for it to be created.

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
