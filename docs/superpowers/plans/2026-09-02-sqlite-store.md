# SQLite Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the file store (JSON snapshot, JSONL log, snapshot and segment directories, flock) with one SQLite database per data file, import existing directories on first open, and make undo emit change-sized events instead of a board copy.

**Architecture:** See the spec. `internal/store` keeps its public surface (`New`, `Load`, `Save`, `Compact`, `LoadAsOf`, `Events`, `BoardStats`, `TailEvents`, `NeedReload`, `ChangedOnDisk`, `Enabled`, `Describe`, `Path`, `SetActor`, `Upgraded`, `DefaultPath`) so the UI and CLI change only where they touched file paths. The legacy reader survives inside an importer. `internal/board` gains two event kinds and a diffing `Replace`.

**Tech Stack:** Go, `modernc.org/sqlite` v1.58.0 (pure Go), `database/sql`, `compress/gzip`, existing test style. Run `go test -race ./...` and `PATH=$HOME/go/bin:$PATH make lint`.

**Spec:** `docs/superpowers/specs/2026-09-02-sqlite-store-design.md` — read it first; every task below refers to it.

## Global Constraints

- Executors run with `model: "opus"`, one task per dispatch; the main session reviews every diff and re-runs the tests itself.
- Never rename or remove a JSON field or event kind. Adding is fine (`docs/compatibility.md`).
- No cgo. `CGO_ENABLED=0 GOOS=windows go build ./...` must keep working after Task 2.
- Golden files under `internal/ui/testdata` must not change in this plan.
- Commit after every task. Do not push.

---

### Task 1: Undo emits change-sized events

**Files:**
- Modify: `internal/board/events.go` (kinds const block; `Apply`; `Describe`)
- Modify: `internal/board/data.go` (`Replace`, ~line 913)
- Modify: `internal/ui/model.go` (`snapshot`, ~line 263: cap the undo stack by bytes)
- Test: `internal/board/board_test.go`, `internal/board/replay_prop_test.go`, `internal/ui/model_test.go`

**Interfaces (verbatim):**
```go
// package board
EvTaskReverted  EventKind = "task.reverted"   // Data: the task as it was; upsert by id
EvBoardReverted EventKind = "board.reverted"  // Data: Board with Tasks omitted; replaces everything but tasks
func (b *Board) Replace(nb Board)              // unchanged signature; now emits a diff
```

- [ ] **Step 1: Failing board test** (append to board_test.go)
```go
func TestReplaceEmitsOnlyTheDifference(t *testing.T) {
	f := NewFile()
	f.Attach()
	b := f.Boards[0]
	t1, _ := b.AddTask(Task{Title: "one"})
	t2, _ := b.AddTask(Task{Title: "two"})
	before := *b // value copy = undo entry
	beforeJSON, _ := json.Marshal(before)
	_ = f.Pending()

	b.UpdateTask(t1.ID, func(t *Task) { t.Title = "one edited" }) // adapt to the real edit API
	b.DeleteTask(t2.ID)
	t3, _ := b.AddTask(Task{Title: "three"})
	b.AddColumn("Review", "", 0)
	_ = f.Pending()

	var restored Board
	json.Unmarshal(beforeJSON, &restored)
	b.Replace(restored)
	events := f.Pending()
	kinds := map[EventKind]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	if kinds[EvTaskReverted] != 2 || kinds[EvTaskDeleted] != 1 || kinds[EvBoardReverted] != 1 || len(events) != 4 {
		t.Fatalf("events = %v", kinds)
	}
	if b.Task(t3.ID) != nil || b.Task(t2.ID) == nil || b.Task(t1.ID).Title != "one edited" == true {
		t.Fatal("board not restored")
	}
	// Replay reproduces the restore from the events alone.
	replayed := NewFile()
	replayed.Boards[0].ID = b.ID
	// (re-create the pre-restore state by replaying everything from the start)
	// Simplest: replay ALL events emitted since NewFile onto a fresh file and compare JSON.
}
```
Write the test so it collects every event from the first `Pending()` onward (keep a slice, append each drain), replays them all onto `NewFile()` (with the same board id), and asserts `json.Marshal(replayed.Boards[0])` equals `json.Marshal(b)` field-for-field on Tasks, Columns, Name, Description and NextID. Fix the placeholder edit API to whatever `board` exposes (search `func (b *Board) UpdateTask` / `EditTask`). Also assert that a `Replace` with an identical board emits **zero** events.

