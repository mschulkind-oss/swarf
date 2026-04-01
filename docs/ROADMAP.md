# Swarf Roadmap

## Distribution & Installation

Swarf is a Go binary but currently distributed via PyPI. This creates friction:
users need Python, then need to understand pip vs pipx vs uvx, and most install
paths break the daemon (see user-stories.md). We need multiple distribution
channels, prioritized by reach and daemon-safety.

### Now

1. **PyPI (current)** — widest reach for the AI tooling audience. Works today
   but the daemon problem is real: `pip install` into a venv gives a binary
   that disappears. `pipx install` and `uv tool install` work but require
   users to know about those tools. Short-term fix: `daemon install` should
   detect ephemeral binary paths and warn/copy.

2. **Nix package** — near-term priority for jail/container support. A flake
   or nixpkgs package lets jail configs add swarf to their package list.
   Updates via flake lock or nixpkgs channel.

3. **Homebrew tap** — `brew tap mschulkind-oss/tap && brew install swarf`.
   Covers macOS and Linux homebrew users. Updates via `brew upgrade`.
   Requires maintaining a tap repo with a formula that points to GitHub
   release binaries.

### Later

4. **Shell installer** — `curl -fsSL https://swarf.dev/install.sh | sh`.
   Downloads the correct platform binary from GitHub releases to
   `~/.local/bin/swarf`. No Python needed. **Update story:** user re-runs
   the installer to update, or we add `swarf self-update` that pulls the
   latest release.

5. **AUR (Arch User Repository)** — `yay -S swarf` or `paru -S swarf`.
   Arch users expect this. A PKGBUILD that pulls the GitHub release binary
   is simple to maintain. Updates when the PKGBUILD version is bumped
   (can be automated with a GitHub Action that submits to AUR on release).

6. **go install** — `go install github.com/mschulkind-oss/swarf@latest`.
   Free, no maintenance, but only for people with Go installed. Good for
   contributors, not end users.

### Update problem

Every channel except brew and nix/AUR leaves updates to the user. Options:

- **`swarf self-update`** — checks GitHub releases, downloads the new binary,
  replaces itself. Simple, works everywhere. Risk: replacing a running binary
  (fine on Linux, tricky on Windows). This is probably the right answer for
  the shell installer path.
- **Release notification** — `swarf doctor` or `swarf status` could check for
  newer versions and print a hint. Low-friction, no auto-mutation.
- **Per-channel native updates** — brew upgrade, nix flake update, AUR
  helpers. No extra work from us, but only covers those channels.

### Container / jail distribution

The primary use case: a jail config lists swarf as a package and it's
available at `/usr/bin/swarf` on startup. This requires either:

- A Nix package (jail uses Nix for package management)
- A static binary baked into the container image
- A bind-mount of the host binary into the container

Nix is the right short-term answer since yolo jails already use nixpkgs.
The package should build from source (Go is well-supported in Nix) or
fetch the release binary.

---

## Planned Features

### Doctor as the Universal Fix-It

Doctor is the central mechanism for noticing and fixing problems. Today it
handles:

- ✓ Missing symlinks → re-created
- ✓ Absolute symlinks → converted to relative
- ✓ Missing system service → offers to install (systemd or launchd)
- ✓ Ephemeral binary location → warns, blocks service install

Init and clone both run doctor as their final step. The pattern is:
**every command that changes state finishes with doctor** so the user
always ends in a healthy state.

Future doctor checks to add:
- Missing global config → offer to create (prompts for backend/remote)
- Missing store → offer to create or clone
- Stale daemon (running but not watching current project) → restart
- Version mismatch (service binary vs current binary) → offer to reinstall
- Missing gitignore entries → add them (already partially done)

**`swarf init` and `swarf doctor` share the same engine** but have one
key difference: init creates `swarf/` in the current directory if it
doesn't exist yet, doctor does not. Both fix system-level issues
(global config, store, service) interactively.

If you have a halfway-done install and come back later, `swarf doctor`
picks up where you left off for system setup. To actually set up a
new project, use `swarf init` — that's the only command that creates
the local `swarf/` directory.

