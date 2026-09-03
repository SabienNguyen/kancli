package board

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LinkKind is the relation a task has to another task. Links are stored on
// one side; the other side is derived (see Relations).
type LinkKind string

const (
	// LinkBlocks: this task blocks the target.
	LinkBlocks LinkKind = "blocks"
	// LinkSubtaskOf: this task is a subtask of the target.
	LinkSubtaskOf LinkKind = "subtask_of"
	// LinkRelates: the tasks are related (symmetric).
	LinkRelates LinkKind = "relates"
)

// Link is one stored, outgoing relation.
type Link struct {
	Kind  LinkKind `json:"kind"`
	Task  int      `json:"task"`
	Board string   `json:"board,omitempty"` // other task's board id; "" = same board
}

// Ref names a task, possibly on another board.
type Ref struct {
	Board string // "" = the current board
	ID    int
}

// String renders a ref the way a user writes it: "#12" or "work#12".
func (r Ref) String() string {
	if r.Board == "" {
		return fmt.Sprintf("#%d", r.ID)
	}
	return fmt.Sprintf("%s#%d", r.Board, r.ID)
}

// boardWord is the shape of the board part of a reference: Unicode
// letters, digits and underscores, in hyphen-separated words. Slug keeps
// Unicode letters, so a board named "Über" has to be typeable as "über#1".
// A word never starts or ends with a hyphen, so the "-" in "PR-#12" is a
// separator rather than the tail of a board name.
const boardWord = `[\p{L}\p{N}_]+(?:-[\p{L}\p{N}_]+)*`

// boardWordRE is the shape of the board part of a reference.
var boardWordRE = regexp.MustCompile(`^` + boardWord + `$`)

// ParseRef reads "#12", "12", "work#12" or "Work#12". cur resolves a bare
// number; f resolves board ids and names (nil f: only bare numbers, plus
// cur's own id or name).
func ParseRef(s string, cur *Board, f *File) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("a task reference is required")
	}
	name, num, hasHash := strings.Cut(s, "#")
	if !hasHash {
		name, num = "", name
	}
	n, err := strconv.Atoi(strings.TrimSpace(num))
	if err != nil || n <= 0 {
		return Ref{}, fmt.Errorf("%q is not a task reference (use #12 or board#12)", s)
	}
	if name == "" {
		return Ref{ID: n}, nil
	}
	if !boardWordRE.MatchString(name) {
		return Ref{}, fmt.Errorf("%q is not a board name", name)
	}
	target := findBoard(name, cur, f)
	if target == nil {
		return Ref{}, fmt.Errorf("no board %q", name)
	}
	if cur != nil && target.ID == cur.ID {
		return Ref{ID: n}, nil
	}
	return Ref{Board: target.ID, ID: n}, nil
}

// findBoard looks a board up by id or name, case insensitively, in f and
// then in cur alone (for boards that belong to no file).
func findBoard(name string, cur *Board, f *File) *Board {
	if f != nil {
		if b := f.Board(name); b != nil {
			return b
		}
		if b := f.Board(strings.ToLower(name)); b != nil {
			return b
		}
	}
	if cur != nil && (strings.EqualFold(cur.ID, name) || strings.EqualFold(cur.Name, name)) {
		return cur
	}
	return nil
}

// Relation is a link as seen from one task, in either direction.
type Relation struct {
	Kind     LinkKind // the stored kind
	Label    string   // "blocks", "blocked by", "subtask of", "subtask", "relates to"
	Task     Task     // the other task
	Board    string   // id of the other task's board ("" when it is this board)
	Outgoing bool     // stored on this task (true) or on the other (false)
}

