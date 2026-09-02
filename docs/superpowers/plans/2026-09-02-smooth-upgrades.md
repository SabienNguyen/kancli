# Smooth Upgrades Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A user who already has kancli installed can upgrade (or accidentally downgrade) without losing data, and every future release is forced to keep loading the data written by earlier releases.

**Architecture:** Three on-disk artefacts exist: the snapshot `board.json` (versioned, `board.FileVersion`), the append-only log `board.events.jsonl` plus its archived segments and snapshot copies (unversioned today), and `config.json` (unversioned). This plan (1) freezes a fixture of every released format under `internal/store/testdata` and loads them in a test, (2) makes the store take a one-time backup before it first touches data written by an older format and tells the user, (3) gives events a format version and a typed "written by a newer kancli" error so downgrades fail loudly instead of corrupting, (4) lets config keys be renamed with a warning instead of silently dropping the user's setting, and (5) writes the compatibility policy down so future plans follow it.

**Tech Stack:** Go 1.2x, standard library `encoding/json`, existing test style in `internal/store/store_test.go` and `internal/board/board_test.go`. Run tests with `go test -race ./...`, lint with `make lint`.

**Spec:** No separate spec. The audit that motivates this plan is summarised in the Global Constraints below.

## Global Constraints

- Executors run with `model: "opus"`, one task per dispatch; the main session reviews every diff and re-runs the tests itself.
- Never change the meaning of an existing JSON field name or event kind string. Add fields; do not rename or remove.
- `board.FileVersion` stays `2` in this plan. Nothing here bumps a format version.
- All new user-facing text goes to stderr via the `cli` package, never to stdout (stdout carries `--json` output and is golden-tested).
- `go test -race ./...` and `make lint` must pass after every task. Do not edit files under `internal/ui/testdata` (golden files).
- Commit after every task with a conventional message. Do not push.

---

### Task 1: Frozen fixtures of every released format

**Files:**
- Create: `internal/store/testdata/v1/board.json`
- Create: `internal/store/testdata/v2/board.json`
- Create: `internal/store/testdata/v2/board.events.jsonl`
- Create: `internal/store/testdata/v2/board.events/000000000001-000000000002.jsonl`
- Create: `internal/store/compat_test.go`

**Interfaces:**
- Consumes: `store.New(path string) *Store`, `(*Store).Load() (*board.File, error)`, `(*Store).Save(*board.File) error`, `(*board.File).Active() *board.Board`, `board.Board.Tasks []board.Task`.
- Produces: the fixture directories; later tasks and every future release add a new `testdata/vN` directory and a row in the test table.

- [ ] **Step 1: Create the v1 fixture** (the single-board format written by the first rewrite; string ids, `status` strings)

`internal/store/testdata/v1/board.json`:
```json
{
  "version": 1,
  "tasks": [
    {
      "id": "a1b2c3",
      "status": "todo",
      "title": "buy milk",
      "description": "strawberry milk",
      "created_at": "2025-06-01T09:00:00Z",
      "updated_at": "2025-06-01T09:00:00Z"
    },
    {
      "id": "d4e5f6",
      "status": "in_progress",
      "title": "write code",
      "description": "don't worry, it's Go",
      "created_at": "2025-06-02T09:00:00Z",
      "updated_at": "2025-06-03T10:30:00Z"
    },
    {
      "id": "g7h8i9",
      "status": "done",
      "title": "stay cool",
      "created_at": "2025-06-03T09:00:00Z",
      "updated_at": "2025-06-04T09:00:00Z"
    }
  ]
}
```

- [ ] **Step 2: Create the v2 fixture** (multi-board snapshot plus an archived segment and a live tail)