---

### Smarter Clone (Second-Machine Setup)

`swarf clone` today requires global config to already exist (with the remote
URL). On a brand-new machine, this creates a chicken-and-egg problem: you need
the config to clone, but the config is what you're trying to restore.

**Desired flow:**
1. `swarf clone <remote-url>` — accepts the remote URL as an argument (no
   prior config needed). Clones the store, writes global config from it.
2. Doctor runs and notices/fixes everything else (service, etc.).
3. Lists projects found in the store with instructions to `swarf init` each.
4. Optionally: if the user is in a project directory that matches a store
   entry, offer to run init immediately.

**Open questions:**
- Should clone accept rclone remotes too? (`swarf clone gdrive:swarf-store`)
  If so, how does it know the backend without a config file?
- Should the store contain a copy of the global config so clone can restore
  it? That would make the second-machine experience truly one command.
- If the user has an existing config (different remote), should clone warn
  or refuse?

---

### Git-Backed Rclone (Unified Store Format)

Today the git and rclone backends are separate paths: git stores commit
history, rclone just copies files. This means rclone users lose history,
and the two codepaths diverge in behavior.

**Proposed change:** The store is *always* a local git repo with auto-commits.
The backend only controls transport — how the repo gets to the remote:

- **git backend:** `git push` / `git pull` (unchanged)
- **rclone backend:** `rclone sync` the store's `.git` directory (or a git
  bundle) to Google Drive / Dropbox / S3 / etc.

**Benefits:**
- History on every backend. `swarf log` and `swarf restore` work identically.
- One codepath for commits, mirroring, conflict detection.
- Rclone remote doesn't need to be human-browseable — it's backup, not a
  file browser. Cryptic `.git` objects on Drive are fine.
- No encryption needed (but could be layered later with `git-crypt` or
  similar).

**Format options for rclone sync:**
1. **Packed `.git/` directory.** Run `git repack -a -d` before each sync to
   consolidate loose objects into a single pack file. A small personal store
   ends up with ~10-15 files total (one .pack, one .idx, HEAD, config, refs).
   Rclone syncs only changed files. This is the sweet spot.
2. **Git bundle (`git bundle create`).** Single file, minimal API calls (1).
   But it's a full rewrite every sync — uploads the entire history each time.
   Worse for bandwidth as the store grows.
3. **Loose `.git/` (no packing).** Hundreds of small object files. Each costs
   one API call. Hits Drive's 3 writes/sec limit quickly.