// ParseLinkSpec turns the words a user types into a stored link. Inverse
// words are normalised so the same relation is always stored the same way:
//
//	12 blocks 15        -> 12 blocks 15
//	12 blocked-by 15    -> 15 blocks 12
//	12 subtask-of 3     -> 12 subtask_of 3
//	3 parent-of 12      -> 12 subtask_of 3
//	12 relates 7        -> 12 relates 7
func ParseLinkSpec(from int, word string, to int) (int, LinkKind, int, error) {
	w := strings.ToLower(strings.TrimSpace(word))
	w = strings.NewReplacer("-", "_", " ", "_").Replace(w)
	switch w {
	case "blocks", "block", "blocking":
		return from, LinkBlocks, to, nil
	case "blocked_by", "blockedby", "after", "depends_on", "dependson":
		return to, LinkBlocks, from, nil
	case "subtask_of", "subtaskof", "subtask", "child_of", "under":
		return from, LinkSubtaskOf, to, nil
	case "parent_of", "parentof", "parent", "has_subtask":
		return to, LinkSubtaskOf, from, nil
	case "relates", "relates_to", "related", "related_to", "see":
		return from, LinkRelates, to, nil
	}
	return 0, "", 0, fmt.Errorf("unknown link kind %q (use blocks, blocked-by, subtask-of, parent-of or relates)", word)
}

// --- resolving refs across boards -------------------------------------------

// TaskAt finds a task on a named board.
func (f *File) TaskAt(boardID string, id int) (*Board, *Task) {
	ob := f.Board(boardID)
	if ob == nil {
		return nil, nil
	}
	t := ob.Task(id)
	if t == nil {
		return nil, nil
	}
	return ob, t
}

// Resolve finds the task a link points at, following l.Board through the
// board's file. It returns nil, nil when the task (or its board) is gone,
// which is also what a board with no file reports for every foreign link.
func (b *Board) Resolve(l Link) (*Board, *Task) {
	if l.Board == "" || l.Board == b.ID {
		t := b.Task(l.Task)
		if t == nil {
			return nil, nil
		}
		return b, t
	}
	if b.file == nil {
		return nil, nil
	}
	return b.file.TaskAt(l.Board, l.Task)
}

// canon expands a ref written relative to b into an absolute one, so refs
// from different boards can be compared.
func (b *Board) canon(r Ref) Ref {
	if r.Board == "" {
		return Ref{Board: b.ID, ID: r.ID}
	}
	return r
}

// linkRef is the ref a stored link points at, relative to b.
func (b *Board) linkRef(l Link) Ref {
	if l.Board == b.ID {
		return Ref{ID: l.Task}
	}
	return Ref{Board: l.Board, ID: l.Task}
}

// boards lists the boards whose links may touch b's tasks: every board of
// the file (b first, so relation order is stable), or b alone.
func (b *Board) boards() []*Board {
	if b.file == nil {
		return []*Board{b}
	}
	out := []*Board{b}
	for _, ob := range b.file.Boards {
		if ob != b {
			out = append(out, ob)
		}
	}
	return out
}

// taskLinksTo reports whether a task of b stores the given link.
func (b *Board) taskLinksTo(t Task, kind LinkKind, to Ref) bool {
	want := b.canon(to)
	for _, l := range t.Links {
		if l.Kind == kind && b.canon(b.linkRef(l)) == want {
			return true
		}
	}
	return false
}

// hasLinkRef reports whether a task of b links to a ref.
func (b *Board) hasLinkRef(from int, kind LinkKind, to Ref) bool {
	t := b.Task(from)
	if t == nil {
		return false
	}
	return b.taskLinksTo(*t, kind, to)
}

// --- adding and removing ----------------------------------------------------

// AddLink records a relation to a task on the same board.
func (b *Board) AddLink(from int, kind LinkKind, to int) error {
	return b.AddLinkTo(from, kind, Ref{ID: to})
}

// AddLinkTo records a relation, possibly to a task on another board.
// Duplicates are ignored, self links and cycles are refused.
func (b *Board) AddLinkTo(from int, kind LinkKind, to Ref) error {
	if to.Board == b.ID {
		to.Board = ""
	}
	if to.Board == "" && from == to.ID {
		return fmt.Errorf("a task cannot link to itself")
	}
	src := b.Task(from)
	if src == nil {
		return fmt.Errorf("no task #%d", from)
	}
	dstBoard, dst := b.Resolve(Link{Kind: kind, Task: to.ID, Board: to.Board})
	if dst == nil {
		return fmt.Errorf("no task %s", to)
	}
	switch kind {
	case LinkBlocks, LinkSubtaskOf, LinkRelates:
	default:
		return fmt.Errorf("unknown link kind %q", kind)
	}
	if kind == LinkRelates && dstBoard.hasLinkRef(to.ID, LinkRelates, Ref{Board: b.ID, ID: from}) {
		return nil // already related the other way round
	}
	if b.hasLinkRef(from, kind, to) {
		return nil
	}
	if kind != LinkRelates && b.reaches(kind, to, Ref{ID: from}) {
		return fmt.Errorf("that would create a cycle: %s already %s %s",
			to, kindVerb(kind), Ref{ID: from})
	}
	src.Links = append(src.Links, Link{Kind: kind, Task: to.ID, Board: to.Board})
	src.UpdatedAt = b.now()
	src.logAt(src.UpdatedAt, "Linked: %s %s", kindVerb(kind), to)
	b.emit(Event{Kind: EvLinkAdded, Task: from, Index: to.ID, Text: string(kind),
		To: to.Board, V: linkEventVersion(to.Board)})
	return nil
}

