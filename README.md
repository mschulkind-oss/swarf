# Swarf

[![CI](https://github.com/mschulkind-oss/swarf/actions/workflows/ci.yml/badge.svg)](https://github.com/mschulkind-oss/swarf/actions/workflows/ci.yml)
[![PyPI](https://img.shields.io/pypi/v/swarf)](https://pypi.org/project/swarf/)
[![Go](https://img.shields.io/badge/go-%3E%3D1.26-blue)](https://go.dev/)
[![License](https://img.shields.io/github/license/mschulkind-oss/swarf)](LICENSE)

Invisible, auto-syncing personal storage for any git repo.

You build software and generate byproduct — research docs, design specs,
agent skills, scratch notes. Swarf gives this material a durable home
alongside any project, without touching the project itself.

## The problem

You have files that should live near your code but don't belong in the repo:

- Agent instructions and skills (`AGENTS.md`, `.copilot/skills/`)
- Research notes, design docs, open questions
- Security audits, experiment logs, scratch work

Today these files are either **untracked and local-only** (one `rm -rf` from
gone) or **committed to the repo** (polluting history, leaking on public
repos). Neither is good.

## How it works

Swarf creates a `swarf/` directory inside your project. Everything in it is
automatically excluded from git via `.git/info/exclude`. A background daemon
watches for changes, mirrors them to a central store, and syncs to your
configured remote (a git repo or any rclone backend).

```
my-project/
├── src/
├── swarf/                         ← private storage (invisible to git)
│   ├── docs/                      ← research, design, anything
│   ├── .links/                    ← files projected into the host tree
│   │   └── AGENTS.md
│   └── open-questions.md
├── AGENTS.md → swarf/.links/AGENTS.md   ← symlink (also gitignored)
└── .git/info/exclude              ← managed by swarf
```

The central store at `~/.local/share/swarf/` mirrors all projects into a
single git repo. The daemon commits changes locally, then pushes to your
remote. For rclone backends, the entire store (including `.git/` for history)
is synced to the remote.

## Install

```bash
# macOS / Linux (recommended)
brew install swarf

# Via PyPI (persistent install to ~/.local/bin/)
pipx install swarf        # or: uv tool install swarf

# Via Go
go install github.com/mschulkind-oss/swarf@latest

# From source
git clone https://github.com/mschulkind-oss/swarf && cd swarf
just deploy   # builds and copies to ~/.local/bin/
```

> **Warning:** Don't use `pip install swarf` inside a virtualenv or `uvx swarf`.
> The daemon records the binary's absolute path and breaks when the venv
> disappears. `swarf doctor` detects this and warns you.

## Quick start

```bash
cd ~/projects/my-app
swarf init
```

On first run, `init` walks you through setup:

```
No global config found. Let's set one up.

  Backend [git/rclone] (git): git
  Remote URL (your private backup repo): git@github.com:you/my-swarf.git
✓ Wrote ~/.config/swarf/config.toml
✓ Created central store at ~/.local/share/swarf
✓ Initialized swarf/ for my-app
  Install systemd service for auto-sync? [Y/n] y
✓ Installed systemd service — daemon is running
✓ Daemon is running (PID 12345)

✓ All checks passed.
```

Next time you run `swarf init` in another project — no prompts, instant setup.

### Rclone backend (Google Drive, Dropbox, S3, etc.)

Set up an rclone remote first:

```bash
brew install rclone
rclone config
#   → n (new remote), name: gdrive, type: drive
#   → scope: drive.file (option 2 — only files rclone creates)
#   → auto config: y (opens browser for OAuth)
```

Then run `swarf init`, pick `rclone`, and select your remote from the
numbered menu. Swarf defaults to `swarf-store` as the directory path:

```
  Backend [git/rclone] (git): rclone

  Pick an rclone remote:

    1. gdrive:

  Enter a number (1-1), or q to quit: 1

  Directory path on gdrive: [swarf-store]:
✓ Remote: gdrive:swarf-store
```

The store is always a local git repo — rclone transports the `.git/`
directory to the remote so you get full history on every backend.

## Using swarf

### Drop files in

```bash
echo "# Design Notes" > swarf/docs/design.md
```

That's it. The daemon commits and syncs after a 5-second quiet period.

### Sweep files into the host tree

Some files need to appear at a specific path in the project (like `AGENTS.md`
in the root). Use `sweep` to move them into swarf while leaving a symlink:

```bash
swarf sweep AGENTS.md
# AGENTS.md → swarf/.links/AGENTS.md

swarf sweep CLAUDE.md .copilot/skills/SKILL.md
```

Both the original path and `swarf/` are automatically gitignored. Symlinks
are relative, so they work across machines and inside containers.

To reverse a sweep:

```bash
swarf unlink AGENTS.md
```

### Auto-sweep

Configure files to be swept automatically whenever they appear:

```toml
# ~/.config/swarf/config.toml
[auto_sweep]
paths = ["AGENTS.md", "CLAUDE.md", ".copilot/skills/"]
```

The daemon watches for these files and sweeps them on creation.

### Check status

```bash
swarf status
```

Shows store state, pending files, remote verification, and daemon health:

```
Store
  Path           ~/.local/share/swarf
  Backend        git
  Remote         git@github.com:you/my-swarf.git
  Pending        all synced to remote
  Last commit    2 minutes ago — auto: sync 3 files
  Local save     2m ago
  Remote push    2m ago
  Remote sync    verified — local and remote match (a1b2c3d4)

Projects
╭──────────┬─────────────────────┬──────────╮
│ PROJECT  │ PATH                │ STATUS   │
├──────────┼─────────────────────┼──────────┤
│ my-app   │ ~/projects/my-app   │ ✓ ok     │
│ api      │ ~/work/api          │ ✓ ok     │
╰──────────┴─────────────────────┴──────────╯

Daemon: running (PID 12345)
```

### Doctor

`swarf doctor` checks everything and fixes what it can:

```bash
swarf doctor
```

Doctor handles: missing config, missing store, broken symlinks, absolute
symlinks, missing gitignore entries, service installation, and remote
reachability. Errors include the exact fix command or config file to edit.

`init` and `doctor` share the same engine — the difference is that `init`
creates `swarf/` in a new directory, while `doctor` only checks and repairs.

## Commands

| Command | Description |
|---------|-------------|
| `swarf init` | Set up swarf in the current project |
| `swarf sweep <file>...` | Move files into `swarf/.links/` and symlink back |
| `swarf unlink <file>...` | Reverse a sweep — restore symlinks to regular files |
| `swarf doctor` | Check health and fix problems (config, store, service, links) |
| `swarf status` | Show projects, sync state, remote verification, daemon health |
| `swarf clone` | Clone the store from your configured remote (new machine setup) |
| `swarf pull` | Pull latest changes from the remote into the store |
| `swarf daemon start` | Start the background sync daemon (`--foreground` for debugging) |
| `swarf daemon stop` | Stop the daemon |
| `swarf daemon status` | Check if the daemon is running |
| `swarf daemon install` | Install as system service (systemd on Linux, launchd on macOS) |
| `swarf docs [topic]` | Browse built-in documentation |

## Second machine setup

```bash
brew install swarf
swarf clone              # clones your store from the configured remote
cd ~/projects/my-app
swarf init               # re-links the project from the store
```

`clone` requires global config to exist (with the remote URL). After cloning,
run `swarf init` in each project directory to recreate the local `swarf/`
directory and symlinks.

## Containers and jails

The daemon runs on the **host**. Containers mount the project directory, so
`swarf/` comes along. Agents read and write to `swarf/` directly — the host
daemon picks up changes and syncs.

Inside a container, `swarf doctor` detects the environment (no global config),
skips system checks, and validates project-local state only. `sweep` and
`unlink` work without the daemon. Symlinks are relative, so they resolve
correctly regardless of mount path.

## Guides

- **[Configuration Guide](docs/CONFIGURATION.md)** — all config options,
  file locations, environment variables, and examples
- **[Roadmap](docs/ROADMAP.md)** — planned features and distribution channels
- **Built-in docs:** `swarf docs` lists all topics (`quickstart`,
  `architecture`, `config`, `sweep`, `daemon`, `backends`)

## License

Apache 2.0
