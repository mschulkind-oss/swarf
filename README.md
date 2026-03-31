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
invisible to the host repo via your global gitignore. Files in `.swarf/` are
version-controlled and automatically synced to a remote backend.

```
my-project/                      ← your repo (public)
├── src/
├── tests/
├── .swarf/                      ← swarf repo (private, globally gitignored)
│   ├── docs/research/           ← durable notes
│   ├── docs/design/             ← specs and decisions
│   ├── links/                   ← files projected into the host tree
│   │   ├── AGENTS.md            ← symlinked to ./AGENTS.md
│   │   └── .copilot/skills/     ← symlinked to ./.copilot/skills/
│   └── open-questions.md
└── scratch/                     ← ephemeral, untracked by anything
```

### Linking

Files in `.swarf/links/` are automatically symlinked into the host repo tree.
A file at `.swarf/links/AGENTS.md` appears as `./AGENTS.md`. A file at
`.swarf/links/.copilot/skills/my-skill/SKILL.md` appears at
`./.copilot/skills/my-skill/SKILL.md`. The directory structure is the manifest.

Linking runs automatically via a [mise](https://mise.jdx.dev/) enter hook —
every time you `cd` into the project, links are verified and fixed. You never
run it manually.

### Syncing

A background daemon watches all your `.swarf/` directories for file changes.
When files change, it waits for a quiet period (debounce), then commits and
syncs to the configured backend. You never think about it.

## Install

```bash
uv tool install swarf
```

## Quick start

```bash
cd ~/projects/my-app

# Initialize with git backend
swarf init --backend git --remote git@github.com:you/my-app-internal.git

# Initialize with Google Drive backend (for work repos you don't control)
swarf init --backend rclone --remote "gdrive:swarf/my-app"

# That's it. The daemon handles everything from here.
```

After `swarf init`:
- `.swarf/` exists with standard directory structure
- A mise enter hook is installed (auto-linking on `cd`)
- The daemon is watching for changes (auto-syncing)
- Your global gitignore hides `.swarf/` from the host repo

## Usage

```bash
# Check sync status across all projects
swarf status

# Validate setup is healthy
swarf doctor

# Start/stop the background daemon
swarf daemon start
swarf daemon stop
swarf daemon status

# Install daemon as a system service (auto-start on login)
swarf daemon install
```

## Backends

### Git (default)

Commits changes after a quiet period, pushes to a remote. Best for personal
projects where you can create a private companion repo.

```bash
swarf init --backend git --remote git@github.com:you/project-internal.git
```

### Rclone

Syncs the `.swarf/` directory to any rclone-supported remote: Google Drive,
Dropbox, S3, OneDrive, etc. Best for work environments where you can't
easily create a private git repo.

```bash
# First: configure rclone with Google Drive (one-time)
# Use drive.file scope for minimal permissions
rclone config

# Then:
swarf init --backend rclone --remote "gdrive:swarf/work-monorepo"
```

The `drive.file` OAuth scope means the token can only access files swarf
created — it can't read your other Google Drive documents.

## How it works on a company monorepo

The same way. Your global gitignore hides `.swarf/` from every repo on your
machine, including ones you don't control. No changes to the monorepo's
`.gitignore`, no submodules, no permission needed.

```bash
cd ~/work/big-monorepo
swarf init --backend rclone --remote "gdrive:swarf/work"
```

Your agent config, personal notes, and research are now durable and synced,
completely invisible to your coworkers and CI.

## How it works with AI agent containers

If you use containerized agents (like [yolo-jail](https://github.com/mschulkind-oss/yolo-jail)),
the daemon runs on the **host**, not inside the container. The container
mounts the workspace directory, so file changes from agents are visible to
the host filesystem. The daemon picks them up and syncs automatically. Agents
never need credentials.

## Global gitignore

Swarf relies on your global gitignore (`~/.config/git/ignore` or equivalent)
to hide `.swarf/` and linked files from host repos. `swarf init` checks and
prompts you to add entries if missing:

```gitignore
/.swarf/
/.mise.local.toml
AGENTS.md
```

This is the invisible layer that makes swarf work on any repo without
modifying it.

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
