package board

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestReplayProperty drives a file through random sequences of every
// mutation and checks the invariants the event store depends on:
//
//   - replaying the recorded events onto an empty file reproduces the
//     exact state (task order, timestamps, history, links, columns, boards);
//   - the replay can be split at any point with a JSON snapshot in between,
//     which is what a compaction does;
//   - a second replay of the same events is a no-op;
//   - the live state survives a JSON round trip;
//   - every event can be described.
func TestReplayProperty(t *testing.T) {
	prevNow := Now
	t.Cleanup(func() { Now = prevNow })

	rapid.Check(t, func(rt *rapid.T) {
		start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
		tick := 0
		Now = func() time.Time { tick++; return start.Add(time.Duration(tick) * time.Minute) }

		f := NewFile()
		f.rec.actor = "prop"
		base := NewFile()
		g := opGen{rt: rt, f: f}

		n := rapid.IntRange(1, 80).Draw(rt, "ops")
		for i := 0; i < n; i++ {
			g.step()
		}

		events := f.Pending()
		for i := range events {
			events[i].Seq = int64(i + 1)
			if events[i].Describe(f) == "" {
				rt.Fatalf("event %d (%s) has no description", i, events[i].Kind)
			}
		}
		want := stateOf(f)

		if err := base.Replay(events); err != nil {
			rt.Fatalf("replay: %v", err)
		}
		if got := stateOf(base); got != want {
			for _, e := range events {
				line, _ := json.Marshal(e)
				rt.Logf("event %s", line)
			}
			rt.Fatalf("replayed state differs after %s\n got %s\nwant %s", g.trace, got, want)
		}
		if err := base.Replay(events); err != nil || stateOf(base) != want {
			rt.Fatalf("second replay changed the state: %v", err)
		}

		// Snapshot in the middle, like a compaction, then replay the tail.
		k := rapid.IntRange(0, len(events)).Draw(rt, "split")
		mid := NewFile()
		if err := mid.Replay(events[:k]); err != nil {
			rt.Fatalf("replay prefix: %v", err)
		}
		data, err := json.Marshal(mid)
		if err != nil {
			rt.Fatal(err)
		}
		snap, err := Decode(data)
		if err != nil {
			rt.Fatalf("decode snapshot: %v", err)
		}
		if err := snap.Replay(events[k:]); err != nil {
			rt.Fatalf("replay tail: %v", err)
		}
		if got := stateOf(snap); got != want {
			rt.Fatalf("snapshot at %d then tail differs\n got %s\nwant %s", k, got, want)
		}

		// The live file survives its own serialisation.
		data, _ = json.Marshal(f)
		again, err := Decode(data)
		if err != nil {
			rt.Fatal(err)
		}
		if got := stateOf(again); got != want {
			rt.Fatalf("json round trip differs\n got %s\nwant %s", got, want)
		}
	})
}

// opGen picks and applies one random mutation per step.
type opGen struct {
	rt    *rapid.T
	f     *File
	snaps []Board
	trace string
}

var opNames = []string{
	"add", "add", "add", "update", "move", "move", "reorder", "delete", "archive", "restore", "archiveDone",
	"comment", "check", "toggle", "uncheck", "attach", "detach",
	"addcol", "updcol", "rmcol", "mvcol",
	"link", "unlink", "xlink", "xunlink",
	"board", "rename", "describe", "kind", "activate", "rmboard",
	"snap", "undo",
}

