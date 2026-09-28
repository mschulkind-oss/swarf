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
| `~/.local/share/swarf/.git/swarf-owner` | This store's owner token (rclone backend; see [Owner token](#owner-token-and---take-over)) |
| `~/.cache/swarf/manifests/<slug>.txt` | The manifest: the files swarf last synced from each project |
| `~/.cache/swarf/delete-holds/<slug>.json` | Hold marker for a project in a [delete hold](#delete-hold-and---allow-deletes) |
| `~/.cache/swarf/sync.lock` | The [sync lock](#sync-lock) |
| `~/.cache/swarf/peers/<peer>/` | Peer cache: a copy of another machine's remote subtree (rclone backend) |

All paths respect XDG conventions. Override with environment variables:

| Variable | Default | Controls |
|----------|---------|----------|
| `XDG_CONFIG_HOME` | `~/.config` | Config directory (config.toml, drawers.toml, daemon files) |
| `XDG_DATA_HOME` | `~/.local/share` | Data directory (central store) |
| `XDG_CACHE_HOME` | `~/.cache` | Cache directory (manifests, hold markers, sync lock, peer caches) |

## Global config (`config.toml`)

Created by `swarf init` on first run. You can also edit it directly.

```toml
[sync]
backend = "git"                                      # "git" or "rclone"
remote = "git@github.com:you/my-swarf-store.git"     # git URL or rclone remote:path
debounce = "5s"                                      # wait after last change before syncing
exclude = [".yolo/"]                                 # drawer paths never copied to the store
pull_interval = "10m"                                # daemon pulls on this interval (default off)

[machine]
id = "laptop"                                        # stable name for this machine (multi-machine sync)

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

Both backends sync any number of machines through one remote, with the
same pull, conflict sidecars, `pull_interval`, `swarf pull --resync`, and
`swarf push --take-over`. They differ in how they keep each machine's
writes apart: rclone gives each machine its own subtree
([Multi-machine rclone](#multi-machine-rclone)), git its own branch
([Multi-machine git](#multi-machine-git)).

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

A stable identifier for this machine. Each backend keeps each machine's
writes apart by it: the rclone backend in its own subtree on the remote
(`<remote>/machines/<id>/`), the git backend on its own machine branch
(`machines/<id>`), so two machines never write to the same place on the
remote — the one rule that makes multi-machine sync safe.

If unset, a default is derived from the hostname (lowercased, with a
trailing `.local` stripped on macOS and any non-alphanumeric characters
replaced with `-`). `swarf init` writes the default to `config.toml`, and
so does the first sync (the daemon's or `swarf push`'s) if it is still
unset then, so a later hostname change cannot move this machine to a new
subtree or branch. You usually don't have to set this by hand. That write
regenerates `config.toml` from its settings, so comments and formatting
in the file are not kept; set `id` yourself to avoid it.

Override if your hostnames are unstable (e.g. containers) or if you want
human-readable machine names on the remote:

```toml
[machine]
id = "desktop"
```

Once set, **don't change it** — the machine's history lives under that
id on the remote. If you do change it, you need to rename the folder
(rclone) or branch (git) manually on the remote.

Every machine needs its **own** id. Two machines that share one (a
generic hostname, or a `config.toml` copied with dotfiles) would upload
over each other's subtree or branch; the
[owner token](#owner-token-and---take-over) (rclone) or the
[machine branch check](#machine-branch-and---take-over) (git) stops the
second one's upload, and `swarf doctor` reports it.

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

#### `pull_interval`

How often the daemon runs `swarf pull` on its own. Off by default: without
it the daemon only pushes, and you run `swarf pull` yourself to bring in
other machines' changes.

```toml
[sync]
pull_interval = "10m"
```

Same units as `debounce` (`ms`, `s`, `m`, `h`). Empty or `"0"` means off.
A value that does not parse is logged in the daemon log and treated as
off.

Each scheduled pull takes the [sync lock](#sync-lock), so it never
overlaps a debounced sync or a `swarf push`/`swarf pull` you run by hand;
a slow pull delays the next one rather than stacking up. The daemon log
records each pull's start and end, every peer that failed, any conflict
sidecars it wrote, and store projects not registered on this machine.

#### `no_fast_list`

Turns off rclone's `--fast-list`, which swarf passes on every store sync and
peer pull. Default `false` — that is, fast listing is **on**.

An rclone sync stat-checks every file in the store, and without the flag
rclone sends one listing request per directory. On a 12,000-file store that
is ~28,000 round-trips, and it dominates the cycle: a no-op sync measured
148s without the flag and 10s with it, against the same remote. Note that
this is a count-of-files cost, not a bytes cost — dropping 10 GB of packfile
out of that same store changed the 148s by about 12 seconds.

`--fast-list` holds the listing in memory in the rclone child process,
proportional to the number of objects rather than their size (tens of MB at
15,000 objects). Set this to `true` if a remote holds so many objects that
the listing no longer fits in RAM:

```toml
[sync]
no_fast_list = true
```

Backends with no recursive-listing support ignore the flag, so leaving it on
costs nothing there.

#### `exclude`

Glob patterns for paths inside a drawer that should **not** be synced. They
stay in your project; they never reach the store, the remote, or a peer.

```toml
[sync]
exclude = [
  ".yolo/",          # a directory of that name at any depth, and all of it
  "node_modules/",
  "*.tmp",           # a base-name glob, at any depth
  "home/ssh/*",      # a glob over the drawer-relative path
]
```

Three forms, each matched against the path relative to the drawer's
`swarf/` directory:

| Pattern | Matches |
|---------|---------|
| ends in `/` | a directory of that name at any depth, and everything under it |
| contains `/` | a glob over the whole relative path, plus everything under a match |
| no `/` | a glob over the file's base name, at any depth |

Globs are `path.Match`, so `*` does not cross a `/`.

Adding an exclude for content that is **already** in the store removes it
from the store on the next sync, and from the remote on the sync after
that — the same mechanism as deleting the file, so the store's git history
still has it. If what you excluded was a secret, treat the exclude as
step one: the blobs stay in the store's history until you rewrite it.

Excludes also keep the daemon from watching those directories, which is
worth having on its own for a tree that churns (jail state, build output):
a watched churning directory wakes a full remote sync per write and has
nothing to copy.

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
    ├── refs/swarf-peers/       ← last commit merged in from each peer
    ├── refs/swarf-machines/    ← git backend: the store's copy of each machine branch
    ├── refs/swarf-pushed/      ← git backend: the commit last pushed to this machine's branch
    ├── refs/swarf-legacy/      ← git backend: the store's copy of each legacy branch
    └── refs/swarf-tombstones/  ← git backend: each tombstone seen on the remote
```

Each project gets a subdirectory matching its slug. The daemon mirrors
file changes (including deletions) from project `swarf/` dirs into the
store, then commits and pushes.

The `refs/swarf-peers/<id>` namespace records the last commit this
machine has merged in from each peer, for both backends. See
"Multi-machine rclone" below for how those refs drive the file-delta
pull. The other `refs/swarf-*` namespaces are the git backend's; see
"Multi-machine git" below. None of them is a branch or tag, so a push
never sends them.

For rclone backends, the store is synced to the remote under
`<remote>/machines/<machine_id>/` — working files are browseable directly
in Google Drive (or wherever), and `.git/` is included for full commit
history. See "Multi-machine rclone" below. For the git backend, the
store is pushed to the branch `machines/<machine_id>`; that remote is an
archive, not something meant to be browsed.

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

**Pull:** `swarf pull` lists folders under `machines/`, copies each
other machine's folder to `~/.cache/swarf/peers/<id>/`, and reconciles
that peer's working tree into the local store file-by-file.

Per-peer progress is tracked with a ref in the local store:

```
refs/swarf-peers/<peer-id>
```

Each ref holds the SHA of the last peer commit this machine has merged
in. The next pull diffs `<that-SHA>..peerHEAD` inside the peer cache
and applies only those file changes. The refs live alongside `refs/heads/`
in the store's `.git/` and never collide with branch or tag refs.

On first pull from a peer (ref missing), the code uses
`git merge-base HEAD peerHEAD` to recover an effective last-seen —
this matters when upgrading from a previous swarf version that relied
on shared git history. If the two repos share no ancestor at all
(unrelated histories, e.g. a machine restored from a backup with a
different initial commit), pull falls back to a **content-union** mode:
peer files are added/modified into the local tree, but nothing is
deleted — there's no way to distinguish "peer deleted this" from
"peer never had it."

Reconciliation outcomes:

- Identical content (either byte-for-byte equal or last-seen==peerHEAD):
  no-op.
- Clean add/modify/delete: applied directly to the working tree.
- Conflict (peer changed a file local also changed, or peer deleted a
  file local has modified): your local version stays as the main file;
  the peer's version is written alongside as
  `<path>.conflict.<peer>.<timestamp>`. Both are committed to the
  store. Open them in your editor, edit the main file to what you
  want, and `rm` the `.conflict.*` sidecar. Next sync propagates the
  resolution to the other machine.

Pull then copies the store back into each registered project's `swarf/`
(the *reverse mirror*). It deletes a project file only when the manifest
says swarf synced it before and the store no longer has it, so a peer's
delete still lands but a file the manifest has never listed is left for
the next forward pass to pick up. A file you save while the pull is
downloading peers is kept too; if a peer changed that same file, the
peer's version is written as a conflict sidecar. Afterwards the manifest
is rewritten to the project's current file set.

Nothing is ever silently overwritten. Because the delta is computed
from each peer's `.git/` at pull time, the machines do not need to
share commit history — each machine's `git log` shows its own edits
plus the auto-sync commits it generated to pull in peer changes.

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
`machines/<id>/` folder on its first sync — check that its `[machine] id`
differs from every other machine's before that sync. To have the daemon
pull on its own, set [`pull_interval`](#pull_interval).

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

## Multi-machine git

Two or more machines can share one git remote, the same way they share
an rclone remote. Instead of a subtree, each machine gets a **machine
branch**: the branch `machines/<machine_id>` on the remote (named after
[`[machine] id`](#id)). Only the machine with that id pushes it, and
never with force, except [`swarf push --take-over`](#machine-branch-and---take-over).

```
git@github.com:you/my-swarf-store.git
  machines/laptop     ← only laptop pushes here
  machines/desktop    ← only desktop pushes here
```

The remote is an archive, not something meant to be browsed. Once any
branch from before machine branches is
[retired](#migrating-from-a-single-branch-git-remote), the only live
branches are machine branches, and a host's web view of the default
branch shows at most a tombstone.

**Push:** the daemon (and `swarf push`) commits the store and pushes
`HEAD` to `machines/<own-id>`. The store records the commit it last
pushed in `refs/swarf-pushed/<machine_id>`. `swarf status` counts
unpushed commits against that ref without contacting the remote, and a
sync pushes whenever `HEAD` has moved past it, so a push that failed
(unreachable remote, bad credentials) is retried on the next cycle even
with nothing new to commit.

**Pull:** `swarf pull` fetches every machine branch into the store as
`refs/swarf-machines/<machine_id>`, the store's copy of that branch as of
the last fetch. These are not remote-tracking branches: `git branch -r`
does not show them, branches gone from the remote are pruned, and no
tags are fetched. Each machine branch other than this machine's is one
peer, merged exactly as the rclone backend merges a peer (see
[Multi-machine rclone](#multi-machine-rclone)): `refs/swarf-peers/<id>`
last-seen refs, conflict sidecars, content-union for unrelated
histories, [`--resync`](#resync-swarf-pull---resync), the reverse mirror
into projects, and releasing a [delete hold](#delete-hold-and---allow-deletes).
There is no peer cache: the fetch brings each peer's history straight
into the store.

**Bootstrap:** a fresh machine does not clone. `swarf pull` creates the
store, adds `[sync] remote` as `origin`, fetches, and pulls every peer,
so every peer file is an add. A store with no `origin` and an empty
`[sync] remote` has nothing to pull from, and pull says so.

**Reinstall recovery:** when the store has no commits and the remote
already has this machine's own machine branch, pull starts the store
from that branch (and records it as pushed) before it pulls the peers.
A machine reinstalled under its old id gets its own data back, and its
next push fast-forwards the branch instead of being refused as another
store's. Files you saved in a project before that first pull are
copied in after the store is started, so they are kept as local edits.
`swarf doctor` reports a store with no commits whose machine branch is
already on the remote, and says to run `swarf pull` before anything is
committed.

### Setting up a second git machine

The same steps as for [rclone](#setting-up-a-second-machine): install
swarf, run `swarf init` with the same git remote, run `swarf pull`, then
`swarf init` in each project. Check that the new machine's
`[machine] id` differs from every other machine's before its first sync.

### Migrating from a single-branch git remote

Before machine branches, every machine pushed its store to the one branch
on the remote, usually `main` or `master`. Swarf now calls any branch on
a git remote that is not under `machines/` a **legacy branch**. There are
no manual steps: every git-backend `swarf pull`, `swarf push`, and daemon
cycle migrates the remote.

1. **Fold in.** Swarf fetches each legacy branch into the store as
   `refs/swarf-legacy/<branch>`. If its tip is not in the store's history
   yet, it is folded in. With no commits in the store, or `HEAD` an
   ancestor of the tip, the store fast-forwards to the tip. Otherwise the
   tip is applied as a peer's changes, with the same file rules and
   conflict sidecars (named `<file>.conflict.<branch>.<timestamp>`, with
   any `/` in the branch name turned into `-`), and committed as
   `auto: fold in legacy branch <branch>` with the tip as a second
   parent. Either way the tip is then an ancestor of `HEAD`, files the
   store has that the tip lacks are kept as local edits, and the result
   is copied into your projects. In a push or daemon cycle the fold runs
   after that command's own forward mirror, so a project in a
   [delete hold](#delete-hold-and---allow-deletes) stays held; only a
   `swarf pull` restores held files.
2. **Retire.** Once the machine branch has been pushed with the legacy
   tip in its history, swarf **retires** the legacy branch: removes it
   from the remote. It deletes the branch. If the remote refuses (GitHub
   refuses deleting the default branch), it force-pushes a tombstone to
   the branch instead. Both updates hold only while the branch still
   points at the commit swarf folded in, so if a machine still running an
   older swarf pushed to it in between, swarf leaves it this time and the
   next pass folds in the new commits. A push retires right after it
   pushes; a pull retires a legacy branch the machine branch already
   holds.
3. **Refused.** If the remote refuses the tombstone too, the branch
   stays, swarf records the refusal, and the next pass tries again.

A **tombstone** is a commit with no parents containing one file,
`SWARF-BRANCHES.md`, which says the store uses per-machine branches and
that this branch is intentionally empty. Its commit message ends with
the trailer `Swarf-Tombstone: true`, which is how swarf recognizes it.
Pull skips tombstones, and the store keeps each one it has seen at
`refs/swarf-tombstones/<branch>`, so it is still recognized after
`git gc`.

The store also stops tracking `origin`: its local branch has no upstream
and there are no remote-tracking branches. The local branch's name no
longer matters, because a push always targets the machine branch.

`swarf doctor` lists each legacy branch still waiting to be retired, and
why: its commits are not folded into this store yet, the machine branch
does not have them yet, it is ready to retire on the next pass, or the
remote refused both deleting it and replacing it with a tombstone. Only
the refusal fails the check; the others clear on the next pull or push.

**Upgrade swarf on every machine that uses the remote.** A machine still
running an older swarf keeps pushing to the legacy branch. While the
branch is still there, the next pass on an upgraded machine folds its
commits in. After a delete, its push creates the branch again, and the
next upgraded pass folds it in and retires it again. After a tombstone,
its push is rejected as a non-fast-forward and its `git pull` refuses
the unrelated history, so nothing it writes is lost, but it stops
syncing until it is upgraded.

## Sync safeguards

Four mechanisms keep a mistake on one machine — a wiped directory, two
machines sharing an id, a push racing a pull — from being synced out to
the others. See [Sync Scenarios](SYNC_SCENARIOS.md) for the situations
each one covers.

### Delete hold and `--allow-deletes`

A **delete hold** is the state a project is in when the forward mirror
(project → store, run by the daemon, `swarf push`, and the start of
`swarf pull`) found that applying the deletes it computed would remove a
suspiciously large share of the project's tracked files, and so applied
none of them.

Only files that the manifest lists, that are gone from the project, and
that the store still has count as "at stake." Paths matched by a sync
`exclude` are left out: dropping them is deliberate. The pass holds when
either:

- the project lost every tracked file and at least **2** are at stake, or
- at least **10** files are at stake and they are at least **half** of
  the tracked files.

During a hold, adds and edits are still copied, the held paths stay in
the manifest (so the hold lasts until you act), and excluded paths are
still pruned from the store. The hold is recorded in a **hold marker**,
`~/.cache/swarf/delete-holds/<slug>.json`, with the count, a sample of up
to 10 paths, and when the hold started. The daemon logs a hold once when
it appears, not every cycle; `swarf status` lists held projects under
"Delete holds" and `swarf doctor` reports a failing "delete hold" check.

Two ways out:

- **Restore the files.** `swarf pull` copies them back from the store
  and clears the hold (it also clears it if the files are gone from the
  store too, because another machine deleted them). This works the same
  with either backend.
- **Confirm the deletes.** `swarf push --allow-deletes` applies the held
  deletes for that one push.

The marker is removed by the next forward pass that does not hold.

### Resync (`swarf pull --resync`)

A plain pull applies only what each peer changed since the last pull
(tracked by `refs/swarf-peers/<peer>`). If this machine's store lost
files and committed the deletes, they do not come back while the peer
stays unchanged. **Resync** — `swarf pull --resync` — ignores that
last-seen ref and unions every file in each peer's current tree into the
store, then advances the ref as a normal pull does. Both backends
support it.

- It never deletes a file because a peer lacks it. The only deletes it
  applies are those a peer made since the last pull, as a plain pull
  would.
- A local file that matches any version a peer ever had is replaced by
  the peer's current copy — even an older version you restored on
  purpose.
- A local file that matches none of them keeps its content and gets a
  conflict sidecar, unless the peer has not changed the file since the
  last pull; that is an ordinary local edit and is kept with no sidecar.
  A peer that changed the file back to an older version has still
  changed it, so that case gets a sidecar, as a plain pull would give.
  (When the last-seen ref is missing or no longer in the peer's history,
  "not changed" falls back to "the peer's current copy is one this store
  already had".)

### Owner token and `--take-over`

An **owner token** is a random value generated once per local store and
kept in `~/.local/share/swarf/.git/swarf-owner`, so it travels to
`<remote>/machines/<machine_id>/` with every rclone upload. Before
uploading, swarf reads the token already on the remote:

| Remote token | Result |
|--------------|--------|
| absent | upload, which claims the subtree |
| this store's | upload |
| a different store's | commit locally, skip the upload, report why |
| unreadable (remote error) | skip the upload for this pass |

A different token means two machines use the same `machine_id` — often a
shared hostname, or a `config.toml` copied with dotfiles. `swarf doctor`
reports it as a failing "machine owner" check, and the daemon logs it
once until it changes. Fix it one of two ways:

- **Different computers:** give this one its own `[machine] id`.
- **This computer replaces the old one:** `swarf push --take-over`
  uploads anyway. It replaces the remote subtree with this store and
  deletes from the remote anything only the old store had there.
  `swarf pull` merges the other machines' subtrees first, but pull skips
  this machine's own id, so it never saves the subtree being replaced —
  copy out anything you need beforehand
  (`rclone copy <remote>/machines/<id> <dir>`).

### Machine branch and `--take-over`

The git backend has no owner token. Its counterpart is the
[machine branch](#multi-machine-git) itself: swarf never forces a push
to it, so a push the remote rejects as not a fast-forward means the
branch holds commits this store never had, pushed by a different store
using the same `machine_id`. The push commits locally only and reports
why. `swarf doctor` reports it as the same failing "machine owner" check
(whenever the remote machine branch exists and is not an ancestor of the
local `HEAD`), `swarf status` says the push is refused, and the daemon
logs it once until it changes. A push that fails for any other reason
(unreachable remote, bad credentials, a server hook) is reported as a
failed sync and retried on the next cycle.

The same two fixes apply:

- **Different computers:** give this one its own `[machine] id`.
- **This computer replaces the old one:** `swarf push --take-over`
  force-pushes this store to the machine branch, discarding the commits
  only the old store pushed there. `swarf pull` starts a store from the
  machine branch only while the store has no commits, so copy out
  anything you need beforehand
  (`git fetch <remote> machines/<id>`).

### Sync lock

The **sync lock** is an advisory file lock (`flock(2)`) on
`~/.cache/swarf/sync.lock`, held for the duration of any push, pull, or
daemon cycle, so two of them never touch the store at the same time. A
`swarf push` or `swarf pull` that finds it held prints

```
Another swarf push, pull, or daemon cycle holds the sync lock; waiting for it to finish...
```

and waits; Ctrl-C ends the wait. The kernel drops the lock when its
holder exits, so a crashed command never leaves it stuck.

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