- [ ] **Step 2: Run** `go test ./internal/board -run TestReplaceEmitsOnlyTheDifference` → FAIL (undefined kinds).

- [ ] **Step 3: Implement.**
  - Add the two kinds after `EvBoardRestored`.
  - `Replace(nb)`: build `cur := map[int]Task` from `b.Tasks` and `next := map[int]Task` from `nb.Tasks`. For each task in `nb.Tasks` (in order): if absent in `cur` or `!bytes.Equal(MustJSON(cur[id]), MustJSON(t))` → emit `Event{Kind: EvTaskReverted, Task: id, To: t.Column, Data: MustJSON(t)}`. For each task in `b.Tasks` absent from `next` → emit `Event{Kind: EvTaskDeleted, Task: id, From: t.Column}` (check what the existing delete emit looks like at the `EvTaskDeleted` emit site and match it). If `nb.Name != b.Name || nb.Description != b.Description || nb.NextID != b.NextID || !bytes.Equal(MustJSON(nb.Columns), MustJSON(b.Columns))` → emit `Event{Kind: EvBoardReverted, Data: MustJSON(boardMeta(nb))}` where `boardMeta` returns a copy with `Tasks = nil`. Emit BEFORE swapping (`emit` reads `b.rec`, which survives the swap anyway; keep the order: compute diff, swap, then emit using the new board's stamp — verify `emit` uses `b.stamp`/`b.now()`; emit all events after the swap so their `At` is consistent, as the current code does).
  - `Apply`: `case EvTaskReverted:` unmarshal into `Task`; if a task with that id exists replace it in place, else append; then `b.touch()` (check the name of the generation bump used elsewhere so the id index is rebuilt). `case EvBoardReverted:` unmarshal into `Board`, then set `b.Name, b.Description, b.Columns, b.NextID` from it, leave `b.Tasks`, `b.touch()`.
  - `Describe`: `task.reverted` → `"reverted task #%d"`, `board.reverted` → `"reverted board settings"`.
  - Property test: the existing `"snap"`/restore op (search `Replace(` in replay_prop_test.go) already exercises `Replace`; the test compares replayed state to live state, which now proves the diff path. Run it with `-rapid.checks=2000` once and paste the result.
  - `internal/ui/model.go` `snapshot()`: after appending, drop from the front while the sum of `len(e.board)` over the stack exceeds `64 << 20`. Add a small test in model_test.go that fakes a 40 MB entry twice and asserts the stack length is 1 (construct entries directly).

- [ ] **Step 4: Run** `go test -race ./...` and lint. Commit: `feat(board): undo emits per-task revert events instead of a board copy`.

---

### Task 2: SQLite store core

**Files:**
- Create: `internal/store/sqlite.go` (open, schema, load, save, compact, prune, as-of, events, change detection)
- Create: `internal/store/legacy.go` (move `readEventFile`, `readEventLine`, `isLastLine`, `lastSeq`, `snapshotSeq` and anything else the importer needs; this task only moves code so Task 3 can use it)
- Rewrite: `internal/store/store.go` (the `Store` type and public API now backed by `*sql.DB`)
- Modify: `go.mod` / `go.sum` (`go get modernc.org/sqlite@v1.58.0`; remove `github.com/gofrs/flock` once nothing imports it)
- Rewrite: `internal/store/store_test.go` (same test names and intent, expressed against the database; delete `TestTornTailIsRepairedBeforeAppend` and `TestStoreRepairsBadFiles` (file-specific) and replace them with `TestSaveMergesConcurrentWriters` and `TestNewerFormatIsRefused`)
- Modify: `internal/store/compat_test.go` (`TestReadEventFileCRLF` stays but targets `legacy.go`; the format-loading test is adapted in Task 3, so for THIS task mark `TestLoadsEveryReleasedFormat`, `TestLoadBacksUpOlderFormatOnce`, `TestLoadBacksUpEventLogToo` with `t.Skip("importer arrives in Task 3")` — the main session will check they are un-skipped in Task 3)

**Interfaces (verbatim, public surface kept):**
```go
func New(path string) *Store            // path "" = in-memory demo store; a ".json" path maps to ".db"
func DefaultPath() (string, error)      // now ends in board.db
func (s *Store) Path() string           // the .db path
func (s *Store) Enabled() bool
func (s *Store) SetActor(actor string)
func (s *Store) Describe() string
func (s *Store) Load() (*board.File, error)
func (s *Store) LoadAsOf(t time.Time) (*board.File, error)
func (s *Store) Save(f *board.File) error
func (s *Store) Compact(f *board.File) error
func (s *Store) TailEvents() int
func (s *Store) Events() ([]board.Event, error)
func (s *Store) BoardStats(b *board.Board, now time.Time, days int) (board.Stats, error)
func (s *Store) NeedReload() bool
func (s *Store) ChangedOnDisk() bool
func (s *Store) Upgraded() (Upgrade, bool)
func (s *Store) ExportEventsJSONL(path string) error   // new, for DuckDB
func (s *Store) Close() error                          // new; idempotent
const StoreFormat = 1
var ErrStale = errors.New(...)                          // keep
const CompactAfter = 500                               // keep
```
`LogPath`, `ArchiveDir`, `SnapDir`, `EventFiles` are deleted (Task 4 fixes the callers; until then keep thin stubs returning "" / nil so the tree compiles, and mark them `// Deprecated: removed in Task 4`).

**Behaviour to implement**, following the spec section "Operations" exactly. Details that matter:
- Open lazily on first use (`New` must not touch disk, because the CLI constructs a store for completion and demo paths). DSN: `file:<path>?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)`; in-memory: `file::memory:?cache=shared` plus `db.SetMaxOpenConns(1)`.
- Schema from the spec, created with `CREATE TABLE IF NOT EXISTS`. `meta.format` written on creation; on open, if `meta.format > StoreFormat` return `fmt.Errorf("%s was written by a newer kancli (store format %d)", path, n)`.
- Event ↔ row mapping: `at` as `e.At.UTC().Format(time.RFC3339Nano)`; read back with `time.Parse` then `.In(time.Local)` so `board.Event.At` behaves as it does today. `data` NULL when `len(e.Data)==0`. `v` written as `board.EventVersion` when 0.
- `Load`: newest snapshot row (`ORDER BY seq DESC LIMIT 1`); if none, `board.NewFile()`; gunzip + `board.Decode`; replay `SELECT ... FROM events WHERE seq > ? ORDER BY seq`; set `f.LastSeq`, `nextSeq`, `tailCount`; remember `PRAGMA data_version`. Keep the bootstrap behaviour: a freshly imported v1/legacy board with no events gets bootstrapped history exactly as before (move `bootstrap` over; it now inserts events and the seq-0 snapshot through the new primitives).
- `Save`: pending events → `BEGIN IMMEDIATE`; compare `data_version`; if moved, read events with `seq > lastSeenSeq`, `f.Replay` them (merge), advance `nextSeq`; insert pending with `seq = nextSeq++`; update `tailCount`; commit; record `data_version`. Then `Compact` when `tailCount >= CompactAfter` or when the database has no snapshot yet (first save, which also inserts the seq-0 empty base from `f.EmptyBase()`).
- `Compact`: refuse with `ErrStale` if `data_version` moved (same semantics as today); insert snapshot (`seq = f.LastSeq`, gzip of `json.Marshal(f)`) with `INSERT OR REPLACE`; prune per spec (keep 0, newest 5, newest per calendar day for 30 days, newest per ISO week older); `tailCount = 0`.
- `LoadAsOf`, `Events`, `BoardStats`, `TailEvents`, `ChangedOnDisk`, `NeedReload` per spec. `BoardStats` fetches only `seq > walker position` (look at how the walker exposes its position; if it does not, keep fetching all and note it).
- `ExportEventsJSONL`: stream rows to the file, one `json.Encoder` line each, in seq order.
- `Describe` unchanged except it shows the `.db` path.

- [ ] **Step 1: Failing tests.** Rewrite `store_test.go` so each existing test keeps its name and intent against the new API (e.g. `TestStoreLogCompactionAndAsOf` asserts `TailEvents()`, that a snapshot row exists after 500 events via a small unexported helper `s.snapshotSeqs()`, and that `LoadAsOf` returns the historical state). Add:
```go
func TestSaveMergesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	a, b := New(path), New(path)
	fa, _ := a.Load(); fb, _ := b.Load()
	fa.Active().AddTask(board.Task{Title: "from a"}) //nolint:errcheck
	if err := a.Save(fa); err != nil { t.Fatal(err) }
	fb.Active().AddTask(board.Task{Title: "from b"}) //nolint:errcheck
	if err := b.Save(fb); err != nil { t.Fatal(err) }          // must merge, not fail
	if !b.NeedReload() && len(fb.Active().Tasks) != 2 { t.Fatalf("b did not merge a's task: %d tasks", len(fb.Active().Tasks)) }
	f, _ := New(path).Load()
	if n := len(f.Active().Tasks); n != 2 { t.Fatalf("want 2 tasks after both saves, got %d", n) }
	if !a.ChangedOnDisk() { t.Error("a should see b's write") }
}
func TestNewerFormatIsRefused(t *testing.T) { /* open db, UPDATE meta SET value='99' WHERE key='format', New(path).Load() must error mentioning "newer kancli" */ }
func TestSnapshotRetention(t *testing.T) { /* insert 40 snapshot rows with dates spread over 90 days via the unexported insert helper, run prune, assert seq 0 kept, newest 5 kept, one per day for the last 30 days, one per week before */ }
func TestEventRoundTripPreservesTime(t *testing.T) { /* save an event with a non-UTC zone and nanoseconds, read back via Events(), assert Equal() and Location()==time.Local */ }
```
Adapt `TestSaveMergesConcurrentWriters` to the real merge semantics: after `b.Save(fb)` either `fb` already contains both tasks (merged in place) or `b.NeedReload()` is true and a reload shows both; assert one of those, then assert the fresh load shows 2.

- [ ] **Step 2: Run** `go test ./internal/store` → compile failures.

- [ ] **Step 3: Implement** per the behaviour list. Keep functions small: `open()`, `ensureSchema()`, `readSnapshot(seq int64 | newest)`, `readEvents(afterSeq, upTo time)`, `insertEvents(tx, events)`, `insertSnapshot(tx, f)`, `prune(tx, now)`, `dataVersion()`.

- [ ] **Step 4: Run** `go test -race ./internal/store`, then `go test -race ./...` (UI and CLI must still pass: they only use the public surface, plus the temporary stubs), lint, and `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./... && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...`.

- [ ] **Step 5: Commit**: `feat(store): SQLite-backed store with snapshot retention and version-based change detection`.

---

### Task 3: Import the legacy directory on first open

**Files:**
- Create: `internal/store/import.go`
- Modify: `internal/store/store.go` (`New`/open: detect legacy layout; `Upgraded`)
- Modify: `internal/store/compat_test.go` (un-skip and adapt the three tests; add the v3 fixture row)
- Create: `internal/store/testdata/v3/board.db` (generated by a helper, see Step 3)
- Modify: `internal/cli/root.go` (the notice text: "migrated your board to %s; the old files are in %s")

**Interfaces (verbatim):**
```go
// importLegacy builds a new database at s.path from the file-store layout
// rooted at base (base+".json" etc.), then moves the old files to backup.
func (s *Store) importLegacy(base string) (Upgrade, error)
func legacyExists(base string) bool   // base+".json" exists
```

- [ ] **Step 1: Un-skip and rewrite the compat tests.** `TestLoadsEveryReleasedFormat` rows: `v1` (dir with board.json), `v2`, `v2-crlf`, and new `v3` (dir with board.db). For directory fixtures the path passed to `New` is still `<tmp>/board.json` (the importer maps it to `board.db`), so the test's `New(path)` calls need no change; assert after the first load that `<tmp>/board.db` exists and `<tmp>/board.json` does not (moved), and that `Upgraded()` reports `From: 2` (or `1` for v1 — the importer reports the legacy file's version), `To: 3`, `Backup` under `<tmp>/board.backups/`. `TestLoadBacksUpOlderFormatOnce` becomes: import once → backup dir `board.backups/v1/` holds the original bytes; a second `New(path).Load()` reports no upgrade; the backup is untouched. `TestLoadBacksUpEventLogToo` becomes: after importing v2, `board.backups/v2/` contains `board.json`, `board.events.jsonl` and the `board.events/` and `board.snapshots/` directories.

