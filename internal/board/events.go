package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// eventKind names what happened. Kinds are stable strings because they are
// written to disk and queried by analytics tools.
type EventKind string

const (
	EvTaskCreated       EventKind = "task.created"
	EvTaskUpdated       EventKind = "task.updated"
	EvTaskMoved         EventKind = "task.moved"
	EvTaskReordered     EventKind = "task.reordered"
	EvTaskDeleted       EventKind = "task.deleted"
	EvTaskArchived      EventKind = "task.archived"
	EvTaskRestored      EventKind = "task.restored"
	EvCommentAdded      EventKind = "comment.added"
	EvChecklistAdded    EventKind = "checklist.added"
	EvChecklistToggled  EventKind = "checklist.toggled"
	EvChecklistRemoved  EventKind = "checklist.removed"
	EvAttachmentAdded   EventKind = "attachment.added"
	EvAttachmentRemoved EventKind = "attachment.removed"
	EvColumnAdded       EventKind = "column.added"
	EvColumnUpdated     EventKind = "column.updated"
	EvColumnRemoved     EventKind = "column.removed"
	EvColumnMoved       EventKind = "column.moved"
	EvBoardAdded        EventKind = "board.added"
	EvBoardRenamed      EventKind = "board.renamed"
	EvBoardDescribed    EventKind = "board.described"
	EvBoardRemoved      EventKind = "board.removed"
	EvBoardActivated    EventKind = "board.activated"
	EvBoardRestored     EventKind = "board.restored"
	EvTaskReverted      EventKind = "task.reverted"  // Data: the task as it was; Apply upserts by id
	EvBoardReverted     EventKind = "board.reverted" // Data: Board with Tasks omitted; Apply replaces everything but Tasks
	EvLinkAdded         EventKind = "link.added"
	EvLinkRemoved       EventKind = "link.removed"
)

// EventVersion is written into every event as "v". Bump it when the
// meaning of an existing kind or its data payload changes; a build
// refuses to replay events with a higher version than it knows. Events
// without "v" (written before this field existed) are version 1.
const EventVersion = 1

// ErrNewerEvents wraps every replay failure caused by data this build
// does not understand, so callers can word the advice.
var ErrNewerEvents = errors.New("written by a newer kancli")

// NewerEventError is the replay failure ErrNewerEvents stands for. It
// carries the offending event's identity and a bare detail (no advice, no
// "newer kancli" phrase) so callers can word the advice once themselves.
type NewerEventError struct {
	Seq    int64
	Kind   EventKind
	Detail string
}

func (e *NewerEventError) Error() string { return e.Detail }

// Is reports NewerEventError as ErrNewerEvents so errors.Is keeps working.
func (e *NewerEventError) Is(target error) bool { return target == ErrNewerEvents }