// linkEventVersion is the version a link event is written with: the
// default for a link inside one board, LinkEventVersion when it names
// another board, whose meaning an older build would get wrong.
func linkEventVersion(otherBoard string) int {
	if otherBoard == "" {
		return 0 // the store fills in EventVersion
	}
	return LinkEventVersion
}

func kindVerb(k LinkKind) string {
	switch k {
	case LinkBlocks:
		return "blocks"
	case LinkSubtaskOf:
		return "is a subtask of"
	default:
		return "relates to"
	}
}

// reaches reports whether following kind links from start leads to goal.
// Both refs are read relative to b, and the walk crosses boards.
func (b *Board) reaches(kind LinkKind, start, goal Ref) bool {
	want := b.canon(goal)
	seen := map[Ref]bool{}
	stack := []Ref{b.canon(start)}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == want {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		ob, t := b.Resolve(Link{Kind: kind, Task: cur.ID, Board: cur.Board})
		if t == nil {
			continue
		}
		for _, l := range t.Links {
			if l.Kind == kind {
				stack = append(stack, ob.canon(ob.linkRef(l)))
			}
		}
	}
	return false
}

// RemoveLink deletes one stored relation to a task on the same board.
func (b *Board) RemoveLink(from int, kind LinkKind, to int) bool {
	return b.RemoveLinkTo(from, kind, Ref{ID: to})
}

// RemoveLinkTo deletes one stored relation, possibly to another board.
func (b *Board) RemoveLinkTo(from int, kind LinkKind, to Ref) bool {
	t := b.Task(from)
	if t == nil {
		return false
	}
	if to.Board == b.ID {
		to.Board = ""
	}
	want := b.canon(to)
	for i, l := range t.Links {
		if l.Kind == kind && b.canon(b.linkRef(l)) == want {
			t.Links = append(t.Links[:i], t.Links[i+1:]...)
			t.UpdatedAt = b.now()
			t.logAt(t.UpdatedAt, "Unlinked: %s %s", kindVerb(kind), to)
			b.emit(Event{Kind: EvLinkRemoved, Task: from, Index: to.ID, Text: string(kind),
				To: to.Board, V: linkEventVersion(to.Board)})
			return true
		}
	}
	return false
}

// RemoveLinksBetween deletes every relation between two tasks of this
// board, in either direction, and returns how many were removed.
func (b *Board) RemoveLinksBetween(a, c int) int {
	n := 0
	for _, pair := range [][2]int{{a, c}, {c, a}} {
		t := b.Task(pair[0])
		if t == nil {
			continue
		}
		want := b.canon(Ref{ID: pair[1]})
		for _, l := range append([]Link(nil), t.Links...) {
			if b.canon(b.linkRef(l)) == want && b.RemoveLink(pair[0], l.Kind, pair[1]) {
				n++
			}
		}
	}
	return n
}

// muted reports whether the board is replaying (or frozen), in which case
// a mutation must not draw conclusions of its own: whatever it would do to
// another board arrives as that board's own event.
func (b *Board) muted() bool {
	return b.rec != nil && b.rec.muted
}

// dropOwnLinksTo strips this board's own links to one of its tasks that is
// going away. They need no event: the deletion event replays this.
func (b *Board) dropOwnLinksTo(id int) {
	target := Ref{Board: b.ID, ID: id}
	b.dropLinks(func(l Link) bool { return b.canon(b.linkRef(l)) == target })
}

