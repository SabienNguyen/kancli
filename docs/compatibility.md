# Data compatibility

kancli keeps its board files in the data directory (`kancli/` under
`$XDG_DATA_HOME` or the OS config dir, or wherever `KANCLI_FILE` points),
and its settings in `config.json` under `$XDG_CONFIG_HOME` or the OS config
dir (or wherever `KANCLI_CONFIG` points):

| File | What it is | Version marker |
|---|---|---|
| `board.json` | snapshot of every board | `"version"` (`board.FileVersion`, currently 2) |
| `board.events.jsonl`, `board.events/*.jsonl` | append-only history, live tail and archived segments | `"v"` on every event (`board.EventVersion`, currently 1) |
| `board.snapshots/*.json` | every snapshot ever written, for `--as-of` | same as `board.json` |
| `config.json` | user settings | none; unknown keys are ignored, renamed keys are aliased |

## What a user gets on upgrade

- The first time a newer kancli opens data written by an older format it
  copies `board.json` and `board.events.jsonl` into
  `board.backups/vN/` (N = the old version) before writing anything, and
  prints one line on stderr saying so. An existing backup directory is
  never overwritten.
- Old snapshots under `board.snapshots/` are migrated on read, so
  `--as-of` keeps working across versions.
- A config key that was renamed keeps working under its old name and
  prints a warning naming the new one.

## What a user gets on downgrade

Running an older kancli on newer data fails before anything is written:

- a newer `board.json` version: "file was written by a newer kancli"
- an event kind or event version this build does not know: "… was
  written by a newer kancli; upgrade kancli …"

Nothing is modified in either case.

## Rules for changing a format (every PR that touches on-disk data)

1. **Never rename or remove** a JSON field or an event kind string. Add
   new ones. Old builds ignore unknown fields; new builds must accept old
   files with the field missing.
2. **Changing the meaning** of an existing field or of an event's data
   payload requires bumping the version: `board.FileVersion` for the
   snapshot, `board.EventVersion` for events. Write a migration in
   `internal/board/decode.go` (snapshot) or a version switch in
   `(*File).Apply` (events) that understands every earlier version.
3. **Freeze a fixture** of the format you are replacing under
   `internal/store/testdata/vN/` and add a row to
   `TestLoadsEveryReleasedFormat` in `internal/store/compat_test.go`.
   Fixtures are never edited afterwards.
4. **Renaming a config key** means adding a row to `renamedKeys` in
   `internal/config/config.go`. Rows are never deleted.
5. Describe the change under "Changed" in `CHANGELOG.md` with the user's
   view: what they will see, where the backup is.
