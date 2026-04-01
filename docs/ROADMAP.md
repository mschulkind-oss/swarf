# Swarf Roadmap

## Planned Features

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