**Google Drive API limits (why this matters):**
- Write limit: **3 requests/second sustained** (hard cap, can't be increased)
- Read limit: 20,000 calls/100 seconds (not a concern)
- Rclone does NOT batch API calls — each file operation costs one call
- Syncing 500 loose git objects = 500 API calls = ~3 minutes of throttling
- Syncing 10-15 packed files = 10-15 API calls = instant

**Recommendation:** Option 1 (sync `.git/` directly). No special packing
needed.

The math works out: each commit creates 1 tree + 1 commit + 1 blob per
changed file. The daemon debounces (5s), so multiple edits batch into one
commit. Typical sync = 3-7 loose objects. At Drive's 3 writes/sec limit,
that's 1-2 seconds. Editing a single file after a previous sync adds only
1 new blob (tree and commit are shared across files in the same commit),
so marginal cost is 1 API call per file.

A swarf store is tiny text files — loose objects are bytes. Repacking is
a space optimization that can wait indefinitely. Just let `git gc --auto`
run with defaults and don't think about it.

**Migration:** Existing rclone users have flat file mirrors without git.
Add a `swarf migrate` or have doctor detect the old format and offer to
convert (init a git repo in the store, commit everything, switch to
git-backed sync).

---

### Non-Interactive Mode (Agent & Script Support)

All prompts in init (and clone) need flag equivalents so agents and scripts
can drive swarf without stdin.

**Flags needed:**
- `swarf init --backend git --remote URL` — skip config prompt
- `swarf init --install-service` / `--no-install-service` — skip daemon prompt
- Combined: `swarf init --backend git --remote URL --install-service`

**Also needed:**
- `swarf clone <remote-url>` accepting the URL as a positional argument
  (see Smarter Clone above)
- All commands should exit cleanly when stdin is not a TTY and required
  input is missing (error with usage hint, not a hung prompt)

---

### Machine-Readable Output

Agents parse JSON, not prose. Key commands need `--json` flags:

- `swarf doctor --json` — array of check objects with name, ok, msg
- `swarf status --json` — daemon health, project list, sync state
- `swarf list --json` — all files across all projects with paths and types

This also enables tooling integrations (editor plugins, CI checks).

---

### Rclone Init UX

The init flow is git-centric. Rclone users need:

- Detection of available rclone remotes (from `rclone listremotes`)
- Interactive remote picker instead of freeform "Remote URL" prompt
- Guidance when no remotes exist ("run `rclone config` first")
- `drive.file` scope recommendation for Google Drive surfaced during init
- Headless environment detection with link to rclone docs

---

### Cross-Project Search

`swarf search "auth middleware"` that searches across all registered
projects' swarf content. The store has everything in one git repo — this
is essentially a `git grep` on the store.

Useful for long-running agents that accumulate context across many projects
and want to find prior research, decisions, or patterns from other repos.

---

### Remote Pull-Down (Inbound Sync)

Today swarf pushes local changes to the remote, but there's no automatic
path for pulling changes *from* the remote back into local projects.

**Problem:** If you push from machine A and then sit down at machine B, the
daemon on B doesn't know anything changed upstream. `swarf pull` exists as
a manual command, but nothing triggers it automatically.

**Open questions:**

- **Poll vs. push notification?** Polling the remote on a timer (e.g. every
  60s) is simple but wasteful for git remotes. A webhook or push-based
  approach is more efficient but adds infrastructure.
- **Conflict resolution.** If both machines edit the same file before syncing,
  what wins? Options: last-write-wins, keep-both with `.conflict` suffix,
  or interactive merge (too heavy for side-files?).
- **Propagation to projects.** After pulling into the central store, the
  daemon needs to reverse-mirror: copy updated files from
  `~/.local/share/swarf/<project>/` back into `<project>/swarf/`. This is
  the inverse of the current mirror direction.
- **Re-linking after pull.** If a pulled file lands in `swarf/links/`, the
  symlink in the project root might not exist yet (new machine, fresh clone).
  The existing re-link logic handles this, but it needs to trigger after
  every inbound sync, not just on startup.

**Possible approach:**

1. Daemon polls remote on a configurable interval (`pull_interval` in config).
2. On changes detected, pull into store, then reverse-mirror to each project.
3. Re-link runs after reverse-mirror, same as startup.
4. Conflicts: keep-both with `<file>.conflict.<timestamp>` for safety,
   log a warning. User resolves manually.

---

### History Browsing and Recovery

The central store is a git repo with auto-commits, so file history exists
but is completely opaque to the user today.

**Problem:** There's no way to browse what changed, when, or recover a
previous version of a file. The git log is there, but the commit messages
are auto-generated and the store path (`~/.local/share/swarf/`) is not
something users think about.

**Open questions:**

- **Granularity.** Each daemon sync creates one commit covering all projects.
  Should we commit per-project instead, so history is cleaner?
- **Interface.** CLI subcommand (`swarf history`, `swarf restore`)? Or just
  document how to use `git log` / `git show` in the store?
- **What to show.** Flat file list with timestamps? Diff view? Just the
  commit log?
- **Recovery UX.** `swarf restore AGENTS.md` could mean "restore from store"
  (re-link) or "restore a previous version" (git checkout from history).
  Need to disambiguate.

**Possible approach:**

1. `swarf log [file]` — shows history for a file (or all files in the
   current project). Translates project-relative paths to store paths,
   runs `git log --oneline` under the hood.
2. `swarf restore <file> [--at <ref>]` — restores a file from history.
   Without `--at`, restores the latest version from the store (useful after
   accidental deletion). With `--at`, checks out a specific revision.
3. Per-project commits in the store for cleaner history.
4. Better auto-commit messages: include project name and changed file list.