`internal/store/testdata/v2/board.json`:
```json
{
  "version": 2,
  "active_board": "main",
  "boards": [
    {
      "id": "main",
      "name": "Main",
      "columns": [
        {"id": "todo", "name": "To Do", "color": "62"},
        {"id": "in_progress", "name": "In Progress", "color": "214"},
        {"id": "done", "name": "Done", "color": "35"}
      ],
      "tasks": [
        {
          "id": 1,
          "column": "in_progress",
          "title": "write code",
          "description": "see #2",
          "priority": "high",
          "labels": ["dev"],
          "links": [{"kind": "relates", "task": 2}],
          "created_at": "2026-08-01T12:00:00+10:00",
          "updated_at": "2026-08-02T12:00:00+10:00"
        },
        {
          "id": 2,
          "column": "todo",
          "title": "buy milk",
          "created_at": "2026-08-01T12:05:00+10:00",
          "updated_at": "2026-08-01T12:05:00+10:00"
        }
      ],
      "next_id": 3
    }
  ],
  "last_seq": 2,
  "snapshot_at": "2026-08-02T12:00:00+10:00"
}
```

`internal/store/testdata/v2/board.events/000000000001-000000000002.jsonl` (already folded into the snapshot; must be ignored by Load and readable by history):
```
{"seq":1,"at":"2026-08-01T12:00:00+10:00","board":"main","kind":"task.created","task":1,"to":"todo","data":{"id":1,"column":"todo","title":"write code","description":"see #2","priority":"high","labels":["dev"],"created_at":"2026-08-01T12:00:00+10:00","updated_at":"2026-08-01T12:00:00+10:00"},"actor":"ui"}
{"seq":2,"at":"2026-08-01T12:05:00+10:00","board":"main","kind":"task.created","task":2,"to":"todo","data":{"id":2,"column":"todo","title":"buy milk","created_at":"2026-08-01T12:05:00+10:00","updated_at":"2026-08-01T12:05:00+10:00"},"actor":"ui"}
```

`internal/store/testdata/v2/board.events.jsonl` (live tail, replayed on top of the snapshot):
```
{"seq":3,"at":"2026-08-03T09:00:00+10:00","board":"main","kind":"task.moved","task":2,"from":"todo","to":"in_progress","actor":"cli"}
{"seq":4,"at":"2026-08-03T09:01:00+10:00","board":"main","kind":"comment.added","task":2,"text":"oat milk","actor":"cli"}
```

Check the exact `data` payload shape of `task.created` and the fields of `task.moved` / `comment.added` against `internal/board/events.go` (search for `EvTaskCreated`, `EvTaskMoved`, `EvCommentAdded` in the emit sites and in `(*File).Apply`). If the real shape differs, fix the fixture, not the code.

- [ ] **Step 3: Write the failing compatibility test**

