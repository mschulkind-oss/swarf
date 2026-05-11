# Configuration Guide

Swarf uses a small set of files in your XDG config and data directories.
Everything is created automatically by `swarf init` on first run — you
only need to edit config manually to change defaults or add auto-sweep.

## File locations

| File | Purpose |
|------|---------|
| `~/.config/swarf/config.toml` | Global settings (backend, remote, debounce, auto-sweep) |
| `~/.config/swarf/drawers.toml` | Registry of initialized projects (managed automatically) |
| `~/.config/swarf/daemon.pid` | PID of the running daemon |
| `~/.config/swarf/daemon.log` | Daemon log output |
| `~/.config/swarf/last-commit` | Timestamp of last successful local commit |
| `~/.config/swarf/last-push` | Timestamp of last successful remote push |
| `~/.local/share/swarf/` | Central backup store (a git repo mirroring all projects) |

All paths respect XDG conventions. Override with environment variables:

| Variable | Default | Controls |
|----------|---------|----------|
| `XDG_CONFIG_HOME` | `~/.config` | Config directory (config.toml, drawers.toml, daemon files) |
| `XDG_DATA_HOME` | `~/.local/share` | Data directory (central store) |

## Global config (`config.toml`)

Created by `swarf init` on first run. You can also edit it directly.

```toml
[sync]
backend = "git"                                      # "git" or "rclone"
remote = "git@github.com:you/my-swarf-store.git"     # git URL or rclone remote:path
debounce = "5s"                                      # wait after last change before syncing

[machine]
id = "laptop"                                        # stable name for this machine (rclone multi-machine)

[auto_sweep]
paths = ["AGENTS.md", "CLAUDE.md"]                   # files to auto-sweep when they appear
```

### `[sync]` section

#### `backend`

The sync transport. Two options:

- **`git`** — The store is a git repo with a remote. The daemon commits
  locally, then pushes. Use any git remote: GitHub, GitLab, Gitea, a bare
  repo over SSH, etc. This is the recommended backend if you have a git host.

- **`rclone`** — The daemon commits locally (the store is always a git repo),
  then uses `rclone sync` to upload the entire store to the remote. Your
  files are browseable directly in Google Drive (or wherever) — organized
  by project, just like the local store. The `.git/` directory is synced
  too, so you get full commit history. Works with any rclone backend:
  Google Drive, Dropbox, S3, B2, SFTP, etc.

#### `remote`

Where to push. Format depends on the backend:

- **Git:** A git remote URL.
  ```
  git@github.com:you/my-swarf-store.git
  https://github.com/you/my-swarf-store.git
  ssh://git@myserver/~/swarf-store.git
  ```

- **Rclone:** An rclone remote path in `name:path` format.
  ```
  gdrive:swarf-store
  dropbox:backups/swarf
  s3:my-bucket/swarf
  ```
  The remote name (before the colon) must match a configured rclone remote
  (`rclone listremotes` to see available ones). The path is created
  automatically on first sync.

### `[machine]` section

#### `id`

A stable identifier for this machine. Used by the rclone backend to keep
each machine's writes in its own subtree on the remote
(`<remote>/machines/<id>/`), so two machines never write to the same file
on the remote at the same time — the one rule that makes multi-machine
rclone safe.

If unset, a default is derived from the hostname (lowercased, with a
trailing `.local` stripped on macOS and any non-alphanumeric characters
replaced with `-`). `swarf doctor` writes the default the first time it
runs on a new machine, so you usually don't have to set this by hand.

Override if your hostnames are unstable (e.g. containers) or if you want
human-readable machine names on the remote:

```toml
[machine]
id = "desktop"
```

Once set, **don't change it** — the machine's history lives under that
id on the remote. If you do change it, you need to rename the folder
manually on the remote.

### `[sync]` section (cont.)

#### `debounce`

How long to wait after the last file change before syncing. Accepts a number
with a unit suffix:

| Unit | Example |
|------|---------|
| `ms` | `500ms` |
| `s` | `5s` (default) |
| `m` | `1m` |
| `h` | `1h` |

A shorter debounce means faster sync but more commits. The default of `5s`
batches rapid edits into a single commit.

### `[auto_sweep]` section

#### `paths`

A list of file paths (relative to the project root) to sweep automatically.
When the daemon detects one of these files appearing as a regular file (not
already a symlink), it sweeps it into `swarf/.links/` and creates the
symlink.

```toml
[auto_sweep]
paths = [
  "AGENTS.md",
  "CLAUDE.md",
  ".copilot/skills/SKILL.md",
  ".cursor/rules/project.md",
]
```

Auto-sweep runs:
- At daemon startup
- When a matching file is created (via fsnotify)
- Every 30 seconds (polling for files that appeared while the daemon was down)

Files that are already symlinks or don't exist are skipped.

## Drawer registry (`drawers.toml`)

Managed automatically by `swarf init`. One entry per project:

```toml
[[drawer]]
slug = "my-app"
host = "/home/you/projects/my-app"

[[drawer]]
slug = "api-server"
host = "/home/you/work/api-server"
```

- **`slug`** — Derived from the directory name. Used as the subdirectory
  name in the central store (`~/.local/share/swarf/my-app/`).
- **`host`** — Absolute path to the project root on this machine.

You generally don't need to edit this file. It's updated when you run
`swarf init` in a project.

## Per-project files

These are created inside each project by `swarf init`:

| Path | Purpose |
|------|---------|
| `swarf/` | Local storage directory (auto-excluded from git) |
| `swarf/.links/` | Files projected into the host tree via symlinks |
| `.git/info/exclude` | Fenced section managing gitignore entries for swarf/ and swept files |

The exclude entries in `.git/info/exclude` are managed in a fenced section
(delimited by comments). Swarf adds entries for `swarf/` itself and for
every swept file. These are per-clone and never committed, so they work on
shared repos and monorepos without touching `.gitignore`.

## Central store layout

The store at `~/.local/share/swarf/` is a git repo that mirrors all projects:

```
~/.local/share/swarf/
├── my-app/
│   ├── .links/
│   │   └── AGENTS.md
│   └── docs/
│       └── design.md
├── api-server/
│   └── docs/
│       └── notes.md
├── README.md              ← auto-generated project table
└── .git/
```

Each project gets a subdirectory matching its slug. The daemon mirrors
file changes (including deletions) from project `swarf/` dirs into the
store, then commits and pushes.

For rclone backends, the store is synced to the remote under
`<remote>/machines/<machine_id>/` — working files are browseable directly
in Google Drive (or wherever), and `.git/` is included for full commit
history. See "Multi-machine rclone" below.

## Multi-machine rclone

Running swarf on two or more machines that share a single rclone remote
(Google Drive, Dropbox, S3, etc.) requires a bit more structure than a
single machine does: if both machines wrote to the same folder, their
`.git/` directories would race and you'd end up with a corrupt store.

Swarf avoids this by giving each machine its own subtree:

```
gdrive:swarf-store/
  machines/
    laptop/        ← only laptop writes here
      .git/
      my-app/
      ...
    desktop/       ← only desktop writes here
      .git/
      my-app/
      ...
```

**Push:** the daemon syncs `~/.local/share/swarf/` → `machines/<own-id>/`.
Because only one machine ever writes under that prefix, `rclone sync`
(destructive by design) is safe.

**Pull:** `swarf pull` lists folders under `machines/`, copies each other
machine's folder to `~/.cache/swarf/peers/<id>/`, registers that as a
git remote, fetches, and merges into the local store.

- Clean merge (fast-forward or auto-merge): silent success.
- Conflict: your local version stays as the main file; the peer's
  version is written alongside as `<path>.conflict.<peer>.<timestamp>`.
  Both are committed to the store. Open them in your editor, edit the
  main file to what you want, and `rm` the `.conflict.*` sidecar. Next
  sync propagates the resolution to the other machine.