- [ ] **Step 2: Run** → FAIL (no importer).

- [ ] **Step 3: Implement `importLegacy`** per the spec's migration section, using the legacy reader in `legacy.go` and `board.DecodeVersion`. Snapshot files in `board.snapshots/` are named `%012d.json` by seq; decode each and insert with that seq (the JSON's `last_seq` should match; prefer the filename). If the directory is absent or empty, the snapshot is `board.json` itself with its `last_seq` (0 for v1, which then triggers bootstrap on the first `Load` exactly as before — make sure bootstrap runs against the new database). Events: archived segments sorted by name, then the tail; assign `v = 1` when missing. All inside one transaction; on any error, close, `os.Remove(dbPath)` (and its `-wal`/`-shm`), return the error, leave legacy files alone. On success, move files into `board.backups/v<From>/` (or `v<From>-<unix>/` when it exists), remove `board.lock`, set `s.upgrade`.
  **Generate the v3 fixture**: write a throwaway test that copies `testdata/v2` to a temp dir, loads it (importing), saves one more task titled "in v3", and copies the resulting `board.db` (after `Close()`; make sure no `-wal` file is left by running `PRAGMA wal_checkpoint(TRUNCATE)` before close) into `internal/store/testdata/v3/board.db`. Delete the throwaway test. Add `*.db -text` to `.gitattributes`. The v3 row in the format test expects titles `write code`, `buy milk`, `in v3`.