`internal/store/compat_test.go`:
```go
package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/SabienNguyen/kancli/internal/board"
)

// Every released on-disk format has a frozen copy under testdata/vN. This
// test proves the current build still opens each one, can save on top of it
// and reads the same state back. Add a directory and a row for every new
// format version; never edit an existing fixture.
func TestLoadsEveryReleasedFormat(t *testing.T) {
	cases := []struct {
		dir      string
		titles   []string // active board, in Tasks order
		columns  []string // column of each task, same order
		comments int      // comments on the last task
	}{
		{"v1", []string{"buy milk", "write code", "stay cool"}, []string{"todo", "in_progress", "done"}, 0},
		{"v2", []string{"write code", "buy milk"}, []string{"in_progress", "in_progress"}, 1},
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			dir := t.TempDir()
			copyTree(t, filepath.Join("testdata", c.dir), dir)
			path := filepath.Join(dir, "board.json")

			st := New(path)
			f, err := st.Load()
			if err != nil {
				t.Fatalf("load %s: %v", c.dir, err)
			}
			check := func(f *board.File, when string) {
				t.Helper()
				b := f.Active()
				if len(b.Tasks) != len(c.titles) {
					t.Fatalf("%s: %d tasks, want %d", when, len(b.Tasks), len(c.titles))
				}
				for i, tk := range b.Tasks {
					if tk.Title != c.titles[i] || tk.Column != c.columns[i] {
						t.Errorf("%s: task %d = %q in %q, want %q in %q", when, i, tk.Title, tk.Column, c.titles[i], c.columns[i])
					}
				}
				if got := len(b.Tasks[len(b.Tasks)-1].Comments); got != c.comments {
					t.Errorf("%s: last task has %d comments, want %d", when, got, c.comments)
				}
			}
			check(f, "after load")

			// A mutation and a save must work on top of the old data.
			if _, err := f.Active().AddTask(board.Task{Title: "added after upgrade"}); err != nil {
				t.Fatal(err)
			}
			if err := st.Save(f); err != nil {
				t.Fatalf("save on top of %s: %v", c.dir, err)
			}
			c.titles = append(c.titles, "added after upgrade")
			c.columns = append(c.columns, f.Active().Columns[0].ID)
			c.comments = 0

			again, err := New(path).Load()
			if err != nil {
				t.Fatalf("reload %s: %v", c.dir, err)
			}
			check(again, "after save and reload")
			if again.Version != board.FileVersion {
				t.Errorf("saved version = %d, want %d", again.Version, board.FileVersion)
			}
		})
	}
}

// copyTree copies the fixture directory into dst so the test never writes
// into testdata.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test -race ./internal/store -run TestLoadsEveryReleasedFormat -v`
Expected: PASS for both rows. If a row fails because the fixture's JSON does not match what the code emits (for example the `task.created` data payload), correct the fixture until it matches what the current code would have written. If a row fails for any other reason, stop and report it: that is a real compatibility bug and the main session decides.

- [ ] **Step 5: Run everything and commit**

Run: `go test -race ./... && make lint`
```bash
git add internal/store/testdata internal/store/compat_test.go
git commit -m "test: freeze v1 and v2 data fixtures and load them on every run"
```

---

### Task 2: Back up before the first write on top of an older format, and say so

**Files:**
- Modify: `internal/board/decode.go` (`Decode`, lines 11-32)
- Modify: `internal/store/store.go` (`Store` struct ~line 60-80, `Load` ~line 165, `readSnapshot` ~line 220, `LoadAsOf` call site ~line 289)
- Modify: `internal/cli/root.go` (after `c.env = e`, ~line 176)
- Test: `internal/board/board_test.go` (append), `internal/store/compat_test.go` (append)

**Interfaces:**
- Consumes: `board.Decode`, `Store.readSnapshot`, fixtures from Task 1.
- Produces, verbatim:
  ```go
  // package board
  // DecodeVersion is Decode plus the version number the bytes were written
  // with (0 or 1 for the original format). Callers use it to notice an
  // upgrade before writing anything.
  func DecodeVersion(data []byte) (f *File, version int, err error)

  // package store
  // Upgrade describes a data directory that an older kancli wrote and this
  // one has just opened.
  type Upgrade struct {
  	From, To int
  	Backup   string // directory holding a copy of the old files
  }
  // Upgraded reports the upgrade Load performed, if any.
  func (s *Store) Upgraded() (Upgrade, bool)
  ```

- [ ] **Step 1: Failing test for `DecodeVersion`** (append to `internal/board/board_test.go`)

```go
func TestDecodeVersionReportsSource(t *testing.T) {
	v1 := []byte(`{"version":1,"tasks":[{"id":"x","status":"done","title":"t","created_at":"2025-06-01T09:00:00Z","updated_at":"2025-06-01T09:00:00Z"}]}`)
	f, ver, err := DecodeVersion(v1)
	if err != nil || ver != 1 || f.Version != FileVersion || len(f.Boards[0].Tasks) != 1 {
		t.Fatalf("v1: ver=%d f.Version=%d tasks=%d err=%v", ver, f.Version, len(f.Boards[0].Tasks), err)
	}
	noVersion := []byte(`{"tasks":[]}`)
	if _, ver, err := DecodeVersion(noVersion); err != nil || ver != 0 {
		t.Fatalf("unversioned: ver=%d err=%v", ver, err)
	}
	cur := NewFile()
	data, _ := json.Marshal(cur)
	if _, ver, err := DecodeVersion(data); err != nil || ver != FileVersion {
		t.Fatalf("current: ver=%d err=%v", ver, err)
	}
	if _, _, err := DecodeVersion([]byte(`{"version":99,"boards":[]}`)); err == nil {
		t.Fatal("newer file must be refused")
	}
}
```

