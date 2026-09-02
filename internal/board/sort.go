package board

import (
	"sort"
	"strings"
)

// sortMode controls the display order of tasks within a column.
type SortMode int

const (
	SortManual SortMode = iota
	SortPriority
	SortDue
	SortCreated
	SortUpdated
	SortTitle
	SortRelevance
	NumSortModes
)

var sortNames = [NumSortModes]string{"manual", "priority", "due", "created", "updated", "title", "relevance"}

func (s SortMode) String() string {
	if s < 0 || s >= NumSortModes {
		return "manual"
	}
	return sortNames[s]
}

func ParseSortMode(s string) (SortMode, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, n := range sortNames {
		if n == s {
			return SortMode(i), true
		}
	}
	return SortManual, false
}

// next cycles through the user-selectable modes; relevance is applied
// automatically while searching.
func (s SortMode) Next() SortMode {
	n := (s + 1) % NumSortModes
	if n == SortRelevance {
		n = SortManual
	}
	return n
}

// sortTasks orders tasks in place for display. Manual order is the board
// order and is left untouched.
func SortTasks(ts []Task, mode SortMode) {
	var less func(i, j int) bool
	switch mode {
	case SortPriority:
		less = func(i, j int) bool {
			if ts[i].Priority != ts[j].Priority {
				return ts[i].Priority > ts[j].Priority
			}
			return dueLess(ts[i], ts[j])
		}
	case SortDue:
		less = func(i, j int) bool { return dueLess(ts[i], ts[j]) }
	case SortCreated:
		less = func(i, j int) bool { return ts[i].CreatedAt.After(ts[j].CreatedAt) }
	case SortUpdated:
		less = func(i, j int) bool { return ts[i].UpdatedAt.After(ts[j].UpdatedAt) }
	case SortTitle:
		less = func(i, j int) bool { return strings.ToLower(ts[i].Title) < strings.ToLower(ts[j].Title) }
	default:
		return
	}
	sort.SliceStable(ts, less)
}

// dueLess puts earlier due dates first and undated tasks last.
func dueLess(a, b Task) bool {
	da, oka := a.DueDate()
	db, okb := b.DueDate()
	switch {
	case oka && okb:
		return da.Before(db)
	case oka:
		return true
	default:
		return false
	}
}
