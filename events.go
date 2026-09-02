package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// eventKind names what happened. Kinds are stable strings because they are
// written to disk and queried by analytics tools.
type eventKind string

const (
	evTaskCreated       eventKind = "task.created"
	evTaskUpdated       eventKind = "task.updated"
	evTaskMoved         eventKind = "task.moved"
	evTaskReordered     eventKind = "task.reordered"
	evTaskDeleted       eventKind = "task.deleted"
	evTaskArchived      eventKind = "task.archived"
	evTaskRestored      eventKind = "task.restored"
	evCommentAdded      eventKind = "comment.added"
	evChecklistAdded    eventKind = "checklist.added"
	evChecklistToggled  eventKind = "checklist.toggled"
	evChecklistRemoved  eventKind = "checklist.removed"
	evAttachmentAdded   eventKind = "attachment.added"
	evAttachmentRemoved eventKind = "attachment.removed"
	evColumnAdded       eventKind = "column.added"
	evColumnUpdated     eventKind = "column.updated"
	evColumnRemoved     eventKind = "column.removed"
	evColumnMoved       eventKind = "column.moved"
	evBoardAdded        eventKind = "board.added"
	evBoardRenamed      eventKind = "board.renamed"
	evBoardRemoved      eventKind = "board.removed"
	evBoardActivated    eventKind = "board.activated"
	evBoardRestored     eventKind = "board.restored"
)