- [ ] **Step 2: Run it**: `go test ./internal/board -run TestDecodeVersionReportsSource` → FAIL, `undefined: DecodeVersion`.

- [ ] **Step 3: Implement** in `internal/board/decode.go`. Replace `Decode` with:

```go
// Decode parses any supported file version.
func Decode(data []byte) (*File, error) {
	f, _, err := DecodeVersion(data)
	return f, err
}

// DecodeVersion is Decode plus the version number the bytes were written
// with (0 or 1 for the original format). Callers use it to notice an
// upgrade before writing anything.
func DecodeVersion(data []byte) (f *File, version int, err error) {
	var probe struct {
		Version int             `json:"version"`
		Boards  json.RawMessage `json:"boards"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, 0, err
	}
	switch {
	case probe.Version > FileVersion:
		return nil, probe.Version, fmt.Errorf("file was written by a newer kancli (version %d)", probe.Version)
	case probe.Version <= 1 && len(probe.Boards) == 0:
		// The original single-board format (or a file with no boards at all).
		f, err := MigrateV1(data)
		return f, probe.Version, err
	}
	f = &File{}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, probe.Version, err
	}
	NormalizeFile(f)
	return f, probe.Version, nil
}
```

- [ ] **Step 4: Run it**: `go test ./internal/board` → PASS.

- [ ] **Step 5: Failing store test** (append to `internal/store/compat_test.go`)

```go
func TestLoadBacksUpOlderFormatOnce(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v1"), dir)
	path := filepath.Join(dir, "board.json")
	original, _ := os.ReadFile(path)

	st := New(path)
	if _, err := st.Load(); err != nil {
		t.Fatal(err)
	}
	up, ok := st.Upgraded()
	if !ok || up.From != 1 || up.To != board.FileVersion {
		t.Fatalf("Upgraded() = %+v, %v", up, ok)
	}
	if up.Backup != filepath.Join(dir, "board.backups", "v1") {
		t.Errorf("backup dir = %q", up.Backup)
	}
	got, err := os.ReadFile(filepath.Join(up.Backup, "board.json"))
	if err != nil || string(got) != string(original) {
		t.Fatalf("backup is not a byte copy of the original: %v", err)
	}

	// The second open of an already-current file reports nothing and leaves
	// the backup alone.
	f, _ := st.Load()
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	st2 := New(path)
	if _, err := st2.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := st2.Upgraded(); ok {
		t.Error("a current-format file must not report an upgrade")
	}
	got2, _ := os.ReadFile(filepath.Join(up.Backup, "board.json"))
	if string(got2) != string(original) {
		t.Error("backup was overwritten")
	}
}

