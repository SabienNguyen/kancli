package main

import (
	"strconv"
	"strings"
	"time"
)

// query is a parsed search. Free words match anywhere in the task; the
// structured terms narrow by field:
//
//	#12            task id
//	@name          assignee
//	label:x  +x    label
//	p:high         priority (or priority:)
//	due:today      due date: today, tomorrow, overdue, week, none, YYYY-MM-DD
//	col:todo       column id or name prefix (or column:, c:)
type query struct {
	words    []string
	ids      []int
	assignee string
	labels   []string
	priority *Priority
	due      string
	column   string
}

func parseQuery(s string) query {
	var q query
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
				if p, err := parsePriority(val); err == nil {
					q.priority = &p
					continue
				}
			case "due", "d":
				q.due = val
				continue
			case "col", "column", "c":
				q.column = val
				continue
			case "assignee", "a", "who":
				q.assignee = val
				continue
			}
		}
		q.words = append(q.words, tok)
	}
	return q
}

func (q query) empty() bool {
	return len(q.words) == 0 && len(q.ids) == 0 && q.assignee == "" && len(q.labels) == 0 &&
		q.priority == nil && q.due == "" && q.column == ""
}

// matches reports whether a task satisfies the query.
func (q query) matches(b *Board, t Task, now time.Time) bool {
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
	if q.column != "" {
		c := b.Column(q.column)
		if c == nil || c.ID != t.Column {
			return false
		}
	}
	if q.due != "" && !matchDue(t, q.due, now) {
		return false
	}
	if len(q.words) > 0 {
		hay := strings.ToLower(strings.Join(append([]string{
			t.Title, t.Description, t.Assignee, strings.Join(t.Labels, " "),
		}, commentTexts(t)...), "\n"))
		for _, w := range q.words {
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
	_, state := dueInfo(t, now)
	due, has := t.DueDate()
	switch want {
	case "none", "no":
		return !has
	case "any", "set":
		return has
	case "overdue", "late":
		return state == dueOverdue
	case "today":
		return state == dueToday
	case "tomorrow":
		return has && daysBetween(now, due) == 1
	case "week", "soon":
		return state == dueToday || state == dueSoon || state == dueOverdue
	}
	if d, err := parseDue(want, now); err == nil {
		return t.Due == d
	}
	return false
}