// Event is one line of the append-only log. State is derived by replaying
// events on top of a snapshot, and analytics read the same lines.
type Event struct {
	Seq   int64           `json:"seq"`
	At    time.Time       `json:"at"`
	Board string          `json:"board,omitempty"`
	Kind  eventKind       `json:"kind"`
	Task  int             `json:"task,omitempty"`
	From  string          `json:"from,omitempty"`
	To    string          `json:"to,omitempty"`
	Index int             `json:"index,omitempty"`
	Text  string          `json:"text,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Actor string          `json:"actor,omitempty"`
}

// recorder collects the events produced by mutations until the store
// appends them to the log.
type recorder struct {
	events []Event
	actor  string
	muted  bool
}

func (r *recorder) drain() []Event {
	out := r.events
	r.events = nil
	return out
}

// attach wires every board to the file's recorder and clock.
func (f *File) attach() {
	if f.rec == nil {
		f.rec = &recorder{}
	}
	for _, b := range f.Boards {
		b.rec = f.rec
		if b.clock == nil {
			b.clock = timeNow
		}
	}
}

func (f *File) emit(e Event) {
	if f.rec == nil || f.rec.muted {
		return
	}
	if e.At.IsZero() {
		e.At = timeNow()
	}
	e.Actor = f.rec.actor
	f.rec.events = append(f.rec.events, e)
}

func (b *Board) now() time.Time {
	if b.clock != nil {
		return b.clock()
	}
	return timeNow()
}

func (b *Board) emit(e Event) {
	if b.rec == nil || b.rec.muted {
		return
	}
	e.At = b.now()
	e.Board = b.ID
	e.Actor = b.rec.actor
	b.rec.events = append(b.rec.events, e)
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// pending returns the events recorded since the last drain.
func (f *File) pending() []Event {
	if f.rec == nil {
		return nil
	}
	return f.rec.drain()
}

// apply replays one event onto the file. Replay is silent (no new events)
// and uses the event's timestamp as "now" so the result matches the
// original mutation exactly.
func (f *File) apply(e Event) error {
	f.attach()
	rec := f.rec
	rec.muted = true
	defer func() { rec.muted = false }()

	switch e.Kind {
	case evBoardAdded:
		var b Board
		if err := json.Unmarshal(e.Data, &b); err != nil {
			return err
		}
		if f.Board(b.ID) == nil {
			nb := &b
			nb.rec = rec
			nb.clock = timeNow
			if len(nb.Columns) == 0 {
				nb.Columns = defaultColumns()
			}
			f.Boards = append(f.Boards, nb)
		}
		return nil
	case evBoardRenamed:
		if b := f.Board(e.Board); b != nil {
			b.Name = e.Text
		}
		return nil
	case evBoardRemoved:
		return ignoreNotFound(f.RemoveBoard(e.Board))
	case evBoardActivated:
		if f.Board(e.To) != nil {
			f.ActiveBoard = e.To
		}
		return nil
	}

	b := f.Board(e.Board)
	if b == nil {
		return nil // the board was deleted later; nothing to do
	}
	at := e.At
	prevClock := b.clock
	b.clock = func() time.Time { return at }
	defer func() { b.clock = prevClock }()

	switch e.Kind {
	case evTaskCreated:
		var t Task
		if err := json.Unmarshal(e.Data, &t); err != nil {
			return err
		}
		if b.Task(t.ID) == nil {
			b.Tasks = append(b.Tasks, t)
			b.touch()
			if b.NextID <= t.ID {
				b.NextID = t.ID + 1
			}
		}
	case evTaskUpdated:
		var t Task
		if err := json.Unmarshal(e.Data, &t); err != nil {
			return err
		}
		return ignoreNotFound(b.UpdateTask(t))
	case evTaskMoved:
		return ignoreNotFound(b.MoveTask(e.Task, e.To))
	case evTaskReordered:
		b.ReorderTask(e.Task, e.Index)
	case evTaskDeleted:
		b.DeleteTask(e.Task)
	case evTaskArchived:
		b.ArchiveTask(e.Task)
	case evTaskRestored:
		b.RestoreTask(e.Task)
	case evCommentAdded:
		return ignoreNotFound(b.AddComment(e.Task, e.Text))
	case evChecklistAdded:
		return ignoreNotFound(b.AddChecklistItem(e.Task, e.Text))
	case evChecklistToggled:
		b.ToggleChecklistItem(e.Task, e.Index)
	case evChecklistRemoved:
		b.RemoveChecklistItem(e.Task, e.Index)
	case evAttachmentAdded:
		return ignoreNotFound(b.AddAttachment(e.Task, e.Text))
	case evAttachmentRemoved:
		b.RemoveAttachment(e.Task, e.Index)
	case evColumnAdded:
		var c Column
		if err := json.Unmarshal(e.Data, &c); err != nil {
			return err
		}
		if b.ColumnIndex(c.ID) < 0 {
			b.Columns = append(b.Columns, c)
		}
	case evColumnUpdated:
		var c Column
		if err := json.Unmarshal(e.Data, &c); err != nil {
			return err
		}
		return ignoreNotFound(b.UpdateColumn(c.ID, c.Name, c.Color, c.WIPLimit))
	case evColumnRemoved:
		return ignoreNotFound(b.RemoveColumn(e.From, e.To))
	case evColumnMoved:
		b.MoveColumn(e.From, e.Index)
	case evBoardRestored:
		var nb Board
		if err := json.Unmarshal(e.Data, &nb); err != nil {
			return err
		}
		nb.rec, nb.clock = b.rec, prevClock
		nb.gen = b.gen + 1
		*b = nb
		b.clock = func() time.Time { return at }
	default:
		return fmt.Errorf("unknown event kind %q", e.Kind)
	}
	return nil
}

// ignoreNotFound drops errors from replaying events whose subject has since
// been deleted; the later events already account for it.
func ignoreNotFound(err error) error {
	return nil
}

// replay applies events in order, skipping ones the snapshot already holds.
func (f *File) replay(events []Event) error {
	for _, e := range events {
		if e.Seq != 0 && e.Seq <= f.LastSeq {
			continue
		}
		if err := f.apply(e); err != nil {
			return fmt.Errorf("event %d (%s): %w", e.Seq, e.Kind, err)
		}
		if e.Seq > f.LastSeq {
			f.LastSeq = e.Seq
		}
	}
	return nil
}

// describe renders an event as a short human sentence.
func (e Event) describe(f *File) string {
	name := func(id string) string {
		if b := f.Board(e.Board); b != nil {
			if c := b.Column(id); c != nil {
				return c.Name
			}
		}
		return id
	}
	ref := fmt.Sprintf("#%d", e.Task)
	switch e.Kind {
	case evTaskCreated:
		var t Task
		_ = json.Unmarshal(e.Data, &t)
		return fmt.Sprintf("created %s %q in %s", ref, t.Title, name(t.Column))
	case evTaskUpdated:
		return "edited " + ref
	case evTaskMoved:
		return fmt.Sprintf("moved %s from %s to %s", ref, name(e.From), name(e.To))
	case evTaskReordered:
		if e.Index < 0 {
			return "moved " + ref + " up"
		}
		return "moved " + ref + " down"
	case evTaskDeleted:
		return "deleted " + ref
	case evTaskArchived:
		return "archived " + ref
	case evTaskRestored:
		return "restored " + ref
	case evCommentAdded:
		return "commented on " + ref
	case evChecklistAdded:
		return fmt.Sprintf("added checklist item %q to %s", e.Text, ref)
	case evChecklistToggled:
		return fmt.Sprintf("toggled checklist item %d on %s", e.Index+1, ref)
	case evChecklistRemoved:
		return fmt.Sprintf("removed checklist item %d from %s", e.Index+1, ref)
	case evAttachmentAdded:
		return fmt.Sprintf("attached %s to %s", e.Text, ref)
	case evAttachmentRemoved:
		return fmt.Sprintf("removed attachment %d from %s", e.Index+1, ref)
	case evColumnAdded:
		var c Column
		_ = json.Unmarshal(e.Data, &c)
		return fmt.Sprintf("added column %q", c.Name)
	case evColumnUpdated:
		var c Column
		_ = json.Unmarshal(e.Data, &c)
		return fmt.Sprintf("updated column %q", c.Name)
	case evColumnRemoved:
		if e.To != "" {
			return fmt.Sprintf("deleted column %s (tasks moved to %s)", e.From, name(e.To))
		}
		return "deleted column " + e.From
	case evColumnMoved:
		return "moved column " + name(e.From)
	case evBoardAdded:
		var b Board
		_ = json.Unmarshal(e.Data, &b)
		return fmt.Sprintf("created board %q", b.Name)
	case evBoardRenamed:
		return fmt.Sprintf("renamed board to %q", e.Text)
	case evBoardRemoved:
		return "deleted board " + e.Board
	case evBoardActivated:
		return "switched to board " + e.To
	case evBoardRestored:
		return "undo"
	}
	return string(e.Kind)
}
