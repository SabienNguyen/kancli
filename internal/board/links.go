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
	Kind LinkKind `json:"kind"`
	Task int      `json:"task"`
}

// Relation is a link as seen from one task, in either direction.
type Relation struct {
	Kind     LinkKind // the stored kind
	Label    string   // "blocks", "blocked by", "subtask of", "subtask", "relates to"
	Task     Task     // the other task
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

// AddLink records a relation. Duplicates are ignored, self links and cycles
// in blocks/subtask chains are refused.
func (b *Board) AddLink(from int, kind LinkKind, to int) error {
	if from == to {
		return fmt.Errorf("a task cannot link to itself")
	}
	src := b.Task(from)
	dst := b.Task(to)
	if src == nil {
		return fmt.Errorf("no task #%d", from)
	}
	if dst == nil {
		return fmt.Errorf("no task #%d", to)
	}
	switch kind {
	case LinkBlocks, LinkSubtaskOf, LinkRelates:
	default:
		return fmt.Errorf("unknown link kind %q", kind)
	}
	if kind == LinkRelates && b.hasLink(to, LinkRelates, from) {
		return nil // already related the other way round
	}
	if b.hasLink(from, kind, to) {
		return nil
	}
	if kind != LinkRelates && b.reaches(kind, to, from) {
		return fmt.Errorf("that would create a cycle: #%d already %s #%d", to, kindVerb(kind), from)
	}
	src.Links = append(src.Links, Link{Kind: kind, Task: to})
	src.UpdatedAt = b.now()
	src.logAt(src.UpdatedAt, "Linked: %s #%d", kindVerb(kind), to)
	b.emit(Event{Kind: EvLinkAdded, Task: from, Index: to, Text: string(kind)})
	return nil
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

func (b *Board) hasLink(from int, kind LinkKind, to int) bool {
	t := b.Task(from)
	if t == nil {
		return false
	}
	for _, l := range t.Links {
		if l.Kind == kind && l.Task == to {
			return true
		}
	}
	return false
}

// reaches reports whether following kind links from start leads to goal.
func (b *Board) reaches(kind LinkKind, start, goal int) bool {
	seen := map[int]bool{}
	stack := []int{start}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == goal {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if t := b.Task(id); t != nil {
			for _, l := range t.Links {
				if l.Kind == kind {
					stack = append(stack, l.Task)
				}
			}
		}
	}
	return false
}

// RemoveLink deletes one stored relation.
func (b *Board) RemoveLink(from int, kind LinkKind, to int) bool {
	t := b.Task(from)
	if t == nil {
		return false
	}
	for i, l := range t.Links {
		if l.Kind == kind && l.Task == to {
			t.Links = append(t.Links[:i], t.Links[i+1:]...)
			t.UpdatedAt = b.now()
			t.logAt(t.UpdatedAt, "Unlinked: %s #%d", kindVerb(kind), to)
			b.emit(Event{Kind: EvLinkRemoved, Task: from, Index: to, Text: string(kind)})
			return true
		}
	}
	return false
}

// RemoveLinksBetween deletes every relation between two tasks, in either
// direction, and returns how many were removed.
func (b *Board) RemoveLinksBetween(a, c int) int {
	n := 0
	for _, pair := range [][2]int{{a, c}, {c, a}} {
		t := b.Task(pair[0])
		if t == nil {
			continue
		}
		for _, l := range append([]Link(nil), t.Links...) {
			if l.Task == pair[1] && b.RemoveLink(pair[0], l.Kind, pair[1]) {
				n++
			}
		}
	}
	return n
}

// dropLinksTo strips links pointing at a deleted task (no events: the
// deletion event replays this).
func (b *Board) dropLinksTo(id int) {
	for i := range b.Tasks {
		t := &b.Tasks[i]
		kept := t.Links[:0]
		for _, l := range t.Links {
			if l.Task != id {
				kept = append(kept, l)
			}
		}
		t.Links = kept
	}
}

// Relations lists every link touching a task, outgoing first.
func (b *Board) Relations(id int) []Relation {
	t := b.Task(id)
	if t == nil {
		return nil
	}
	var out []Relation
	for _, l := range t.Links {
		if other := b.Task(l.Task); other != nil {
			out = append(out, Relation{Kind: l.Kind, Label: forwardLabel(l.Kind), Task: *other, Outgoing: true})
		}
	}
	for _, other := range b.Tasks {
		if other.ID == id {
			continue
		}
		for _, l := range other.Links {
			if l.Task == id {
				out = append(out, Relation{Kind: l.Kind, Label: inverseLabel(l.Kind), Task: other})
			}
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
// it is in the last column or archived.
func (b *Board) isFinished(t Task) bool {
	if t.Archived() {
		return true
	}
	done := b.DoneColumn()
	return done != nil && t.Column == done.ID
}

// Blockers returns the unfinished tasks that block a task.
func (b *Board) Blockers(id int) []Task {
	var out []Task
	for _, other := range b.Tasks {
		for _, l := range other.Links {
			if l.Kind == LinkBlocks && l.Task == id && !b.isFinished(other) {
				out = append(out, other)
			}
		}
	}
	return out
}

// IsBlocked reports whether a task has any unfinished blocker.
func (b *Board) IsBlocked(id int) bool {
	return len(b.Blockers(id)) > 0
}

// Subtasks returns the tasks that declare id as their parent.
func (b *Board) Subtasks(id int) []Task {
	var out []Task
	for _, other := range b.Tasks {
		for _, l := range other.Links {
			if l.Kind == LinkSubtaskOf && l.Task == id {
				out = append(out, other)
			}
		}
	}
	return out
}

// SubtaskProgress returns finished and total subtasks of a task.
func (b *Board) SubtaskProgress(id int) (int, int) {
	subs := b.Subtasks(id)
	done := 0
	for _, s := range subs {
		if b.isFinished(s) {
			done++
		}
	}
	return done, len(subs)
}

// Parent returns the task this one is a subtask of, if any.
func (b *Board) Parent(id int) *Task {
	t := b.Task(id)
	if t == nil {
		return nil
	}
	for _, l := range t.Links {
		if l.Kind == LinkSubtaskOf {
			return b.Task(l.Task)
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

var mentionRE = regexp.MustCompile(`(?:^|[^\w&])#(\d+)\b`)

// Mentions extracts task numbers written as #12 in free text.
func Mentions(text string) []int {
	seen := map[int]bool{}
	var out []int
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// linkMentions turns #N mentions in text into "relates to" links.
func (b *Board) linkMentions(from int, text string) {
	for _, id := range Mentions(text) {
		if id != from && b.Task(id) != nil {
			_ = b.AddLink(from, LinkRelates, id)
		}
	}
}
