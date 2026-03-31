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

Swarf creates a `.swarf/` directory inside your project — a separate git repo,
invisible to the host repo via `.git/info/exclude` (managed automatically by
swarf). A background daemon watches for changes and syncs to a remote backend
(a git repo or any rclone-supported cloud storage).

```
my-project/                      <- your repo (public)
├── src/
├── tests/
├── .swarf/                      <- swarf repo (private, auto-excluded)
│   ├── docs/research/           <- durable notes
│   ├── docs/design/             <- specs and decisions
│   ├── links/                   <- files projected into the host tree
│   │   └── AGENTS.md            <- symlinked to ./AGENTS.md
│   └── open-questions.md
└── AGENTS.md -> .swarf/links/AGENTS.md
```

## Install

Requires Python 3.13+.

```bash
uv tool install swarf
```

## Quick start

No setup needed — `swarf init` automatically configures your repo's
`.git/info/exclude` so `.swarf/` and related files stay invisible. If you
use [mise](https://mise.jdx.dev/), swarf also installs an enter hook that
auto-links files when you `cd` into a project.

### Option A: Git backend (you control the remote)

Best when you can create a private companion repo (personal projects, GitHub).

```bash
cd ~/projects/my-app

# Create a private repo for your swarf (GitHub, Gitea, bare local, etc.)
# Example with a local bare repo for testing:
git init --bare ~/swarf-remotes/my-app.git

# Initialize
swarf init --backend git --remote ~/swarf-remotes/my-app.git

# Push the initial commit so the daemon can sync later
cd .swarf && git push -u origin master && cd ..

# Start the daemon
swarf daemon start
```

### Option B: Rclone backend (Google Drive, Dropbox, S3, etc.)

Best for work environments where you can't easily create a private git repo.

```bash
# 1. Install rclone (if not already)
brew install rclone        # macOS
# or: sudo apt install rclone  # Debian/Ubuntu
# or: mise use rclone          # via mise

# 2. Configure a Google Drive remote (one-time, interactive)
rclone config
#   -> n (new remote)
#   -> name: gdrive
#   -> type: drive
#   -> scope: drive.file (option 2 — can only see files rclone created)
#   -> auto config: y (opens browser)
#   -> done

# 3. Verify it works
rclone lsd gdrive:

# 4. Initialize swarf
cd ~/projects/my-app
swarf init --backend rclone --remote "gdrive:swarf/my-app"

# 5. Start the daemon
swarf daemon start
```

The `drive.file` scope means the OAuth token can only access files swarf
created — it cannot read your other Google Drive documents.

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

You can also create files directly in `.swarf/links/` — the mise enter hook
runs `swarf link` automatically when you `cd` into the project, creating
symlinks for any new files.

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
| `swarf doctor` | Validate setup health |
| `swarf status` | Show all drawers and sync status |
| `swarf daemon start` | Start background sync daemon |
| `swarf daemon stop` | Stop the daemon |
| `swarf daemon status` | Check if daemon is running |
| `swarf daemon install` | Install as systemd user service (auto-start on login) |

### Init options

```bash
swarf init --backend git --remote <url>     # git backend with remote
swarf init --backend rclone --remote <path> # rclone backend
swarf init                                  # git backend, no remote (local only)
```

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
swarf init --backend rclone --remote "gdrive:swarf/work"
swarf daemon start
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

Each `.swarf/` directory has a `config.toml`:

```toml
[sync]
backend = "git"          # or "rclone"
remote = "origin"        # git remote name, or rclone remote path
debounce = "5s"          # wait this long after last change before syncing
```

The daemon reads a central registry at `~/.config/swarf/drawers.toml` that
lists all known `.swarf/` directories and their backends.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

Apache 2.0
