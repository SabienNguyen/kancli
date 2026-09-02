package main

import (
	"sort"
	"strings"
)

// sortMode controls the display order of tasks within a column.
type sortMode int

const (
	sortManual sortMode = iota
	sortPriority
	sortDue
	sortCreated
	sortUpdated
	sortTitle
	numSortModes
)

var sortNames = [numSortModes]string{"manual", "priority", "due", "created", "updated", "title"}

func (s sortMode) String() string {
	if s < 0 || s >= numSortModes {
		return "manual"
	}
	return sortNames[s]
}

func parseSortMode(s string) (sortMode, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, n := range sortNames {
		if n == s {
			return sortMode(i), true
		}
	}
	return sortManual, false
}

func (s sortMode) next() sortMode { return (s + 1) % numSortModes }

// sortTasks orders tasks in place for display. Manual order is the board
// order and is left untouched.
func sortTasks(ts []Task, mode sortMode) {
	var less func(i, j int) bool
	switch mode {
	case sortPriority:
		less = func(i, j int) bool {
			if ts[i].Priority != ts[j].Priority {
				return ts[i].Priority > ts[j].Priority
			}
			return dueLess(ts[i], ts[j])
		}
	case sortDue:
		less = func(i, j int) bool { return dueLess(ts[i], ts[j]) }
	case sortCreated:
		less = func(i, j int) bool { return ts[i].CreatedAt.After(ts[j].CreatedAt) }
	case sortUpdated:
		less = func(i, j int) bool { return ts[i].UpdatedAt.After(ts[j].UpdatedAt) }
	case sortTitle:
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
