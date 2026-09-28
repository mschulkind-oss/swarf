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
		{
			Name:        "conflicts",
			Title:       "Resolving Conflicts",
			Description: "What <file>.conflict.<peer>.<ts> sidecars are and how to fix them",
			Content:     conflictsDoc,
		},
		{
			Name:        "safety",
			Title:       "Sync Safeguards",
			Description: "Delete holds, resync, owner tokens, and the sync lock",
			Content:     safetyDoc,
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
  exclude = [".yolo/"]     # drawer paths to keep out of the store entirely
  pull_interval = "10m"    # daemon pulls on this interval (empty or
                           # "0" is off, the default)

  [machine]
  id = "laptop"            # this machine's folder on an rclone remote,
                           # or its branch machines/<id> on a git
                           # remote; every machine needs its own

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

  The swarf daemon watches every registered project's swarf/ directory
  for file changes and syncs to your configured backend after a debounce
  period. It pulls from other machines only when [sync] pull_interval is
  set (see below).

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
       c. Commits and pushes the central store (git or rclone). With
          git, it first folds in any legacy branch still on the remote
          and afterwards retires it (see 'swarf docs backends').
    6. New projects are picked up automatically (polled every 30s)
    7. Auto-sweep targets are checked at startup and on file creation

  The daemon is a single process for all projects — one watcher,
  one backup, one remote.

  Each sync cycle holds the sync lock, so it never runs at the same time
  as a 'swarf push' or 'swarf pull'. When a cycle finds a project in a
  delete hold, or finds its remote subtree (rclone) or machine branch
  (git) written by another store, it logs that once rather than every
  cycle. See 'swarf docs safety'.

  Scheduled pull (git and rclone backends):

    [sync]
    pull_interval = "10m"

    The daemon runs a pull on that interval, under the sync lock like
    any other pull. Off by default (empty or "0"). Each pull's start,
    end, failed peers, and conflict sidecars go to the daemon log.

  PID file:   ~/.config/swarf/daemon.pid
  Log file:   ~/.config/swarf/daemon.log

  Under a service manager the same lines also go to stderr, so the
  supervisor's own log works:

    journalctl --user -u swarf -f          (systemd)
    ~/Library/Logs/swarf/swarf.err.log     (launchd)
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

  On a new machine:
    swarf pull     # creates the store and seeds it from your remote

  To pull updates:
    swarf pull     # merges every other machine's changes

  Any number of machines can share one git remote. Each machine pushes
  its store to its own machine branch, machines/<machine_id> on the
  remote, which only that machine pushes, and never with force. 'swarf
  pull' fetches every other machine's branch and merges it file by
  file, exactly as with rclone (see Multi-machine rclone below):
  conflict sidecars, 'swarf pull --resync', [sync] pull_interval, and
  the copy back into your projects' swarf/ all work the same. A fresh
  machine's first pull creates the store; a reinstalled one (same
  machine id, store with no commits) starts from its own machine
  branch, so it gets its old data back.

  The remote is an archive, not something meant to be browsed.

  A push refused because this machine's branch has commits the store
  lacks means another store uses the same machine id. Give this machine
  its own [machine] id, or, if it replaces the old one:

    swarf push --take-over     # force-push; discards what only the
                               # old store pushed to the branch

  Migrating a remote from one shared branch
  -----------------------------------------
  Older swarf pushed every machine to one branch (main or master). A
  legacy branch is any branch on the remote not under machines/. Every
  git-backend pull, push, and daemon cycle migrates on its own:

    1. It folds each legacy branch into the store: fast-forwards to it,
       or merges it as it would a peer (sidecars are named
       <file>.conflict.<branch>.<timestamp>), and copies the result
       into your projects.
    2. Once this machine's branch has been pushed with that content, it
       retires the legacy branch: removes it from the remote. It deletes
       the branch, or, when the remote refuses (GitHub refuses deleting
       the default branch, and a plain bare repository refuses deleting
       the branch its HEAD names), replaces it with a tombstone. A branch that
       moved in between is left for the next pass to fold in again.

  A tombstone is a commit with no parents holding one file,
  SWARF-BRANCHES.md, which says the store uses machine branches and this
  branch is intentionally empty. Its commit message ends with the
  trailer "Swarf-Tombstone: true", which is how swarf recognizes it;
  pull skips it. 'swarf doctor' lists any legacy branch still waiting
  to be retired, and why.

  Upgrade swarf on every machine that uses the remote. An older swarf
  still pushes to the legacy branch: a deleted branch comes back (the
  next upgraded pass folds it in and retires it again), and a tombstone
  rejects its push, so nothing is lost, but that machine stops syncing
  until it is upgraded.

  Rclone (Google Drive, Dropbox, S3, etc.)
  -----------------------------------------
  Uses rclone to sync the store to any cloud provider. Each machine writes
  to its own subtree under <remote>/machines/<machine_id>/, so two machines
  never write to the same file at the same time. Files are browseable
  directly in Google Drive (or wherever), organized by machine and then
  by project. The .git/ directory is included too, so you get full commit
  history.

    Backend: rclone
    Remote:  gdrive:swarf-store

  Setup:
    1. Install rclone: brew install rclone
    2. Configure a remote: rclone config
    3. Run 'swarf init', pick rclone, select your remote from the menu

  For Google Drive, use the 'drive.file' scope — this restricts the
  OAuth token to files rclone created, so it can't read your other docs.

    rclone config
    > n (new remote), name: gdrive, type: drive
    > scope: drive.file (option 2)
    > auto config: y (opens browser)

  Then pick 'gdrive:' in the swarf init menu and accept the default
  path 'swarf-store'.

  Multi-machine rclone
  --------------------
  To add a second machine that shares the same rclone remote:

    1. Install swarf and run 'swarf init' (same rclone remote as the first).
    2. 'swarf pull' — bootstraps the store from every existing peer's
       folder, seeding working files and git history in one step.
    3. cd into each project and run 'swarf init' to re-link.

  Pulling from peers:
    swarf pull     # fetches every other machine's folder, reconciles
                   # into the local store file-by-file

  Each machine tracks each peer's last-seen tip under refs/swarf-peers/<id>
  in the local store. Subsequent pulls only apply the file changes since
  that point, so two machines never need a shared commit history — they
  just exchange file-level diffs over rclone.

  After merging, pull copies the store back into each project's swarf/.
  It deletes a project file only when swarf synced it before and the
  store no longer has it, so a file you saved during the pull is kept.

  Conflicts (both machines edited the same file before syncing) are kept
  visible as sidecars: <file>.conflict.<peer>.<timestamp>. See
  'swarf docs conflicts' for the full resolution workflow.

  To have the daemon pull on its own, set [sync] pull_interval (see
  'swarf docs daemon'). For recovering lost files with
  'swarf pull --resync', see 'swarf docs safety'.

  Machine id
  ----------
  Each machine has a stable id used for its folder on an rclone remote
  and its machine branch on a git remote. Default is derived from the
  hostname and written on first run. Override by adding to
  ~/.config/swarf/config.toml:

    [machine]
    id = "laptop"

  Do not change the id after the first sync — the remote has a folder
  or branch under that name.

  Every machine needs its own id. If two share one, the second one's
  push commits locally and refuses to upload over the first's folder
  (rclone: the owner token, see 'swarf docs safety') or branch (git:
  the push is not a fast-forward).
