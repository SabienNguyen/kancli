package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// columnStay summarises how long tasks spend in one column.
type columnStay struct {
	Column  string        `json:"column"`
	Name    string        `json:"name"`
	Samples int           `json:"samples"`
	Mean    time.Duration `json:"mean"`
	Median  time.Duration `json:"median"`
}

// weekCount is throughput for one ISO week (Monday start).
type weekCount struct {
	Week    time.Time `json:"week"`
	Created int       `json:"created"`
	Done    int       `json:"done"`
}

// dayCount is the number of tasks in progress at the end of a day.
type dayCount struct {
	Day   time.Time `json:"day"`
	Count int       `json:"count"`
}

// agingTask is a live task and how long it has sat in its column.
type agingTask struct {
	ID     int           `json:"id"`
	Title  string        `json:"title"`
	Column string        `json:"column"`
	Since  time.Time     `json:"since"`
	Age    time.Duration `json:"age"`
}

// labelStat summarises one label.
type labelStat struct {
	Label       string        `json:"label"`
	Open        int           `json:"open"`
	Done        int           `json:"done"`
	CycleMedian time.Duration `json:"cycle_median"`
}

// finishedTask is a task that reached the done column.
type finishedTask struct {
	ID        int           `json:"id"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"created_at"`
	DoneAt    time.Time     `json:"done_at"`
	Cycle     time.Duration `json:"cycle"`
	Labels    []string      `json:"labels,omitempty"`
}

// boardStats is everything the stats screen and command show.
type boardStats struct {
	Board       string         `json:"board"`
	Generated   time.Time      `json:"generated"`
	Days        int            `json:"days"`
	Events      int            `json:"events"`
	Live        int            `json:"live"`
	InProgress  int            `json:"in_progress"`
	Overdue     int            `json:"overdue"`
	DueToday    int            `json:"due_today"`
	Finished    []finishedTask `json:"finished"`
	CycleMean   time.Duration  `json:"cycle_mean"`
	CycleMedian time.Duration  `json:"cycle_median"`
	CycleP90    time.Duration  `json:"cycle_p90"`
	Stays       []columnStay   `json:"stays"`
	Weeks       []weekCount    `json:"weeks"`
	WIP         []dayCount     `json:"wip"`
	Aging       []agingTask    `json:"aging"`
	Labels      []labelStat    `json:"labels"`
}

// taskTrack is the per-task state the event walk maintains.
type taskTrack struct {
	id        int
	title     string
	labels    []string
	createdAt time.Time
	column    string
	since     time.Time
	doneAt    time.Time
	gone      bool // archived or deleted
}

