package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestBoard() *Board {
	return sampleFile().Boards[0]
}

func TestAddTaskAssignsSequentialIDs(t *testing.T) {
	b := newBoard("Test")
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

func idsOf(ts []Task) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func TestUpdateTaskRecordsChanges(t *testing.T) {
	b := newTestBoard()
	u := *b.Task(3)
	u.Title = "Buy oat milk"
	u.Priority = priorityHigh
	u.Due = "2030-01-01"
	u.Labels = []string{"Home", "shop", "home"}
	u.Assignee = "me"
	if err := b.UpdateTask(u); err != nil {
		t.Fatal(err)
	}
	got := b.Task(3)
	if got.Title != "Buy oat milk" || got.Priority != priorityHigh || got.Due != "2030-01-01" || got.Assignee != "me" {
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
	f := newFile()
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
	for p := Priority(0); p < numPriorities; p++ {
		data, _ := json.Marshal(p)
		var back Priority
		if err := json.Unmarshal(data, &back); err != nil || back != p {
			t.Errorf("round trip %v -> %s -> %v (%v)", p, data, back, err)
		}
	}
	if p, err := parsePriority("HI"); err != nil || p != priorityHigh {
		t.Errorf("parsePriority(HI) = %v, %v", p, err)
	}
	if _, err := parsePriority("banana"); err == nil {
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
		got, err := parseDue(in, now)
		if err != nil || got != want {
			t.Errorf("parseDue(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseDue("someday", now); err == nil {
		t.Error("garbage should fail")
	}
	label, state := dueInfo(Task{Due: "2026-08-30"}, now)
	if label != "3d overdue" || state != dueOverdue {
		t.Errorf("overdue = %q, %v", label, state)
	}
	if label, state := dueInfo(Task{Due: "2026-09-02"}, now); label != "today" || state != dueToday {
		t.Errorf("today = %q, %v", label, state)
	}
	if label, _ := dueInfo(Task{Due: "2026-09-04"}, now); label != "in 2d" {
		t.Errorf("in 2d = %q", label)
	}
	if label, _ := dueInfo(Task{Due: "2026-11-01"}, now); label != "Nov 1" {
		t.Errorf("later = %q", label)
	}
}

func TestQuery(t *testing.T) {
	b := newTestBoard()
	now := timeNow()
	count := func(q string) int {
		pq := parseQuery(q)
		n := 0
		for _, task := range b.Live() {
			if pq.matches(b, task, now) {
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
	if !parseQuery("").empty() || parseQuery("x").empty() {
		t.Error("empty() is wrong")
	}
}

func TestSortTasks(t *testing.T) {
	b := newTestBoard()
	ts := b.TasksIn("todo")
	sortTasks(ts, sortPriority)
	if ts[0].ID != 1 || ts[len(ts)-1].Priority != priorityNone {
		t.Errorf("priority order = %v", idsOf(ts))
	}
	sortTasks(ts, sortDue)
	if ts[0].ID != 2 || ts[len(ts)-1].Due != "" {
		t.Errorf("due order = %v", idsOf(ts))
	}
	sortTasks(ts, sortTitle)
	if ts[0].Title != "Buy milk" {
		t.Errorf("title order = %v", ts[0].Title)
	}
	if sortManual.next() != sortPriority || sortTitle.next() != sortManual {
		t.Error("sort cycling is wrong")
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{"In Progress": "in_progress", "  QA / Review ": "qa_review", "": "item", "Été!": "été"}
	for in, want := range cases {
		if got := slug(in); got != want {
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
	if err := json.Unmarshal([]byte(`{"title":"x","priority":""}`), &task); err != nil || task.Priority != priorityNone {
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
	if got := daysBetween(springForward, next); got != 1 {
		t.Errorf("spring forward: daysBetween = %d, want 1", got)
	}
	if got := daysBetween(next, springForward); got != -1 {
		t.Errorf("spring forward reversed: daysBetween = %d, want -1", got)
	}
	label, state := dueInfo(Task{Due: "2026-03-08"}, next)
	if state != dueOverdue || label != "1d overdue" {
		t.Errorf("day after DST: %q %v", label, state)
	}
	fallBack := time.Date(2026, 11, 1, 12, 0, 0, 0, loc)
	if got := daysBetween(fallBack, fallBack.AddDate(0, 0, 1)); got != 1 {
		t.Errorf("fall back: daysBetween = %d, want 1", got)
	}
}

func TestPSString(t *testing.T) {
	if got := psString(`Fix $(Remove-Item x) it's "bad"`); got != `'Fix $(Remove-Item x) it''s "bad"'` {
		t.Errorf("psString = %s", got)
	}
}
