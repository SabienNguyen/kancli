package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// stateOf returns a comparable projection of a file's state.
func stateOf(f *File) string {
	type row struct {
		Board   string
		Columns []Column
		Tasks   []Task
		Active  string
	}
	var rows []row
	for _, b := range f.Boards {
		rows = append(rows, row{b.ID, b.Columns, b.Tasks, f.ActiveBoard})
	}
	data, _ := json.Marshal(rows)
	return string(data)
}

func TestReplayReproducesEveryMutation(t *testing.T) {
	base := newFile()
	base.rec.actor = "test"
	baseline := stateOf(base)

	f := newFile()
	f.rec.actor = "test"
	b := f.Active()
	t1, _ := b.AddTask(Task{Title: "one", Labels: []string{"a"}})
	t2, _ := b.AddTask(Task{Title: "two", Priority: priorityHigh, Due: "2030-01-01"})
	b.AddTask(Task{Title: "three"}) //nolint:errcheck // test data
	u := *t1
	u.Title = "one edited"
	u.Assignee = "me"
	b.UpdateTask(u)                          //nolint:errcheck // test data
	b.MoveTask(t2.ID, "in_progress")         //nolint:errcheck // test data
	b.ReorderTask(3, -1)                     //nolint:errcheck // test data
	b.AddComment(t1.ID, "hello")             //nolint:errcheck // test data
	b.AddChecklistItem(t1.ID, "step")        //nolint:errcheck // test data
	b.AddChecklistItem(t1.ID, "step 2")      //nolint:errcheck // test data
	b.ToggleChecklistItem(t1.ID, 1)          //nolint:errcheck // test data
	b.RemoveChecklistItem(t1.ID, 0)          //nolint:errcheck // test data
	b.AddAttachment(t1.ID, "https://x.test") //nolint:errcheck // test data
	b.AddAttachment(t1.ID, "notes.txt")      //nolint:errcheck // test data
	b.RemoveAttachment(t1.ID, 0)             //nolint:errcheck // test data
	col, _ := b.AddColumn("Review", "99", 2)
	b.MoveColumn(col.ID, -1)              //nolint:errcheck // test data
	b.UpdateColumn(col.ID, "QA", "98", 3) //nolint:errcheck // test data
	b.MoveTask(t2.ID, col.ID)             //nolint:errcheck // test data
	b.RemoveColumn("in_progress", "todo") //nolint:errcheck // test data
	b.ArchiveTask(3)                      //nolint:errcheck // test data
	b.RestoreTask(3)                      //nolint:errcheck // test data
	b.DeleteTask(t1.ID)                   //nolint:errcheck // test data
	work, _ := f.AddBoard("Work")         //nolint:errcheck // test data
	work.AddTask(Task{Title: "on work"})  //nolint:errcheck // test data
	f.Activate(work.ID)                   //nolint:errcheck // test data
	f.RenameBoard(work.ID, "Office")      //nolint:errcheck // test data
	snap, _ := json.Marshal(*b)
	var restored Board
	json.Unmarshal(snap, &restored) //nolint:errcheck // test data
	restored.Tasks = restored.Tasks[:1]
	b.Replace(restored)
	other, _ := f.AddBoard("Temp")
	f.RemoveBoard(other.ID) //nolint:errcheck // test data

	events := f.pending()
	if len(events) < 25 {
		t.Fatalf("expected many events, got %d", len(events))
	}
	for i := range events {
		events[i].Seq = int64(i + 1)
	}
	if stateOf(base) != baseline {
		t.Fatal("recording must not touch another file")
	}
	if err := base.replay(events); err != nil {
		t.Fatal(err)
	}
	if got, want := stateOf(base), stateOf(f); got != want {
		t.Errorf("replayed state differs:\n got %s\nwant %s", got, want)
	}
	if base.LastSeq != int64(len(events)) {
		t.Errorf("LastSeq = %d", base.LastSeq)
	}
	// Replaying again is a no-op thanks to LastSeq.
	if err := base.replay(events); err != nil || stateOf(base) != stateOf(f) {
		t.Error("second replay changed the state")
	}
	// Every event has a description.
	for _, e := range events {
		if e.describe(f) == "" {
			t.Errorf("event %s has no description", e.Kind)
		}
	}
}