- [ ] **Step 4: CLI notice.** In `internal/cli/root.go` the existing notice prints "upgraded your board from format v%d to v%d". Change it to: `kancli: moved your board into %s (format v%d → v%d); the old files are in %s` using `e.Store.Path()`.

- [ ] **Step 5: Run** `go test -race ./...`, lint. Commit: `feat(store): import the file-store directory into board.db on first open`.

---

### Task 4: Callers, DuckDB export, dead code

**Files:**
- Modify: `internal/cli/cli.go` (`stats -q` / `-sql` paths: replace `c.env.Store.EventFiles()` with an exported temp file; `compact` output; anything printing log paths)
- Modify: `internal/cli/env.go` (path handling: a `.json` file/config value still works; `DefaultPath` now `.db`)
- Modify: `internal/ui/model.go` (uses of `Describe`, `TailEvents`, `ChangedOnDisk` unchanged; remove any path-specific text)
- Modify: `internal/store/duck.go` (`EventFiles` deleted; `WriteStateFile` unchanged; add `WriteEventsFile(s *Store) (string, func(), error)`)
- Delete: the deprecated stubs from Task 2, `flock` from go.mod, any now-unused code (`writeAtomic`, `exists` if unused, etc.). `go vet` and `golangci-lint` (unused) will tell you.
- Test: `internal/cli/cli_test.go` (the DuckDB path is only exercised when the binary exists; keep the existing missing-binary test), `internal/store/store_test.go` `TestSQLViews` (feed it the exported JSONL)