// removeForeignLinksTo removes the links other boards hold to a task of b
// that has just gone, one link.removed event per link on the board that
// stores it. During replay it does nothing: those events follow in the log
// with their own timestamps, exactly like linkMentions.
func (b *Board) removeForeignLinksTo(id int) {
	if b.file == nil || b.muted() {
		return
	}
	target := Ref{Board: b.ID, ID: id}
	for _, ob := range b.file.Boards {
		if ob == b {
			continue
		}
		ob.removeLinksMatching(func(l Link) bool { return ob.canon(ob.linkRef(l)) == target })
	}
}

// removeLinksToBoard removes this board's links into a board that is going
// away, each as its own event. Replay leaves it to those events.
func (b *Board) removeLinksToBoard(id string) {
	if b.muted() {
		return
	}
	b.removeLinksMatching(func(l Link) bool { return b.canon(b.linkRef(l)).Board == id })
}

// removeLinksMatching removes every selected link through RemoveLinkTo, so
// each removal is recorded, logged on the task and undone the same way a
// hand-typed unlink is.
func (b *Board) removeLinksMatching(drop func(Link) bool) {
	for i := range b.Tasks {
		id := b.Tasks[i].ID
		for _, l := range append([]Link(nil), b.Tasks[i].Links...) {
			if drop(l) {
				b.RemoveLinkTo(id, l.Kind, b.linkRef(l))
			}
		}
	}
}

// dropLinks removes the links a predicate selects and reports whether any
// were removed.
func (b *Board) dropLinks(drop func(Link) bool) bool {
	changed := false
	for i := range b.Tasks {
		t := &b.Tasks[i]
		kept := t.Links[:0]
		for _, l := range t.Links {
			if !drop(l) {
				kept = append(kept, l)
			}
		}
		if len(kept) != len(t.Links) {
			changed = true
		}
		t.Links = kept
	}
	return changed
}

// --- reading ----------------------------------------------------------------

// incoming lists the tasks of any board that link to one of b's tasks with
// the given kind, together with the board they live on.
func (b *Board) incoming(id int, kind LinkKind) []Relation {
	target := Ref{Board: b.ID, ID: id}
	var out []Relation
	for _, ob := range b.boards() {
		for _, other := range ob.Tasks {
			if ob == b && other.ID == id {
				continue
			}
			for _, l := range other.Links {
				if l.Kind != kind || ob.canon(ob.linkRef(l)) != target {
					continue
				}
				rel := Relation{Kind: l.Kind, Task: other}
				if ob != b {
					rel.Board = ob.ID
				}
				out = append(out, rel)
			}
		}
	}
	return out
}

// Relations lists every link touching a task, outgoing first.
func (b *Board) Relations(id int) []Relation {
	t := b.Task(id)
	if t == nil {
		return nil
	}
	var out []Relation
	for _, l := range t.Links {
		ob, other := b.Resolve(l)
		if other == nil {
			continue
		}
		rel := Relation{Kind: l.Kind, Label: forwardLabel(l.Kind), Task: *other, Outgoing: true}
		if ob != b {
			rel.Board = ob.ID
		}
		out = append(out, rel)
	}
	for _, kind := range []LinkKind{LinkBlocks, LinkSubtaskOf, LinkRelates} {
		for _, rel := range b.incoming(id, kind) {
			rel.Label = inverseLabel(rel.Kind)
			out = append(out, rel)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return labelOrder(out[i].Label) < labelOrder(out[j].Label)
		}
		return out[i].Task.ID < out[j].Task.ID
	})
	return out
}

func forwardLabel(k LinkKind) string {
	switch k {
	case LinkBlocks:
		return "blocks"
	case LinkSubtaskOf:
		return "subtask of"
	default:
		return "relates to"
	}
}

func inverseLabel(k LinkKind) string {
	switch k {
	case LinkBlocks:
		return "blocked by"
	case LinkSubtaskOf:
		return "subtask"
	default:
		return "relates to"
	}
}

func labelOrder(label string) int {
	switch label {
	case "blocked by":
		return 0
	case "blocks":
		return 1
	case "subtask of":
		return 2
	case "subtask":
		return 3
	default:
		return 4
	}
}

// isFinished reports whether a task counts as done for blocking purposes:
// it is in the last column of this board (the task's own) or archived.
func (b *Board) isFinished(t Task) bool {
	if t.Archived() {
		return true
	}
	done := b.DoneColumn()
	return done != nil && t.Column == done.ID
}

