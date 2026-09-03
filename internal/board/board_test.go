package board

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestBoard() *Board {
	return SampleFile().Boards[0]
}

func idsOf(ts []Task) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func TestAddTaskAssignsSequentialIDs(t *testing.T) {
	b := NewBoard("Test")
	a, err := b.AddTask(Task{Title: "  first  "})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := b.AddTask(Task{Title: "second", Column: "done"})
	if a.ID != 1 || c.ID != 2 {
		t.Errorf("ids = %d, %d; want 1, 2", a.ID, c.ID)
	}
	if a.Title != "first" || a.Column != "todo" || c.Column != "done" {
		t.Errorf("unexpected tasks: %+v %+v", a, c)
	}
	if a.CreatedAt.IsZero() || len(a.History) != 1 {
		t.Error("created task should have a timestamp and a history entry")
	}
	if _, err := b.AddTask(Task{Title: " "}); err == nil {
		t.Error("empty title should be rejected")
	}
	if _, err := b.AddTask(Task{Title: "x", Column: "nope"}); err == nil {
		t.Error("unknown column should be rejected")
	}
}

func TestMoveReorderArchive(t *testing.T) {
	b := newTestBoard()
	if err := b.MoveTask(3, "done"); err != nil {
		t.Fatal(err)
	}
	got := b.TasksIn("done")
	if got[len(got)-1].ID != 3 {
		t.Errorf("moved task should be last in Done, got %v", got)
	}
	if last := b.Task(3).History[len(b.Task(3).History)-1].Text; !strings.Contains(last, "Moved from To Do to Done") {
		t.Errorf("history = %q", last)
	}
	if err := b.MoveTask(3, "nope"); err == nil {
		t.Error("move to an unknown column should fail")
	}

	// Reorder within To Do: 1,2,4 -> move 2 up.
	if !b.ReorderTask(2, -1) {
		t.Fatal("reorder should succeed")
	}
	ids := idsOf(b.TasksIn("todo"))
	if !slices.Equal(ids, []int{2, 1, 4}) {
		t.Errorf("after reorder = %v", ids)
	}
	if b.ReorderTask(2, -1) {
		t.Error("reordering the first task up should be a no-op")
	}

	if !b.ArchiveTask(1) || b.Task(1).ArchivedAt == nil {
		t.Fatal("archive failed")
	}
	if slices.Contains(idsOf(b.TasksIn("todo")), 1) {
		t.Error("archived task still on the board")
	}
	if len(b.ArchivedTasks()) != 1 {
		t.Errorf("archived = %d", len(b.ArchivedTasks()))
	}
	if !b.RestoreTask(1) || b.Task(1).Archived() {
		t.Error("restore failed")
	}
	if n := b.ArchiveDone(); n != 3 {
		t.Errorf("ArchiveDone = %d, want 3", n)
	}
	if b.CountIn("done") != 0 {
		t.Error("Done should be empty after archiving")
	}
	if !b.DeleteTask(2) || b.Task(2) != nil {
		t.Error("delete failed")
	}
}

func TestUpdateTaskRecordsChanges(t *testing.T) {
	b := newTestBoard()
	u := *b.Task(3)
	u.Title = "Buy oat milk"
	u.Priority = PriorityHigh
	u.Due = "2030-01-01"
	u.Labels = []string{"Home", "shop", "home"}
	u.Assignee = "me"
	if err := b.UpdateTask(u); err != nil {
		t.Fatal(err)
	}
	got := b.Task(3)
	if got.Title != "Buy oat milk" || got.Priority != PriorityHigh || got.Due != "2030-01-01" || got.Assignee != "me" {
		t.Errorf("update not applied: %+v", got)
	}
	if !slices.Equal(got.Labels, []string{"home", "shop"}) {
		t.Errorf("labels = %v", got.Labels)
	}
	last := got.History[len(got.History)-1].Text
	for _, want := range []string{"renamed", "priority high", "due 2030-01-01", "labels home, shop", "assigned to me"} {
		if !strings.Contains(last, want) {
			t.Errorf("history %q missing %q", last, want)
		}
	}
	u.Title = ""
	if err := b.UpdateTask(u); err == nil {
		t.Error("empty title should be rejected")
	}
}

