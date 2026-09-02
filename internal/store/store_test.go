package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/config"
)

func TestStoreLogCompactionAndAsOf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.json")
	st := New(path)
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
	if !exists(path) || st.TailEvents() != 0 {
		t.Fatalf("first save should compact: exists=%v tail=%d", exists(path), st.TailEvents())
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
	again, err := New(path).Load()
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

	// Compaction archives the tail and keeps everything readable.
	if err := st.Compact(f); err != nil {
		t.Fatal(err)
	}
	segs, _ := filepath.Glob(filepath.Join(st.archiveDir, "*.jsonl"))
	if len(segs) != 2 || exists(st.logPath) { // the first save compacted too
		t.Errorf("archive = %v, tail exists = %v", segs, exists(st.logPath))
	}
	events, _ = st.Events()
	if len(events) != 3 {
		t.Errorf("events after compaction = %d", len(events))
	}
	snaps, _ := filepath.Glob(filepath.Join(st.snapDir, "*.json"))
	if len(snaps) != 3 { // empty base, first save, this compaction
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

	// A torn last line is tolerated; damage in the middle is not.
	b.AddTask(board.Task{Title: "third"}) //nolint:errcheck // test data
	st.Save(f)                            //nolint:errcheck // test data
	fh, _ := os.OpenFile(st.logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(`{"seq":99,"kind":"task.cre`) //nolint:errcheck // test data
	fh.Close()
	torn, err := New(path).Load()
	if err != nil || torn.Active().Task(3) == nil {
		t.Errorf("torn tail should be dropped: %v", err)
	}
	os.WriteFile(st.logPath, []byte("{bad}\n{\"seq\":5,\"kind\":\"task.deleted\",\"task\":3}\n"), 0o644) //nolint:errcheck // test data
	if _, err := New(path).Load(); err == nil {
		t.Error("damage in the middle of the log should be reported")
	}
}

func TestStoreBootstrapsHistoryForOldFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	created := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	old := `{"version":2,"boards":[{"id":"main","name":"Main","tasks":[
		{"id":1,"column":"done","title":"finished","created_at":"` + created.Format(time.RFC3339) + `","updated_at":"` + updated.Format(time.RFC3339) + `"},
		{"id":2,"column":"todo","title":"open","created_at":"` + updated.Format(time.RFC3339) + `","updated_at":"` + updated.Format(time.RFC3339) + `"}]}]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	st := New(path)
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
	if _, err := st2.Load(); err != nil || st2.TailEvents() != 0 {
		t.Errorf("second load should find a compacted store: %v tail=%d", err, st2.TailEvents())
	}
}

func TestSQLViews(t *testing.T) {
	sql := SQLViews("/tmp/state.json", []string{"/data/board.events/000000000001-000000000010.jsonl", "/data/board.events.jsonl"}, map[string]string{"main": "done", "work": "shipped"})
	for _, want := range []string{"CREATE OR REPLACE VIEW tasks", "read_json_auto('/tmp/state.json')", "'/data/board.events.jsonl'", "format = 'newline_delimited'", "VIEW cycle_times", "VIEW column_stays", "('main', 'done'), ('work', 'shipped')", "JOIN done_columns dc"} {
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

func TestTornTailIsRepairedBeforeAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	st := New(path)
	f, _ := st.Load()
	f.Active().AddTask(board.Task{Title: "one"}) //nolint:errcheck // test data
	st.Save(f)                                   //nolint:errcheck // test data
	f.Active().AddTask(board.Task{Title: "two"}) //nolint:errcheck // test data
	st.Save(f)                                   //nolint:errcheck // test data
	// Crash mid-write.
	fh, _ := os.OpenFile(st.logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(`{"seq":9,"kind":"task.cre`) //nolint:errcheck // test data
	fh.Close()

	st2 := New(path)
	f2, err := st2.Load()
	if err != nil {
		t.Fatal(err)
	}
	f2.Active().AddTask(board.Task{Title: "three"}) //nolint:errcheck // test data
	if err := st2.Save(f2); err != nil {
		t.Fatal(err)
	}
	f2.Active().AddTask(board.Task{Title: "four"}) //nolint:errcheck // test data
	if err := st2.Save(f2); err != nil {
		t.Fatal(err)
	}
	f3, err := New(path).Load()
	if err != nil {
		t.Fatalf("log unreadable after appending past a torn line: %v", err)
	}
	if got := len(f3.Active().Tasks); got != 4 {
		t.Errorf("tasks after torn-tail repair = %d, want 4", got)
	}
}

func TestCompactRefusesToDropForeignEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	a := New(path)
	fa, _ := a.Load()
	fa.Active().AddTask(board.Task{Title: "from a"}) //nolint:errcheck // test data
	a.Save(fa)                                       //nolint:errcheck // test data

	b := New(path)
	fb, _ := b.Load()
	fb.Active().AddTask(board.Task{Title: "from b"}) //nolint:errcheck // test data
	b.Save(fb)                                       //nolint:errcheck // test data

	// A has not seen b's event; compacting must not fold it away.
	if err := a.Compact(fa); !errors.Is(err, ErrStale) {
		t.Fatalf("compact with foreign events = %v, want errStale", err)
	}
	fa, _ = a.Load()
	if err := a.Compact(fa); err != nil {
		t.Fatal(err)
	}
	f, _ := New(path).Load()
	if len(f.Active().Tasks) != 2 {
		t.Errorf("tasks after compact = %d, want 2", len(f.Active().Tasks))
	}

	// B, still holding the old sequence numbers, appends after A's compaction:
	// its event must get a fresh number and survive a reload.
	fb.Active().AddComment(1, "late comment") //nolint:errcheck // test data
	if err := b.Save(fb); err != nil {
		t.Fatal(err)
	}
	f, err := New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.Active().Task(1).Comments); got != 1 {
		t.Errorf("comment appended after a foreign compaction was lost (%d comments)", got)
	}
}

func TestBootstrapDoesNotArchiveFromCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	old := `{"version":2,"boards":[{"id":"main","name":"Main","tasks":[
		{"id":1,"column":"done","title":"old","created_at":"2026-07-01T09:00:00Z","updated_at":"2026-07-05T09:00:00Z","archived_at":"2026-07-10T09:00:00Z"}]}]}`
	os.WriteFile(path, []byte(old), 0o644) //nolint:errcheck // test data
	st := New(path)
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
	path := filepath.Join(t.TempDir(), "nested", "board.json")
	st := New(path)
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Boards) != 1 || len(f.Boards[0].Tasks) != 0 {
		t.Fatal("missing file should yield one empty board")
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
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"priority": "high"`) || !strings.Contains(string(data), `"version": 2`) {
		t.Errorf("unexpected file contents:\n%s", data)
	}
	if st.ChangedOnDisk() {
		t.Error("file should not count as changed right after save")
	}
	time.Sleep(10 * time.Millisecond)
	os.Chtimes(path, time.Now(), time.Now().Add(time.Second))
	if !st.ChangedOnDisk() {
		t.Error("external modification should be detected")
	}
}

func TestStoreMigratesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	v1 := `{"version":1,"tasks":[
		{"id":"abc","status":"todo","title":"buy milk","description":"strawberry","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
		{"id":"def","status":"in_progress","title":"write code"},
		{"id":"ghi","status":"done","title":"stay cool"}]}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := New(path).Load()
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
}

func TestStoreRepairsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	if _, err := New(write("corrupt.json", "{nope")).Load(); err == nil {
		t.Error("corrupt file should fail")
	}
	if _, err := New(write("newer.json", `{"version": 99}`)).Load(); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer file error = %v", err)
	}
	f, err := New(write("sparse.json", `{"version":2,"boards":[{"name":"X","tasks":[
		{"title":"a","column":"todo"},{"id":5,"title":"b","column":"bogus","due":"garbage"},{"id":5,"title":"c"}]}]}`)).Load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	if b.ID != "x" || len(b.Columns) != 3 {
		t.Errorf("board not normalised: %+v", b)
	}
	ids := idsOf(b.Tasks)
	if ids[1] != 5 || ids[0] == 0 || ids[2] == 0 || ids[0] == ids[2] || ids[2] == 5 {
		t.Errorf("ids not repaired: %v", ids)
	}
	if b.Tasks[1].Column != "todo" || b.Tasks[1].Due != "" {
		t.Errorf("bad column/due not repaired: %+v", b.Tasks[1])
	}
	if b.NextID <= 5 {
		t.Errorf("next id = %d", b.NextID)
	}
}

func TestDefaultPaths(t *testing.T) {
	t.Setenv("KANCLI_FILE", "/tmp/custom.json")
	if p, _ := DefaultPath(); p != "/tmp/custom.json" {
		t.Errorf("KANCLI_FILE path = %q", p)
	}
	t.Setenv("KANCLI_FILE", "")
	t.Setenv("XDG_DATA_HOME", "/data")
	if p, _ := DefaultPath(); p != filepath.Join("/data", "kancli", "board.json") {
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

func idsOf(ts []board.Task) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func TestReviewReport(t *testing.T) {
	f := board.DemoFile()
	st := New("")
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

func sumDone(ws []board.WeekCount) int {
	n := 0
	for _, w := range ws {
		n += w.Done
	}
	return n
}