- [ ] **Step 1**: grep for `EventFiles|LogPath|ArchiveDir|SnapDir|board.events|board.snapshots|\.jsonl` across `internal/cli internal/ui README.md docs/compatibility.md` and fix every hit (docs are Task 5; code here).
- [ ] **Step 2**: `WriteEventsFile` = `os.CreateTemp("", "kancli-events-*.jsonl")` + `s.ExportEventsJSONL`. Both CLI call sites (`cli.go` ~745 and ~92) create it next to the state file and defer both cleanups.
- [ ] **Step 3**: `go test -race ./...`, lint, `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`. If the `duckdb` binary is installed locally (`command -v duckdb`), run `kancli stats -q "select count(*) from events"` against a temp board and paste the output; if not, say so.
- [ ] **Step 4**: Commit: `refactor: point the CLI and DuckDB bridge at the SQLite store; drop the file-store code`.

---

### Task 5: Docs and compatibility policy

**Files:** `docs/compatibility.md`, `README.md` (Storage and files section, Upgrading section), `CHANGELOG.md`.

- [ ] **Step 1: compatibility.md.** Replace the file table with: `board.db` (SQLite; tables `events`, `snapshots`, `meta`; `meta.format` = store format, currently 1); `config.json` unchanged. Describe: import of the old directory on first open and where the backup lands; that an older kancli sees an empty board afterwards; snapshot retention; that `board.FileVersion` (JSON shape) and `board.EventVersion` are unchanged; new event kinds `task.reverted`, `board.reverted`; add rule: "bumping the store format means adding a migration in `internal/store/import.go` or a schema migration keyed on `meta.format`, plus a `testdata/vN` fixture."
- [ ] **Step 2: README.** Storage section: one file, WAL, what `kancli compact` does now, how to back up (copy `board.db`; or `kancli export`), how to inspect (`sqlite3 board.db` / Datasette), that JSONL export remains via `kancli export` and the DuckDB bridge. Upgrading section: the first run moves the old files to `board.backups/v2/` and prints where.
- [ ] **Step 3: CHANGELOG** under `### Changed`: "The data file is now a single SQLite database, `board.db`. The first run imports your existing `board.json` and history and moves the old files to `board.backups/`. Snapshot history is pruned instead of kept forever, `--as-of` is indexed, and undo writes only what changed. Requires nothing extra: the driver is pure Go."
- [ ] **Step 4**: Commit: `docs: SQLite store, migration and updated compatibility policy`.
