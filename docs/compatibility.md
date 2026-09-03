# Data compatibility

kancli keeps its board in the data directory (`kancli/` under
`$XDG_DATA_HOME` or the OS config dir, or wherever `KANCLI_FILE` points),
and its settings in `config.json` under `$XDG_CONFIG_HOME` or the OS config
dir (or wherever `KANCLI_CONFIG` points):

| File | What it is | Version marker |
|---|---|---|
| `board.db` | one SQLite database in WAL mode, holding everything: `events` (the append-only history, one row per event), `snapshots` (folded state, kept under a retention policy) and `meta` | `meta.format`, the store format, currently 1. Snapshot rows carry a board file whose `"version"` is `board.FileVersion` (2); every event row carries `v`, `board.EventVersion` (1) |
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
   snapshot, `board.EventVersion` for events. Write a migration in
   `internal/board/decode.go` (snapshot) or a version switch in
   `(*File).Apply` (events) that understands every earlier version.
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
