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

### 1. Configure your backend (one-time)

Create `~/.config/swarf/config.toml` with your backup destination. This is
a single location shared across every project on your machine.

**Option A: Git backend** — best for personal use (GitHub, Gitea, etc.)

```bash
# Create one private repo for all your swarf
gh repo create my-swarf --private

mkdir -p ~/.config/swarf
cat > ~/.config/swarf/config.toml << 'EOF'
[sync]
backend = "git"
remote = "git@github.com:you/my-swarf.git"
EOF
```

**Option B: Google Drive** — best for work (no repo needed)

```bash
# Install rclone and configure a Google Drive remote
brew install rclone   # or: mise use rclone
rclone config
#   -> n (new remote), name: gdrive, type: drive
#   -> scope: drive.file (option 2 — can only see files rclone created)
#   -> auto config: y (opens browser)

# Verify it works
rclone lsd gdrive:

mkdir -p ~/.config/swarf
cat > ~/.config/swarf/config.toml << 'EOF'
[sync]
backend = "rclone"
remote = "gdrive:swarf"
EOF
```

### 2. Verify your setup

```bash
swarf doctor
# ✓ Global config exists
# ✓ Backend reachable
```

### 3. Start using swarf in any project

```bash
cd ~/projects/my-app
swarf init
swarf daemon start
```

That's it. `swarf init` creates `.swarf/`, configures `.git/info/exclude`,
and installs a [mise](https://mise.jdx.dev/) enter hook. The daemon syncs
all your projects to the backend you configured in step 1.

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

Global config lives at `~/.config/swarf/config.toml`:

```toml
[sync]
backend = "git"          # or "rclone"
remote = "origin"        # git remote or rclone remote path
debounce = "5s"          # wait this long after last change before syncing
```

This is set once and applies to all projects.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

Apache 2.0
