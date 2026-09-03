package board

import (
	"encoding/json"
	"testing"
)

// twoBoards builds a file with the default "main" board plus a "work"
// board, both created the way a replay creates them, so ids are stable.
func twoBoards(t *testing.T) (*File, *Board, *Board) {
	t.Helper()
	f := NewFile()
	main := f.Active()
	work, err := f.AddBoard("Work")
	if err != nil {
		t.Fatalf("AddBoard: %v", err)
	}
	if main.ID != "main" || work.ID != "work" {
		t.Fatalf("unexpected board ids %q/%q", main.ID, work.ID)
	}
	return f, main, work
}

func relLabels(rels []Relation) []string {
	var out []string
	for _, r := range rels {
		out = append(out, r.Label+" "+Ref{Board: r.Board, ID: r.Task.ID}.String())
	}
	return out
}

func TestCrossBoardLinkRoundTrip(t *testing.T) {
	f, main, work := twoBoards(t)

	// Work has one more column than main, so "done" is not its last one:
	// finishing must be judged by each task's own board.
	if _, err := work.AddColumn("Review", "", 0); err != nil {
		t.Fatalf("AddColumn: %v", err)
	}

	goalTask, _ := main.AddTask(Task{Title: "Ship goal boards"})
	oneTask, _ := work.AddTask(Task{Title: "ticket one"})
	twoTask, _ := work.AddTask(Task{Title: "ticket two"})
	// Moving a task rewrites the slice, so hold on to the ids, not the
	// pointers.
	goal := Ref{ID: goalTask.ID}
	one := Ref{ID: oneTask.ID}
	two := Ref{ID: twoTask.ID}

	// From the ticket side: work#1 subtask-of main#1.
	if err := work.AddLinkTo(one.ID, LinkSubtaskOf, Ref{Board: main.ID, ID: goal.ID}); err != nil {
		t.Fatalf("AddLinkTo: %v", err)
	}
	// From the goal side: "parent-of" normalises to a link stored on the
	// ticket, which is where the cross-board link lives.
	from, kind, to, err := ParseLinkSpec(two.ID, "parent-of", goal.ID)
	if err != nil {
		t.Fatalf("ParseLinkSpec: %v", err)
	}
	if kind != LinkSubtaskOf || from != goal.ID || to != two.ID {
		t.Fatalf("ParseLinkSpec gave %d %s %d", from, kind, to)
	}
	if err := work.AddLinkTo(two.ID, LinkSubtaskOf, Ref{Board: main.ID, ID: goal.ID}); err != nil {
		t.Fatalf("AddLinkTo two: %v", err)
	}

	if done, total := main.SubtaskProgress(goal.ID); done != 0 || total != 2 {
		t.Fatalf("progress = %d/%d, want 0/2", done, total)
	}
	// "done" is not the last column on work, so this is not finished yet.
	if err := work.MoveTask(one.ID, "done"); err != nil {
		t.Fatalf("MoveTask: %v", err)
	}
	if done, total := main.SubtaskProgress(goal.ID); done != 0 || total != 2 {
		t.Fatalf("progress after done column = %d/%d, want 0/2", done, total)
	}
	if err := work.MoveTask(one.ID, "review"); err != nil {
		t.Fatalf("MoveTask review: %v", err)
	}
	if done, total := main.SubtaskProgress(goal.ID); done != 1 || total != 2 {
		t.Fatalf("progress after last column = %d/%d, want 1/2", done, total)
	}

	if subs := main.Subtasks(goal.ID); len(subs) != 2 {
		t.Fatalf("Subtasks = %d, want 2", len(subs))
	}
	if p := work.Parent(one.ID); p == nil || p.Title != "Ship goal boards" {
		t.Fatalf("Parent = %v", p)
	}

	// Relations name the other board on both sides.
	got := relLabels(work.Relations(one.ID))
	if len(got) != 1 || got[0] != "subtask of main#1" {
		t.Fatalf("ticket relations = %v", got)
	}
	got = relLabels(main.Relations(goal.ID))
	if len(got) != 2 || got[0] != "subtask work#1" || got[1] != "subtask work#2" {
		t.Fatalf("goal relations = %v", got)
	}

	// Blocking across boards.
	if err := work.AddLinkTo(two.ID, LinkBlocks, Ref{Board: main.ID, ID: goal.ID}); err != nil {
		t.Fatalf("AddLinkTo blocks: %v", err)
	}
	if !main.IsBlocked(goal.ID) {
		t.Fatal("goal should be blocked by work#2")
	}
	if bl := main.Blockers(goal.ID); len(bl) != 1 || bl[0].Title != "ticket two" {
		t.Fatalf("Blockers = %v", bl)
	}
	if n := main.BlockedCount(); n != 1 {
		t.Fatalf("BlockedCount = %d, want 1", n)
	}

	// Cycles are refused across boards: main#1 blocks work#2 already
	// (stored the other way round), so this would close the loop.
	if err := main.AddLinkTo(goal.ID, LinkBlocks, Ref{Board: work.ID, ID: two.ID}); err == nil {
		t.Fatal("expected a cycle error")
	}

	// A link stored on main pointing into work.
	threeTask, _ := work.AddTask(Task{Title: "ticket three"})
	three := Ref{Board: work.ID, ID: threeTask.ID}
	if err := main.AddLinkTo(goal.ID, LinkRelates, Ref{Board: work.ID, ID: three.ID}); err != nil {
		t.Fatalf("AddLinkTo relates: %v", err)
	}
	if !main.hasLinkRef(goal.ID, LinkRelates, Ref{Board: work.ID, ID: three.ID}) {
		t.Fatal("relates link not stored")
	}
	// Deleting the far task removes the link on the other board.
	if !work.DeleteTask(three.ID) {
		t.Fatal("DeleteTask")
	}
	if main.hasLinkRef(goal.ID, LinkRelates, Ref{Board: work.ID, ID: three.ID}) {
		t.Fatal("link to a deleted foreign task survived")
	}

	// Removing the board removes every link into it.
	fourTask, _ := work.AddTask(Task{Title: "ticket four"})
	four := Ref{Board: work.ID, ID: fourTask.ID}
	if err := main.AddLinkTo(goal.ID, LinkRelates, Ref{Board: work.ID, ID: four.ID}); err != nil {
		t.Fatalf("AddLinkTo four: %v", err)
	}
	if err := f.RemoveBoard(work.ID); err != nil {
		t.Fatalf("RemoveBoard: %v", err)
	}
	if links := main.Task(goal.ID).Links; len(links) != 0 {
		t.Fatalf("links into a removed board survived: %v", links)
	}
	if rels := main.Relations(goal.ID); len(rels) != 0 {
		t.Fatalf("relations after RemoveBoard = %v", relLabels(rels))
	}

	// Self links and unknown ends are still refused.
	if err := main.AddLinkTo(goal.ID, LinkRelates, Ref{ID: goal.ID}); err == nil {
		t.Fatal("expected a self link error")
	}
	if err := main.AddLinkTo(goal.ID, LinkRelates, Ref{Board: "nope", ID: 1}); err == nil {
		t.Fatal("expected an unknown board error")
	}
}