func TestLoadBacksUpEventLogToo(t *testing.T) {
	// Simulate a future upgrade: a v2 directory whose snapshot claims to be
	// older than the current format. Both the snapshot and the live log are
	// copied.
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v2"), dir)
	path := filepath.Join(dir, "board.json")
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), `"version": 2`, `"version": 1`, 1))
	// version 1 with boards present is decoded as the current format but is
	// still reported as an upgrade from 1.
	os.WriteFile(path, data, 0o644) //nolint:errcheck // test data

	st := New(path)
	if _, err := st.Load(); err != nil {
		t.Fatal(err)
	}
	up, ok := st.Upgraded()
	if !ok {
		t.Fatal("expected an upgrade")
	}
	for _, name := range []string{"board.json", "board.events.jsonl"} {
		if _, err := os.Stat(filepath.Join(up.Backup, name)); err != nil {
			t.Errorf("%s not backed up: %v", name, err)
		}
	}
}
```
Add `"strings"` to the test file's imports.

- [ ] **Step 6: Run it**: `go test ./internal/store -run 'TestLoadBacksUp'` → FAIL, `st.Upgraded undefined`.

- [ ] **Step 7: Implement in `internal/store/store.go`**

Add to the `Store` struct: `upgrade *Upgrade` and a `backupDir string` set in `New` as `base + ".backups"` next to `lockPath`.

Add the type and accessor:
```go
// Upgrade describes a data directory that an older kancli wrote and this
// one has just opened.
type Upgrade struct {
	From, To int
	Backup   string // directory holding a copy of the old files
}

// Upgraded reports the upgrade Load performed, if any.
func (s *Store) Upgraded() (Upgrade, bool) {
	if s.upgrade == nil {
		return Upgrade{}, false
	}
	return *s.upgrade, true
}
```

Change `readSnapshot` to return the version as a fourth value: `func (s *Store) readSnapshot(path string) (f *board.File, fresh bool, version int, err error)`; it calls `board.DecodeVersion` and passes the version through (return `board.FileVersion` for the not-exists case). Update the `LoadAsOf` call site to discard it.

In `Load`, right after `f, fresh, version, err := s.readSnapshot(s.path)` succeeds and before `readEventFile`:
```go
	if version < board.FileVersion && exists(s.path) {
		if err := s.backupOld(version); err != nil {
			return nil, err
		}
	}
```
and add:
```go
// backupOld copies the snapshot and the live log as they were before this
// kancli touches them, once per source version. Nothing is overwritten:
// a directory that already exists is left alone.
func (s *Store) backupOld(from int) error {
	dir := filepath.Join(s.backupDir, fmt.Sprintf("v%d", from))
	s.upgrade = &Upgrade{From: from, To: board.FileVersion, Backup: dir}
	if exists(dir) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup %s: %w", dir, err)
	}
	for _, src := range []string{s.path, s.logPath} {
		data, err := os.ReadFile(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("back up %s: %w", src, err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(src)), data, 0o644); err != nil {
			return fmt.Errorf("back up %s: %w", src, err)
		}
	}
	return nil
}
```
Note `TestLoadBacksUpOlderFormatOnce` expects `Upgraded()` to be false on a fresh Store over a current-format file, which the `version < FileVersion` guard gives; the first Store keeps reporting the upgrade for its lifetime, which is what the CLI wants.

- [ ] **Step 8: Run the store tests**: `go test -race ./internal/store` → PASS.

- [ ] **Step 9: Tell the user** in `internal/cli/root.go`, immediately after `c.env = e`:
```go
	if up, ok := e.Store.Upgraded(); ok {
		fmt.Fprintf(c.stderr, "kancli: upgraded your board from format v%d to v%d; the old files are in %s\n", up.From, up.To, up.Backup)
	}
