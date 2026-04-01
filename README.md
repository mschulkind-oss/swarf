# Swarf

Invisible, auto-syncing personal storage for any git repo.

Swarf is the metal shavings left on the workshop floor after machining — the
byproduct of making something. When you build software (especially with AI
agents), you generate a lot of byproduct: research docs, design specs, agent
skills, open questions, scratch notes. Swarf gives this material a durable
home alongside any project, without touching the project itself.

## The problem

You have files that should live near your code but don't belong in the repo:

- Agent-generated research and design docs
- Personal notes, roadmaps, and open questions
- Agent skill files and config (`.copilot/skills/`, `AGENTS.md`)
- Security audits, experiment logs, scratch work

Today these files are either **untracked and local-only** (one `rm -rf` from
gone) or **committed to the repo** (polluting history, leaking on public
repos). Neither is good.

## How it works

Swarf creates a `.swarf/` directory inside your project — regular files,
invisible to the host repo via `.git/info/exclude` (managed automatically).
A background daemon watches for changes, mirrors them to a central backup
store, and syncs to a remote backend (git or rclone).

```
my-project/                      <- your repo (public)
├── src/
├── tests/
├── .swarf/                      <- private storage (auto-excluded from git)
│   ├── docs/research/           <- durable notes
│   ├── docs/design/             <- specs and decisions
│   ├── links/                   <- files projected into the host tree
│   │   └── AGENTS.md            <- symlinked to ./AGENTS.md
│   └── open-questions.md
└── AGENTS.md -> .swarf/links/AGENTS.md
```

Files live locally in each project. The daemon mirrors all `.swarf/` dirs
to `~/.local/share/swarf/` (a git repo), then pushes to your remote.

## Install

```bash
# Via pip / uvx (any platform — no Go required)
pip install swarf
# or: uvx swarf

# Via Go
go install github.com/mschulkind-oss/swarf@latest

# From source
git clone https://github.com/mschulkind-oss/swarf && cd swarf
just deploy   # builds and copies to ~/.local/bin/
```

## Quick start

```bash
cd ~/projects/my-app
swarf init
```

That's it. If this is your first time, `swarf init` walks you through
everything:

```
$ swarf init

No global config found. Let's set one up.

Backend (git/rclone) [git]: git
Remote URL: git@github.com:you/my-swarf.git
✓ Wrote ~/.config/swarf/config.toml

Created .mise.local.toml with enter hook.

✓ Initialized swarf in /home/you/projects/my-app/.swarf
  Backend: git
  Remote: git@github.com:you/my-swarf.git

Daemon is not running. Install as system service? [Y/n]: y
✓ Installed and started swarf daemon
```

Next time you run `swarf init` in another project, it reuses your config
and the daemon picks it up automatically — no prompts.

### Rclone backend (Google Drive, Dropbox, S3, etc.)

If you prefer cloud storage over a git repo, install rclone first:

```bash
brew install rclone   # or: mise use rclone
rclone config
#   -> n (new remote), name: gdrive, type: drive
#   -> scope: drive.file (option 2 — can only see files rclone created)
#   -> auto config: y (opens browser)
```

Then run `swarf init` and enter `rclone` as the backend and `gdrive:swarf`
as the remote. The `drive.file` scope means the OAuth token can only access
files swarf created — it cannot read your other Google Drive documents.

### Verify your setup

```bash
swarf doctor
# ✓ Global config: backend=git, remote=git@github.com:you/my-swarf.git
# ✓ Git remote reachable
# ✓ Daemon is running
# ✓ .swarf/ directory exists
# ✓ .swarf/ is gitignored
# ...
```

## Using swarf

### Adding content

Put files directly into `.swarf/`:

```bash
# Research notes, design docs, anything you want backed up
echo "# Architecture Notes" > .swarf/docs/design/architecture.md
echo "# Open Questions" >> .swarf/open-questions.md
```

The daemon auto-commits and syncs after a 5-second quiet period.

### Sweeping files into swarf

Use `swarf sweep` to move existing files into `.swarf/links/` and replace
them with symlinks. The host repo automatically ignores the symlinks via
`.git/info/exclude`.

```bash
# Sweep an existing file into swarf
swarf sweep AGENTS.md
# AGENTS.md -> .swarf/links/AGENTS.md

# Sweep multiple files at once
swarf sweep .copilot/skills/SKILL.md .cursor/rules/project.md

# Verify
ls -la AGENTS.md
# AGENTS.md -> .swarf/links/AGENTS.md
```

You can also configure auto-sweep in `~/.config/swarf/config.toml` to
automatically sweep common files (like `AGENTS.md`) whenever you `cd` into
a project. See [Configuration](#configuration).

### Checking health

```bash
swarf doctor    # validate setup: gitignore, git repo, remote, links
swarf status    # show all projects, sync state, daemon status
```

## Commands

| Command | Description |
|---------|-------------|
| `swarf init` | Initialize `.swarf/` in current project |
| `swarf sweep <file>...` | Move files into `.swarf/links/` and symlink back |
| `swarf doctor` | Validate setup and backend health |
| `swarf status` | Show all projects and sync status |
| `swarf daemon start` | Start background sync daemon |
| `swarf daemon stop` | Stop the daemon |
| `swarf daemon status` | Check if daemon is running |
| `swarf daemon install` | Install as systemd user service (auto-start on login) |

### Daemon as a system service

To have the daemon start automatically on login:

```bash
swarf daemon install
```

This creates a systemd user service. Check logs with:

```bash
journalctl --user -u swarf -f
```

## How it works on a company monorepo

The same way. `swarf init` writes to `.git/info/exclude`, which is per-repo
and never committed. No changes to the monorepo's `.gitignore`, no submodules,
no permission needed.

```bash
cd ~/work/big-monorepo
swarf init
```

Your agent config, personal notes, and research are now durable and synced,
completely invisible to your coworkers and CI.

## How it works with AI agent containers

If you use containerized agents (like [yolo-jail](https://github.com/mschulkind-oss/yolo-jail)),
the daemon runs on the **host**, not inside the container. The container
mounts the workspace directory, so file changes from agents are visible to
the host filesystem. The daemon picks them up and syncs automatically. Agents
never need credentials.

## Configuration

Global config lives at `~/.config/swarf/config.toml` (created automatically
by `swarf init` on first run):

```toml
[sync]
backend = "git"          # or "rclone"
remote = "origin"        # git remote or rclone remote path
debounce = "5s"          # wait this long after last change before syncing

[auto_sweep]
# Files to automatically sweep into .swarf/links/ when entering a project.
# Only swept if the file exists and isn't already a symlink.
paths = ["AGENTS.md", "CLAUDE.md", ".copilot/skills/"]
```

This is set once and applies to all projects.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

Apache 2.0
