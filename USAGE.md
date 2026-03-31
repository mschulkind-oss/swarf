# Swarf Usage Guide

Step-by-step instructions for testing swarf with both git and rclone/gdrive backends.

## Prerequisites

```bash
# Install swarf (editable, from source)
uv tool install . --force

# Verify
swarf --version
```

## 1. No global setup needed

Swarf automatically manages `.git/info/exclude` in each repo so `.swarf/`
and related files stay invisible. No global gitignore configuration required.

## 2. Testing the git backend

### 2a. Create a test repo

```bash
mkdir /tmp/test-swarf-git && cd /tmp/test-swarf-git
git init
git commit --allow-empty -m "init"
```

### 2b. Create a bare remote (simulates GitHub)

```bash
git init --bare /tmp/test-swarf-git-remote
```

### 2c. Initialize swarf

```bash
swarf init --backend git --remote /tmp/test-swarf-git-remote
```

You should see:
- `.swarf/` directory created with `docs/`, `links/`, `open-questions.md`
- `.mise.local.toml` created
- `.git/info/exclude` updated with swarf-managed entries
- Summary with backend and remote

### 2d. Verify the setup

```bash
swarf doctor
```

### 2e. Add some content

```bash
echo "# Research Notes" > .swarf/docs/research/notes.md
echo "# Agent Config" > .swarf/links/AGENTS.md
```

### 2f. Sweep a file into swarf

```bash
swarf sweep AGENTS.md
# Should show: swept AGENTS.md
ls -la AGENTS.md
# Should be a symlink -> .swarf/links/AGENTS.md
```

### 2g. Push the initial branch to the remote

The daemon needs a remote branch to push to:

```bash
cd .swarf
git push -u origin master
cd ..
```

### 2h. Start the daemon and test auto-sync

```bash
# Start in foreground so you can see the logs
swarf daemon start --foreground &

# Make a change
echo "New finding" >> .swarf/docs/research/notes.md

# Wait for debounce (5s default) + sync
sleep 7

# Check the remote received the push
git -C /tmp/test-swarf-git-remote log --oneline
# Should show: auto: sync 1 file

# Stop the daemon
swarf daemon stop
```

### 2i. Check status

```bash
swarf status
```

## 3. Testing the rclone/gdrive backend

### 3a. Install rclone

If not already installed:

```bash
mise install rclone
mise use rclone
```

Or: `brew install rclone` / `apt install rclone`

### 3b. Configure the gdrive remote (one-time, interactive)

```bash
rclone config
```

Walk through the wizard:
1. `n` for new remote
2. Name it `gdrive`
3. Type: `drive` (Google Drive)
4. Client ID/secret: leave blank (uses rclone's built-in)
5. Scope: `drive.file` (option 2) — can only access files rclone created
6. Service account: leave blank
7. Auto config: `y` (opens browser for OAuth)
8. Shared drive: `n`
9. Confirm: `y`

Verify it works:

```bash
rclone lsd gdrive:
```

### 3c. Test with a local rclone target first (no cloud needed)

If you want to test without gdrive credentials:

```bash
mkdir /tmp/test-swarf-rclone-target
mkdir /tmp/test-swarf-rclone && cd /tmp/test-swarf-rclone
git init
git commit --allow-empty -m "init"

swarf init --backend rclone --remote /tmp/test-swarf-rclone-target
```

### 3d. Test with gdrive

```bash
mkdir /tmp/test-swarf-gdrive && cd /tmp/test-swarf-gdrive
git init
git commit --allow-empty -m "init"

swarf init --backend rclone --remote "gdrive:swarf/test-project"
```

### 3e. Add content and sync

```bash
echo "# Design Doc" > .swarf/docs/design/architecture.md
```

### 3f. Start daemon and verify

```bash
swarf daemon start --foreground &
echo "Updated" >> .swarf/docs/design/architecture.md
sleep 7

# For local target:
ls /tmp/test-swarf-rclone-target/
# Should contain: docs/, config.toml, open-questions.md (no .git/)

# For gdrive:
rclone ls "gdrive:swarf/test-project"
# Should list your files

swarf daemon stop
```

## 4. Daemon as a system service

To have the daemon auto-start on login:

```bash
swarf daemon install
```

This creates a systemd user service. Manage it with:

```bash
just status-service    # or: systemctl --user status swarf
just logs              # or: journalctl --user -u swarf -f
just restart-service   # or: systemctl --user restart swarf
```

## 5. Key commands reference

| Command | Description |
|---------|-------------|
| `swarf init --backend git --remote <url>` | Initialize with git backend |
| `swarf init --backend rclone --remote <path>` | Initialize with rclone backend |
| `swarf sweep <file>...` | Move files into `.swarf/links/` and symlink back |
| `swarf doctor` | Validate setup health |
| `swarf status` | Show all drawers and sync status |
| `swarf daemon start` | Start background sync |
| `swarf daemon start --foreground` | Start in foreground (for debugging) |
| `swarf daemon stop` | Stop the daemon |
| `swarf daemon status` | Check if daemon is running |
| `swarf daemon install` | Install systemd user service |

## 6. File layout after init

```
your-project/
├── .swarf/                    # git repo, hidden via .git/info/exclude
│   ├── config.toml            # backend + remote + debounce
│   ├── docs/
│   │   ├── research/          # put research notes here
│   │   └── design/            # put design docs here
│   ├── links/                 # files here get symlinked into host tree
│   │   └── AGENTS.md          # → ./AGENTS.md
│   └── open-questions.md
├── .mise.local.toml           # auto-link on cd (hidden via .git/info/exclude)
├── AGENTS.md → .swarf/links/AGENTS.md
└── (your code)
```

## Troubleshooting

**"swarf is already initialized here"** — `.swarf/` exists. Remove it to re-init: `rm -rf .swarf`

**Gitignore warnings** — Run `swarf init` to auto-configure `.git/info/exclude`.

**Daemon exits immediately** — No drawers registered. Run `swarf init` first in at least one project.

**Push fails** — For git backend, ensure the remote branch exists: `cd .swarf && git push -u origin master`

**rclone sync fails** — Test your remote manually: `rclone lsd gdrive:` or `rclone ls <remote>`
