package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
)

// pinNow freezes board.Now at the address of the returned variable, so a
// test can move the clock by assigning to it.
func pinNow(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	now := start
	board.Now = func() time.Time { return now }
	t.Cleanup(func() { board.Now = time.Now })
	return &now
}

// TestPruneKeepsFiveNewestPlusBase covers the retention rule: sequence zero
// and the five newest snapshots always survive, including when every
// snapshot shares one wall-clock day (so the daily bucket keeps only one of
// them) and when the snapshot being written lands on the sequence the
// newest existing snapshot already holds.
func TestPruneKeepsFiveNewestPlusBase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.db")
	st := New(path)
	defer func() { _ = st.Close() }()

	pinNow(t, time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local))
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	b.SetClock(nil)

	// Thirty snapshots, one event apart, all stamped the same day.
	for i := 0; i < 30; i++ {
		if _, err := b.AddTask(board.Task{Title: fmt.Sprintf("task %d", i)}); err != nil {
			t.Fatal(err)
		}
		if err := st.Compact(f); err != nil {
			t.Fatal(err)
		}
	}
	want := []int64{0, 26, 27, 28, 29, 30}
	seqs, err := st.snapshotSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seqs, want) {
		t.Fatalf("snapshots = %v, want %v", seqs, want)
	}

	// Compacting again with no new events rewrites the snapshot at the
	// sequence the newest one already holds. That replacement must not
	// count twice against the five newest.
	if err := st.Compact(f); err != nil {
		t.Fatal(err)
	}
	seqs, err = st.snapshotSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seqs, want) {
		t.Errorf("after rewriting the newest snapshot: snapshots = %v, want %v", seqs, want)
	}
}

// TestPruneBucketsByEventTime covers a history whose snapshots were all
// written in one wall-clock instant over events spanning many days, which
// is what an import or a scripted bulk run leaves behind. Retention has to
// bucket by the time of the events a snapshot folds in: bucketing by the
// snapshot's own time collapses the whole history into one day, keeps only
// the newest five, and leaves an --as-of for any earlier point replaying
// from the empty base (LoadAsOf picks its base by sequence).
func TestPruneBucketsByEventTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.db")
	st := New(path)
	defer func() { _ = st.Close() }()

	// A Monday, so the ISO weeks below start where the days do.
	day := func(i int) time.Time { return time.Date(2026, 1, 4+i, 9, 0, 0, 0, time.Local) }
	fold := time.Date(2026, 3, 1, 12, 0, 0, 0, time.Local) // every snapshot is written here
	now := pinNow(t, day(1))
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	b.SetClock(nil)

	// Forty compactions: one event per day over forty days, each folded at
	// the same instant, so every snapshot row carries the same at.
	for i := 1; i <= 40; i++ {
		*now = day(i)
		if _, err := b.AddTask(board.Task{Title: fmt.Sprintf("task %d", i)}); err != nil {
			t.Fatal(err)
		}
		*now = fold
		if err := st.Compact(f); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := st.snapshotRows()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.seq != 0 && !r.at.Equal(fold) {
			t.Fatalf("snapshot %d was folded at %v, want every fold at %v", r.seq, r.at, fold)
		}
	}

	// Measured from the newest event (day 40), the last 30 days are days 11
	// to 40: one snapshot each. Day 10 lands exactly on the cutoff, so it
	// and the days before it fall into ISO weeks: days 1 to 7 are one week
	// (newest: sequence 7) and days 8 to 10 the next (sequence 10). The
	// empty base always stays.
	want := []int64{0, 7, 10}
	for seq := int64(11); seq <= 40; seq++ {
		want = append(want, seq)
	}
	seqs, err := st.snapshotSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seqs, want) {
		t.Errorf("snapshots = %v, want %v", seqs, want)
	}
}

// TestImportLeavesNoJournalFiles checks that the first run on a legacy
// directory ends with board.db alone: the importer's own connection is
// checkpointed and closed before the store opens, so Close can truncate
// and remove the write-ahead log.
func TestImportLeavesNoJournalFiles(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "v2"), dir)
	st := New(filepath.Join(dir, "board.json"))
	if _, err := st.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Events(); err != nil { // a read command, as the CLI would
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "board.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		if exists(db + suffix) {
			t.Errorf("%s was left behind after the first run", filepath.Base(db+suffix))
		}
	}
}

// TestLoadAsOfPicksBaseBySequence covers history whose snapshots were
// written long after the events they hold, which is what the importer and
// any bulk write leave behind. The base snapshot has to be chosen by
// sequence number, not by the snapshot's wall-clock time.
func TestLoadAsOfPicksBaseBySequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.db")
	st := New(path)
	defer func() { _ = st.Close() }()

	real := time.Date(2026, 9, 2, 10, 0, 0, 0, time.Local)
	now := pinNow(t, time.Date(2024, 6, 1, 9, 0, 0, 0, time.Local))
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	b.SetClock(nil)

	// A 2024 event, snapshotted "today".
	if _, err := b.AddTask(board.Task{Title: "from 2024"}); err != nil {
		t.Fatal(err)
	}
	*now = real
	if err := st.Compact(f); err != nil {
		t.Fatal(err)
	}
	firstSnap := f.LastSeq

	// A 2025 event, snapshotted "today" as well.
	*now = time.Date(2025, 6, 1, 9, 0, 0, 0, time.Local)
	if _, err := b.AddTask(board.Task{Title: "from 2025"}); err != nil {
		t.Fatal(err)
	}
	*now = real
	if err := st.Compact(f); err != nil {
		t.Fatal(err)
	}

	// As of the end of 2024 the first snapshot is the right base: it is the
	// newest one at or before the last event of 2024.
	asOf := time.Date(2024, 12, 31, 23, 59, 59, 0, time.Local)
	db, err := st.conn()
	if err != nil {
		t.Fatal(err)
	}
	_, seq, found, err := st.readSnapshot(db, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if !found || seq != firstSnap {
		t.Errorf("base snapshot as of 2024-12-31 = %d (found=%v), want %d", seq, found, firstSnap)
	}
	past, err := st.LoadAsOf(asOf)
	if err != nil {
		t.Fatal(err)
	}
	if got := past.Active().Tasks; len(got) != 1 || got[0].Title != "from 2024" {
		t.Errorf("as of 2024-12-31: %d tasks, want just \"from 2024\"", len(got))
	}

	// Before any event at all, the empty base at sequence zero answers.
	before := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	_, seq, found, err = st.readSnapshot(db, before)
	if err != nil {
		t.Fatal(err)
	}
	if !found || seq != 0 {
		t.Errorf("base snapshot as of 2020 = %d (found=%v), want 0", seq, found)
	}
	empty, err := st.LoadAsOf(before)
	if err != nil {
		t.Fatalf("as-of before any event: %v", err)
	}
	if got := len(empty.Active().Tasks); got != 0 {
		t.Errorf("as of 2020: %d tasks, want 0", got)
	}
}