// Event is one line of the append-only log. State is derived by replaying
// events on top of a snapshot, and analytics read the same lines.
type Event struct {
	V     int             `json:"v,omitempty"`
	Seq   int64           `json:"seq"`
	At    time.Time       `json:"at"`
	Board string          `json:"board,omitempty"`
	Kind  EventKind       `json:"kind"`
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
func (f *File) Attach() {
	if f.rec == nil {
		f.rec = &recorder{}
	}
	for _, b := range f.Boards {
		b.rec = f.rec
		if b.clock == nil {
			b.clock = Now
		}
	}
}

func (f *File) emit(e Event) {
	if f.rec == nil || f.rec.muted {
		return
	}
	if e.At.IsZero() {
		e.At = Now()
	}
	e.Actor = f.rec.actor
	f.rec.events = append(f.rec.events, e)
}

// now reads the clock and remembers the reading for the event the current
// mutation is about to emit.
func (b *Board) now() time.Time {
	t := Now()
	if b.clock != nil {
		t = b.clock()
	}
	b.stamp = t
	return t
}

// emit records an event at the time of the mutation that produced it: the
// last now() reading when there was one, otherwise a fresh one. Reusing
// the reading matters because replay uses the event time as "now", and the
// replayed task must end up byte-for-byte identical to the original.
func (b *Board) emit(e Event) {
	at := b.stamp
	b.stamp = time.Time{}
	if b.rec == nil || b.rec.muted {
		return
	}
	if at.IsZero() {
		at = b.now()
		b.stamp = time.Time{}
	}
	e.At = at
	e.Board = b.ID
	e.Actor = b.rec.actor
	b.rec.events = append(b.rec.events, e)
}

func MustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// SetClock replaces the board's clock (tests and demo data use it to
// backdate events). nil restores the real clock.
func (b *Board) SetClock(clock func() time.Time) {
	if clock == nil {
		clock = Now
	}
	b.clock = clock
}

// SetActor names the process whose events are being recorded.
func (f *File) SetActor(actor string) {
	f.Attach()
	f.rec.actor = actor
}

// Freeze stops the file from recording events (read-only views).
func (f *File) Freeze() {
	f.Attach()
	f.rec.muted = true
}

// EmptyBase returns a copy of f with the same boards and columns but no
// tasks, sequence zero and no snapshot time: the state before any event.
func (f *File) EmptyBase() *File {
	empty := *f
	empty.Boards = nil
	for _, b := range f.Boards {
		nb := *b
		nb.Tasks = nil
		nb.NextID = 1
		nb.rec, nb.clock, nb.byID = nil, nil, nil
		empty.Boards = append(empty.Boards, &nb)
	}
	empty.LastSeq, empty.SnapshotAt = 0, time.Time{}
	empty.rec = nil
	return &empty
}

// Pending returns the events recorded since the last drain.
func (f *File) Pending() []Event {
	if f.rec == nil {
		return nil
	}
	return f.rec.drain()
}

// apply replays one event onto the file. Replay is silent (no new events)
// and uses the event's timestamp as "now" so the result matches the
// original mutation exactly.
func (f *File) Apply(e Event) error {
	if e.V > EventVersion {
		return &NewerEventError{Seq: e.Seq, Kind: e.Kind,
			Detail: fmt.Sprintf("format v%d, this build reads v%d", e.V, EventVersion)}
	}
	f.Attach()
	rec := f.rec
	rec.muted = true
	defer func() { rec.muted = false }()

	switch e.Kind {
	case EvBoardAdded:
		var b Board
		if err := json.Unmarshal(e.Data, &b); err != nil {
			return err
		}
		if f.Board(b.ID) == nil {
			nb := &b
			nb.rec = rec
			nb.clock = Now
			if len(nb.Columns) == 0 {
				nb.Columns = DefaultColumns()
			}
			f.Boards = append(f.Boards, nb)
		}
		return nil
	case EvBoardRenamed:
		if b := f.Board(e.Board); b != nil {
			b.Name = e.Text
		}
		return nil
	case EvBoardDescribed:
		if b := f.Board(e.Board); b != nil {
			b.Description = e.Text
		}
		return nil
	case EvBoardRemoved:
		return ignoreNotFound(f.RemoveBoard(e.Board))
	case EvBoardActivated:
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
	case EvTaskCreated:
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
	case EvTaskUpdated:
		var t Task
		if err := json.Unmarshal(e.Data, &t); err != nil {
			return err
		}
		return ignoreNotFound(b.UpdateTask(t))
	case EvTaskMoved:
		return ignoreNotFound(b.MoveTask(e.Task, e.To))
	case EvTaskReordered:
		b.ReorderTask(e.Task, e.Index)
	case EvTaskDeleted:
		b.DeleteTask(e.Task)
	case EvTaskArchived:
		b.ArchiveTask(e.Task)
	case EvTaskRestored:
		b.RestoreTask(e.Task)
	case EvCommentAdded:
		return ignoreNotFound(b.AddComment(e.Task, e.Text))
	case EvChecklistAdded:
		return ignoreNotFound(b.AddChecklistItem(e.Task, e.Text))
	case EvChecklistToggled:
		b.ToggleChecklistItem(e.Task, e.Index)
	case EvChecklistRemoved:
		b.RemoveChecklistItem(e.Task, e.Index)
	case EvAttachmentAdded:
		return ignoreNotFound(b.AddAttachment(e.Task, e.Text))
	case EvAttachmentRemoved:
		b.RemoveAttachment(e.Task, e.Index)
	case EvColumnAdded:
		var c Column
		if err := json.Unmarshal(e.Data, &c); err != nil {
			return err
		}
		if b.ColumnIndex(c.ID) < 0 {
			b.Columns = append(b.Columns, c)
		}
	case EvColumnUpdated:
		var c Column
		if err := json.Unmarshal(e.Data, &c); err != nil {
			return err
		}
		return ignoreNotFound(b.UpdateColumn(c.ID, c.Name, c.Color, c.WIPLimit))
	case EvColumnRemoved:
		return ignoreNotFound(b.RemoveColumn(e.From, e.To))
	case EvColumnMoved:
		b.MoveColumn(e.From, e.Index)
	case EvLinkAdded:
		return ignoreNotFound(b.AddLink(e.Task, LinkKind(e.Text), e.Index))
	case EvLinkRemoved:
		b.RemoveLink(e.Task, LinkKind(e.Text), e.Index)
	case EvTaskReverted:
		var t Task
		if err := json.Unmarshal(e.Data, &t); err != nil {
			return err
		}
		b.revertTask(t, e.Index)
	case EvBoardReverted:
		var nb Board
		if err := json.Unmarshal(e.Data, &nb); err != nil {
			return err
		}
		b.Name, b.Description, b.Columns, b.NextID = nb.Name, nb.Description, nb.Columns, nb.NextID
		b.touch()
	case EvBoardRestored:
		var nb Board
		if err := json.Unmarshal(e.Data, &nb); err != nil {
			return err
		}
		nb.rec, nb.clock = b.rec, prevClock
		nb.gen = b.gen + 1
		*b = nb
		b.clock = func() time.Time { return at }
	default:
		return &NewerEventError{Seq: e.Seq, Kind: e.Kind,
			Detail: fmt.Sprintf("unknown event kind %q", e.Kind)}
	}
	return nil
}

// ignoreNotFound drops errors from replaying events whose subject has since
// been deleted; the later events already account for it.
func ignoreNotFound(err error) error {
	return nil
}

// replay applies events in order, skipping ones the snapshot already holds.
func (f *File) Replay(events []Event) error {
	for _, e := range events {
		if e.Seq != 0 && e.Seq <= f.LastSeq {
			continue
		}
		if err := f.Apply(e); err != nil {
			return fmt.Errorf("event %d (%s): %w", e.Seq, e.Kind, err)
		}
		if e.Seq > f.LastSeq {
			f.LastSeq = e.Seq
		}
	}
	return nil
}

// describe renders an event as a short human sentence.
func (e Event) Describe(f *File) string {
	name := func(id string) string {
		if b := f.Board(e.Board); b != nil {
			return ColName(b, id)
		}
		return id
	}
	ref := fmt.Sprintf("#%d", e.Task)
	switch e.Kind {
	case EvTaskCreated:
		var t Task
		_ = json.Unmarshal(e.Data, &t)
		return fmt.Sprintf("created %s %q in %s", ref, t.Title, name(t.Column))
	case EvTaskUpdated:
		return "edited " + ref
	case EvTaskMoved:
		return fmt.Sprintf("moved %s from %s to %s", ref, name(e.From), name(e.To))
	case EvTaskReordered:
		if e.Index < 0 {
			return "moved " + ref + " up"
		}
		return "moved " + ref + " down"
	case EvTaskDeleted:
		return "deleted " + ref
	case EvTaskArchived:
		return "archived " + ref
	case EvTaskRestored:
		return "restored " + ref
	case EvCommentAdded:
		return "commented on " + ref
	case EvChecklistAdded:
		return fmt.Sprintf("added checklist item %q to %s", e.Text, ref)
	case EvChecklistToggled:
		return fmt.Sprintf("toggled checklist item %d on %s", e.Index+1, ref)
	case EvChecklistRemoved:
		return fmt.Sprintf("removed checklist item %d from %s", e.Index+1, ref)
	case EvAttachmentAdded:
		return fmt.Sprintf("attached %s to %s", e.Text, ref)
	case EvAttachmentRemoved:
		return fmt.Sprintf("removed attachment %d from %s", e.Index+1, ref)
	case EvColumnAdded:
		var c Column
		_ = json.Unmarshal(e.Data, &c)
		return fmt.Sprintf("added column %q", c.Name)
	case EvColumnUpdated:
		var c Column
		_ = json.Unmarshal(e.Data, &c)
		return fmt.Sprintf("updated column %q", c.Name)
	case EvColumnRemoved:
		if e.To != "" {
			return fmt.Sprintf("deleted column %s (tasks moved to %s)", e.From, name(e.To))
		}
		return "deleted column " + e.From
	case EvColumnMoved:
		return "moved column " + name(e.From)
	case EvBoardAdded:
		var b Board
		_ = json.Unmarshal(e.Data, &b)
		return fmt.Sprintf("created board %q", b.Name)
	case EvBoardRenamed:
		return fmt.Sprintf("renamed board to %q", e.Text)
	case EvBoardDescribed:
		if e.Text == "" {
			return "cleared the board description"
		}
		return fmt.Sprintf("described board: %q", e.Text)
	case EvBoardRemoved:
		return "deleted board " + e.Board
	case EvBoardActivated:
		return "switched to board " + e.To
	case EvBoardRestored:
		return "undo"
	case EvTaskReverted:
		return "reverted " + ref
	case EvBoardReverted:
		return "reverted board settings"
	case EvLinkAdded:
		return fmt.Sprintf("linked %s %s #%d", ref, kindVerb(LinkKind(e.Text)), e.Index)
	case EvLinkRemoved:
		return fmt.Sprintf("unlinked %s %s #%d", ref, kindVerb(LinkKind(e.Text)), e.Index)
	}
	return string(e.Kind)
}