func (g *opGen) step() {
	rt := g.rt
	b := g.f.Active()
	op := rapid.SampledFrom(opNames).Draw(rt, "op")
	g.trace += op + " "
	word := rapid.StringMatching(`[a-z]{1,8}`)
	switch op {
	case "add":
		t := Task{
			Title:       word.Draw(rt, "title"),
			Description: rapid.SampledFrom([]string{"", "notes", "see #1 and #2"}).Draw(rt, "desc"),
			Column:      g.column(b, true),
			Priority:    Priority(rapid.IntRange(0, 4).Draw(rt, "prio")),
			Due:         rapid.SampledFrom([]string{"", "2026-02-01", "2025-12-31"}).Draw(rt, "due"),
			Labels:      rapid.SliceOfN(word, 0, 3).Draw(rt, "labels"),
			Assignee:    rapid.SampledFrom([]string{"", "sam", "alex"}).Draw(rt, "who"),
		}
		b.AddTask(t) //nolint:errcheck // random input may be invalid
	case "update":
		if t := g.task(b); t != nil {
			u := *t
			u.Title = word.Draw(rt, "newTitle")
			u.Priority = Priority(rapid.IntRange(0, 4).Draw(rt, "newPrio"))
			u.Labels = rapid.SliceOfN(word, 0, 3).Draw(rt, "newLabels")
			u.Description = rapid.SampledFrom([]string{"", "changed", "blocked by #1"}).Draw(rt, "newDesc")
			b.UpdateTask(u) //nolint:errcheck
		}
	case "move":
		if t := g.task(b); t != nil {
			b.MoveTask(t.ID, g.column(b, false)) //nolint:errcheck
		}
	case "reorder":
		if t := g.task(b); t != nil {
			b.ReorderTask(t.ID, g.delta())
		}
	case "delete":
		if t := g.task(b); t != nil {
			b.DeleteTask(t.ID)
		}
	case "archive":
		if t := g.task(b); t != nil {
			b.ArchiveTask(t.ID)
		}
	case "restore":
		if t := g.task(b); t != nil {
			b.RestoreTask(t.ID)
		}
	case "archiveDone":
		b.ArchiveDone()
	case "comment":
		if t := g.task(b); t != nil {
			ref := ""
			if o := g.task(b); o != nil {
				ref = fmt.Sprintf(" see #%d", o.ID)
			}
			b.AddComment(t.ID, word.Draw(rt, "comment")+ref) //nolint:errcheck
		}
	case "check":
		if t := g.task(b); t != nil {
			b.AddChecklistItem(t.ID, word.Draw(rt, "item")) //nolint:errcheck
		}
	case "toggle":
		if t := g.task(b); t != nil {
			b.ToggleChecklistItem(t.ID, rapid.IntRange(0, len(t.Checklist)).Draw(rt, "idx"))
		}
	case "uncheck":
		if t := g.task(b); t != nil {
			b.RemoveChecklistItem(t.ID, rapid.IntRange(0, len(t.Checklist)).Draw(rt, "idx"))
		}
	case "attach":
		if t := g.task(b); t != nil {
			b.AddAttachment(t.ID, "https://x.test/"+word.Draw(rt, "ref")) //nolint:errcheck
		}
	case "detach":
		if t := g.task(b); t != nil {
			b.RemoveAttachment(t.ID, rapid.IntRange(0, len(t.Attachments)).Draw(rt, "idx"))
		}
	case "addcol":
		b.AddColumn(word.Draw(rt, "col"), rapid.SampledFrom([]string{"", "99", "#ff0000"}).Draw(rt, "color"), rapid.IntRange(0, 3).Draw(rt, "wip")) //nolint:errcheck
	case "updcol":
		if len(b.Columns) > 0 {
			c := b.Columns[rapid.IntRange(0, len(b.Columns)-1).Draw(rt, "ci")]
			b.UpdateColumn(c.ID, word.Draw(rt, "colName"), c.Color, rapid.IntRange(0, 3).Draw(rt, "wip")) //nolint:errcheck
		}
	case "rmcol":
		if len(b.Columns) > 1 {
			b.RemoveColumn(g.column(b, false), g.column(b, false)) //nolint:errcheck
		}
	case "mvcol":
		if len(b.Columns) > 0 {
			b.MoveColumn(g.column(b, false), g.delta())
		}
	case "link":
		from, to := g.task(b), g.task(b)
		if from != nil && to != nil {
			kind := rapid.SampledFrom([]LinkKind{LinkBlocks, LinkSubtaskOf, LinkRelates}).Draw(rt, "kind")
			b.AddLink(from.ID, kind, to.ID) //nolint:errcheck
		}
	case "unlink":
		from, to := g.task(b), g.task(b)
		if from != nil && to != nil {
			b.RemoveLinksBetween(from.ID, to.ID)
		}
	case "xlink":
		if ob := g.otherBoard(b); ob != nil {
			from, to := g.task(b), g.task(ob)
			if from != nil && to != nil {
				kind := rapid.SampledFrom([]LinkKind{LinkBlocks, LinkSubtaskOf, LinkRelates}).Draw(rt, "xkind")
				b.AddLinkTo(from.ID, kind, Ref{Board: ob.ID, ID: to.ID}) //nolint:errcheck // random input may be invalid
			}
		}
	case "xunlink":
		if ob := g.otherBoard(b); ob != nil {
			from, to := g.task(b), g.task(ob)
			if from != nil && to != nil {
				for _, kind := range []LinkKind{LinkBlocks, LinkSubtaskOf, LinkRelates} {
					b.RemoveLinkTo(from.ID, kind, Ref{Board: ob.ID, ID: to.ID})
				}
			}
		}
	case "board":
		g.f.AddBoard(word.Draw(rt, "board")) //nolint:errcheck
	case "rename":
		g.f.RenameBoard(g.board(), word.Draw(rt, "newName")) //nolint:errcheck
	case "describe":
		g.f.DescribeBoard(g.board(), word.Draw(rt, "desc")) //nolint:errcheck
	case "kind":
		g.f.SetBoardKind(g.board(), rapid.SampledFrom([]string{"", "tasks", "goals", "Goals", "epics"}).Draw(rt, "kind")) //nolint:errcheck // random input may be invalid
	case "activate":
		g.f.Activate(g.board()) //nolint:errcheck
	case "rmboard":
		g.f.RemoveBoard(g.board()) //nolint:errcheck
	case "snap":
		data, _ := json.Marshal(*b)
		var copy Board
		if err := json.Unmarshal(data, &copy); err == nil {
			g.snaps = append(g.snaps, copy)
		}
	case "undo":
		if len(g.snaps) > 0 {
			s := g.snaps[rapid.IntRange(0, len(g.snaps)-1).Draw(rt, "snap")]
			if s.ID == b.ID {
				data, _ := json.Marshal(s)
				var fresh Board
				json.Unmarshal(data, &fresh) //nolint:errcheck
				b.Replace(fresh)
			}
		}
	}
}

