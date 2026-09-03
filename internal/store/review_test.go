package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
)

// A store must not fold away events it never replayed, even when its
// change counter has already caught up with the database: the counter is
// read after the events, so a commit landing in between is invisible to it.
// The sequence number of the newest event is the second, independent guard.
func TestLoadBaselineIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	a := newTestStore(t, path)
	fa, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fa.Active().AddTask(board.Task{Title: "from a"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(fa); err != nil {
		t.Fatal(err)
	}

	b := newTestStore(t, path)
	fb, err := b.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fb.Active().AddTask(board.Task{Title: "from b"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(fb); err != nil {
		t.Fatal(err)
	}

	// The window: b's commit is on disk but a never replayed it, while a's
	// recorded change counter already matches the database.
	ver, err := a.dataVersion()
	if err != nil {
		t.Fatal(err)
	}
	a.dataVer = ver

	if err := a.Compact(fa); !errors.Is(err, ErrStale) {
		t.Fatalf("compact over an unreplayed event = %v, want ErrStale", err)
	}
	// A comment rather than a task: two writers adding a task from the same
	// state collide on the task number, which is a separate known limit.
	if err := fa.Active().AddComment(1, "late from a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(fa); err != nil {
		t.Fatal(err)
	}
	if n := len(fa.Active().Tasks); n != 2 {
		t.Errorf("a merged into %d tasks, want 2 (b's task was dropped)", n)
	}

	c := newTestStore(t, path)
	f, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]bool{}
	for _, tk := range f.Active().Tasks {
		titles[tk.Title] = true
	}
	for _, want := range []string{"from a", "from b"} {
		if !titles[want] {
			t.Errorf("task %q was lost: %v", want, titles)
		}
	}
	if n := len(f.Active().Task(1).Comments); n != 1 {
		t.Errorf("a's comment was lost: %d comments", n)
	}
}

// The file store named its files after whatever path was configured, with
// no .json in sight: KANCLI_FILE=~/tasks, -file board.data. Those boards
// have to import too.
func TestImportsLegacyPathsWithoutJSONExtension(t *testing.T) {
	cases := []struct {
		name string
		file string // the configured path, holding the old board.json
		base string // the stem its sidecars are named after
		db   string // where the database must end up
	}{
		{"no extension", "tasks", "tasks", "tasks.db"},
		{"other extension", "board.data", "board", "board.db"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			copyTree(t, filepath.Join("testdata", "v2"), dir)
			mustRename(t, filepath.Join(dir, "board.json"), filepath.Join(dir, c.file))
			if c.base != "board" {
				mustRename(t, filepath.Join(dir, "board.events.jsonl"), filepath.Join(dir, c.base+".events.jsonl"))
				mustRename(t, filepath.Join(dir, "board.events"), filepath.Join(dir, c.base+".events"))
			}

			st := newTestStore(t, filepath.Join(dir, c.file))
			f, err := st.Load()
			if err != nil {
				t.Fatalf("load %s: %v", c.file, err)
			}
			if want := filepath.Join(dir, c.db); st.Path() != want {
				t.Errorf("Path() = %q, want %q", st.Path(), want)
			}
			if !exists(filepath.Join(dir, c.db)) {
				t.Fatalf("%s was not created", c.db)
			}
			titles := []string{"write code", "buy milk"}
			if len(f.Active().Tasks) != len(titles) {
				t.Fatalf("%d tasks, want %d", len(f.Active().Tasks), len(titles))
			}
			for i, tk := range f.Active().Tasks {
				if tk.Title != titles[i] {
					t.Errorf("task %d = %q, want %q", i, tk.Title, titles[i])
				}
			}
			up, ok := st.Upgraded()
			if !ok || up.From != 2 || up.To != DatabaseFormat {
				t.Fatalf("Upgraded() = %+v, %v", up, ok)
			}
			if exists(filepath.Join(dir, c.file)) {
				t.Errorf("%s was left behind instead of moved to the backup", c.file)
			}
			if !exists(filepath.Join(up.Backup, c.file)) {
				t.Errorf("%s is not in the backup %s", c.file, up.Backup)
			}
		})
	}
}

func mustRename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

// An export with no events must return no file at all: a zero-byte JSONL
// file makes DuckDB's read_json_auto fail to bind seq.
func TestWriteEventsFileSkipsAnEmptyExport(t *testing.T) {
	st := newTestStore(t, filepath.Join(t.TempDir(), "board.db"))
	if _, err := st.Load(); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := WriteEventsFile(st)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if path != "" {
		t.Errorf("WriteEventsFile on an empty log = %q, want no file", path)
	}
}

// A walker whose sequence has run past the database (the log was replaced
// with a shorter one) has to be rebuilt, or the stats stay stuck forever.
func TestStatsWalkerRebuildsWhenTheLogShrinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	st := newTestStore(t, path)
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"one", "two", "three"} {
		if _, err := f.Active().AddTask(board.Task{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	before, err := st.BoardStats(f.Active(), now, 14)
	if err != nil {
		t.Fatal(err)
	}
	if before.Events == 0 {
		t.Fatal("no events were folded into the stats")
	}

	db, err := st.conn()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM events WHERE seq > 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM snapshots WHERE seq > 0`); err != nil {
		t.Fatal(err)
	}
	f, err = st.Load()
	if err != nil {
		t.Fatal(err)
	}
	after, err := st.BoardStats(f.Active(), now, 14)
	if err != nil {
		t.Fatal(err)
	}
	if after.Events >= before.Events {
		t.Errorf("stats after the log shrank fold %d events, before %d: the walker was not rebuilt",
			after.Events, before.Events)
	}
}

// An interrupted upgrade leaves both files behind. Opening then has to
// stop rather than silently run against one of them.
func TestBothDatabaseAndLegacyFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v3"), dir)
	legacy, err := os.ReadFile(filepath.Join("testdata", "v2", "board.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "board.json"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	st := newTestStore(t, filepath.Join(dir, "board.json"))
	_, err = st.Load()
	if err == nil {
		t.Fatal("opening a directory holding both files must fail")
	}
	for _, want := range []string{"board.db", "board.json", "move one of them away"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// A backup that fails after the database was written must take the
// database with it, so the next run imports again instead of running on a
// board whose legacy files are still live.
func TestFailedBackupRemovesTheImportedDatabase(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v2"), dir)
	// A file where the backup directory belongs: MkdirAll cannot win.
	blocker := filepath.Join(dir, "board.backups")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "board.json")

	st := newTestStore(t, path)
	if _, err := st.Load(); err == nil {
		t.Fatal("a failing backup must be reported")
	}
	if exists(filepath.Join(dir, "board.db")) {
		t.Error("the imported database was left behind after the backup failed")
	}
	if !exists(path) {
		t.Fatal("the legacy board.json was moved even though the backup failed")
	}

	// With the blocker gone the next run imports from scratch.
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	retry := newTestStore(t, path)
	f, err := retry.Load()
	if err != nil {
		t.Fatalf("retry after a failed backup: %v", err)
	}
	if len(f.Active().Tasks) != 2 {
		t.Errorf("%d tasks after the retry, want 2", len(f.Active().Tasks))
	}
	if _, ok := retry.Upgraded(); !ok {
		t.Error("the retry did not report the upgrade")
	}
}

// shortImportWait keeps the tests from sitting out the real timeouts.
func shortImportWait(t *testing.T) {
	t.Helper()
	wait, poll := importWait, importPoll
	importWait, importPoll = 100*time.Millisecond, time.Millisecond
	t.Cleanup(func() { importWait, importPoll = wait, poll })
}

// noImportLeftovers fails when a temporary import database survived.
func noImportLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".importing-") {
			t.Errorf("temporary import database left behind: %s", e.Name())
		}
	}
}

// Two processes may both find a legacy directory and start importing. The
// loser must leave the winner's database exactly as it found it, and say
// nothing: the board is imported, which is all the caller asked for.
func TestImportLosesGracefully(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v2"), dir)
	path := filepath.Join(dir, "board.json")
	shortImportWait(t)

	winner := []byte("the winner's database")
	prev := beforeImportRename
	beforeImportRename = func() {
		if err := os.WriteFile(filepath.Join(dir, "board.db"), winner, 0o644); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeImportRename = prev })

	st := newTestStore(t, path)
	if err := st.maybeImportLegacy(); err != nil {
		t.Fatalf("losing the import race must not be an error: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "board.db"))
	if err != nil || string(got) != string(winner) {
		t.Fatalf("the winner's database was replaced: %q, %v", got, err)
	}
	if up, ok := st.Upgraded(); ok {
		t.Errorf("the loser reported an upgrade: %+v", up)
	}
	if !exists(path) {
		t.Error("the loser moved the legacy board.json, which is the winner's to move")
	}
	noImportLeftovers(t, dir)
}

// The marker file serialises importers: one is running, the other waits
// and then gives up rather than importing the same directory twice.
func TestImportLockPreventsSecondImport(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v2"), dir)
	path := filepath.Join(dir, "board.json")
	marker := filepath.Join(dir, "board.import.lock")
	shortImportWait(t)
	if err := os.WriteFile(marker, []byte("12345\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newTestStore(t, path)
	_, err := st.Load()
	if err == nil {
		t.Fatal("a held import marker must stop a second import")
	}
	if !strings.Contains(err.Error(), "importing") {
		t.Errorf("error should say another import is running: %v", err)
	}
	if exists(filepath.Join(dir, "board.db")) {
		t.Error("the second importer built a database anyway")
	}
	if !exists(path) {
		t.Error("the second importer moved the legacy files")
	}
	noImportLeftovers(t, dir)

	// A marker older than the stale limit belonged to a process that died.
	old := time.Now().Add(-2 * staleImportLock)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}
	next := newTestStore(t, path)
	f, err := next.Load()
	if err != nil {
		t.Fatalf("a stale marker must be taken over: %v", err)
	}
	if len(f.Active().Tasks) != 2 {
		t.Errorf("%d tasks after the import, want 2", len(f.Active().Tasks))
	}
	if exists(marker) {
		t.Error("the marker was not removed when the import finished")
	}
}
