# Git Backend Multi-Machine Plan

Plan and task list for letting two or more machines share one git remote,
the way they already share an rclone remote. Vocabulary (project, store,
manifest, peer cache, forward mirror, reverse mirror) is from
[Sync Scenarios](SYNC_SCENARIOS.md); delete hold, resync, owner token, and
sync lock are from [Sync Hardening](SYNC_HARDENING.md).

## Why it doesn't work today

- Every machine pushes the store's single branch to the same branch on the
  remote. The second machine's push is rejected as non-fast-forward, and the
  git backend retries it forever without resolving it.
- `swarf pull` on the git backend is a bare `git pull`: no reverse mirror
  into projects, no conflict sidecars, and a real conflict leaves merge
  markers in the store for the daemon to commit.
- None of the rclone pull machinery (per-peer file changes, sidecars,
  `--resync`, scheduled pull) runs for git.

The rclone pull logic is already git logic: it fetches each peer's store as
a git remote, diffs from the last-seen ref, and applies files one by one.
rclone is only how the peer's `.git/` reaches this machine. This plan keeps
that logic and gives git its own transport.

## Terms introduced by this plan

Coined here; each is used only in this sense.

- **machine branch** — the branch `machines/<machine_id>` on the git remote.
  Only the machine with that `machine_id` pushes it, and never with force
  (except `--take-over`). It is the git counterpart of the rclone
  `<remote>/machines/<machine_id>/` subtree.
- **legacy branch** — any branch on a git remote that is not under
  `machines/`. In practice it is the `main` (or `master`) branch that every
  machine pushed to before this change.
- **retiring** a legacy branch — removing it from the remote once its
  content is safe in a machine branch: delete it, or, when the host refuses
  (GitHub refuses to delete the default branch), replace it with a
  tombstone.
- **tombstone** — a commit with no parents containing one file,
  `SWARF-BRANCHES.md`, which says the store uses per-machine branches and
  that this branch is intentionally empty. Its commit message carries the
  trailer `Swarf-Tombstone: true`, which is how swarf recognizes it.

The remote is an archive, not something meant to be browsed. After
migration, the only live branches are machine branches.

## Tasks

Each task is test first, one commit per logical change, `just check` green.
Tests use a local bare repository as the git remote.

### 1. Per-peer pull reads from the store's own objects

Refactor with no behavior change. The per-peer code currently reads diffs
and file contents from the rclone peer cache directory. It already fetches
the peer's objects into the store, so make every read go through the store
using a peer tip commit, and make the per-peer function take the peer's tip
(and a name for messages and refs) rather than a cache directory. rclone
keeps downloading the peer cache and fetching from it; only the reads move.
The existing pull and multi-machine suites are the QA.

One consequence needs care: the store keeps the last-seen commit's objects
even after a peer rebuilds its store, where the peer cache lost them. So the
last-seen ref is used only while it is an ancestor of the peer's tip;
otherwise a plain pull fails that peer and a resync applies no deletes, as
before. Tests pin both cases.

### 2. Git push to the machine branch

- The git backend pushes `HEAD` to `refs/heads/machines/<machine_id>`,
  never with force.
- A non-fast-forward rejection means another store is writing this
  `machine_id`. Commit locally only, report it as the rclone owner-token
  refusal is reported (once per cycle in the daemon log, in `swarf push`
  output, and in `swarf doctor`), and name `--take-over`.
- `swarf push --take-over` force-pushes the machine branch. It is no longer
  refused on the git backend. The message says it discards what only the
  other store pushed.
- `swarf status` counts unpushed commits against the machine branch.
- `swarf doctor` reports a mismatch when the remote machine branch exists
  and is not an ancestor of the local `HEAD`.

### 3. Git pull from peer machine branches

- Fetch `refs/heads/machines/*` into the store (under a swarf-owned ref
  namespace, not as tracking branches the user would see), and run the
  task 1 per-peer code for every machine branch except our own. Last-seen
  refs, conflict sidecars, union mode for unrelated histories, `--resync`,
  the forward and reverse mirrors, and delete-hold release all behave as
  they do for rclone.
- Bootstrap: a fresh machine no longer clones. `swarf pull` creates the
  store, adds the remote, fetches, and pulls every peer (every peer file is
  an add).
- Reinstall recovery: if the store has no commits and the remote already
  has this machine's own machine branch, start the store from it (check it
  out) before pulling peers. This gives a reinstalled machine its old data
  back.
- `[sync] pull_interval` and `swarf pull --resync` are supported on the git
  backend.

### 4. Migrate and retire the legacy branch

On any git-backend pull or push, if the remote has a legacy branch that is
not a tombstone:

- **Fold it in.** If the store has no commits, or `HEAD` is an ancestor of
  the legacy tip, fast-forward the store to the legacy tip. Otherwise apply
  it as if it were a peer (same file rules and sidecars) and make the
  resulting commit record the legacy tip as a second parent, so the legacy
  tip becomes an ancestor of `HEAD`. Either way the fold runs between a
  forward and a reverse mirror, as a pull does, so the store's
  uncommitted files (the forward mirror's) count as local edits: a
  fast-forward keeps whatever the store has that the legacy tip does not
  staged for the next push to commit. A push (and each daemon cycle)
  folds in before it commits.
- **Retire it** only after the machine branch has been pushed and the
  remote legacy tip is an ancestor of it. Delete the branch; if the remote
  refuses, force-push a tombstone to it. If the legacy tip moved in between
  (a machine still running an older swarf pushed to it), do nothing this
  time; the next pass folds in the new commits. If the remote refuses the
  tombstone too, the branch stays, the refusal is recorded, and the next
  pass tries again.
- Pull skips tombstones. An older swarf on another machine then fails to
  push to the tombstoned branch (non-fast-forward) instead of writing into
  it, and `git pull` refuses the unrelated history. So nothing is lost, but
  every machine needs to be upgraded.
- Stop tracking the legacy branch locally. The store's local branch name no
  longer matters: push always targets the machine branch.
- `swarf doctor` reports a legacy branch that is still waiting to be
  retired, and why.

Tests: a remote with only `main` (with history) plus two machines that
migrate in either order; the case where `main` moved after we folded it in;
a remote whose pre-receive hook refuses deleting `main` (tombstone path);
an older-swarf push after the tombstone is rejected; pull ignores the
tombstone.

### 5. Documentation

README, `docs/CONFIGURATION.md`, `swarf docs` (the backend and safety
topics), Sync Scenarios, Sync Hardening, and ROADMAP: git is now a
multi-machine backend; describe machine branches, migration, the tombstone,
and the upgrade-every-machine note. Remove the "git backend is
single-machine" statements and the git-only refusals that no longer apply.

## Out of scope

- Browsing the store on the remote (it is an archive).
- Retiring a peer's machine branch (the rclone equivalent is also open).

## QA

Filled in when the tasks land.
