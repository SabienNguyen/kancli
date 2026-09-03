package board

import (
	"strconv"
	"strings"
	"time"
)

// query is a parsed search. Free Words match anywhere in the task; the
// structured terms narrow by field:
//
//	#12            task id
//	@name          assignee
//	label:x  +x    label
//	p:high         priority (or priority:)
//	due:today      due date: today, tomorrow, overdue, week, none, YYYY-MM-DD
//	col:todo       column id or name prefix (or column:, c:)
type Query struct {
	Words     []string
	ids       []int
	assignee  string
	labels    []string
	priority  *Priority
	due       string
	column    string
	blocked   string // "yes" or "no"
	blocks    Ref    // tasks that block this ref
	blockedBy Ref    // tasks blocked by this ref
	parent    Ref    // subtasks of this ref
	has       []string
}

func ParseQuery(s string) Query {
	var q Query
	for _, tok := range strings.Fields(strings.ToLower(s)) {
		key, val, hasKey := strings.Cut(tok, ":")
		switch {
		case strings.HasPrefix(tok, "#") && len(tok) > 1:
			if n, err := strconv.Atoi(tok[1:]); err == nil {
				q.ids = append(q.ids, n)
				continue
			}
		case strings.HasPrefix(tok, "@") && len(tok) > 1:
			q.assignee = tok[1:]
			continue
		case strings.HasPrefix(tok, "+") && len(tok) > 1:
			q.labels = append(q.labels, tok[1:])
			continue
		}
		if hasKey && val != "" {
			switch key {
			case "label", "l", "tag", "t":
				q.labels = append(q.labels, val)
				continue
			case "p", "priority", "prio":
				if p, err := ParsePriority(val); err == nil {
					q.priority = &p
					continue
				}
			case "due", "d":
				q.due = val
				continue
			case "col", "column", "c":
				q.column = val
				continue
			case "blocked", "is":
				if val == "blocked" || val == "yes" || val == "true" {
					q.blocked = "yes"
					continue
				}
				if val == "unblocked" || val == "no" || val == "false" {
					q.blocked = "no"
					continue
				}
			case "blocks":
				if r, ok := parseFilterRef(val); ok {
					q.blocks = r
					continue
				}
			case "blockedby", "blocked-by", "blocked_by":
				if r, ok := parseFilterRef(val); ok {
					q.blockedBy = r
					continue
				}
			case "parent", "subtaskof", "subtask-of", "subtask_of":
				if r, ok := parseFilterRef(val); ok {
					q.parent = r
					continue
				}
			case "has":
				q.has = append(q.has, val)
				continue
			case "assignee", "a", "who":
				q.assignee = val
				continue
			}
		}
		q.Words = append(q.Words, tok)
	}
	return q
}

func (q Query) Empty() bool {
	return len(q.Words) == 0 && len(q.ids) == 0 && q.assignee == "" && len(q.labels) == 0 &&
		q.priority == nil && q.due == "" && q.column == "" && q.blocked == "" && q.blocks.ID == 0 &&
		q.blockedBy.ID == 0 && q.parent.ID == 0 && len(q.has) == 0
}

// parseFilterRef reads the value of a link filter: "12", "#12" or
// "work#12". The board is kept as typed and resolved against the searched
// board's file in Matches, because a query is parsed without one.
func parseFilterRef(val string) (Ref, bool) {
	name, num, hasHash := strings.Cut(val, "#")
	if !hasHash {
		name, num = "", name
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return Ref{}, false
	}
	if name != "" && !boardWordRE.MatchString(name) {
		return Ref{}, false
	}
	return Ref{Board: name, ID: n}, true
}

// resolveFilterRef turns a filter ref into one relative to b. It fails when
// the board it names is unknown, and then nothing matches.
func resolveFilterRef(b *Board, r Ref) (Ref, bool) {
	if r.Board == "" {
		return r, true
	}
	out, err := ParseRef(r.String(), b, b.file)
	if err != nil {
		return Ref{}, false
	}
	return out, true
}

// matches reports whether a task satisfies the query.
func (q Query) Matches(b *Board, t Task, now time.Time) bool {
	if len(q.ids) > 0 {
		found := false
		for _, id := range q.ids {
			if id == t.ID {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if q.assignee != "" && !strings.Contains(strings.ToLower(t.Assignee), q.assignee) {
		return false
	}
	for _, l := range q.labels {
		if !hasLabel(t.Labels, l) {
			return false
		}
	}
	if q.priority != nil && t.Priority != *q.priority {
		return false
	}
	if q.blocked == "yes" && !b.IsBlocked(t.ID) {
		return false
	}
	if q.blocked == "no" && b.IsBlocked(t.ID) {
		return false
	}
	if q.blocks.ID != 0 {
		r, ok := resolveFilterRef(b, q.blocks)
		if !ok || !b.taskLinksTo(t, LinkBlocks, r) {
			return false
		}
	}
	if q.blockedBy.ID != 0 {
		r, ok := resolveFilterRef(b, q.blockedBy)
		if !ok {
			return false
		}
		ob, other := b.Resolve(Link{Kind: LinkBlocks, Task: r.ID, Board: r.Board})
		if other == nil || !ob.taskLinksTo(*other, LinkBlocks, Ref{Board: b.ID, ID: t.ID}) {
			return false
		}
	}
	if q.parent.ID != 0 {
		r, ok := resolveFilterRef(b, q.parent)
		if !ok || !b.taskLinksTo(t, LinkSubtaskOf, r) {
			return false
		}
	}
	for _, h := range q.has {
		switch h {
		case "subtasks", "subtask", "children":
			if len(b.Subtasks(t.ID)) == 0 {
				return false
			}
		case "blockers", "blocker":
			if !b.IsBlocked(t.ID) {
				return false
			}
		case "links", "link":
			if len(b.Relations(t.ID)) == 0 {
				return false
			}
		case "parent":
			if b.Parent(t.ID) == nil {
				return false
			}
		case "due":
			if t.Due == "" {
				return false
			}
		case "checklist":
			if len(t.Checklist) == 0 {
				return false
			}
		default:
			return false
		}
	}
	if q.column != "" {
		c := b.Column(q.column)
		if c == nil || c.ID != t.Column {
			return false
		}
	}
	if q.due != "" && !matchDue(t, q.due, now) {
		return false
	}
	if len(q.Words) > 0 {
		hay := strings.ToLower(strings.Join(append([]string{
			t.Title, t.Description, t.Assignee, strings.Join(t.Labels, " "),
		}, commentTexts(t)...), "\n"))
		for _, w := range q.Words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	return true
}

func commentTexts(t Task) []string {
	out := make([]string, 0, len(t.Comments)+len(t.Checklist))
	for _, c := range t.Comments {
		out = append(out, c.Text)
	}
	for _, c := range t.Checklist {
		out = append(out, c.Text)
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.HasPrefix(l, want) {
			return true
		}
	}
	return false
}

func matchDue(t Task, want string, now time.Time) bool {
	_, state := DueInfo(t, now)
	due, has := t.DueDate()
	switch want {
	case "none", "no":
		return !has
	case "any", "set":
		return has
	case "overdue", "late":
		return state == DueOverdue
	case "today":
		return state == DueToday
	case "tomorrow":
		return has && DaysBetween(now, due) == 1
	case "week", "soon":
		return state == DueToday || state == DueSoon || state == DueOverdue
	}
	if d, err := ParseDue(want, now); err == nil {
		return t.Due == d
	}
	return false
}
