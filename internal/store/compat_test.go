package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
