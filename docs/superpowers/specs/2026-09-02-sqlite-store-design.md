# SQLite store design

Date: 2026-09-02. Status: approved in conversation, implementation planned in
`docs/superpowers/plans/2026-09-02-sqlite-store.md`.

## Why

Measured on a generated board with 50,000 tasks, the file store has three
problems that all grow with board size rather than with the size of a change:

| Problem | Measured | Cause |
|---|---|---|
| Snapshot history | 1.4 GB in `board.snapshots/` for a 27 MB board | every compaction keeps a full copy, nothing is ever deleted |
| `--as-of` | 14.5 s | decodes whole snapshot files, then replays every event |
| Undo | 15.7 MB per `board.restored` event; the reader caps a line at 32 MB | undo persists a copy of the whole board |

Everyday commands still take 0.3 s and 200 MB at that size because every
launch parses the entire snapshot. That last cost is accepted in this phase
(archived tasks stay in memory); it is the reason to move to SQLite now so a
later phase can load live tasks through an index.

## Decisions

- **Engine**: SQLite through `modernc.org/sqlite` (pure Go, no cgo, builds on
  Linux, macOS and Windows; verified SQLite 3.53.4 with FTS5, JSON and the
  session extension compiled in).
- **One file** `board.db` in WAL mode replaces `board.json`,
  `board.events.jsonl`, `board.events/`, `board.snapshots/` and `board.lock`.
  `KANCLI_FILE`, `-file` and the config `file` key keep working: a configured
  `.json` path maps to the `.db` next to it.
- **Commits are fsynced**: the database runs with `synchronous(FULL)`, so a
  save that returned success is on disk even if the machine loses power
  before the next WAL checkpoint.
- **Event sourcing stays.** Events are the source of truth; snapshots are a
  cache with retention; load is newest snapshot plus replay of the tail.
- **DuckDB stays for analytics**, fed by a temporary state file (unchanged)
  and a temporary `events.jsonl` exported from the database.
- **`board.FileVersion` stays 2**: the JSON shape of a board is unchanged.
  The store format is a separate number kept in the database (`format = 1`).
  For the user-facing notice the legacy directory counts as format 2 and the
  database as format 3.
- **Undo records changes, not state.** The in-app undo stack still holds a
  copy of the board in memory, but applying an undo emits only the events
  needed to get from the current board to the restored one.

## Schema

```sql
CREATE TABLE meta      (key TEXT PRIMARY KEY, value TEXT NOT NULL);
-- format: store format number (1); created: RFC3339
CREATE TABLE events    (
  seq   INTEGER PRIMARY KEY,   -- global, monotonically increasing
  v     INTEGER NOT NULL,      -- board.EventVersion at write time
  at    TEXT    NOT NULL,      -- RFC3339Nano, UTC
  board TEXT    NOT NULL,
  kind  TEXT    NOT NULL,
  task  INTEGER, from_col TEXT, to_col TEXT, idx INTEGER, text TEXT,
  data  BLOB,                  -- raw JSON payload, may be NULL
  actor TEXT
);
CREATE INDEX events_at ON events(at);
CREATE INDEX events_board_seq ON events(board, seq);
CREATE TABLE snapshots (
  seq   INTEGER PRIMARY KEY,   -- last event folded in; 0 = empty base
  at    TEXT    NOT NULL,      -- RFC3339Nano, UTC
  state BLOB    NOT NULL       -- gzip(compact JSON of board.File)
);
```

`board.Event` maps one-to-one onto an `events` row. Time is stored as UTC
RFC3339Nano text so `at` sorts and compares lexically.

## Operations

- **Open** (`store.New(path)` then `Load`): opens the database with
  `busy_timeout=3000`, `journal_mode=WAL`, `synchronous=NORMAL`, creates the
  schema if missing, refuses a `meta.format` greater than the one it knows
  ("written by a newer kancli"). If the database does not exist but a legacy
  `board.json` does, the importer runs first (below).
- **Load**: newest snapshot → gunzip → `board.Decode` → replay
  `events WHERE seq > snapshot.seq ORDER BY seq`. Records the tail count and
  `PRAGMA data_version`.
- **Save**: `BEGIN IMMEDIATE`; if `data_version` moved since the last
  load/save, another process wrote: replay their new events into the file
  first (the existing merge behaviour), then insert pending events with
  consecutive sequence numbers, commit. Compacts when the tail reaches 500
  events, or on the first save of a new database (which also writes the
  empty base snapshot at seq 0, as today).
- **Compact**: insert a snapshot row for the current `LastSeq`, then prune:
  keep seq 0, the newest 5, the newest per calendar day for the last 30 days,
  and the newest per ISO week before that.
- **LoadAsOf(t)**: newest snapshot with `at <= t`, then replay events with
  `seq > snapshot.seq AND at <= t`. Result is frozen (read-only) as today.
- **Events()**: all events by seq (used by `log`, `review`, stats).
  **BoardStats** keeps the incremental walker and only fetches events after
  the walker's position.
- **ChangedOnDisk / NeedReload**: `PRAGMA data_version` differs from the
  value seen at the last load or save.
- **TailEvents**: events since the newest snapshot (for the header).
- **Demo mode** (empty path): an in-memory database.
- **ExportEventsJSONL(path)**: writes every event as one JSON line, for
  DuckDB.

## Migration from the file store

On open, when `<base>.db` does not exist and `<base>.json` does:

1. Read every snapshot in `board.snapshots/` (any file version, through
   `board.DecodeVersion`) and insert it with its sequence number, gzip'd.
   If the directory is empty, decode `board.json` itself as the only
   snapshot. A version-1 file goes through `MigrateV1` and gets the
   bootstrapped history exactly as the file store did.
2. Read archived segments in order, then the tail, with the existing
   byte-accurate reader, and insert every event. Missing `v` becomes 1.
3. Insert `meta.format = 1`.
4. Move (not copy) `board.json`, `board.events.jsonl`, `board.events/` and
   `board.snapshots/` into `board.backups/v2/`, and remove `board.lock`.
   Moving means an older kancli cannot silently keep writing to files the
   new one no longer reads. An existing `board.backups/v2/` is left alone
   and the legacy files are moved into `board.backups/v2-<unix time>/`.
5. Report `Upgrade{From: 2, To: 3, Backup: dir}` so the CLI prints its
   notice.

The whole import runs inside one transaction on the new database; on error
the database file is removed and the legacy files are untouched.

## Undo

`Board.Replace(nb)` (called by the app's undo/redo) becomes a diff: for each
task id present in `nb` whose JSON differs from the current task, or absent
now, emit `task.reverted` with the task as data; for each task present now
but absent in `nb`, emit `task.deleted`; if name, description, columns or
`next_id` differ, emit `board.reverted` with the board minus tasks as data.
Then it swaps in `nb` as before. `Apply` handles the two new kinds:
`task.reverted` upserts the task by id (and rebuilds the index),
`board.reverted` replaces everything except `Tasks`. `board.restored` is
still understood for old logs. The in-memory undo stack additionally drops
its oldest entries when their total size exceeds 64 MB.

## Compatibility

Per `docs/compatibility.md`: this adds a store format, event kinds and
fields; it renames or removes nothing. Frozen fixtures `v1` and `v2` prove
the importer; a new `v3` fixture is a small `board.db` that proves the
database format loads. An older kancli opening a directory where the files
have been moved sees an empty board, which is why the notice names the
backup directory.

## Out of scope for this phase

Loading only live tasks, FTS5 search, per-device ids and sync. The schema
does not need to change for any of them.