```
Check that `fmt` is imported in root.go (it is, `fmt.Fprintln` is used at line ~598).

- [ ] **Step 10: Run everything and commit**

Run: `go test -race ./... && make lint`
```bash
git add internal/board/decode.go internal/board/board_test.go internal/store/store.go internal/store/compat_test.go internal/cli/root.go
git commit -m "feat: back up older-format data before the first write and say so"
```

---

### Task 3: Event format version and a loud "newer kancli" error

**Files:**
- Modify: `internal/board/events.go` (`Event` struct line 42, `Apply` line 182, `default:` case line 299)
- Modify: `internal/store/store.go` (`append` ~line 496 where events are marshalled, `Load` ~line 181 where the replay error is wrapped)
- Test: `internal/board/board_test.go` (append), `internal/store/compat_test.go` (append)

**Interfaces:**
- Produces, verbatim:
  ```go
  // package board
  // EventVersion is written into every event as "v". Bump it when the
  // meaning of an existing kind or its data payload changes; a build
  // refuses to replay events with a higher version than it knows.
  const EventVersion = 1
  // Event gains the field:
  V int `json:"v,omitempty"`
  // ErrNewerEvents wraps every replay failure caused by data this build
  // does not understand, so callers can word the advice.
  var ErrNewerEvents = errors.New("written by a newer kancli")
  ```

- [ ] **Step 1: Failing board test** (append to `internal/board/board_test.go`)

```go
func TestReplayRefusesNewerEvents(t *testing.T) {
	f := NewFile()
	b := f.Boards[0]
	unknown := Event{Seq: 1, Board: b.ID, Kind: "task.teleported", Task: 1}
	err := f.Replay([]Event{unknown})
	if !errors.Is(err, ErrNewerEvents) {
		t.Fatalf("unknown kind: err = %v, want ErrNewerEvents", err)
	}
	if !strings.Contains(err.Error(), "task.teleported") {
		t.Errorf("message should name the kind: %v", err)
	}
	future := Event{Seq: 1, Board: b.ID, Kind: EvTaskDeleted, Task: 1, V: EventVersion + 1}
	err = f.Replay([]Event{future})
	if !errors.Is(err, ErrNewerEvents) {
		t.Fatalf("newer v: err = %v, want ErrNewerEvents", err)
	}
	// The version tag never changes what a known event does.
	tk, _ := b.AddTask(Task{Title: "x"})
	f.Pending() // drain
	current := Event{Seq: 5, Board: b.ID, Kind: EvTaskDeleted, Task: tk.ID, V: EventVersion}
	if err := f.Replay([]Event{current}); err != nil {
		t.Fatal(err)
	}
	if b.Task(tk.ID) != nil {
		t.Error("v-tagged delete was not applied")
	}
}
```
Add `"errors"` and `"strings"` to the imports if missing. If `b.Task(id)` or `f.Pending()` are named differently, use the real names from `internal/board` (search for `func (b *Board) Task(` and `func (f *File) Pending(`), and note the change in your report.

- [ ] **Step 2: Run it**: `go test ./internal/board -run TestReplayRefusesNewerEvents` → FAIL (undefined `ErrNewerEvents`, `EventVersion`, field `V`).

- [ ] **Step 3: Implement in `internal/board/events.go`**

Above `type Event struct`:
```go
// EventVersion is written into every event as "v". Bump it when the
// meaning of an existing kind or its data payload changes; a build
// refuses to replay events with a higher version than it knows. Events
// without "v" (written before this field existed) are version 1.
const EventVersion = 1

// ErrNewerEvents wraps every replay failure caused by data this build
// does not understand, so callers can word the advice.
var ErrNewerEvents = errors.New("written by a newer kancli")
```
Add `V int \`json:"v,omitempty"\`` as the first field of `Event`, and `"errors"` to the imports.

At the top of `Apply`, before `f.Attach()`:
```go
	if e.V > EventVersion {
		return fmt.Errorf("%w: event %d (%s) is format v%d, this build reads v%d", ErrNewerEvents, e.Seq, e.Kind, e.V, EventVersion)
	}
```
Replace the `default:` case body with:
```go
		return fmt.Errorf("%w: unknown event kind %q at seq %d", ErrNewerEvents, e.Kind, e.Seq)
```

- [ ] **Step 4: Run it**: `go test ./internal/board` → PASS (the existing unknown-kind assertion in `board_test.go` still passes because it only checks for a non-nil error).

- [ ] **Step 5: Failing store test** (append to `internal/store/compat_test.go`)

```go
func TestStoreWritesEventVersionAndRefusesNewer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.json")
	st := New(path)
	f, _ := st.Load()
	f.Active().AddTask(board.Task{Title: "x"}) //nolint:errcheck // test data
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	f.Active().AddTask(board.Task{Title: "y"}) //nolint:errcheck // test data
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(st.LogPath())
	if !strings.Contains(string(log), `"v":1`) {
		t.Fatalf("events are not version-tagged:\n%s", log)
	}

	// A future kancli appends something this build cannot read.
	fh, _ := os.OpenFile(st.LogPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(`{"v":1,"seq":99,"at":"2030-01-01T00:00:00Z","board":"main","kind":"task.teleported","task":1}` + "\n") //nolint:errcheck // test data
	fh.Close()
	_, err := New(path).Load()
	if !errors.Is(err, board.ErrNewerEvents) {
		t.Fatalf("err = %v, want ErrNewerEvents", err)
	}
	for _, want := range []string{"task.teleported", "upgrade kancli", st.LogPath()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}
```
Add `"errors"` to imports if missing.

- [ ] **Step 6: Run it**: `go test ./internal/store -run TestStoreWritesEventVersionAndRefusesNewer` → FAIL on the `"v":1` check.

- [ ] **Step 7: Implement in `internal/store/store.go`**

In `append`, in the loop that marshals each event before writing it to the buffered writer, set the version first:
```go
		if e.V == 0 {
			e.V = board.EventVersion
		}
```
(`e` must be the loop copy that gets marshalled; if the loop ranges by value and marshals `e`, this is enough.)

In `Load`, replace the replay error wrap with:
```go
	if err := f.Replay(events); err != nil {
		return nil, replayError(s.logPath, err)
	}
```
and add:
```go
// replayError words a replay failure. Data from a newer build gets advice
// instead of a bare parse error.
func replayError(path string, err error) error {
	if errors.Is(err, board.ErrNewerEvents) {
		return fmt.Errorf("%s was %w; upgrade kancli to the version that wrote it (or newer) and try again: %v",
			path, board.ErrNewerEvents, err)
	}
	return fmt.Errorf("replay %s: %w", path, err)
}
```
Apply the same `replayError` to the replay call inside `LoadAsOf` / the archived-segment replay (search for `.Replay(` in store.go) so history views give the same advice.

- [ ] **Step 8: Run everything and commit**

Run: `go test -race ./... && make lint`
```bash
git add internal/board/events.go internal/board/board_test.go internal/store/store.go internal/store/compat_test.go
git commit -m "feat: version-tag events and refuse logs from a newer kancli with advice"
```

---

### Task 4: Renamed config keys keep working, with a warning

**Files:**
- Modify: `internal/config/config.go` (`Config` struct line 14, `Load` line 56)
- Modify: `internal/cli/root.go` (next to the upgrade notice from Task 2)
- Test: `internal/config/config_test.go` (create if absent, else append)

**Interfaces:**
- Produces, verbatim:
  ```go
  // package config
  // Config gains:
  Warnings []string `json:"-"` // advice about the file, for stderr
  // renamedKeys maps a key from an older release to its current name. A
  // file that still uses the old key is read as if it used the new one and
  // gets a warning. Add a row here whenever a key is renamed; never delete
  // rows.
  var renamedKeys = map[string]string{}
  ```

- [ ] **Step 1: Failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHonoursRenamedKeys(t *testing.T) {
	renamedKeys["compact_cards"] = "compact"
	defer delete(renamedKeys, "compact_cards")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"compact_cards": true, "theme": "mono"}`), 0o644) //nolint:errcheck // test data

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Compact || c.Theme != "mono" {
		t.Fatalf("old key not honoured: %+v", c)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], `"compact_cards"`) || !strings.Contains(c.Warnings[0], `"compact"`) {
		t.Fatalf("warnings = %v", c.Warnings)
	}

	// The new key wins when both are present, and there is still a warning
	// about the stale one.
	os.WriteFile(path, []byte(`{"compact_cards": true, "compact": false}`), 0o644) //nolint:errcheck // test data
	c, err = Load(path)
	if err != nil || c.Compact || len(c.Warnings) != 1 {
		t.Fatalf("both keys: %+v err=%v", c, err)
	}

	// Unknown keys are still ignored silently (forward compatibility).
	os.WriteFile(path, []byte(`{"from_the_future": 1}`), 0o644) //nolint:errcheck // test data
	c, err = Load(path)
	if err != nil || len(c.Warnings) != 0 {
		t.Fatalf("unknown key: %+v err=%v", c, err)
	}
}
```

- [ ] **Step 2: Run it**: `go test ./internal/config -run TestLoadHonoursRenamedKeys` → FAIL (undefined `renamedKeys`, `Warnings`).

- [ ] **Step 3: Implement in `internal/config/config.go`**

Add the `Warnings` field and `renamedKeys` var as pinned above. In `Load`, replace the single `json.Unmarshal(data, &c)` with:
```go
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	for old, now := range renamedKeys {
		v, ok := raw[old]
		if !ok {
			continue
		}
		if _, exists := raw[now]; !exists {
			raw[now] = v
		}
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s: %q is now called %q; rename it", path, old, now))
	}
	fixed, err := json.Marshal(raw)
	if err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := json.Unmarshal(fixed, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
```
Sort `c.Warnings` before returning (`sort.Strings`) so output is deterministic across map iteration.

- [ ] **Step 4: Run it**: `go test ./internal/config` → PASS.

- [ ] **Step 5: Print the warnings** in `internal/cli/root.go`, right after the upgrade notice added in Task 2:
```go
	for _, w := range e.Cfg.Warnings {
		fmt.Fprintln(c.stderr, "kancli:", w)
	}
```

- [ ] **Step 6: Run everything and commit**

Run: `go test -race ./... && make lint`
```bash
git add internal/config/config.go internal/config/config_test.go internal/cli/root.go
git commit -m "feat: honour renamed config keys with a warning instead of dropping them"
```

---

### Task 5: Write the compatibility policy down

**Files:**
- Create: `docs/compatibility.md`
- Modify: `README.md` (add an "Upgrading" section after the install section; find it by searching for `install.sh`)
- Modify: `CHANGELOG.md` (under `## Unreleased` → `### Added`)

**Interfaces:** none (docs only). No tests; verify by reading and by `make lint` still passing.

- [ ] **Step 1: Create `docs/compatibility.md`**

```markdown
# Data compatibility

kancli keeps three kinds of files in the data directory (`kancli/` under
`$XDG_DATA_HOME` or the OS config dir, or wherever `KANCLI_FILE` points):

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
```

- [ ] **Step 2: Add an "Upgrading" section to `README.md`** right after the install instructions:

```markdown
## Upgrading

Rebuild or reinstall the binary the same way you installed it (`./install.sh`
from an updated checkout, or the release archive). Your data is picked up as
is. If the data format changed, the first run copies the old files to
`board.backups/vN/` next to `board.json` and prints where they are; a
renamed config setting keeps working and prints its new name. An older
kancli refuses to open data from a newer one rather than touching it. See
`docs/compatibility.md` for the details.
```

- [ ] **Step 3: Add to `CHANGELOG.md`** under `## Unreleased` → `### Added`, as the first bullet:

```markdown
- Safer upgrades: the first run on data from an older kancli copies the old
  files to `board.backups/vN/` and says so; events carry a format version
  and an older kancli refuses to open a newer log with advice to upgrade;
  renamed config keys keep working with a warning. Frozen fixtures of every
  released data format are loaded by the test suite.
```

- [ ] **Step 4: Check and commit**

Run: `make lint && go test -race ./...`
```bash
git add docs/compatibility.md README.md CHANGELOG.md
git commit -m "docs: compatibility policy and upgrading notes"
```
