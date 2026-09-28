# Sync Hardening Plan

Plan and task list for making two-machine sync safe enough to leave running.
It builds on the failure analysis in [Sync Scenarios](SYNC_SCENARIOS.md),
which is the research stage for this work. The vocabulary there (project,
store, manifest, peer cache, forward mirror, reverse mirror) is used here
unchanged.

## Terms introduced by this plan

These are coined here. Each is defined once and used only in this sense.

- **delete hold** — the state a project is in when the forward mirror found
  that applying the deletes it computed would remove a suspiciously large
  share of the project's tracked files, and so applied none of them. The
  held paths stay in the manifest, so the hold persists until the files come
  back or the user confirms the deletes.
- **hold marker** — the file `~/.cache/swarf/delete-holds/<slug>.json` that
  records a delete hold, so the daemon, `swarf status`, and `swarf doctor`
  can see it. Removed as soon as a forward pass completes without holding.
- **resync** — `swarf pull --resync`: a pull that ignores the last-seen
  ref for each peer (`refs/swarf-peers/<peer>`, see Sync Scenarios,
  Scenario 4) and instead unions every file in the peer's current tree into
  the store. It never deletes a file because the peer's tree lacks it; the
  only deletes it applies are those the peer committed since the last-seen
  ref, exactly as a plain pull would. This is the same "content-union"
  mode pull already uses for peers whose history shares no commit with
  ours.
- **owner token** — a random value generated once per local store and kept
  in the store's `.git/` directory, so it travels to
  `<remote>/machines/<machine_id>/` with every sync. It is how a machine
  recognizes that a remote subtree it is about to overwrite was written by a
  different store using the same `machine_id`.
- **sync lock** — an advisory file lock at `~/.cache/swarf/sync.lock` held
  for the duration of any push, pull, or daemon cycle, so two of them never
  touch the store at the same time.

## What changed from the earlier recommendation

Sync Scenarios recommends removing the forward mirror from `swarf pull`.
That recommendation is withdrawn. The reverse mirror at the end of a pull
deletes every project file that is not in the store, so without the forward
mirror a file saved moments before a pull (before the daemon's debounce
fired, or with no daemon running) would be deleted from the project.
`TestMultiMachine_LocalEditSurvivesPull` pins that case. The forward mirror
stays; the delete hold (task 1) is what makes it safe, and task 2 makes the
reverse mirror stop deleting files it has no record of syncing.

## Tasks

Each task is one commit (or a small series), test first, `just check` green.

### 1. Delete hold in the forward mirror

The fix for Scenarios 6, 7, and 8 (emptied-directory case).

- Before applying any delete, count the manifest entries that are missing
  from the project **and** still exist in the store. Paths matched by a sync
  exclude are not counted: dropping them is deliberate.
- Hold when either:
  - the project would lose every tracked file and at least 2 are at stake, or
  - at least 10 files would be deleted and they are at least half the
    manifest.
- On hold: apply none of the counted deletes, still copy adds and
  modifications, keep the held paths in the manifest, and write the hold
  marker (count, a sample of paths, first-seen time). Excluded paths are
  still pruned from the store during a hold: they are not part of it.
- On any pass that does not hold, remove the hold marker. Pull also removes
  it after its reverse mirror when the project no longer has deletes that
  would hold, so restoring the files with `swarf pull` ends the hold at
  once rather than on the next forward pass.
- `swarf push --allow-deletes` applies held deletes for that one pass.
- The daemon logs a hold once when it appears, not on every cycle.
- `swarf status` and `swarf doctor` show each held project with the two ways
  out: restore the files (for rclone, `swarf pull` copies them back from the
  store), or confirm with `swarf push --allow-deletes`.

Tests: the Scenario 6 reproduction (two machines, B pushes with a populated
manifest, B's project is wiped, B pushes again, B's store must keep every
file) as an integration test; unit tests for both thresholds, the exclude
carve-out, manifest retention, and marker lifecycle.

### 2. Reverse mirror deletes only what it has seen synced

