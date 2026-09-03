# Changelog

## Unreleased

### Added

- Goal boards: mark a board as goals (`kancli boards new Roadmap --goals`,
  `boards kind`, or `k` in the board picker). Links can point at tasks on
  other boards, written `work#12`, from the link prompt, `kancli link`, `#`
  mentions and the `parent:`/`blocks:`/`blockedby:` filters; a goal's
  progress counts its tickets across boards, and `g` jumps to a linked task
  on another board.
- Board descriptions: `e` in the board picker, `kancli boards describe`, and
  `--desc` on `kancli boards new`. Shown in the picker and in `kancli boards`.
- Safer upgrades: the first run on data from an older kancli copies the old
  files to `board.backups/vN/` and says so; events carry a format version
  and an older kancli refuses to open a newer log with advice to upgrade;
  renamed config keys keep working with a warning. Frozen fixtures of every
  released data format are loaded by the test suite.
- Columns from the command line: `kancli columns add | edit | rename |
  move | rm`, with completion of column names and palette colours.
- The full help (`?`) wraps to the terminal width, so the column keys and
  `?`/`q` are no longer cut off on narrower terminals.
- The command line is built on Cobra: grouped `kancli --help`, per-command
  help and examples, `kancli completion bash|zsh|fish|powershell` with
  completion of task ids, column and board names, and `--long` flags. The
  old single-dash spellings (`-json`, `-as-of`) still work.
- Task descriptions are Markdown, rendered with Glamour in the task view:
  headings, lists, emphasis, code and links.
- Cards slide to their new column when moved, driven by a Harmonica spring.
  `--no-animations` or `"no_animations": true` turns it off.
- Image attachments (png, jpg, gif) are previewed inline in the task view on
  terminals that speak the kitty graphics protocol (kitty, Ghostty).
  `"images": "off"` disables it, `"on"` forces it.
- Golden-file tests of every screen with teatest, and a property-based test
  (rapid) that replays random mutation sequences through the event log.

### Changed

- The board is now a single SQLite database, `board.db`. The first run
  imports your existing `board.json` and history and moves the old files to
  `board.backups/v2/`, printing where. Snapshot history is pruned instead of
  kept forever, `--as-of` is indexed, and undo writes only what changed.
  Requires nothing extra: the driver is pure Go.

### Fixed

- Replayed events now reproduce the live state exactly: an event carries the
  same timestamp the mutation wrote into the task, and links created from
  `#12` mentions are restored from their own events during replay.
- Deleting a column and moving its tasks to a column further right logged
  the wrong destination, so history and other processes saw the tasks in
  the wrong column. Found by the new property test.
- Undo wrote a copy of the whole board into the history; it now records
  only the tasks that changed.

- Links between tasks: blocks / blocked by, subtask / parent, relates to.
  Blocked marker on cards and in the header, subtask progress on parents,
  automatic links from `#12` mentions, a warning when finishing a task with
  open subtasks or blockers, `l`/`g`/`X` in the task view, `kancli link`
  and `kancli unlink`, and `blocked:`, `blocks:`, `blockedby:`, `parent:`
  and `has:` search terms.

- Event-sourced storage: every change is appended to `board.events.jsonl`;
  snapshots are written on quit, `ctrl+s`, `kancli compact` or every 500
  events, and archived segments keep the full history.
- `-as-of DATE` opens the board (or runs `list`, `show`, `stats`, `export`)
  as it was at that time, read-only.
- Stats (`S` in the app, `kancli stats`): cycle time, time per column,
  weekly throughput, work in progress per day, aging tasks and per-label
  figures, drawn with sparklines and bar charts.
- `kancli review` writes a Markdown review of the last week from the log.
- `kancli log` prints recent events in words or as JSON.
- DuckDB bridge: `kancli stats -q SQL` over `tasks`, `events`, `moves`,
  `column_stays` and `cycle_times` views; `kancli stats -sql` prints the
  view definitions; Parquet export of tasks or events.
- Relevance-ranked search results for free-text queries.
- Similar-task detection when adding a task and on the task page.
- Concurrent writers are merged instead of refused; the "file changed on
  disk" conflict mode is gone.
- Boards that predate the log get a bootstrapped history from their task
  timestamps.

- Task priority, due dates (with natural input like `fri` or `+3d`), labels,
  assignee, checklists, attachments, comments and a per-task activity log.
- User-visible task numbers (`#12`) on every card.
- Configurable columns: add, rename, recolour, reorder and delete columns,
  with optional WIP limits shown in the column header.
- Multiple boards in one file, with a board picker (`b`) and `-board` flag.
- Archive: archive tasks (`a`), archive every done task (`Z`), browse,
  restore or purge archived tasks (`z`).
- Search across all columns and fields with `/`, including `#id`, `@who`,
  `+label`, `p:high`, `due:today` and `col:name` filters.
- Sort modes (`s`): manual, priority, due, created, updated, title.
- Undo and redo (`u` / `U`) for every change.
- Task detail view (`enter`) with checklist toggling, comments, attachment
  opening and history.
- Multi-select with `space` for bulk move, archive and delete.
- Mouse support: click to focus and select, click again to open, wheel to
  scroll.
- Detection of external edits to the data file: automatic reload when there
  is nothing unsaved, and a refusal to overwrite otherwise (`R` reloads,
  `ctrl+s` overwrites).
- Non-interactive CLI: `add`, `list`, `show`, `move`, `done`, `archive`,
  `restore`, `rm`, `due` (with `-notify` desktop notifications), `export`
  and `import` (JSON, CSV, Markdown), `boards`, `columns`, `config`, `keys`.
- Overdue / due-today badge in the header.
- Config file with theme, ASCII borders, compact cards, default sort and key
  binding overrides. Themes: default, high-contrast, mono. `NO_COLOR` is
  respected.
- Automatic migration of version 1 board files.
- CI on Linux, macOS and Windows, golangci-lint, and GoReleaser releases with
  a `-version` flag.

### Changed

- The data file format is now version 2 (multiple boards, numeric task ids).
- `enter` opens the task; `e` edits it.
