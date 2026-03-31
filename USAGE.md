# Swarf Usage Guide

Step-by-step instructions for testing swarf with both git and rclone/gdrive backends.

## Prerequisites

```bash
# Install swarf (editable, from source)
uv tool install . --force

# Verify
swarf --version
```

## 1. Configure your backend (one-time)

Create `~/.config/swarf/config.toml` with your backup destination.

### Option A: Git backend

```bash
# Create a bare remote (simulates a private GitHub repo)
git init --bare /tmp/test-swarf-remote

mkdir -p ~/.config/swarf
cat > ~/.config/swarf/config.toml << 'EOF'
[sync]
backend = "git"
remote = "/tmp/test-swarf-remote"
EOF
```

### Option B: Rclone backend (local target, no cloud needed)

```bash
mkdir /tmp/test-swarf-rclone-target

mkdir -p ~/.config/swarf
cat > ~/.config/swarf/config.toml << 'EOF'
[sync]
backend = "rclone"
remote = "/tmp/test-swarf-rclone-target"
EOF
```

### Option C: Google Drive

```bash
# Install rclone and configure gdrive (one-time, interactive)
brew install rclone   # or: mise use rclone
rclone config
#   -> n (new remote), name: gdrive, type: drive
#   -> scope: drive.file (option 2)
#   -> auto config: y (opens browser)

# Verify
rclone lsd gdrive:

mkdir -p ~/.config/swarf
cat > ~/.config/swarf/config.toml << 'EOF'
[sync]
backend = "rclone"
remote = "gdrive:swarf"
EOF
```

### Optional: auto-sweep

Add paths to auto-sweep into `.swarf/links/` whenever you enter a project:

```toml
[auto_sweep]
paths = ["AGENTS.md", "CLAUDE.md", ".copilot/skills/"]
```

## 2. Verify your setup

```bash
swarf doctor
# ✓ Global config: backend=git, remote=...
# ✓ Git remote reachable: ...
```

## 3. Testing with a project

### 3a. Create a test repo

```bash
mkdir /tmp/test-swarf && cd /tmp/test-swarf
git init
git commit --allow-empty -m "init"
```

### 3b. Initialize swarf

```bash
swarf init
```

You should see:
- `.swarf/` directory created with `docs/`, `links/`, `open-questions.md`
- `.mise.local.toml` created with enter hook
- `.git/info/exclude` updated with swarf-managed entries
- Backend and remote from your global config
- Offer to install/start the daemon

### 3c. Add some content

```bash
echo "# Research Notes" > .swarf/docs/research/notes.md
echo "# Agent Config" > AGENTS.md
```

### 3d. Sweep a file into swarf

```bash
swarf sweep AGENTS.md
# Should show: swept AGENTS.md
ls -la AGENTS.md
# Should be a symlink -> .swarf/links/AGENTS.md
```

### 3e. For git backend: push the initial branch

```bash
cd .swarf
git push -u origin master
cd ..
```

### 3f. Start the daemon and test auto-sync

```bash
# Start in foreground so you can see the logs
swarf daemon start --foreground &

# Make a change
echo "New finding" >> .swarf/docs/research/notes.md

# Wait for debounce (5s default) + sync
sleep 7

# For git backend — check the remote received the push
git -C /tmp/test-swarf-remote log --oneline
# Should show: auto: sync 1 file

# For rclone backend — check files were synced
ls /tmp/test-swarf-rclone-target/

# Stop the daemon
swarf daemon stop
```

### 3g. Check status

```bash
swarf status
swarf doctor
```

## 4. Daemon as a system service

To have the daemon auto-start on login:

```bash
swarf daemon install
```

This creates a systemd user service. Manage it with:

```bash
systemctl --user status swarf
journalctl --user -u swarf -f
systemctl --user restart swarf
```

## 5. Key commands reference

| Command | Description |
|---------|-------------|
| `swarf init` | Initialize `.swarf/` in current project |
| `swarf sweep <file>...` | Move files into `.swarf/links/` and symlink back |
| `swarf enter` | Run link + auto-sweep (called by mise hook) |
| `swarf doctor` | Validate setup and backend health |
| `swarf status` | Show all projects and sync status |
| `swarf daemon start` | Start background sync |
| `swarf daemon start --foreground` | Start in foreground (for debugging) |
| `swarf daemon stop` | Stop the daemon |
| `swarf daemon status` | Check if daemon is running |
| `swarf daemon install` | Install systemd user service |

## 6. File layout after init

```
your-project/
├── .swarf/                    # git repo, hidden via .git/info/exclude
│   ├── config.toml            # per-drawer config (from global)
│   ├── docs/
│   │   ├── research/          # put research notes here
│   │   └── design/            # put design docs here
│   ├── links/                 # files here get symlinked into host tree
│   │   └── AGENTS.md          # → ./AGENTS.md
│   └── open-questions.md
├── .mise.local.toml           # auto enter hook (hidden via .git/info/exclude)
├── AGENTS.md → .swarf/links/AGENTS.md
└── (your code)
```

## Troubleshooting

**"swarf is already initialized here"** — `.swarf/` exists. Remove it to re-init: `rm -rf .swarf`

**"No global config found"** — Create `~/.config/swarf/config.toml` (see step 1).

**Daemon exits immediately** — No drawers registered. Run `swarf init` in at least one project.

**Push fails** — For git backend, ensure the remote branch exists: `cd .swarf && git push -u origin master`

**rclone sync fails** — Test your remote manually: `rclone lsd gdrive:` or `rclone ls <remote>`