`

const conflictsDoc = `
  RESOLVING CONFLICTS

  When two machines edit the same file between syncs — or one machine
  deletes a file the other modified — swarf keeps BOTH versions visible
  so nothing is silently overwritten.

  What you'll see
  ---------------
  After 'swarf pull' (or a daemon pull), the output ends with:

    ! Pulled 1 peer(s) with 2 conflict file(s):
        docs/notes.md.conflict.laptop.20260511T230815Z
        config.yml.conflict.laptop.20260511T230815Z
      Open each original file, edit it to reflect what you want,
      then rm the .conflict.* sidecar.

  The canonical path (docs/notes.md) holds YOUR version. Each sidecar
  holds the PEER's version at the moment they pushed. Both are staged
  and committed to the store so neither side is lost.

  Sidecar filename format
  -----------------------
    <original-path>.conflict.<peer-id>.<timestamp>

    original-path    Path inside swarf/, identical to the file in
                     conflict. Lives next to the original so you can
                     diff them easily.
    peer-id          Machine id of the peer whose change you're
                     reconciling with (see 'swarf docs backends'), or
                     the name of the legacy git branch being folded in,
                     with any / turned into -.
    timestamp        UTC, in 20060102T150405Z form. Lets you tell
                     multiple rounds of conflicts apart.

  Resolving
  ---------
  Three-step loop for each sidecar:

    1. Open the ORIGINAL file (e.g. swarf/<project>/docs/notes.md)
       and the sidecar side by side.
    2. Edit the original to the final content you want. You can pull
       lines from the sidecar, keep your version whole, take theirs
       whole, or merge by hand — whatever is right.
    3. Delete the sidecar:

           rm swarf/<project>/docs/notes.md.conflict.laptop.20260511T230815Z

  The daemon's next sync commits your resolution. The next time the
  other machine pulls, it sees your merged version and the sidecar's
  content disappears from disk on that side too.

  When deletes conflict
  ---------------------
  If a peer deletes a file that you've locally modified, swarf keeps
  YOUR local version and writes the peer's pre-delete content into the
  sidecar. Same resolution loop: edit the original to what you want,
  rm the sidecar. If you decide their delete was correct, rm BOTH:

      rm swarf/<project>/notes.md
      rm swarf/<project>/notes.md.conflict.laptop.20260511T230815Z

  Nothing ever disappears without your say-so.

  Finding stale sidecars
  ----------------------
  To see sidecars you haven't resolved yet:

      find ~/.local/share/swarf -name '*.conflict.*'

  Or for a specific project:

      find ~/projects/my-app/swarf -name '*.conflict.*'

  Why this scheme
  ---------------
  Two alternatives were rejected:

    - Git merge + conflict markers inline (<<<<<<<) — breaks non-text
      files (images, PDFs) and tools that don't tolerate garbage in
      their input format.
    - Silently pick a winner (last-write-wins or pick-by-timestamp) —
      data loss, and no way to tell it happened.

  The sidecar approach keeps both versions as regular files that any
  editor can open, works for every file type, and makes it obvious at
  a glance that something needs human attention.

  Related:
    swarf docs backends    # machine ids and the multi-machine setup
    swarf docs daemon      # how syncs are scheduled