// task picks a random task of b, live or archived, or nil for an id that
// does not exist.
func (g *opGen) task(b *Board) *Task {
	if len(b.Tasks) == 0 || rapid.IntRange(0, 9).Draw(g.rt, "missing") == 0 {
		return nil
	}
	return &b.Tasks[rapid.IntRange(0, len(b.Tasks)-1).Draw(g.rt, "task")]
}

// column picks a column id; allowEmpty sometimes returns "" (the first
// column), and either way an unknown id now and then.
func (g *opGen) column(b *Board, allowEmpty bool) string {
	switch {
	case len(b.Columns) == 0, rapid.IntRange(0, 14).Draw(g.rt, "badcol") == 0:
		return "nope"
	case allowEmpty && rapid.Bool().Draw(g.rt, "emptycol"):
		return ""
	}
	return b.Columns[rapid.IntRange(0, len(b.Columns)-1).Draw(g.rt, "col")].ID
}

// otherBoard picks a random board that is not b, or nil when there is none.
func (g *opGen) otherBoard(b *Board) *Board {
	var others []*Board
	for _, ob := range g.f.Boards {
		if ob != b {
			others = append(others, ob)
		}
	}
	if len(others) == 0 {
		return nil
	}
	return others[rapid.IntRange(0, len(others)-1).Draw(g.rt, "otherboard")]
}

func (g *opGen) board() string {
	if rapid.IntRange(0, 9).Draw(g.rt, "badboard") == 0 {
		return "nope"
	}
	return g.f.Boards[rapid.IntRange(0, len(g.f.Boards)-1).Draw(g.rt, "board")].ID
}

func (g *opGen) delta() int {
	return rapid.SampledFrom([]int{-2, -1, 1, 2}).Draw(g.rt, "delta")
}