func TestStoreLogCompactionAndAsOf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.json")
	st := newStore(path)
	f, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	fixed := time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)
	timeNow = func() time.Time { return fixed }
	defer func() { timeNow = time.Now }()
	b.clock = timeNow
	if _, err := b.AddTask(Task{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := st.save(f); err != nil { // first save writes the base snapshot
		t.Fatal(err)
	}
	if !exists(path) || st.tailEvents() != 0 {
		t.Fatalf("first save should compact: exists=%v tail=%d", exists(path), st.tailEvents())
	}
	fixed = fixed.AddDate(0, 0, 3)
	b.AddTask(Task{Title: "second"}) //nolint:errcheck // test data
	if err := st.save(f); err != nil {
		t.Fatal(err)
	}
	if st.tailEvents() != 1 {
		t.Errorf("tail = %d, want 1", st.tailEvents())
	}
	fixed = fixed.AddDate(0, 0, 3)
	b.MoveTask(1, "done") //nolint:errcheck // test data
	st.save(f)            //nolint:errcheck // test data

	// A fresh store replays the tail on top of the snapshot.
	again, err := newStore(path).load()
	if err != nil {
		t.Fatal(err)
	}
	if stateOf(again) != stateOf(f) {
		t.Errorf("reloaded state differs:\n got %s\nwant %s", stateOf(again), stateOf(f))
	}

	// The full history is readable and ordered.
	events, err := st.events()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Kind != evTaskCreated || events[2].Kind != evTaskMoved {
		t.Errorf("events = %+v", events)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Error("events are not in sequence order")
		}
	}

	// Compaction archives the tail and keeps everything readable.
	if err := st.compact(f); err != nil {
		t.Fatal(err)
	}
	segs, _ := filepath.Glob(filepath.Join(st.archiveDir, "*.jsonl"))
	if len(segs) != 2 || exists(st.logPath) { // the first save compacted too
		t.Errorf("archive = %v, tail exists = %v", segs, exists(st.logPath))
	}
	events, _ = st.events()
	if len(events) != 3 {
		t.Errorf("events after compaction = %d", len(events))
	}
	snaps, _ := filepath.Glob(filepath.Join(st.snapDir, "*.json"))
	if len(snaps) != 3 { // empty base, first save, this compaction
		t.Errorf("snapshots = %v", snaps)
	}

	// As-of views.
	past, err := st.loadAsOf(time.Date(2026, 8, 2, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(past.Active().Tasks); got != 1 {
		t.Errorf("as of Aug 2: %d tasks, want 1", got)
	}
	mid, err := st.loadAsOf(time.Date(2026, 8, 5, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if got := mid.Active(); len(got.Tasks) != 2 || got.Task(1).Column != "todo" {
		t.Errorf("as of Aug 5: %d tasks, task 1 in %s", len(got.Tasks), got.Task(1).Column)
	}
	empty, err := st.loadAsOf(time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil || len(empty.Active().Tasks) != 0 {
		t.Errorf("as-of before any event should be an empty board: %v", err)
	}

	// A torn last line is tolerated; damage in the middle is not.
	b.AddTask(Task{Title: "third"}) //nolint:errcheck // test data
	st.save(f)                      //nolint:errcheck // test data
	fh, _ := os.OpenFile(st.logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString(`{"seq":99,"kind":"task.cre`) //nolint:errcheck // test data
	fh.Close()
	torn, err := newStore(path).load()
	if err != nil || torn.Active().Task(3) == nil {
		t.Errorf("torn tail should be dropped: %v", err)
	}
	os.WriteFile(st.logPath, []byte("{bad}\n{\"seq\":5,\"kind\":\"task.deleted\",\"task\":3}\n"), 0o644) //nolint:errcheck // test data
	if _, err := newStore(path).load(); err == nil {
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
	st := newStore(path)
	f, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Active().Tasks) != 2 || f.Active().Task(1).Column != "done" {
		t.Fatalf("state changed by bootstrap: %+v", f.Active().Tasks)
	}
	events, err := st.events()
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
	before, err := st.loadAsOf(created.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := before.Active(); len(got.Tasks) != 1 || got.Task(1).Column != "todo" {
		t.Errorf("as-of just after creation: %+v", got.Tasks)
	}
	st2 := newStore(path)
	if _, err := st2.load(); err != nil || st2.tailEvents() != 0 {
		t.Errorf("second load should find a compacted store: %v tail=%d", err, st2.tailEvents())
	}
}

func TestStatsFromEvents(t *testing.T) {
	f := demoFile()
	st := newStore("")
	if err := st.save(f); err != nil {
		t.Fatal(err)
	}
	events, _ := st.events()
	b := f.Active()
	now := timeNow()
	s := computeStats(b, events, now, 90)
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
	var inProg columnStay
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
	var feature labelStat
	for _, l := range s.Labels {
		if l.Label == "feature" {
			feature = l
		}
	}
	if feature.Done != 3 || feature.Open != 1 {
		t.Errorf("feature label = %+v", feature)
	}
	// The window trims the finished sample.
	short := computeStats(b, events, now, 7)
	if len(short.Finished) >= len(s.Finished) || len(short.Weeks) != 1 {
		t.Errorf("7 day window: finished %d weeks %d", len(short.Finished), len(short.Weeks))
	}
	if mean, median, p90 := summarize([]time.Duration{4, 1, 3, 2}); mean != 2 || median != 2 || p90 != 4 {
		t.Errorf("summarize = %v %v %v", mean, median, p90)
	}
	for d, want := range map[time.Duration]string{0: "0h", 30 * time.Minute: "30m", 5 * time.Hour: "5h", 50 * time.Hour: "2d 2h", 72 * time.Hour: "3d"} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func sumDone(ws []weekCount) int {
	n := 0
	for _, w := range ws {
		n += w.Done
	}
	return n
}

func TestReviewReport(t *testing.T) {
	f := demoFile()
	st := newStore("")
	st.save(f) //nolint:errcheck // test data
	events, _ := st.events()
	md := reviewReport(f.Active(), events, timeNow(), 30)
	for _, want := range []string{"# Demo · review of the last 30 days", "## Summary", "- Finished: **", "## Finished", "- [x] #", "## Needs attention", "overdue", "## Labels", "| feature |", "## Throughput"} {
		if !strings.Contains(md, want) {
			t.Errorf("review missing %q:\n%s", want, md)
		}
	}
}

func TestRelevanceAndSimilarity(t *testing.T) {
	b := newTestBoard()
	q := parseQuery("release")
	scores := map[string]int{}
	for _, task := range b.Live() {
		scores[task.Title] = relevance(q, task)
	}
	if scores["Write the release notes"] <= scores["Buy milk"] || scores["Buy milk"] != 0 {
		t.Errorf("relevance scores = %v", scores)
	}
	if sim := similarity("Write the release notes", "write release notes"); sim < similarThreshold {
		t.Errorf("reworded duplicate similarity = %.2f", sim)
	}
	if sim := similarity("Buy milk", "Plan the team offsite"); sim >= similarThreshold {
		t.Errorf("unrelated similarity = %.2f", sim)
	}
	got := similarTasks(b, "Fix flaky resize test", 0, 3)
	if len(got) != 1 || got[0].Task.ID != 2 {
		t.Errorf("similarTasks = %+v", got)
	}
	if similarity("", "x") != 0 || similarity("same", "same") != 1 {
		t.Error("edge cases")
	}
}

func TestSQLViews(t *testing.T) {
	sql := sqlViews("/tmp/state.json", []string{"/data/board.events/000000000001-000000000010.jsonl", "/data/board.events.jsonl"})
	for _, want := range []string{"CREATE OR REPLACE VIEW tasks", "read_json_auto('/tmp/state.json')", "'/data/board.events.jsonl'", "format = 'newline_delimited'", "VIEW cycle_times", "VIEW column_stays"} {
		if !strings.Contains(sql, want) {
			t.Errorf("views missing %q", want)
		}
	}
	empty := sqlViews("/tmp/s.json", nil)
	if !strings.Contains(empty, "WHERE false") {
		t.Error("empty event view should still exist")
	}
	if sqlLiteral("it's") != "'it''s'" {
		t.Error("sqlLiteral")
	}
	t.Setenv("KANCLI_DUCKDB", "/definitely/not/here")
	if _, err := runDuckDB("/definitely/not/here", empty, "SELECT 1", "box"); err == nil {
		t.Error("missing binary should fail")
	}
	if _, err := runDuckDB("/definitely/not/here", empty, "SELECT 1", "xml"); err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("bad format error = %v", err)
	}
}

func TestParseAsOf(t *testing.T) {
	now := time.Date(2026, 9, 2, 15, 0, 0, 0, time.Local)
	if got, err := parseAsOf("2026-08-25", now); err != nil || got.Format("2006-01-02 15:04") != "2026-08-25 23:59" {
		t.Errorf("date = %v, %v", got, err)
	}
	if got, err := parseAsOf("2026-08-25 14:00", now); err != nil || got.Hour() != 14 {
		t.Errorf("datetime = %v, %v", got, err)
	}
	if got, err := parseAsOf("-7d", now); err != nil || got.Day() != 26 {
		t.Errorf("relative = %v, %v", got, err)
	}
	if _, err := parseAsOf("whenever", now); err == nil {
		t.Error("garbage should fail")
	}
}

func TestEventsEqualAfterJSONRoundTrip(t *testing.T) {
	e := Event{Seq: 3, At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Board: "b", Kind: evTaskMoved, Task: 7, From: "a", To: "c", Actor: "ui"}
	data, _ := json.Marshal(e)
	var back Event
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(e, back) {
		t.Errorf("round trip: %v %+v", err, back)
	}
}