// finished reports whether the task of a relation is done on its own board.
func (b *Board) finished(rel Relation) bool {
	ob := b
	if rel.Board != "" && b.file != nil {
		if found := b.file.Board(rel.Board); found != nil {
			ob = found
		}
	}
	return ob.isFinished(rel.Task)
}

// Blockers returns the unfinished tasks that block a task, on any board.
func (b *Board) Blockers(id int) []Task {
	var out []Task
	for _, rel := range b.incoming(id, LinkBlocks) {
		if !b.finished(rel) {
			out = append(out, rel.Task)
		}
	}
	return out
}

// BlockerRefs names the unfinished blockers of a task the way a user
// writes them, relative to this board: "#4" here, "work#1" elsewhere.
func (b *Board) BlockerRefs(id int) []Ref {
	var out []Ref
	for _, rel := range b.incoming(id, LinkBlocks) {
		if !b.finished(rel) {
			out = append(out, Ref{Board: rel.Board, ID: rel.Task.ID})
		}
	}
	return out
}

// IsBlocked reports whether a task has any unfinished blocker.
func (b *Board) IsBlocked(id int) bool {
	return len(b.Blockers(id)) > 0
}

// Subtasks returns the tasks that declare id as their parent, on any board.
func (b *Board) Subtasks(id int) []Task {
	var out []Task
	for _, rel := range b.incoming(id, LinkSubtaskOf) {
		out = append(out, rel.Task)
	}
	return out
}

// SubtaskProgress returns finished and total subtasks of a task. A subtask
// counts as finished by the rules of its own board.
func (b *Board) SubtaskProgress(id int) (int, int) {
	subs := b.incoming(id, LinkSubtaskOf)
	done := 0
	for _, rel := range subs {
		if b.finished(rel) {
			done++
		}
	}
	return done, len(subs)
}

// Parent returns the task this one is a subtask of, if any. It may live on
// another board.
func (b *Board) Parent(id int) *Task {
	t := b.Task(id)
	if t == nil {
		return nil
	}
	for _, l := range t.Links {
		if l.Kind == LinkSubtaskOf {
			if _, other := b.Resolve(l); other != nil {
				return other
			}
		}
	}
	return nil
}

// BlockedCount is the number of live, unfinished tasks with a blocker.
func (b *Board) BlockedCount() int {
	n := 0
	for _, t := range b.Live() {
		if !b.isFinished(t) && b.IsBlocked(t.ID) {
			n++
		}
	}
	return n
}

// --- mentions ---------------------------------------------------------------

// mentionRE finds "#12" and "work#12" in free text. The character before
// the reference must not be part of a word (so "&#38;" and "a#12" are not
// mentions), and the optional board word cannot swallow a trailing hyphen,
// so "see PR-#12" mentions #12 of this board.
var mentionRE = regexp.MustCompile(`(?:^|[^\p{L}\p{N}_&])(` + boardWord + `)?#(\d+)\b`)

// Mentions extracts task numbers written as #12 in free text. Mentions of
// tasks on other boards (work#12) are not numbers of this board and are
// left out; MentionRefs returns those too.
func Mentions(text string) []int {
	seen := map[int]bool{}
	var out []int
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		if m[1] != "" {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// MentionRefs extracts every task reference in free text, including
// work#12 forms. cur is the board a bare #12 means; f resolves board names.
// A reference to an unknown board is not a mention.
func MentionRefs(text string, cur *Board, f *File) []Ref {
	seen := map[Ref]bool{}
	var out []Ref
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		spec := m[2]
		if m[1] != "" {
			spec = m[1] + "#" + m[2]
		}
		r, err := ParseRef(spec, cur, f)
		if err != nil || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// linkMentions turns #N mentions in text into "relates to" links. During
// replay it does nothing: the links it created were logged as their own
// link.added events, which follow in the log with their own timestamps.
func (b *Board) linkMentions(from int, text string) {
	if b.rec != nil && b.rec.muted {
		return
	}
	for _, r := range MentionRefs(text, b, b.file) {
		if r.Board == "" && r.ID == from {
			continue
		}
		_ = b.AddLinkTo(from, LinkRelates, r)
	}
}