Nothing is ever silently overwritten. Both machines' histories survive
in git even when working-tree content conflicts.

### Setting up a second machine

On the new machine, after installing swarf:

```bash
# 1. Write global config pointing at the same rclone remote.
swarf init                     # interactive, or edit config.toml by hand

# 2. Seed the store from every existing peer's folder on the remote.
swarf pull

# 3. Re-init each project directory.
cd ~/projects/my-app && swarf init
```

`swarf pull` bootstraps: when there is no local store yet, it creates
one and reconciles every peer's working files and git history into it
via the file-delta pull path. Each peer's last-seen tip is tracked in
`refs/swarf-peers/<id>` so subsequent pulls apply only incremental
changes. The new machine starts writing under its own
`machines/<id>/` folder on its first sync.

### Migrating from a flat (single-machine) rclone remote

Older swarf versions wrote directly to `<remote>/` (with no `machines/`
subfolder). The new code refuses to push to a remote in this layout to
avoid clobbering data. To migrate:

1. Stop the daemon on every machine using this remote: `swarf daemon stop`.
2. Pick a machine id for the machine that currently owns the data (the
   default is the hostname; set `[machine].id` in `config.toml` to
   override).
3. On the remote, move everything currently at the root — including
   `.git/` — into `machines/<your-id>/`. You can do this in the web UI,
   with `rclone move`, or `rclone sync`-then-cleanup. Example:
   ```bash
   rclone move <remote>: <remote>:/machines/<your-id> --exclude 'machines/**'
   ```
4. Start the daemon again: `swarf daemon start`. `swarf doctor` confirms
   the new layout is in place.

`swarf doctor` detects the legacy layout and prints these steps whenever
it sees `.git/` at the remote root.

## Daemon service

`swarf daemon install` creates a system service that starts the daemon on
login.

### Linux (systemd)

- **Service file:** `~/.config/systemd/user/swarf.service`
- **Logs:** `journalctl --user -u swarf -f`
- **Control:**
  ```bash
  systemctl --user status swarf
  systemctl --user restart swarf
  systemctl --user stop swarf
  ```

### macOS (launchd)

- **Plist:** `~/Library/LaunchAgents/com.swarf.daemon.plist`
- **Logs:** `~/Library/Logs/swarf/swarf.out.log` and `swarf.err.log`
- **Control:**
  ```bash
  launchctl list | grep swarf
  launchctl unload ~/Library/LaunchAgents/com.swarf.daemon.plist
  launchctl load ~/Library/LaunchAgents/com.swarf.daemon.plist
  ```

The service records the absolute path to the swarf binary. If you move or
reinstall swarf to a different location, re-run `swarf daemon install`.

## Troubleshooting

### "Remote push: never" in `swarf status`

The daemon hasn't successfully pushed yet. Check:

1. Is the daemon running? `swarf daemon status`
2. Is the remote reachable? `swarf doctor` (shows detailed error with fix)
3. Check daemon logs for errors.

### "Invalid rclone remote" or "remote not found"

Your `config.toml` has a bad remote value. Common cause: typing a number
instead of selecting from the menu in the old init flow.

Fix: delete `~/.config/swarf/config.toml` and run `swarf doctor` to
reconfigure, or edit the file directly.

### "Binary is at an ephemeral location"

You installed swarf into a virtualenv or temp directory. The daemon service
will break when that environment is removed.

Fix: install swarf persistently (`brew install swarf`, `pipx install swarf`,
or `uv tool install swarf`), then re-run `swarf daemon install`.

### Doctor says "No swarf/ here"

You're running `swarf doctor` in a directory that doesn't have swarf set up.
This is informational, not an error. Run `swarf init` to set it up.

### Symlinks broke after a fresh clone

Run `swarf init` in the project. It re-creates symlinks from `swarf/.links/`.
The daemon also re-links at startup, and `swarf doctor` fixes missing links.