// computeStats derives analytics for a board from its event history. days
// bounds the throughput and WIP series and the finished-task sample.
func computeStats(b *Board, events []Event, now time.Time, days int) boardStats {
	if days <= 0 {
		days = 90
	}
	st := boardStats{Board: b.ID, Generated: now, Days: days}
	first, done := "", ""
	if len(b.Columns) > 0 {
		first = b.Columns[0].ID
		done = b.DoneColumn().ID
	}
	colName := func(id string) string {
		if c := b.Column(id); c != nil {
			return c.Name
		}
		return id
	}
	isWIP := func(col string) bool { return col != "" && col != first && col != done }

	tracks := map[int]*taskTrack{}
	stays := map[string][]time.Duration{}
	type delta struct {
		at time.Time
		n  int
	}
	var wipDeltas []delta
	var finished []finishedTask
	weeks := map[time.Time]*weekCount{}
	weekOf := func(t time.Time) time.Time {
		y, m, d := t.Date()
		day := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
		offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
		return day.AddDate(0, 0, -offset)
	}
	bump := func(t time.Time, created, doneN int) {
		w := weekOf(t)
		wc := weeks[w]
		if wc == nil {
			wc = &weekCount{Week: w}
			weeks[w] = wc
		}
		wc.Created += created
		wc.Done += doneN
	}
	enter := func(tr *taskTrack, col string, at time.Time) {
		if tr.column != "" {
			stays[tr.column] = append(stays[tr.column], at.Sub(tr.since))
			if isWIP(tr.column) {
				wipDeltas = append(wipDeltas, delta{at, -1})
			}
		}
		tr.column, tr.since = col, at
		if isWIP(col) {
			wipDeltas = append(wipDeltas, delta{at, 1})
		}
		if col == done && tr.doneAt.IsZero() {
			tr.doneAt = at
			bump(at, 0, 1)
			finished = append(finished, finishedTask{ID: tr.id, Title: tr.title, CreatedAt: tr.createdAt, DoneAt: at, Cycle: at.Sub(tr.createdAt), Labels: tr.labels})
		} else if col != done {
			tr.doneAt = time.Time{}
		}
	}
	leave := func(tr *taskTrack, at time.Time) {
		if tr.gone {
			return
		}
		if tr.column != "" {
			stays[tr.column] = append(stays[tr.column], at.Sub(tr.since))
			if isWIP(tr.column) {
				wipDeltas = append(wipDeltas, delta{at, -1})
			}
		}
		tr.gone = true
	}

	for _, e := range events {
		if e.Board != b.ID {
			continue
		}
		st.Events++
		switch e.Kind {
		case evTaskCreated:
			var t Task
			if err := json.Unmarshal(e.Data, &t); err != nil {
				continue
			}
			tr := &taskTrack{id: t.ID, title: t.Title, labels: t.Labels, createdAt: e.At}
			tracks[t.ID] = tr
			bump(e.At, 1, 0)
			enter(tr, t.Column, e.At)
		case evTaskUpdated:
			var t Task
			if err := json.Unmarshal(e.Data, &t); err == nil {
				if tr := tracks[t.ID]; tr != nil {
					tr.title, tr.labels = t.Title, t.Labels
				}
			}
		case evTaskMoved:
			if tr := tracks[e.Task]; tr != nil && !tr.gone {
				enter(tr, e.To, e.At)
			}
		case evTaskArchived, evTaskDeleted:
			if tr := tracks[e.Task]; tr != nil {
				leave(tr, e.At)
			}
		case evTaskRestored:
			if tr := tracks[e.Task]; tr != nil && tr.gone {
				tr.gone = false
				tr.column = ""
				enter(tr, e.To, e.At)
			}
		case evColumnRemoved:
			for _, tr := range tracks {
				if !tr.gone && tr.column == e.From {
					if e.To == "" {
						leave(tr, e.At)
					} else {
						enter(tr, e.To, e.At)
					}
				}
			}
		case evBoardRestored:
			// Undo replaces the board wholesale; re-sync the tracks with it.
			var nb Board
			if err := json.Unmarshal(e.Data, &nb); err != nil {
				continue
			}
			seen := map[int]bool{}
			for _, t := range nb.Tasks {
				seen[t.ID] = true
				tr := tracks[t.ID]
				if tr == nil {
					tr = &taskTrack{id: t.ID, title: t.Title, labels: t.Labels, createdAt: t.CreatedAt}
					tracks[t.ID] = tr
					enter(tr, t.Column, e.At)
					continue
				}
				switch {
				case t.Archived() && !tr.gone:
					leave(tr, e.At)
				case !t.Archived() && tr.gone:
					tr.gone, tr.column = false, ""
					enter(tr, t.Column, e.At)
				case !t.Archived() && tr.column != t.Column:
					enter(tr, t.Column, e.At)
				}
			}
			for id, tr := range tracks {
				if !seen[id] {
					leave(tr, e.At)
				}
			}
		}
	}

	// Headline counts from the live board.
	for _, t := range b.Live() {
		st.Live++
		if isWIP(t.Column) {
			st.InProgress++
		}
		if t.Column != done {
			switch _, s := dueInfo(t, now); s {
			case dueOverdue:
				st.Overdue++
			case dueToday:
				st.DueToday++
			}
		}
	}

	// Finished tasks within the window, newest first.
	cutoff := now.AddDate(0, 0, -days)
	for _, ft := range finished {
		if ft.DoneAt.After(cutoff) {
			st.Finished = append(st.Finished, ft)
		}
	}
	sort.Slice(st.Finished, func(i, j int) bool { return st.Finished[i].DoneAt.After(st.Finished[j].DoneAt) })
	cycles := make([]time.Duration, 0, len(st.Finished))
	for _, ft := range st.Finished {
		cycles = append(cycles, ft.Cycle)
	}
	st.CycleMean, st.CycleMedian, st.CycleP90 = summarize(cycles)

	// Column stays.
	for _, c := range b.Columns {
		ds := stays[c.ID]
		mean, median, _ := summarize(ds)
		st.Stays = append(st.Stays, columnStay{Column: c.ID, Name: c.Name, Samples: len(ds), Mean: mean, Median: median})
	}

	// Weekly throughput for the window.
	nWeeks := (days + 6) / 7
	start := weekOf(now).AddDate(0, 0, -7*(nWeeks-1))
	for i := 0; i < nWeeks; i++ {
		w := start.AddDate(0, 0, 7*i)
		wc := weekCount{Week: w}
		if got := weeks[w]; got != nil {
			wc = *got
		}
		st.Weeks = append(st.Weeks, wc)
	}

	// WIP at the end of each day.
	sort.SliceStable(wipDeltas, func(i, j int) bool { return wipDeltas[i].at.Before(wipDeltas[j].at) })
	nDays := min(days, 60)
	y, mo, d := now.Date()
	todayStart := time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
	for i := nDays - 1; i >= 0; i-- {
		day := todayStart.AddDate(0, 0, -i)
		end := day.AddDate(0, 0, 1)
		if i == 0 {
			end = now.Add(time.Nanosecond)
		}
		count := 0
		for _, dl := range wipDeltas {
			if dl.at.Before(end) {
				count += dl.n
			}
		}
		st.WIP = append(st.WIP, dayCount{Day: day, Count: max(0, count)})
	}

	// Aging live tasks (not done), oldest first.
	for _, t := range b.Live() {
		if t.Column == done {
			continue
		}
		since := t.CreatedAt
		if tr := tracks[t.ID]; tr != nil && !tr.since.IsZero() {
			since = tr.since
		}
		st.Aging = append(st.Aging, agingTask{ID: t.ID, Title: t.Title, Column: colName(t.Column), Since: since, Age: now.Sub(since)})
	}
	sort.Slice(st.Aging, func(i, j int) bool { return st.Aging[i].Age > st.Aging[j].Age })
	if len(st.Aging) > 10 {
		st.Aging = st.Aging[:10]
	}

	// Labels: open counts from the board, done counts and cycle times from
	// the finished sample.
	labels := map[string]*labelStat{}
	get := func(l string) *labelStat {
		ls := labels[l]
		if ls == nil {
			ls = &labelStat{Label: l}
			labels[l] = ls
		}
		return ls
	}
	for _, t := range b.Live() {
		if t.Column == done {
			continue
		}
		for _, l := range t.Labels {
			get(l).Open++
		}
	}
	labelCycles := map[string][]time.Duration{}
	for _, ft := range st.Finished {
		for _, l := range ft.Labels {
			get(l).Done++
			labelCycles[l] = append(labelCycles[l], ft.Cycle)
		}
	}
	for l, ls := range labels {
		_, ls.CycleMedian, _ = summarize(labelCycles[l])
		st.Labels = append(st.Labels, *ls)
	}
	sort.Slice(st.Labels, func(i, j int) bool {
		if st.Labels[i].Open+st.Labels[i].Done != st.Labels[j].Open+st.Labels[j].Done {
			return st.Labels[i].Open+st.Labels[i].Done > st.Labels[j].Open+st.Labels[j].Done
		}
		return st.Labels[i].Label < st.Labels[j].Label
	})
	return st
}

// summarize returns mean, median and 90th percentile of durations.
func summarize(ds []time.Duration) (mean, median, p90 time.Duration) {
	if len(ds) == 0 {
		return 0, 0, 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	mean = total / time.Duration(len(sorted))
	median = sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	p90 = sorted[min(len(sorted)-1, int(float64(len(sorted))*0.9))]
	return mean, median, p90
}

// humanDuration renders a duration as "3d 4h" style text.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "0h"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0 && hours > 0:
		return strings.TrimSpace(itoa(days) + "d " + itoa(hours) + "h")
	case days > 0:
		return itoa(days) + "d"
	case hours > 0:
		return itoa(hours) + "h"
	default:
		return itoa(max(1, mins)) + "m"
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