`

const safetyDoc = `
  SYNC SAFEGUARDS

  Four mechanisms keep a mistake on one machine from being synced out
  to the others. The forward mirror below is the copy from a project's
  swarf/ into the store (run by the daemon, 'swarf push', and the start
  of 'swarf pull'); the manifest is ~/.cache/swarf/manifests/<slug>.txt,
  the files swarf last synced from that project.

  Delete hold
  -----------
  A delete hold is the state a project is in when the forward mirror
  found that applying its deletes would remove a suspiciously large
  share of the project's tracked files, and so applied none of them.
  It holds when:

    - the project lost every tracked file, and at least 2 are at stake, or
    - at least 10 files are at stake and they are half or more of the
      tracked files.

  "At stake" means listed in the manifest, gone from the project, and
  still in the store. Sync excludes do not count. Adds and edits are
  still copied during a hold, and the held files stay in the manifest,
  so the hold lasts until you act.

  The hold marker, ~/.cache/swarf/delete-holds/<slug>.json, records the
  count, a sample of paths, and when the hold started. 'swarf status'
  and 'swarf doctor' list held projects. Two ways out:

    swarf pull                     # copies the files back from the
                                   # store and clears the hold
    swarf push --allow-deletes     # confirm: apply the deletes this once

  Resync
  ------
  A plain pull applies only what each peer changed since the last pull.
  If this store lost files and committed the deletes, they stay lost
  while the peer is unchanged. Resync ignores what was already pulled
  and copies in every file each peer has now:

    swarf pull --resync

  It never deletes a file because a peer lacks it; the only deletes it
  applies are the ones a peer made since the last pull. A local file
  that differs from every version a peer ever had keeps its content and
  gets a conflict sidecar (see 'swarf docs conflicts').

  Owner token (rclone)
  --------------------
  An owner token is a random value each store keeps in
  ~/.local/share/swarf/.git/swarf-owner. It is uploaded with the store
  to <remote>/machines/<machine_id>/. Before uploading, swarf reads the
  token already there: absent or this store's, it uploads; a different
  store's, it commits locally and refuses to upload, because two
  machines share one machine_id. If the remote token cannot be read at
  all, the upload is skipped for that pass. 'swarf doctor' reports a
  mismatch.

  Fix it by giving this machine its own [machine] id, or, if this
  computer replaces the old one:

    swarf push --take-over

  That replaces the remote subtree with this store and deletes from the
  remote anything only the old store had there. 'swarf pull' never
  pulls this machine's own subtree, so copy out anything you need from
  it first (rclone copy <remote>/machines/<id> <dir>).

  Machine branch (git)
  --------------------
  The git backend has no owner token. Each machine pushes to its own
  machine branch, machines/<machine_id>, and never forces the push, so
  a push the remote rejects as not a fast-forward means the branch has
  commits from a different store with the same machine_id. The push
  commits locally only, the daemon logs it once, and 'swarf doctor'
  reports it as the same "machine owner" check. The same two fixes
  apply; 'swarf push --take-over' force-pushes this store to the branch
  and discards the commits only the old store pushed there (copy them
  out first with git fetch <remote> machines/<id>).

  Sync lock
  ---------
  The sync lock is an advisory lock on ~/.cache/swarf/sync.lock held for
  the whole of any push, pull, or daemon cycle, so two of them never
  touch the store at the same time. A command that finds it held says
  so and waits; Ctrl-C ends the wait.

  Scheduled pulls ([sync] pull_interval) take the same lock. See
  'swarf docs daemon'.
`