Replace `mirror.Dir` in pull's reverse step with a manifest-aware variant:
copy store to project as now, but delete a project file only when the
manifest lists it (swarf synced it before) and the store no longer has it.
A project file the manifest has never listed is left alone. Afterwards,
rewrite the manifest to the project's current file set so the next forward
pass starts from what pull left behind.

Tests: unit tests for the new function (untracked project file survives,
tracked file removed from store is deleted from project, excludes are
untouched); the existing pull and multi-machine suites stay green.

### 3. `swarf pull --resync`

Recovery for a machine that has already diverged, as the reproduction in
the status review showed: after B's store lost files, later pulls never
restored them because nothing changed on A.

- `--resync` runs the existing union mode for every peer regardless of the
  last-seen ref, then advances the ref as a normal pull does.
- Never deletes a file because the peer lacks it; deletes the peer made
  since the last-seen ref are still applied, as a plain pull would, since
  advancing the ref past them would lose them for good. Divergent content
  follows the existing union-mode rules (conflict sidecar when local
  differs and is not a known peer version), except that a local copy of
  a file the peer has not changed since the last-seen ref is an ordinary
  local edit: it is kept with no sidecar, as a plain pull would. When the
  last-seen ref is unknown (absent, or not in a rebuilt peer's history),
  "not changed" falls back to "the store's history already had the
  peer's current content".

Tests: integration test that creates the divergence (B's store loses files
and commits, A unchanged), shows a plain pull does not restore them, and
shows `--resync` does, with the project repopulated.

### 4. Sync lock and scheduled pull

The fix for Scenario 5, plus the missing piece that makes sync automatic.

- Push, pull, and the daemon cycle each hold the sync lock. A CLI command
  that finds it held waits (with a message) rather than failing.
- New `[sync] pull_interval` (a duration such as `"10m"`; empty or `"0"`
  means off, which is the default). When set and the backend is rclone, the
  daemon runs a pull on that interval under the sync lock. With the git
  backend the daemon logs once that scheduled pull is unsupported and does
  not schedule it.
- Fix the `swarf pull` help text, which currently claims the daemon pulls
  periodically.

Tests: config parsing; lock exclusion between two holders; the daemon's
scheduling decision (interval set/unset, backend rclone/git).

### 5. `machine_id` ownership check

The fix for two machines sharing a `machine_id` (often a shared hostname,
or a `config.toml` copied with dotfiles).

- Each store gets an owner token in its `.git/` directory, created on first
  use.
- Before uploading, the rclone backend reads the owner token already on the
  remote for this machine's subtree. Absent: proceed (the upload claims it).
  Same: proceed. Different: refuse to upload, commit locally only, and report
  why. If the remote token cannot be read at all (as opposed to being
  absent), the upload is skipped for that pass rather than risk
  overwriting a subtree whose owner is unknown.
- `swarf push --take-over` uploads anyway, for a machine that genuinely
  replaces the old one. The message says plainly that this replaces the
  subtree and deletes from the remote anything only the old store had
  there: `swarf pull` first merges the other peers' changes, but pull skips
  this machine's own id (see Out of scope), so it never saves the subtree
  being replaced.
- `swarf doctor` reports the mismatch.

Tests: backend unit tests for absent/same/different/take-over; an
integration test where two machines configured with the same id push, and
the second is refused without touching the first's subtree.

### 6. Documentation

- README: say plainly that multi-machine sync is the rclone backend; the git
  backend is single-machine (both machines push one branch, the second push
  fails, and pull does not mirror into projects).
- Configuration: `pull_interval`, the delete hold, `--allow-deletes`,
  `--resync`, `--take-over`.
- Built-in docs (`swarf docs`) where the same topics are covered.
- Sync Scenarios: update statuses and the verification matrix, and mark the
  withdrawn recommendation.

## Out of scope

- Multi-machine support for the git backend.
- Retiring a peer (Scenario 10).
- A machine that is reinstalled under its old `machine_id` does not pull
  its own old subtree back (pull skips `self`). Other peers still have the
  data; noted here, not fixed.

## QA

Filled in when the tasks land.