func TestCrossBoardLinkReplay(t *testing.T) {
	f, main, work := twoBoards(t)
	goal, _ := main.AddTask(Task{Title: "goal"})
	one, _ := work.AddTask(Task{Title: "one"})
	two, _ := work.AddTask(Task{Title: "two"})
	three, _ := work.AddTask(Task{Title: "three"})

	if err := work.AddLinkTo(one.ID, LinkSubtaskOf, Ref{Board: main.ID, ID: goal.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := main.AddLinkTo(goal.ID, LinkRelates, Ref{Board: work.ID, ID: two.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := main.AddLinkTo(goal.ID, LinkBlocks, Ref{Board: work.ID, ID: three.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if !main.RemoveLinkTo(goal.ID, LinkRelates, Ref{Board: work.ID, ID: two.ID}) {
		t.Fatal("RemoveLinkTo")
	}
	// Deleting a task cleans up links on the other board with no extra
	// events: the deletion event replays the same cleanup.
	work.DeleteTask(three.ID)

	events := f.Pending()
	for i := range events {
		events[i].Seq = int64(i + 1)
		if events[i].Describe(f) == "" {
			t.Fatalf("event %d (%s) has no description", i, events[i].Kind)
		}
	}
	base := NewFile()
	if err := base.Replay(events); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got, want := stateOf(base), stateOf(f); got != want {
		for _, e := range events {
			line, _ := json.Marshal(e)
			t.Logf("event %s", line)
		}
		t.Fatalf("replayed state differs\n got %s\nwant %s", got, want)
	}
}

func TestCrossBoardLinkEvents(t *testing.T) {
	f, main, work := twoBoards(t)
	goal, _ := main.AddTask(Task{Title: "goal"})
	one, _ := work.AddTask(Task{Title: "one"})
	f.Pending()

	if err := main.AddLinkTo(goal.ID, LinkBlocks, Ref{Board: work.ID, ID: one.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	events := f.Pending()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Kind != EvLinkAdded || e.Board != main.ID || e.Task != goal.ID || e.Index != one.ID || e.To != work.ID {
		t.Fatalf("event = %+v", e)
	}
	if got, want := e.Describe(f), "linked #1 blocks work#1"; got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
	if !main.RemoveLinkTo(goal.ID, LinkBlocks, Ref{Board: work.ID, ID: one.ID}) {
		t.Fatal("RemoveLinkTo")
	}
	events = f.Pending()
	if len(events) != 1 || events[0].To != work.ID {
		t.Fatalf("remove event = %+v", events)
	}
	if got, want := events[0].Describe(f), "unlinked #1 blocks work#1"; got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
}

func TestParseRef(t *testing.T) {
	f, main, work := twoBoards(t)

	cases := []struct {
		in   string
		cur  *Board
		file *File
		want Ref
	}{
		{"#12", main, f, Ref{ID: 12}},
		{"12", main, f, Ref{ID: 12}},
		{" 12 ", main, f, Ref{ID: 12}},
		{"work#12", main, f, Ref{Board: "work", ID: 12}},
		{"Work#12", main, f, Ref{Board: "work", ID: 12}},
		{"WORK#12", main, f, Ref{Board: "work", ID: 12}},
		{"main#5", main, f, Ref{ID: 5}},
		{"main#5", work, f, Ref{Board: "main", ID: 5}},
		{"work#5", work, f, Ref{ID: 5}},
		{"12", nil, nil, Ref{ID: 12}},
		{"#12", main, nil, Ref{ID: 12}},
		{"main#7", main, nil, Ref{ID: 7}},
	}
	for _, c := range cases {
		got, err := ParseRef(c.in, c.cur, c.file)
		if err != nil {
			t.Errorf("ParseRef(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseRef(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}

	bad := []struct {
		in   string
		cur  *Board
		file *File
	}{
		{"", main, f},
		{"#", main, f},
		{"abc", main, f},
		{"#0", main, f},
		{"#-1", main, f},
		{"nope#3", main, f},
		{"work#12", main, nil},
		{"work#12", nil, nil},
		{"wo rk#12", main, f},
		{"work#12x", main, f},
	}
	for _, c := range bad {
		if got, err := ParseRef(c.in, c.cur, c.file); err == nil {
			t.Errorf("ParseRef(%q) = %+v, want an error", c.in, got)
		}
	}

	if got := (Ref{ID: 12}).String(); got != "#12" {
		t.Errorf("Ref.String = %q", got)
	}
	if got := (Ref{Board: "work", ID: 12}).String(); got != "work#12" {
		t.Errorf("Ref.String = %q", got)
	}
}

func TestTaskAtAndResolve(t *testing.T) {
	f, main, work := twoBoards(t)
	goal, _ := main.AddTask(Task{Title: "goal"})
	one, _ := work.AddTask(Task{Title: "one"})

	if b, task := f.TaskAt("work", one.ID); b != work || task == nil || task.Title != "one" {
		t.Fatalf("TaskAt = %v %v", b, task)
	}
	if b, task := f.TaskAt("nope", 1); b != nil || task != nil {
		t.Fatalf("TaskAt(nope) = %v %v", b, task)
	}
	if b, task := f.TaskAt("work", 99); b != nil || task != nil {
		t.Fatalf("TaskAt(missing) = %v %v", b, task)
	}
	if b, task := main.Resolve(Link{Kind: LinkRelates, Task: goal.ID}); b != main || task == nil {
		t.Fatalf("Resolve local = %v %v", b, task)
	}
	if b, task := main.Resolve(Link{Kind: LinkRelates, Task: one.ID, Board: "work"}); b != work || task == nil {
		t.Fatalf("Resolve foreign = %v %v", b, task)
	}

	// A board with no file treats every foreign link as unresolvable.
	bare := NewBoard("Bare")
	bare.AddTask(Task{Title: "x"}) //nolint:errcheck // test data
	if b, task := bare.Resolve(Link{Kind: LinkRelates, Task: 1, Board: "work"}); b != nil || task != nil {
		t.Fatalf("bare Resolve = %v %v", b, task)
	}
	if rels := bare.Relations(1); rels != nil {
		t.Fatalf("bare Relations = %v", rels)
	}
}

func TestMentionsAcrossBoards(t *testing.T) {
	f, main, work := twoBoards(t)
	work.AddTask(Task{Title: "one"}) //nolint:errcheck // test data
	work.AddTask(Task{Title: "two"}) //nolint:errcheck // test data

	if got := Mentions("see #12 and work#3 and &#38;"); len(got) != 1 || got[0] != 12 {
		t.Fatalf("Mentions = %v, want [12]", got)
	}
	refs := MentionRefs("see #12 and work#3 and nope#4 and &#38;", main, f)
	want := []Ref{{ID: 12}, {Board: "work", ID: 3}}
	if len(refs) != len(want) {
		t.Fatalf("MentionRefs = %v, want %v", refs, want)
	}
	for i := range refs {
		if refs[i] != want[i] {
			t.Fatalf("MentionRefs = %v, want %v", refs, want)
		}
	}
	if got := MentionRefs("see work#3", main, nil); len(got) != 0 {
		t.Fatalf("MentionRefs with no file = %v", got)
	}

	// Mentions auto-link, across boards too.
	task, err := main.AddTask(Task{Title: "goal", Description: "covers work#1 and work#2 and nope#9"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	links := main.Task(task.ID).Links
	if len(links) != 2 {
		t.Fatalf("links = %+v, want 2", links)
	}
	for i, l := range links {
		if l.Kind != LinkRelates || l.Board != "work" || l.Task != i+1 {
			t.Fatalf("link %d = %+v", i, l)
		}
	}
}

func TestQueryRefsAcrossBoards(t *testing.T) {
	_, main, work := twoBoards(t)
	goal, _ := main.AddTask(Task{Title: "goal"})
	other, _ := main.AddTask(Task{Title: "other"})
	one, _ := work.AddTask(Task{Title: "one"})

	// main#1 subtask-of work#1, main#1 blocks work#1, work#1 blocks main#2.
	if err := main.AddLinkTo(goal.ID, LinkSubtaskOf, Ref{Board: work.ID, ID: one.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := main.AddLinkTo(goal.ID, LinkBlocks, Ref{Board: work.ID, ID: one.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := work.AddLinkTo(one.ID, LinkBlocks, Ref{Board: main.ID, ID: other.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}

	matched := func(b *Board, q string) []int {
		query := ParseQuery(q)
		var out []int
		for _, t := range b.Tasks {
			if query.Matches(b, t, Now()) {
				out = append(out, t.ID)
			}
		}
		return out
	}
	eq := func(name string, got []int, want ...int) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	eq("parent:work#1", matched(main, "parent:work#1"), goal.ID)
	eq("parent:Work#1", matched(main, "parent:Work#1"), goal.ID)
	eq("blocks:work#1", matched(main, "blocks:work#1"), goal.ID)
	eq("blockedby:work#1", matched(main, "blockedby:work#1"), other.ID)
	eq("parent:#1", matched(main, "parent:#1"))
	eq("parent:nope#1", matched(main, "parent:nope#1"))
	eq("parent:main#1", matched(work, "parent:main#1"))
	eq("blockedby:main#1", matched(work, "blockedby:main#1"), one.ID)

	if ParseQuery("parent:work#1").Empty() {
		t.Fatal("parent:work#1 should not be an empty query")
	}
	if !ParseQuery("").Empty() {
		t.Fatal("empty query")
	}

	// A board with no file: bare numbers keep working, refs match nothing.
	bare := NewBoard("Bare")
	a, _ := bare.AddTask(Task{Title: "a"})
	bb, _ := bare.AddTask(Task{Title: "b"})
	if err := bare.AddLink(a.ID, LinkSubtaskOf, bb.ID); err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	eq("bare parent:2", matched(bare, "parent:2"), a.ID)
	eq("bare parent:work#2", matched(bare, "parent:work#2"))
	eq("bare parent:bare#2", matched(bare, "parent:bare#2"), a.ID)
}
