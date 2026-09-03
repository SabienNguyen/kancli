package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
)

// Every released on-disk format has a frozen copy under testdata/vN. This
// test proves the current build still opens each one, can save on top of it
// and reads the same state back. Add a directory and a row for every new
// format version; never edit an existing fixture.
func TestLoadsEveryReleasedFormat(t *testing.T) {
	cases := []struct {
		name     string
		dir      string
		titles   []string // active board, in Tasks order
		columns  []string // column of each task, same order
		comments int      // comments on the last task
		crlf     bool     // rewrite the event logs with CRLF endings first
		from     int      // the format the importer reports, 0 for none
	}{
		{"v1", "v1", []string{"buy milk", "write code", "stay cool"}, []string{"todo", "in_progress", "done"}, 0, false, 1},
		{"v2", "v2", []string{"write code", "buy milk"}, []string{"in_progress", "in_progress"}, 1, false, 2},
		// A Windows checkout (or an editor) can turn the log into CRLF.
		{"v2-crlf", "v2", []string{"write code", "buy milk"}, []string{"in_progress", "in_progress"}, 1, true, 2},
		// The database format: nothing to import, it is opened as it is.
		{"v3", "v3", []string{"write code", "buy milk", "in v3"}, []string{"in_progress", "in_progress", "todo"}, 0, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			copyTree(t, filepath.Join("testdata", c.dir), dir)
			if c.crlf {
				toCRLF(t, dir)
			}
			// The configured path is still the file store's name; the store
			// maps it to the board.db beside it.
			path := filepath.Join(dir, "board.json")

			st := New(path)
			defer st.Close()
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

			// The database is there, and an imported directory has been
			// moved out of the way.
			if !exists(filepath.Join(dir, "board.db")) {
				t.Fatal("board.db was not created")
			}
			up, ok := st.Upgraded()
			if c.from == 0 {
				if ok {
					t.Errorf("Upgraded() = %+v, want none", up)
				}
			} else {
				if !ok || up.From != c.from || up.To != DatabaseFormat {
					t.Fatalf("Upgraded() = %+v, %v; want From %d, To %d", up, ok, c.from, DatabaseFormat)
				}
				if want := filepath.Join(dir, "board.backups"); !strings.HasPrefix(up.Backup, want+string(filepath.Separator)) {
					t.Errorf("backup dir = %q, want one under %q", up.Backup, want)
				}
				if exists(filepath.Join(dir, "board.json")) {
					t.Error("board.json was copied, not moved")
				}
			}

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

			next := New(path)
			defer next.Close()
			again, err := next.Load()
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

// toCRLF rewrites every .jsonl under dir with CRLF line endings, the way a
// Windows checkout would.
func toCRLF(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".jsonl" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lf := strings.ReplaceAll(string(data), "\r\n", "\n")
		return os.WriteFile(p, []byte(strings.ReplaceAll(lf, "\n", "\r\n")), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadEventFileCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.events.jsonl")
	data := []byte(`{"v":1,"seq":1,"at":"2026-01-01T00:00:00Z","board":"main","kind":"task.added","task":1}` + "\r\n" +
		`{"v":1,"seq":2,"at":"2026-01-01T00:01:00Z","board":"main","kind":"task.moved","task":1}` + "\r\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	events, consumed, err := readEventFile(path)
	if err != nil {
		t.Fatalf("readEventFile: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if consumed != int64(len(data)) {
		t.Errorf("consumed = %d, want %d (the whole file)", consumed, len(data))
	}

	// A torn last line is reported by leaving it unconsumed.
	torn := append(append([]byte(nil), data...), []byte(`{"seq":3,"kind":"task.moved"`)...)
	if err := os.WriteFile(path, torn, 0o644); err != nil {
		t.Fatal(err)
	}
	events, consumed, err = readEventFile(path)
	if err != nil {
		t.Fatalf("readEventFile with a torn tail: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if consumed != int64(len(data)) {
		t.Errorf("consumed = %d, want %d (the torn line's start)", consumed, len(data))
	}
}

func TestLoadBacksUpOlderFormatOnce(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v1"), dir)
	path := filepath.Join(dir, "board.json")
	original, _ := os.ReadFile(path)

	st := New(path)
	defer st.Close()
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	up, ok := st.Upgraded()
	if !ok || up.From != 1 || up.To != DatabaseFormat {
		t.Fatalf("Upgraded() = %+v, %v", up, ok)
	}
	if up.Backup != filepath.Join(dir, "board.backups", "v1") {
		t.Errorf("backup dir = %q", up.Backup)
	}
	got, err := os.ReadFile(filepath.Join(up.Backup, "board.json"))
	if err != nil || string(got) != string(original) {
		t.Fatalf("backup is not a byte copy of the original: %v", err)
	}

	// The second open finds the database and reports nothing, leaving the
	// backup alone.
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	st2 := New(path)
	defer st2.Close()
	if _, err := st2.Load(); err != nil {
		t.Fatal(err)
	}
	if up2, ok := st2.Upgraded(); ok {
		t.Errorf("an already-imported board must not report an upgrade: %+v", up2)
	}
	got2, _ := os.ReadFile(filepath.Join(up.Backup, "board.json"))
	if string(got2) != string(original) {
		t.Error("backup was overwritten")
	}
}

func TestLoadBacksUpEventLogToo(t *testing.T) {
	// The whole file-store layout moves into the backup, not just the
	// state file: the live log, the archived segments and the snapshots.
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v2"), dir)
	snapDir := filepath.Join(dir, "board.snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state, _ := os.ReadFile(filepath.Join(dir, "board.json"))
	if err := os.WriteFile(filepath.Join(snapDir, "000000000002.json"), state, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "board.json")

	st := New(path)
	defer st.Close()
	if _, err := st.Load(); err != nil {
		t.Fatal(err)
	}
	up, ok := st.Upgraded()
	if !ok {
		t.Fatal("expected an upgrade")
	}
	if want := filepath.Join(dir, "board.backups", "v2"); up.Backup != want {
		t.Fatalf("backup dir = %q, want %q", up.Backup, want)
	}
	for _, name := range []string{"board.json", "board.events.jsonl", "board.events", "board.snapshots"} {
		if _, err := os.Stat(filepath.Join(up.Backup, name)); err != nil {
			t.Errorf("%s not backed up: %v", name, err)
		}
		if exists(filepath.Join(dir, name)) {
			t.Errorf("%s was left behind", name)
		}
	}
}

func TestStoreWritesEventVersionAndRefusesNewer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.db")
	st := New(path)
	defer st.Close()
	f, _ := st.Load()
	f.Active().AddTask(board.Task{Title: "x"}) //nolint:errcheck // test data
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	f.Active().AddTask(board.Task{Title: "y"}) //nolint:errcheck // test data
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	events, err := st.Events()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.V != board.EventVersion {
			t.Fatalf("event %d is not version-tagged: %+v", e.Seq, e)
		}
	}

	// A future kancli writes something this build cannot read.
	db, err := st.conn()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events (`+eventColumns+`) VALUES (99, 1, ?, 'main', 'task.teleported', 1, '', '', 0, '', NULL, 'future')`,
		formatTime(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}

	next := New(path)
	defer next.Close()
	_, err = next.Load()
	if !errors.Is(err, board.ErrNewerEvents) {
		t.Fatalf("err = %v, want ErrNewerEvents", err)
	}
	for _, want := range []string{"task.teleported", "upgrade kancli", "event 99", path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
	// The advice is worded once, not once per wrapping layer.
	if n := strings.Count(err.Error(), "written by a newer kancli"); n != 1 {
		t.Errorf("advice appears %d times, want 1: %v", n, err)
	}
}