func TestChecklistCommentsAttachments(t *testing.T) {
	b := newTestBoard()
	if err := b.AddChecklistItem(3, "one"); err != nil {
		t.Fatal(err)
	}
	b.AddChecklistItem(3, "two")
	b.ToggleChecklistItem(3, 1)
	done, total := b.Task(3).ChecklistProgress()
	if done != 1 || total != 2 {
		t.Errorf("progress = %d/%d", done, total)
	}
	b.RemoveChecklistItem(3, 0)
	if len(b.Task(3).Checklist) != 1 || b.Task(3).Checklist[0].Text != "two" {
		t.Errorf("checklist = %v", b.Task(3).Checklist)
	}
	if err := b.AddComment(3, "  "); err == nil {
		t.Error("blank comment should be rejected")
	}
	b.AddComment(3, "hello")
	if len(b.Task(3).Comments) != 1 {
		t.Error("comment not added")
	}
	b.AddAttachment(3, "https://example.com")
	b.RemoveAttachment(3, 0)
	if len(b.Task(3).Attachments) != 0 {
		t.Error("attachment not removed")
	}
}

func TestColumns(t *testing.T) {
	b := newTestBoard()
	col, err := b.AddColumn("Review", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if col.ID != "review" || col.WIPLimit != 3 || col.Color == "" {
		t.Errorf("column = %+v", col)
	}
	if _, err := b.AddColumn("review", "", 0); err == nil {
		t.Error("duplicate column name should be rejected")
	}
	if b.Column("rev") == nil || b.Column("REVIEW") == nil || b.Column("in") == nil {
		t.Error("column lookup by prefix/name failed")
	}
	if !b.MoveColumn("review", -1) || b.Columns[2].ID != "review" {
		t.Error("column move failed")
	}
	if err := b.UpdateColumn("review", "QA", "99", 0); err != nil || b.Column("qa").Name != "QA" {
		t.Errorf("update column: %v", err)
	}
	// Deleting a column with tasks moves them.
	n := b.CountIn("in_progress")
	if err := b.RemoveColumn("in_progress", "todo"); err != nil {
		t.Fatal(err)
	}
	if b.Column("in_progress") != nil || b.CountIn("todo") != 4+n {
		t.Error("tasks were not moved on column delete")
	}
	if b.WIPExceeded("todo") {
		t.Error("todo has no WIP limit")
	}
	b.Column("todo").WIPLimit = 2
	if !b.WIPExceeded("todo") {
		t.Error("todo should exceed its WIP limit")
	}
	for len(b.Columns) > 1 {
		if err := b.RemoveColumn(b.Columns[0].ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.RemoveColumn(b.Columns[0].ID, ""); err == nil {
		t.Error("the last column must not be removable")
	}
}

func TestFileBoards(t *testing.T) {
	f := NewFile()
	b, err := f.AddBoard("Work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.AddBoard("work"); err == nil {
		t.Error("duplicate board name should be rejected")
	}
	if f.Board("WORK") != b || f.Board("work") != b {
		t.Error("board lookup by name failed")
	}
	f.ActiveBoard = b.ID
	if err := f.RemoveBoard(b.ID); err != nil {
		t.Fatal(err)
	}
	if f.ActiveBoard != f.Boards[0].ID {
		t.Error("active board should fall back after delete")
	}
	if err := f.RemoveBoard(f.Boards[0].ID); err == nil {
		t.Error("the last board must not be removable")
	}
}

func TestPriorityJSON(t *testing.T) {
	for p := Priority(0); p < NumPriorities; p++ {
		data, _ := json.Marshal(p)
		var back Priority
		if err := json.Unmarshal(data, &back); err != nil || back != p {
			t.Errorf("round trip %v -> %s -> %v (%v)", p, data, back, err)
		}
	}
	if p, err := ParsePriority("HI"); err != nil || p != PriorityHigh {
		t.Errorf("parsePriority(HI) = %v, %v", p, err)
	}
	if _, err := ParsePriority("banana"); err == nil {
		t.Error("bad priority should fail")
	}
}

func TestParseDue(t *testing.T) {
	now := time.Date(2026, 9, 2, 15, 0, 0, 0, time.Local) // a Wednesday
	cases := map[string]string{
		"":           "",
		"none":       "",
		"today":      "2026-09-02",
		"tomorrow":   "2026-09-03",
		"2026-12-25": "2026-12-25",
		"2026/12/25": "2026-12-25",
		"+3d":        "2026-09-05",
		"+1w":        "2026-09-09",
		"+1m":        "2026-10-02",
		"fri":        "2026-09-04",
		"Wednesday":  "2026-09-09",
		"01-15":      "2027-01-15",
	}
	for in, want := range cases {
		got, err := ParseDue(in, now)
		if err != nil || got != want {
			t.Errorf("parseDue(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseDue("someday", now); err == nil {
		t.Error("garbage should fail")
	}
	label, state := DueInfo(Task{Due: "2026-08-30"}, now)
	if label != "3d overdue" || state != DueOverdue {
		t.Errorf("overdue = %q, %v", label, state)
	}
	if label, state := DueInfo(Task{Due: "2026-09-02"}, now); label != "today" || state != DueToday {
		t.Errorf("today = %q, %v", label, state)
	}
	if label, _ := DueInfo(Task{Due: "2026-09-04"}, now); label != "in 2d" {
		t.Errorf("in 2d = %q", label)
	}
	if label, _ := DueInfo(Task{Due: "2026-11-01"}, now); label != "Nov 1" {
		t.Errorf("later = %q", label)
	}
}

func TestQuery(t *testing.T) {
	b := newTestBoard()
	now := Now()
	count := func(q string) int {
		pq := ParseQuery(q)
		n := 0
		for _, task := range b.Live() {
			if pq.Matches(b, task, now) {
				n++
			}
		}
		return n
	}
	cases := map[string]int{
		"":             8,
		"milk":         1,
		"#5":           1,
		"@sam":         2,
		"+docs":        1,
		"label:bug":    1,
		"p:urgent":     1,
		"due:today":    1,
		"due:overdue":  1,
		"due:none":     3,
		"col:done":     2,
		"c:in":         2,
		"c:in +review": 1,
		"cli export":   1, // matches description words
		"works":        1,
	}
	for q, want := range cases {
		if got := count(q); got != want {
			t.Errorf("query %q matched %d, want %d", q, got, want)
		}
	}
	if !ParseQuery("").Empty() || ParseQuery("x").Empty() {
		t.Error("empty() is wrong")
	}
}

func TestSortTasks(t *testing.T) {
	b := newTestBoard()
	ts := b.TasksIn("todo")
	SortTasks(ts, SortPriority)
	if ts[0].ID != 1 || ts[len(ts)-1].Priority != PriorityNone {
		t.Errorf("priority order = %v", idsOf(ts))
	}
	SortTasks(ts, SortDue)
	if ts[0].ID != 2 || ts[len(ts)-1].Due != "" {
		t.Errorf("due order = %v", idsOf(ts))
	}
	SortTasks(ts, SortTitle)
	if ts[0].Title != "Buy milk" {
		t.Errorf("title order = %v", ts[0].Title)
	}
	if SortManual.Next() != SortPriority || SortTitle.Next() != SortManual {
		t.Error("sort cycling is wrong")
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{"In Progress": "in_progress", "  QA / Review ": "qa_review", "": "item", "Été!": "été"}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoveColumnKeepsArchivedTasks(t *testing.T) {
	b := newTestBoard()
	b.ArchiveDone() // #7 and #8 archived, still in "done"
	if err := b.RemoveColumn("done", "todo"); err != nil {
		t.Fatal(err)
	}
	if got := len(b.ArchivedTasks()); got != 2 {
		t.Fatalf("archived tasks after column delete = %d, want 2", got)
	}
	for _, task := range b.ArchivedTasks() {
		if task.Column != "todo" {
			t.Errorf("archived task %d left in %q", task.ID, task.Column)
		}
	}
	if b.AllIn("todo") != 6 || b.CountIn("todo") != 4 {
		t.Errorf("AllIn/CountIn = %d/%d", b.AllIn("todo"), b.CountIn("todo"))
	}
}

func TestEmptyPriorityDecodesAsNone(t *testing.T) {
	var task Task
	if err := json.Unmarshal([]byte(`{"title":"x","priority":""}`), &task); err != nil || task.Priority != PriorityNone {
		t.Errorf("empty priority = %v, %v", task.Priority, err)
	}
}

func TestDaysBetweenAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	springForward := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
	next := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	if got := DaysBetween(springForward, next); got != 1 {
		t.Errorf("spring forward: daysBetween = %d, want 1", got)
	}
	if got := DaysBetween(next, springForward); got != -1 {
		t.Errorf("spring forward reversed: daysBetween = %d, want -1", got)
	}
	label, state := DueInfo(Task{Due: "2026-03-08"}, next)
	if state != DueOverdue || label != "1d overdue" {
		t.Errorf("day after DST: %q %v", label, state)
	}
	fallBack := time.Date(2026, 11, 1, 12, 0, 0, 0, loc)
	if got := DaysBetween(fallBack, fallBack.AddDate(0, 0, 1)); got != 1 {
		t.Errorf("fall back: daysBetween = %d, want 1", got)
	}
}

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
	base := NewFile()
	base.rec.actor = "test"
	baseline := stateOf(base)

	f := NewFile()
	f.rec.actor = "test"
	b := f.Active()
	t1, _ := b.AddTask(Task{Title: "one", Labels: []string{"a"}})
	t2, _ := b.AddTask(Task{Title: "two", Priority: PriorityHigh, Due: "2030-01-01"})
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

	events := f.Pending()
	if len(events) < 25 {
		t.Fatalf("expected many events, got %d", len(events))
	}
	for i := range events {
		events[i].Seq = int64(i + 1)
	}
	if stateOf(base) != baseline {
		t.Fatal("recording must not touch another file")
	}
	if err := base.Replay(events); err != nil {
		t.Fatal(err)
	}
	if got, want := stateOf(base), stateOf(f); got != want {
		t.Errorf("replayed state differs:\n got %s\nwant %s", got, want)
	}
	if base.LastSeq != int64(len(events)) {
		t.Errorf("LastSeq = %d", base.LastSeq)
	}
	// Replaying again is a no-op thanks to LastSeq.
	if err := base.Replay(events); err != nil || stateOf(base) != stateOf(f) {
		t.Error("second replay changed the state")
	}
	// Every event has a description.
	for _, e := range events {
		if e.Describe(f) == "" {
			t.Errorf("event %s has no description", e.Kind)
		}
	}
}

func TestRelevanceAndSimilarity(t *testing.T) {
	b := newTestBoard()
	q := ParseQuery("release")
	scores := map[string]int{}
	for _, task := range b.Live() {
		scores[task.Title] = Relevance(q, task)
	}
	if scores["Write the release notes"] <= scores["Buy milk"] || scores["Buy milk"] != 0 {
		t.Errorf("relevance scores = %v", scores)
	}
	if sim := Similarity("Write the release notes", "write release notes"); sim < SimilarThreshold {
		t.Errorf("reworded duplicate similarity = %.2f", sim)
	}
	if sim := Similarity("Buy milk", "Plan the team offsite"); sim >= SimilarThreshold {
		t.Errorf("unrelated similarity = %.2f", sim)
	}
	got := SimilarTasks(b, "Fix flaky resize test", 0, 3)
	if len(got) != 1 || got[0].Task.ID != 2 {
		t.Errorf("similarTasks = %+v", got)
	}
	if Similarity("", "x") != 0 || Similarity("same", "same") != 1 {
		t.Error("edge cases")
	}
}

func TestEventsEqualAfterJSONRoundTrip(t *testing.T) {
	e := Event{Seq: 3, At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Board: "b", Kind: EvTaskMoved, Task: 7, From: "a", To: "c", Actor: "ui"}
	data, _ := json.Marshal(e)
	var back Event
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(e, back) {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

func TestTaskIndexStaysCorrect(t *testing.T) {
	b := newTestBoard()
	if b.Task(3) == nil || b.taskIndex(3) != 2 {
		t.Fatal("index lookup failed")
	}
	b.DeleteTask(2)
	if b.taskIndex(3) != 1 || b.Task(2) != nil || b.taskIndex(99) != -1 {
		t.Error("index not updated after delete")
	}
	b.MoveTask(1, "done") //nolint:errcheck // test data
	if b.taskIndex(1) != len(b.Tasks)-1 {
		t.Error("index not updated after move")
	}
	// Direct edits to the slice are still handled.
	b.Tasks = append(b.Tasks, Task{ID: 500, Title: "direct", Column: "todo"})
	if b.Task(500) == nil {
		t.Error("index should self-heal after a direct append")
	}
	b.Tasks = b.Tasks[1:]
	if b.Task(b.Tasks[0].ID) == nil || b.taskIndex(b.Tasks[0].ID) != 0 {
		t.Error("index should self-heal after a direct removal")
	}
}

func TestWeeklyThroughputIgnoresTimeZone(t *testing.T) {
	b := newTestBoard()
	at := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC) // a Wednesday
	events := []Event{
		{Seq: 1, At: at, Board: b.ID, Kind: EvTaskCreated, Task: 1, To: "todo", Data: MustJSON(Task{ID: 1, Title: "x", Column: "todo"})},
		{Seq: 2, At: at.Add(time.Hour), Board: b.ID, Kind: EvTaskMoved, Task: 1, From: "todo", To: "done"},
	}
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.Local)
	st := ComputeStats(b, events, now, 7)
	if len(st.Weeks) != 1 || st.Weeks[0].Done != 1 || st.Weeks[0].Created != 1 {
		t.Errorf("weeks = %+v", st.Weeks)
	}
}

func TestDecodeFileWithoutVersionOrWithNullBoard(t *testing.T) {
	f, err := Decode([]byte(`{"active_board":"work","boards":[{"id":"work","name":"Work","tasks":[{"id":1,"title":"keep","column":"todo"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Boards) != 1 || f.Boards[0].Name != "Work" || len(f.Boards[0].Tasks) != 1 {
		t.Errorf("version-less v2 file was not decoded as v2: %+v", f.Boards[0])
	}
	f, err = Decode([]byte(`{"version":2,"boards":[null,{"name":"Real"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Boards) != 1 || f.Boards[0].Name != "Real" {
		t.Errorf("null board not dropped: %+v", f.Boards)
	}
	f, err = Decode([]byte(`{"version":2,"boards":[null]}`))
	if err != nil || len(f.Boards) != 1 {
		t.Errorf("all-null boards should yield a default board: %v %v", f, err)
	}
}

func TestLinks(t *testing.T) {
	b := newTestBoard()
	if err := b.AddLink(1, LinkBlocks, 2); err != nil {
		t.Fatal(err)
	}
	if err := b.AddLink(1, LinkBlocks, 2); err != nil {
		t.Fatal("duplicate link should be ignored")
	}
	if got := len(b.Task(1).Links); got != 1 {
		t.Errorf("links on #1 = %d, want 1", got)
	}
	if err := b.AddLink(2, LinkBlocks, 1); err == nil {
		t.Error("cycle should be refused")
	}
	if err := b.AddLink(1, LinkBlocks, 1); err == nil {
		t.Error("self link should be refused")
	}
	if err := b.AddLink(1, LinkBlocks, 99); err == nil {
		t.Error("link to a missing task should be refused")
	}
	if !b.IsBlocked(2) || b.IsBlocked(1) {
		t.Error("#2 should be blocked by #1")
	}
	rels := b.Relations(2)
	if len(rels) != 1 || rels[0].Label != "blocked by" || rels[0].Task.ID != 1 || rels[0].Outgoing {
		t.Errorf("relations of #2 = %+v", rels)
	}
	// Finishing the blocker unblocks.
	b.MoveTask(1, "done") //nolint:errcheck // test data
	if b.IsBlocked(2) {
		t.Error("a blocker in Done should not block")
	}

	// Subtasks and progress.
	b.AddLink(3, LinkSubtaskOf, 4) //nolint:errcheck // test data
	b.AddLink(5, LinkSubtaskOf, 4) //nolint:errcheck // test data
	if p := b.Parent(3); p == nil || p.ID != 4 {
		t.Error("parent lookup failed")
	}
	if done, total := b.SubtaskProgress(4); total != 2 || done != 0 {
		t.Errorf("progress = %d/%d", done, total)
	}
	b.ArchiveTask(5)
	if done, _ := b.SubtaskProgress(4); done != 1 {
		t.Error("archived subtask should count as finished")
	}
	if err := b.AddLink(4, LinkSubtaskOf, 3); err == nil {
		t.Error("subtask cycle should be refused")
	}

	// Relates is symmetric and de-duplicated.
	b.AddLink(6, LinkRelates, 7) //nolint:errcheck // test data
	b.AddLink(7, LinkRelates, 6) //nolint:errcheck // test data
	if len(b.Task(6).Links)+len(b.Task(7).Links) != 1 {
		t.Error("relates should be stored once")
	}
	if n := b.RemoveLinksBetween(7, 6); n != 1 || len(b.Relations(6)) != 0 {
		t.Errorf("RemoveLinksBetween removed %d", n)
	}

	// Deleting a task drops links pointing at it.
	b.DeleteTask(4)
	if len(b.Task(3).Links) != 0 {
		t.Error("links to a deleted task should be dropped")
	}
	if b.BlockedCount() != 0 {
		t.Errorf("blocked count = %d", b.BlockedCount())
	}
}

func TestLinkSpecAndMentions(t *testing.T) {
	cases := []struct {
		word     string
		from, to int
		kind     LinkKind
	}{
		{"blocks", 1, 2, LinkBlocks}, {"blocked-by", 2, 1, LinkBlocks}, {"subtask-of", 1, 2, LinkSubtaskOf},
		{"parent-of", 2, 1, LinkSubtaskOf}, {"relates", 1, 2, LinkRelates}, {"Blocked By", 2, 1, LinkBlocks},
	}
	for _, c := range cases {
		from, kind, to, err := ParseLinkSpec(1, c.word, 2)
		if err != nil || from != c.from || to != c.to || kind != c.kind {
			t.Errorf("ParseLinkSpec(1, %q, 2) = %d %s %d %v", c.word, from, kind, to, err)
		}
	}
	if _, _, _, err := ParseLinkSpec(1, "hates", 2); err == nil {
		t.Error("unknown kind should fail")
	}
	if got := Mentions("see #3 and #12, not &#4 or #x"); len(got) != 2 || got[0] != 3 || got[1] != 12 {
		t.Errorf("Mentions = %v", got)
	}

	b := newTestBoard()
	tk, _ := b.AddTask(Task{Title: "follow up", Description: "after #1 and #2 land"})
	rels := b.Relations(tk.ID)
	if len(rels) != 2 || rels[0].Label != "relates to" {
		t.Errorf("mentions should create relates links: %+v", rels)
	}
	b.AddComment(3, "blocked on #1") //nolint:errcheck // test data
	if len(b.Relations(3)) != 1 {
		t.Error("comment mention should link")
	}

	// Links replay from events exactly.
	f := NewFile()
	nb := f.Boards[0]
	fixed := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	nb.SetClock(func() time.Time { return fixed })
	nb.AddTask(Task{Title: "a"})  //nolint:errcheck // test data
	nb.AddTask(Task{Title: "b"})  //nolint:errcheck // test data
	nb.AddLink(1, LinkBlocks, 2)  //nolint:errcheck // test data
	nb.AddLink(2, LinkRelates, 1) //nolint:errcheck // test data
	nb.RemoveLink(2, LinkRelates, 1)
	events := f.Pending()
	replayed := NewFile()
	for i := range events {
		events[i].Seq = int64(i + 1)
	}
	if err := replayed.Replay(events); err != nil {
		t.Fatal(err)
	}
	if stateOf(replayed) != stateOf(f) {
		t.Errorf("replayed links differ:\n%s\n%s", stateOf(replayed), stateOf(f))
	}

	q := ParseQuery("blocked:yes")
	if !q.Matches(nb, *nb.Task(2), Now()) || q.Matches(nb, *nb.Task(1), Now()) {
		t.Error("blocked:yes query")
	}
	if !ParseQuery("blocks:2").Matches(nb, *nb.Task(1), Now()) || !ParseQuery("has:links").Matches(nb, *nb.Task(2), Now()) {
		t.Error("blocks:/has: queries")
	}
}

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

func TestDescribeBoard(t *testing.T) {
	f := NewFile()
	f.Attach()
	b := f.Boards[0]
	if err := f.DescribeBoard(b.ID, "  Client work  "); err != nil || b.Description != "Client work" {
		t.Fatalf("describe: %q %v", b.Description, err)
	}
	if err := f.DescribeBoard("nope", "x"); err == nil {
		t.Error("unknown board should fail")
	}
	events := f.Pending()
	if len(events) != 1 || events[0].Kind != EvBoardDescribed || events[0].Text != "Client work" || events[0].Board != b.ID {
		t.Fatalf("events = %+v", events)
	}
	if s := events[0].Describe(f); !strings.Contains(s, "Client work") {
		t.Errorf("Describe = %q", s)
	}
	// Replay reproduces it, and clearing works.
	fresh := NewFile()
	fresh.Boards[0].ID = b.ID
	if err := fresh.Replay(events); err != nil || fresh.Boards[0].Description != "Client work" {
		t.Fatalf("replay: %q %v", fresh.Boards[0].Description, err)
	}
	// Describing with the same text again is a no-op: no second event.
	if err := f.DescribeBoard(b.ID, "Client work"); err != nil {
		t.Fatal(err)
	}
	if got := f.Pending(); len(got) != 0 {
		t.Errorf("re-describing with the same text emitted %+v", got)
	}
	// Newlines and runs of whitespace collapse to one line.
	if err := f.DescribeBoard(b.ID, "line one\n\tline  two"); err != nil || b.Description != "line one line two" {
		t.Fatalf("normalize: %q %v", b.Description, err)
	}
	if err := f.DescribeBoard(b.ID, ""); err != nil || b.Description != "" {
		t.Fatalf("clear: %q %v", b.Description, err)
	}
	// A whitespace-only description of a fresh board normalizes to "" and
	// so changes nothing and emits nothing.
	blank := NewFile()
	blank.Attach()
	if err := blank.DescribeBoard(blank.Boards[0].ID, "   "); err != nil {
		t.Fatal(err)
	}
	if got := blank.Pending(); len(got) != 0 {
		t.Errorf("whitespace-only describe emitted %+v", got)
	}
	// Survives a JSON round trip of the file.
	f.DescribeBoard(b.ID, "again") //nolint:errcheck // test data
	data, _ := json.Marshal(f)
	back, err := Decode(data)
	if err != nil || back.Boards[0].Description != "again" {
		t.Fatalf("round trip: %q %v", back.Boards[0].Description, err)
	}
}
