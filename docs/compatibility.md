# Data compatibility

kancli keeps its board in the data directory (`kancli/` under
`$XDG_DATA_HOME` or the OS config dir, or wherever `KANCLI_FILE` points),
and its settings in `config.json` under `$XDG_CONFIG_HOME` or the OS config
dir (or wherever `KANCLI_CONFIG` points):

| File | What it is | Version marker |
|---|---|---|
| `board.db` | one SQLite database in WAL mode, holding everything: `events` (the append-only history, one row per event), `snapshots` (folded state, kept under a retention policy) and `meta` | `meta.format`, the store format, currently 1. Snapshot rows carry a board file whose `"version"` is `board.FileVersion` (2); every event row carries `v`: `board.EventVersion` (1) for an unchanged event kind, its own number for a kind whose meaning changed, and this build reads up to `board.MaxEventVersion` (2) |
| `config.json` | user settings | none; unknown keys are ignored, renamed keys are aliased |

Older kancli builds kept the same data in `board.json`,
`board.events.jsonl`, `board.events/`, `board.snapshots/` and `board.lock`.
Nothing about the JSON shapes changed: `board.FileVersion` is still 2 and
`board.EventVersion` is still 1. Only the container is new, and the store
format in `meta.format` is its own number. Every commit is fsynced (the
database runs with `synchronous(FULL)`), so a change kancli has reported as
saved survives a power failure.

## What a user gets on upgrade

- The first time this build opens a data directory that still has the old
  `board.json`, it imports it: every snapshot under `board.snapshots/` (a
  version-1 file is migrated first), then the archived event segments and
  the tail, into a new `board.db`. The whole import is one transaction; if
  it fails the half-written database is deleted and the old files are left
  untouched.
- The old files are then **moved**, not copied, into `board.backups/vN/`
  next to the database (N = the version of the old `board.json`, so
  normally `board.backups/v2/`). An existing backup directory is never
  overwritten; the files go to `board.backups/vN-<unix time>/` instead.
  `board.lock` is removed. One line on stderr names the directory.
- Moving is deliberate. An older kancli started in the same directory
  afterwards finds no `board.json` and shows an **empty board** rather than
  writing to files this build no longer reads. That is why the notice names
  the backup directory: it is where the old data is, and pointing an old
  build at it gets the old board back.
- Snapshots are pruned instead of kept forever. The retention policy keeps
  sequence 0 (the empty base), the newest 5 snapshots, the newest per
  calendar day for the last 30 days, and the newest per ISO week before
  that. The days are measured by the timestamps of the events each
  snapshot folds in, not by when the fold ran, so an imported or bulk
  written history keeps a base per day of its own history. Events are never pruned, so `--as-of` still answers for any point
  in the history; it just replays a few more events for old dates.
- Undo no longer writes a copy of the whole board. It emits the two event
  kinds `task.reverted` (upsert one task by id) and `board.reverted`
  (everything except the tasks) for what actually changed. The older
  `board.restored` kind is still understood when reading old history.
- Old snapshots are migrated on read, so `--as-of` keeps working across
  versions.
- A config key that was renamed keeps working under its old name and
  prints a warning naming the new one.

## Goal boards and cross-board links

Additive in the file: `board.FileVersion` is unchanged and every existing
file loads as it did. `board.EventVersion`, the version events are written
with by default, is still 1; only the events that name another board are
stamped `v: 2`, and this build reads up to `board.MaxEventVersion` (2):

- `Link.board` (json `board,omitempty`): the id of the other task's board.
  Absent or empty means the link points at a task on the same board, which
  is what every link written before this version is.
- `Board.kind` (json `kind,omitempty`): `""` or `"tasks"` for a ticket
  board, `"goals"` for a goal board. Absent means a ticket board.
- The event kind `board.kind` (`Text` = the new kind) records the change.
  The `link.added` / `link.removed` events carry the other task's board in
  their otherwise unused `to` field. Such an event is written with `v: 2`,
  because an older build would ignore `to` and apply the link to whatever
  task has that number on the event's own board. Link events inside one
  board, and every other kind, keep the default `v: 1`.
- Deleting a task, or a whole board, drops the links other boards hold to
  it. Each of those removals is its own `link.removed` event on the board
  that stores the link (also `v: 2`), so the history and a replay agree
  with the live state on both sides.

The caveat: an older kancli ignores both fields, so it must not read a log
that has any of these version-2 events — and it does not: it stops with
"written by a newer kancli" and changes nothing. A board file (snapshot)
with cross-board links opened by an older build would still show them as
dangling (`no task #12`, looked up on the wrong board) and drop
`Link.board` on its next write of that task, which silently turns the link
into a same-board one.

## What a user gets on downgrade

Running an older kancli on newer data fails before anything is written:

- a `meta.format` higher than the build knows: "… was written by a newer
  kancli"
- a newer `board.json` version inside a snapshot: "file was written by a
  newer kancli"
- an event kind or event version this build does not know: "… was
  written by a newer kancli; upgrade kancli …"

Nothing is modified in any of these cases. A build that predates `board.db`
does not fail, because it never looks at the database; it sees an empty
directory, which the upgrade notice warns about.

## Rules for changing a format (every PR that touches on-disk data)

1. **Never rename or remove** a JSON field or an event kind string. Add
   new ones. Old builds ignore unknown fields; new builds must accept old
   files with the field missing.
2. **Changing the meaning** of an existing field or of an event's data
   payload requires bumping the version: `board.FileVersion` for the
   snapshot, and for events a version of the affected kind: stamp the new
   number on the events whose meaning changed and raise
   `board.MaxEventVersion` (what this build can read), leaving
   `board.EventVersion` as the default other kinds are written with. Write
   a migration in `internal/board/decode.go` (snapshot) or a version switch
   in `(*File).Apply` (events) that understands every earlier version.
3. **Bumping the store format** (`store.StoreFormat`, written to
   `meta.format`) means a schema migration keyed on `meta.format` — or an
   importer like `internal/store/import.go` when the whole container
   changes — plus a `testdata/vN` fixture and a row in
   `TestLoadsEveryReleasedFormat`.
4. **Freeze a fixture** of the format you are replacing under
   `internal/store/testdata/vN/` and add a row to
   `TestLoadsEveryReleasedFormat` in `internal/store/compat_test.go`.
   Fixtures are never edited afterwards.
5. **Renaming a config key** means adding a row to `renamedKeys` in
   `internal/config/config.go`. Rows are never deleted.
6. Describe the change under "Changed" in `CHANGELOG.md` with the user's
   view: what they will see, where the backup is.

## Known limits

- Two kancli processes that both add a task starting from the same state
  allocate the same task number. The merge keeps one of the two. This was
  true of the file store as well; fixing it properly needs per-device ids,
  which are out of scope for now.
- A clean exit checkpoints the write-ahead log, so copying `board.db` after
  quitting is a valid backup. Copying it while the app is running may miss
  recent writes: take `board.db-wal` too, or use `kancli export`.
