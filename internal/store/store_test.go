package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/config"
)

// newTestStore returns a store for path that is closed when the test ends,
// so it never holds board.db past the temp directory's cleanup.
func newTestStore(t *testing.T, path string) *Store {
	t.Helper()
	st := New(path)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedSnapshot writes state (any released file version) into a fresh
// database as the snapshot at sequence zero, which is what the importer
// leaves behind for a board that predates the event log.
func seedSnapshot(t *testing.T, path, state string) {
	t.Helper()
	f, err := board.Decode([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	st := New(path)
	defer st.Close()
	db, err := st.conn()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := insertSnapshot(tx, f, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLogCompactionAndAsOf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.json") // still accepted; maps to board.db
	st := New(path)
	defer st.Close()
	if st.Path() != filepath.Join(dir, "board.db") {
		t.Fatalf("a .json path should map to the database beside it: %q", st.Path())
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	fixed := time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)
	board.Now = func() time.Time { return fixed }
	defer func() { board.Now = time.Now }()
	b.SetClock(nil)
	if _, err := b.AddTask(board.Task{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(f); err != nil { // first save writes the base snapshot
		t.Fatal(err)
	}
	if !exists(st.Path()) || st.TailEvents() != 0 {
		t.Fatalf("first save should compact: exists=%v tail=%d", exists(st.Path()), st.TailEvents())
	}
	fixed = fixed.AddDate(0, 0, 3)
	b.AddTask(board.Task{Title: "second"}) //nolint:errcheck // test data
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	if st.TailEvents() != 1 {
		t.Errorf("tail = %d, want 1", st.TailEvents())
	}
	fixed = fixed.AddDate(0, 0, 3)
	b.MoveTask(1, "done") //nolint:errcheck // test data
	st.Save(f)            //nolint:errcheck // test data

	// A fresh store replays the tail on top of the snapshot.
	other := New(path)
	defer other.Close()
	again, err := other.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stateOf(again) != stateOf(f) {
		t.Errorf("reloaded state differs:\n got %s\nwant %s", stateOf(again), stateOf(f))
	}

	// The full history is readable and ordered.
	events, err := st.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != board.EvTaskCreated || events[2].Kind != board.EvTaskMoved {
		t.Errorf("events = %+v", events)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Error("events are not in sequence order")
		}
	}

	// Compaction folds the tail into a snapshot and keeps everything readable.
	if err := st.Compact(f); err != nil {
		t.Fatal(err)
	}
	if st.TailEvents() != 0 {
		t.Errorf("tail after compaction = %d", st.TailEvents())
	}
	events, _ = st.Events()
	if len(events) != 3 {
		t.Errorf("events after compaction = %d", len(events))
	}
	snaps, err := st.snapshotSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snaps, []int64{0, 1, 3}) { // empty base, first save, this compaction
		t.Errorf("snapshots = %v", snaps)
	}

	// As-of views.
	past, err := st.LoadAsOf(time.Date(2026, 8, 2, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(past.Active().Tasks); got != 1 {
		t.Errorf("as of Aug 2: %d tasks, want 1", got)
	}
	mid, err := st.LoadAsOf(time.Date(2026, 8, 5, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if got := mid.Active(); len(got.Tasks) != 2 || got.Task(1).Column != "todo" {
		t.Errorf("as of Aug 5: %d tasks, task 1 in %s", len(got.Tasks), got.Task(1).Column)
	}
	empty, err := st.LoadAsOf(time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil || len(empty.Active().Tasks) != 0 {
		t.Errorf("as-of before any event should be an empty board: %v", err)
	}

	// A tail of CompactAfter events folds itself into a snapshot.
	for i := 0; i < CompactAfter; i++ {
		b.AddTask(board.Task{Title: fmt.Sprintf("bulk %d", i)}) //nolint:errcheck // test data
		if err := st.Save(f); err != nil {
			t.Fatal(err)
		}
	}
	if st.TailEvents() != 0 {
		t.Errorf("tail after %d events = %d, want a fresh snapshot", CompactAfter, st.TailEvents())
	}
	snaps, err = st.snapshotSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(snaps, int64(3+CompactAfter)) {
		t.Errorf("no snapshot for the folded tail: %v", snaps)
	}
	reloaded, err := newTestStore(t, path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if stateOf(reloaded) != stateOf(f) {
		t.Error("state differs after the automatic compaction")
	}
}

func TestStoreBootstrapsHistoryForOldFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	created := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	seedSnapshot(t, path, `{"version":2,"boards":[{"id":"main","name":"Main","tasks":[
		{"id":1,"column":"done","title":"finished","created_at":"`+created.Format(time.RFC3339)+`","updated_at":"`+updated.Format(time.RFC3339)+`"},
		{"id":2,"column":"todo","title":"open","created_at":"`+updated.Format(time.RFC3339)+`","updated_at":"`+updated.Format(time.RFC3339)+`"}]}]}`)

	st := New(path)
	defer st.Close()
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Active().Tasks) != 2 || f.Active().Task(1).Column != "done" {
		t.Fatalf("state changed by bootstrap: %+v", f.Active().Tasks)
	}
	events, err := st.Events()
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(events))
	for _, e := range events {
		kinds = append(kinds, string(e.Kind))
	}
	if !slices.Equal(kinds, []string{"task.created", "task.created", "task.moved"}) && !slices.Equal(kinds, []string{"task.created", "task.moved", "task.created"}) {
		t.Errorf("bootstrap events = %v", kinds)
	}
	if !events[0].At.Equal(created) {
		t.Errorf("first event at %v, want %v", events[0].At, created)
	}
	before, err := st.LoadAsOf(created.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := before.Active(); len(got.Tasks) != 1 || got.Task(1).Column != "todo" {
		t.Errorf("as-of just after creation: %+v", got.Tasks)
	}
	st2 := New(path)
	defer st2.Close()
	if _, err := st2.Load(); err != nil || st2.TailEvents() != 0 {
		t.Errorf("second load should find a compacted store: %v tail=%d", err, st2.TailEvents())
	}
}

func TestSQLViews(t *testing.T) {
	st := New(filepath.Join(t.TempDir(), "board.db"))
	defer st.Close()
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Active().AddTask(board.Task{Title: "sql"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	events, cleanup, err := WriteEventsFile(st)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if data, err := os.ReadFile(events); err != nil || !strings.Contains(string(data), `"kind":"task.created"`) {
		t.Fatalf("exported events = %q, %v", data, err)
	}
	sql := SQLViews("/tmp/state.json", []string{events}, map[string]string{"main": "done", "work": "shipped"})
	for _, want := range []string{"CREATE OR REPLACE VIEW tasks", "read_json_auto('/tmp/state.json')", SQLLiteral(events), "format = 'newline_delimited'", "VIEW cycle_times", "VIEW column_stays", "('main', 'done'), ('work', 'shipped')", "JOIN done_columns dc"} {
		if !strings.Contains(sql, want) {
			t.Errorf("views missing %q", want)
		}
	}
	empty := SQLViews("/tmp/s.json", nil, nil)
	if !strings.Contains(empty, "WHERE false") || !strings.Contains(empty, "WHERE board IS NOT NULL") {
		t.Error("empty views should still be defined")
	}
	if strings.Contains(sql, "rowid") {
		t.Error("views must not rely on rowid")
	}
	if SQLLiteral("it's") != "'it''s'" {
		t.Error("sqlLiteral")
	}
	t.Setenv("KANCLI_DUCKDB", "/definitely/not/here")
	if _, err := RunDuckDB("/definitely/not/here", empty, "SELECT 1", "box"); err == nil {
		t.Error("missing binary should fail")
	}
	if _, err := RunDuckDB("/definitely/not/here", empty, "SELECT 1", "xml"); err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("bad format error = %v", err)
	}
}

func TestIncrementalStatsMatchFullWalk(t *testing.T) {
	f := board.DemoFile()
	st := New("")
	defer st.Close()
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	now := board.Now()
	all, _ := st.Events()
	full := board.ComputeStats(b, all, now, 90)

	// Feed the same events in three chunks to a persistent walker.
	w := board.NewStatsWalker(b)
	third := len(all) / 3
	w.Feed(all[:third])
	w.Feed(all[:2*third]) // overlap is skipped by sequence
	w.Feed(all)
	inc := w.Finish(b, now, 90)
	fullJSON, _ := json.Marshal(full)
	incJSON, _ := json.Marshal(inc)
	if string(fullJSON) != string(incJSON) {
		t.Errorf("incremental stats differ from a full walk:\n%s\n%s", incJSON, fullJSON)
	}

	// The store caches the walker and folds in new events.
	first, err := st.BoardStats(b, now, 90)
	if err != nil {
		t.Fatal(err)
	}
	if first.Events != full.Events {
		t.Errorf("cached stats events = %d, want %d", first.Events, full.Events)
	}
	b.AddTask(board.Task{Title: "later", Column: "in_progress"}) //nolint:errcheck // test data
	st.Save(f)                                                   //nolint:errcheck // test data
	second, _ := st.BoardStats(b, now, 90)
	if second.Events != first.Events+1 || second.InProgress != first.InProgress+1 {
		t.Errorf("new event not folded in: events %d→%d wip %d→%d", first.Events, second.Events, first.InProgress, second.InProgress)
	}
	// Changing the done column invalidates the walker.
	b.AddColumn("Shipped", "", 0) //nolint:errcheck // test data
	third2, _ := st.BoardStats(b, now, 90)
	if len(third2.Finished) != 0 {
		t.Errorf("after a new last column nothing should count as finished, got %d", len(third2.Finished))
	}
}

// Two stores hold the same board; the second one to save must fold the
// first one's events into its file instead of losing or overwriting them.
// Both writers touch different things: two independent AddTask calls would
// allocate the same task id, which is a conflict no merge can resolve.
func TestSaveMergesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	seed := New(path)
	f0, err := seed.Load()
	if err != nil {
		t.Fatal(err)
	}
	f0.Active().AddTask(board.Task{Title: "seed"}) //nolint:errcheck // test data
	if err := seed.Save(f0); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	a, b := New(path), New(path)
	defer a.Close()
	defer b.Close()
	fa, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	fb, err := b.Load()
	if err != nil {
		t.Fatal(err)
	}
	fa.Active().AddTask(board.Task{Title: "from a"}) //nolint:errcheck // test data
	if err := a.Save(fa); err != nil {
		t.Fatal(err)
	}
	fb.Active().AddComment(1, "from b") //nolint:errcheck // test data
	if err := b.Save(fb); err != nil {  // must merge, not fail
		t.Fatal(err)
	}
	if !b.NeedReload() && len(fb.Active().Tasks) != 2 {
		t.Fatalf("b did not merge a's task: %d tasks", len(fb.Active().Tasks))
	}
	c := New(path)
	defer c.Close()
	f, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.Active().Tasks); n != 2 {
		t.Fatalf("want 2 tasks after both saves, got %d", n)
	}
	if n := len(f.Active().Task(1).Comments); n != 1 {
		t.Errorf("b's comment was lost: %d comments", n)
	}
	if !a.ChangedOnDisk() {
		t.Error("a should see b's write")
	}
}

func TestNewerFormatIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	st := New(path)
	if _, err := st.Load(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE meta SET value = '99' WHERE key = 'format'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	next := New(path)
	defer next.Close()
	_, err = next.Load()
	if err == nil || !strings.Contains(err.Error(), "newer kancli") {
		t.Fatalf("load of a newer store format = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "store format 99") {
		t.Errorf("error should name the format it found: %v", err)
	}
}

func TestSnapshotRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	st := New(path)
	defer st.Close()
	db, err := st.conn()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.Local)
	f := board.NewFile()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := insertSnapshot(tx, f, 0); err != nil { // the empty base
		t.Fatal(err)
	}
	// Twenty snapshots four days apart, reaching back 90 days, then twenty
	// six hours apart over the last five days.
	for i := 1; i <= 40; i++ {
		if i <= 20 {
			f.SnapshotAt = now.AddDate(0, 0, -90+4*(i-1))
		} else {
			f.SnapshotAt = now.Add(-time.Duration(41-i) * 6 * time.Hour)
		}
		if err := insertSnapshot(tx, f, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.snapshotRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 41 {
		t.Fatalf("seeded %d snapshots, want 41", len(rows))
	}

	// The policy: sequence zero, the newest five, the newest per calendar
	// day for the last 30 days and the newest per ISO week before that.
	// The days are the days of the events each snapshot folds in; these
	// snapshots have no events, so each one answers with its own time, and
	// the 30 days are counted back from the newest of them rather than
	// from the wall clock.
	want := map[int64]bool{0: true}
	for _, r := range rows[len(rows)-5:] {
		want[r.seq] = true
	}
	newest := map[string]int64{}
	cutoff := bucketNow(rows).AddDate(0, 0, -30)
	for _, r := range rows {
		key := r.eventAt.Format("2006-01-02")
		if !r.eventAt.After(cutoff) {
			y, w := r.eventAt.ISOWeek()
			key = fmt.Sprintf("%04d-W%02d", y, w)
		}
		if r.seq > newest[key] {
			newest[key] = r.seq
		}
	}
	for _, seq := range newest {
		want[seq] = true
	}

	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := prune(tx, rows); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := st.snapshotSeqs()
	if err != nil {
		t.Fatal(err)
	}
	kept := map[int64]bool{}
	for _, seq := range got {
		kept[seq] = true
	}
	if len(got) >= 41 {
		t.Errorf("nothing was pruned: %v", got)
	}
	if !kept[0] {
		t.Error("the empty base must never be pruned")
	}
	for seq := int64(36); seq <= 40; seq++ {
		if !kept[seq] {
			t.Errorf("the newest five must be kept, %d is gone: %v", seq, got)
		}
	}
	for seq := range want {
		if !kept[seq] {
			t.Errorf("snapshot %d should have been kept: %v", seq, got)
		}
	}
	for _, seq := range got {
		if !want[seq] {
			t.Errorf("snapshot %d should have been pruned: %v", seq, got)
		}
	}
}

func TestEventRoundTripPreservesTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	st := New(path)
	defer st.Close()
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("UTC+7", 7*3600)
	at := time.Date(2026, 3, 4, 5, 6, 7, 123456789, zone)
	if err := st.append(f, []board.Event{{At: at, Board: f.Active().ID, Kind: board.EvTaskCreated,
		Task: 1, To: "todo", Data: board.MustJSON(board.Task{ID: 1, Title: "x", Column: "todo"})}}); err != nil {
		t.Fatal(err)
	}
	events, err := st.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	got := events[0]
	if !got.At.Equal(at) {
		t.Errorf("time changed in the round trip: %v, want %v", got.At, at)
	}
	if got.At.Location() != time.Local {
		t.Errorf("event time came back in %v, want the local zone", got.At.Location())
	}
	if got.At.Nanosecond() != at.Nanosecond() {
		t.Errorf("nanoseconds lost: %d", got.At.Nanosecond())
	}
	if got.V != board.EventVersion {
		t.Errorf("event version = %d, want %d", got.V, board.EventVersion)
	}
	if len(got.Data) == 0 || !strings.Contains(string(got.Data), `"title":"x"`) {
		t.Errorf("data lost in the round trip: %s", got.Data)
	}
}

func TestCompactRefusesToDropForeignEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	a := New(path)
	defer a.Close()
	fa, _ := a.Load()
	fa.Active().AddTask(board.Task{Title: "from a"}) //nolint:errcheck // test data
	a.Save(fa)                                       //nolint:errcheck // test data

	b := New(path)
	defer b.Close()
	fb, _ := b.Load()
	fb.Active().AddTask(board.Task{Title: "from b"}) //nolint:errcheck // test data
	b.Save(fb)                                       //nolint:errcheck // test data

	// A has not seen b's event; compacting must not fold it away.
	if err := a.Compact(fa); !errors.Is(err, ErrStale) {
		t.Fatalf("compact with foreign events = %v, want ErrStale", err)
	}
	fa, _ = a.Load()
	if err := a.Compact(fa); err != nil {
		t.Fatal(err)
	}
	c := New(path)
	defer c.Close()
	f, _ := c.Load()
	if len(f.Active().Tasks) != 2 {
		t.Errorf("tasks after compact = %d, want 2", len(f.Active().Tasks))
	}

	// B, still holding the old sequence numbers, appends after A's compaction:
	// its event must get a fresh number and survive a reload.
	fb.Active().AddComment(1, "late comment") //nolint:errcheck // test data
	if err := b.Save(fb); err != nil {
		t.Fatal(err)
	}
	d := New(path)
	defer d.Close()
	f, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.Active().Task(1).Comments); got != 1 {
		t.Errorf("comment appended after a foreign compaction was lost (%d comments)", got)
	}
}

func TestBootstrapDoesNotArchiveFromCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	seedSnapshot(t, path, `{"version":2,"boards":[{"id":"main","name":"Main","tasks":[
		{"id":1,"column":"done","title":"old","created_at":"2026-07-01T09:00:00Z","updated_at":"2026-07-05T09:00:00Z","archived_at":"2026-07-10T09:00:00Z"}]}]}`)
	st := New(path)
	defer st.Close()
	if _, err := st.Load(); err != nil {
		t.Fatal(err)
	}
	early, err := st.LoadAsOf(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if tk := early.Active().Task(1); tk == nil || tk.Archived() || tk.Column != "todo" {
		t.Errorf("as-of before archive: %+v", tk)
	}
	late, _ := st.LoadAsOf(time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC))
	if tk := late.Active().Task(1); tk == nil || !tk.Archived() {
		t.Errorf("as-of after archive: %+v", tk)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "board.db")
	st := New(path)
	defer st.Close()
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Boards) != 1 || len(f.Boards[0].Tasks) != 0 {
		t.Fatal("a missing database should yield one empty board")
	}
	want := board.SampleFile()
	if err := st.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Boards[0].Tasks) != len(want.Boards[0].Tasks) {
		t.Fatalf("loaded %d tasks, want %d", len(got.Boards[0].Tasks), len(want.Boards[0].Tasks))
	}
	a, b := want.Boards[0].Tasks[0], got.Boards[0].Tasks[0]
	if a.ID != b.ID || a.Title != b.Title || a.Priority != b.Priority || a.Due != b.Due || len(a.Checklist) != len(b.Checklist) {
		t.Errorf("task changed in round trip:\n%+v\n%+v", a, b)
	}
	if !exists(path) {
		t.Fatalf("%s was not created", path)
	}
	// The snapshot is the compressed JSON of the file.
	db, err := st.conn()
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	if err := db.QueryRow(`SELECT state FROM snapshots ORDER BY seq DESC LIMIT 1`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	state, err := gunzip(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"priority":"high"`) || !strings.Contains(string(state), `"version":2`) {
		t.Errorf("unexpected snapshot contents:\n%s", state)
	}
	if st.ChangedOnDisk() {
		t.Error("the database should not count as changed right after a save")
	}

	// Another writer is noticed.
	other := New(path)
	defer other.Close()
	of, err := other.Load()
	if err != nil {
		t.Fatal(err)
	}
	of.Active().AddTask(board.Task{Title: "from elsewhere"}) //nolint:errcheck // test data
	if err := other.Save(of); err != nil {
		t.Fatal(err)
	}
	if !st.ChangedOnDisk() {
		t.Error("a write from another connection should be detected")
	}
}

func TestStoreMigratesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	seedSnapshot(t, path, `{"version":1,"tasks":[
		{"id":"abc","status":"todo","title":"buy milk","description":"strawberry","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
		{"id":"def","status":"in_progress","title":"write code"},
		{"id":"ghi","status":"done","title":"stay cool"}]}`)
	st := New(path)
	defer st.Close()
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	if len(b.Tasks) != 3 || b.Tasks[0].ID != 1 || b.Tasks[2].ID != 3 {
		t.Errorf("migrated tasks = %+v", b.Tasks)
	}
	if b.Tasks[1].Column != "in_progress" || b.Tasks[2].Column != "done" || b.Tasks[0].Description != "strawberry" {
		t.Errorf("columns not preserved: %+v", b.Tasks)
	}
	if b.NextID != 4 {
		t.Errorf("next id = %d", b.NextID)
	}
	// The migrated state survives a reload of the database.
	again := New(path)
	defer again.Close()
	f2, err := again.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stateOf(f2) != stateOf(f) {
		t.Errorf("state differs after reload:\n%s\n%s", stateOf(f2), stateOf(f))
	}
}

func TestDefaultPaths(t *testing.T) {
	t.Setenv("KANCLI_FILE", "/tmp/custom.json")
	if p, _ := DefaultPath(); p != "/tmp/custom.json" {
		t.Errorf("KANCLI_FILE path = %q", p)
	}
	t.Setenv("KANCLI_FILE", "")
	t.Setenv("XDG_DATA_HOME", "/data")
	if p, _ := DefaultPath(); p != filepath.Join("/data", "kancli", "board.db") {
		t.Errorf("XDG path = %q", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	if p, _ := config.DefaultPath(); p != filepath.Join("/cfg", "kancli", "config.json") {
		t.Errorf("config path = %q", p)
	}
}

// stateOf renders the tasks and columns of every board for comparisons.
func stateOf(f *board.File) string {
	data, _ := json.Marshal(f.Boards)
	return string(data)
}

func TestReviewReport(t *testing.T) {
	f := board.DemoFile()
	st := New("")
	defer st.Close()
	st.Save(f) //nolint:errcheck // test data
	events, _ := st.Events()
	md := board.ReviewReport(f.Active(), events, board.Now(), 30)
	for _, want := range []string{"# Demo · review of the last 30 days", "## Summary", "- Finished: **", "## Finished", "- [x] #", "## Needs attention", "overdue", "## Labels", "| feature |", "## Throughput"} {
		if !strings.Contains(md, want) {
			t.Errorf("review missing %q:\n%s", want, md)
		}
	}
}

func TestStatsFromEvents(t *testing.T) {
	f := board.DemoFile()
	st := New("")
	defer st.Close()
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	events, _ := st.Events()
	b := f.Active()
	now := board.Now()
	s := board.ComputeStats(b, events, now, 90)
	if s.Events != len(events) || s.Live != 8 || s.InProgress != 2 {
		t.Errorf("headline = events %d live %d wip %d", s.Events, s.Live, s.InProgress)
	}
	if len(s.Finished) != 10 { // 2 in the sample board + 8 archived demo tasks
		t.Errorf("finished = %d", len(s.Finished))
	}
	if s.CycleMedian <= 0 || s.CycleP90 < s.CycleMedian {
		t.Errorf("cycle times median %v p90 %v", s.CycleMedian, s.CycleP90)
	}
	if len(s.Weeks) != 13 || sumDone(s.Weeks) != 10 {
		t.Errorf("weeks = %d, done = %d", len(s.Weeks), sumDone(s.Weeks))
	}
	if len(s.WIP) != 60 || s.WIP[len(s.WIP)-1].Count != 2 {
		t.Errorf("wip series = %d days, now %d", len(s.WIP), s.WIP[len(s.WIP)-1].Count)
	}
	var inProg board.ColumnStay
	for _, cs := range s.Stays {
		if cs.Column == "in_progress" {
			inProg = cs
		}
	}
	if inProg.Samples != 9 || inProg.Mean <= 0 { // "Stay cool" skipped In Progress
		t.Errorf("in progress stays = %+v", inProg)
	}
	if len(s.Aging) == 0 || s.Aging[0].Age < s.Aging[len(s.Aging)-1].Age {
		t.Errorf("aging = %+v", s.Aging)
	}
	var feature board.LabelStat
	for _, l := range s.Labels {
		if l.Label == "feature" {
			feature = l
		}
	}
	if feature.Done != 3 || feature.Open != 1 {
		t.Errorf("feature label = %+v", feature)
	}
	// The window trims the finished sample.
	short := board.ComputeStats(b, events, now, 7)
	if len(short.Finished) >= len(s.Finished) || len(short.Weeks) != 1 {
		t.Errorf("7 day window: finished %d weeks %d", len(short.Finished), len(short.Weeks))
	}
	if mean, median, p90 := board.Summarize([]time.Duration{4, 1, 3, 2}); mean != 2 || median != 2 || p90 != 4 {
		t.Errorf("board.Summarize = %v %v %v", mean, median, p90)
	}
	for d, want := range map[time.Duration]string{0: "0h", 30 * time.Minute: "30m", 5 * time.Hour: "5h", 50 * time.Hour: "2d 2h", 72 * time.Hour: "3d"} {
		if got := board.HumanDuration(d); got != want {
			t.Errorf("board.HumanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestExportEventsJSONL(t *testing.T) {
	st := New("")
	defer st.Close()
	f := board.DemoFile()
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := st.ExportEventsJSONL(path); err != nil {
		t.Fatal(err)
	}
	lines, _, err := readEventFile(path)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := st.Events()
	if len(lines) != len(events) || len(lines) == 0 {
		t.Fatalf("exported %d lines, want %d", len(lines), len(events))
	}
	for i, e := range lines {
		if e.Seq != events[i].Seq || e.Kind != events[i].Kind || !e.At.Equal(events[i].At) {
			t.Errorf("line %d = %+v, want %+v", i, e, events[i])
		}
	}
}

func sumDone(ws []board.WeekCount) int {
	n := 0
	for _, w := range ws {
		n += w.Done
	}
	return n
}

// TestCloseCheckpointsTheLog covers the promise that copying board.db is a
// valid backup: a clean exit leaves no write-ahead log beside it.
func TestCloseCheckpointsTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.db")
	st := New(path)
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Active().AddTask(board.Task{Title: "durable"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if exists(path + suffix) {
			t.Errorf("%s should be gone after Close", filepath.Base(path)+suffix)
		}
	}
	if err := st.Close(); err != nil {
		t.Errorf("Close should be idempotent: %v", err)
	}
	again, err := newTestStore(t, path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Active().Tasks) != 1 || again.Active().Tasks[0].Title != "durable" {
		t.Errorf("reopened board = %+v", again.Active().Tasks)
	}
}
