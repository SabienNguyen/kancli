package board

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// columnStay summarises how long tasks spend in one column.
type ColumnStay struct {
	Column  string        `json:"column"`
	Name    string        `json:"name"`
	Samples int           `json:"samples"`
	Mean    time.Duration `json:"mean"`
	Median  time.Duration `json:"median"`
}

// weekCount is throughput for one ISO week (Monday start).
type WeekCount struct {
	Week    time.Time `json:"week"`
	Created int       `json:"created"`
	Done    int       `json:"done"`
}

// dayCount is the number of tasks in progress at the end of a day.
type DayCount struct {
	Day   time.Time `json:"day"`
	Count int       `json:"count"`
}

// agingTask is a live task and how long it has sat in its column.
type AgingTask struct {
	ID     int           `json:"id"`
	Title  string        `json:"title"`
	Column string        `json:"column"`
	Since  time.Time     `json:"since"`
	Age    time.Duration `json:"age"`
}

// labelStat summarises one label.
type LabelStat struct {
	Label       string        `json:"label"`
	Open        int           `json:"open"`
	Done        int           `json:"done"`
	CycleMedian time.Duration `json:"cycle_median"`
}

// finishedTask is a task that reached the done column.
type FinishedTask struct {
	ID        int           `json:"id"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"created_at"`
	DoneAt    time.Time     `json:"done_at"`
	Cycle     time.Duration `json:"cycle"`
	Labels    []string      `json:"labels,omitempty"`
}

// Stats is everything the stats screen and command show.
type Stats struct {
	Board       string         `json:"board"`
	Generated   time.Time      `json:"generated"`
	Days        int            `json:"days"`
	Events      int            `json:"events"`
	Live        int            `json:"live"`
	InProgress  int            `json:"in_progress"`
	Overdue     int            `json:"overdue"`
	Blocked     int            `json:"blocked"`
	DueToday    int            `json:"due_today"`
	Finished    []FinishedTask `json:"finished"`
	CycleMean   time.Duration  `json:"cycle_mean"`
	CycleMedian time.Duration  `json:"cycle_median"`
	CycleP90    time.Duration  `json:"cycle_p90"`
	Stays       []ColumnStay   `json:"stays"`
	Weeks       []WeekCount    `json:"weeks"`
	WIP         []DayCount     `json:"wip"`
	Aging       []AgingTask    `json:"aging"`
	Labels      []LabelStat    `json:"labels"`
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

type wipDelta struct {
	at time.Time
	n  int
}

// statsWalker folds events into the intermediate state that Stats is
// derived from. It can be kept between calls and fed only new events.
type StatsWalker struct {
	board  string
	first  string
	done   string
	seq    int64
	events int

	tracks   map[int]*taskTrack
	stays    map[string][]time.Duration
	wip      []wipDelta
	finished []FinishedTask
	weeks    map[int64]*WeekCount // keyed by the week's Unix time
}

// Seq is the sequence number of the last event the walker has folded in.
func (w *StatsWalker) Seq() int64 { return w.seq }

func NewStatsWalker(b *Board) *StatsWalker {
	w := &StatsWalker{board: b.ID, tracks: map[int]*taskTrack{}, stays: map[string][]time.Duration{}, weeks: map[int64]*WeekCount{}}
	if len(b.Columns) > 0 {
		w.first = b.Columns[0].ID
		w.done = b.DoneColumn().ID
	}
	return w
}

// compatible reports whether the walker's notion of first and done columns
// still matches the board; if not, the history must be re-walked.
func (w *StatsWalker) Compatible(b *Board) bool {
	first, done := "", ""
	if len(b.Columns) > 0 {
		first = b.Columns[0].ID
		done = b.DoneColumn().ID
	}
	return w.board == b.ID && w.first == first && w.done == done
}

func (w *StatsWalker) isWIP(col string) bool { return col != "" && col != w.first && col != w.done }

// weekOf returns the Monday starting the local week containing t. Times
// are normalised to the local zone so events written on another machine
// (or read back as UTC) land in the same buckets as "now".
func weekOf(t time.Time) time.Time {
	t = t.In(time.Local)
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
	return day.AddDate(0, 0, -offset)
}

func (w *StatsWalker) bump(t time.Time, created, doneN int) {
	wk := weekOf(t)
	wc := w.weeks[wk.Unix()]
	if wc == nil {
		wc = &WeekCount{Week: wk}
		w.weeks[wk.Unix()] = wc
	}
	wc.Created += created
	wc.Done += doneN
}

func (w *StatsWalker) enter(tr *taskTrack, col string, at time.Time) {
	if tr.column != "" {
		w.stays[tr.column] = append(w.stays[tr.column], at.Sub(tr.since))
		if w.isWIP(tr.column) {
			w.wip = append(w.wip, wipDelta{at, -1})
		}
	}
	tr.column, tr.since = col, at
	if w.isWIP(col) {
		w.wip = append(w.wip, wipDelta{at, 1})
	}
	if col == w.done && tr.doneAt.IsZero() {
		tr.doneAt = at
		w.bump(at, 0, 1)
		w.finished = append(w.finished, FinishedTask{ID: tr.id, Title: tr.title, CreatedAt: tr.createdAt, DoneAt: at, Cycle: at.Sub(tr.createdAt), Labels: tr.labels})
	} else if col != w.done {
		tr.doneAt = time.Time{}
	}
}

func (w *StatsWalker) leave(tr *taskTrack, at time.Time) {
	if tr.gone {
		return
	}
	if tr.column != "" {
		w.stays[tr.column] = append(w.stays[tr.column], at.Sub(tr.since))
		if w.isWIP(tr.column) {
			w.wip = append(w.wip, wipDelta{at, -1})
		}
	}
	tr.gone = true
}

// syncTrack re-syncs one task's track with the task as an undo left it:
// task.reverted carries a single task, board.restored a whole board of
// them, and both must fold in the same way.
func (w *StatsWalker) syncTrack(t Task, at time.Time) {
	tr := w.tracks[t.ID]
	if tr == nil {
		tr = &taskTrack{id: t.ID, title: t.Title, labels: t.Labels, createdAt: t.CreatedAt}
		w.tracks[t.ID] = tr
		w.enter(tr, t.Column, at)
		return
	}
	switch {
	case t.Archived() && !tr.gone:
		w.leave(tr, at)
	case !t.Archived() && tr.gone:
		w.unfinish(tr, t.Column)
		tr.gone, tr.column = false, ""
		w.enter(tr, t.Column, at)
	case !t.Archived() && tr.column != t.Column:
		w.unfinish(tr, t.Column)
		w.enter(tr, t.Column, at)
	}
}

// unfinish takes back the completion recorded for a task that an undo has
// moved back out of the done column: the task is not finished any more, so
// neither its cycle time nor its week's throughput should say it is.
func (w *StatsWalker) unfinish(tr *taskTrack, col string) {
	if col == w.done || tr.doneAt.IsZero() {
		return
	}
	for i := len(w.finished) - 1; i >= 0; i-- {
		if w.finished[i].ID == tr.id && w.finished[i].DoneAt.Equal(tr.doneAt) {
			w.finished = append(w.finished[:i], w.finished[i+1:]...)
			break
		}
	}
	w.bump(tr.doneAt, 0, -1)
	tr.doneAt = time.Time{}
}

// feed applies events the walker has not seen yet. Events must arrive in
// sequence order.
func (w *StatsWalker) Feed(events []Event) {
	for _, e := range events {
		if e.Board != w.board || (e.Seq != 0 && e.Seq <= w.seq) {
			continue
		}
		if e.Seq > w.seq {
			w.seq = e.Seq
		}
		w.events++
		switch e.Kind {
		case EvTaskCreated:
			var t Task
			if err := json.Unmarshal(e.Data, &t); err != nil {
				continue
			}
			tr := &taskTrack{id: t.ID, title: t.Title, labels: t.Labels, createdAt: e.At}
			w.tracks[t.ID] = tr
			w.bump(e.At, 1, 0)
			w.enter(tr, t.Column, e.At)
		case EvTaskUpdated:
			var t Task
			if err := json.Unmarshal(e.Data, &t); err == nil {
				if tr := w.tracks[t.ID]; tr != nil {
					tr.title, tr.labels = t.Title, t.Labels
				}
			}
		case EvTaskMoved:
			if tr := w.tracks[e.Task]; tr != nil && !tr.gone {
				w.enter(tr, e.To, e.At)
			}
		case EvTaskArchived, EvTaskDeleted:
			if tr := w.tracks[e.Task]; tr != nil {
				w.leave(tr, e.At)
			}
		case EvTaskRestored:
			if tr := w.tracks[e.Task]; tr != nil && tr.gone {
				tr.gone = false
				tr.column = ""
				w.enter(tr, e.To, e.At)
			}
		case EvColumnRemoved:
			for _, tr := range w.tracks {
				if !tr.gone && tr.column == e.From {
					if e.To == "" {
						w.leave(tr, e.At)
					} else {
						w.enter(tr, e.To, e.At)
					}
				}
			}
		case EvTaskReverted:
			// Undo put one task back as it was; re-sync its track.
			var t Task
			if err := json.Unmarshal(e.Data, &t); err != nil {
				continue
			}
			w.syncTrack(t, e.At)
		case EvBoardRestored:
			// Undo replaces the board wholesale; re-sync the tracks with it.
			var nb Board
			if err := json.Unmarshal(e.Data, &nb); err != nil {
				continue
			}
			seen := map[int]bool{}
			for _, t := range nb.Tasks {
				seen[t.ID] = true
				w.syncTrack(t, e.At)
			}
			for id, tr := range w.tracks {
				if !seen[id] {
					w.leave(tr, e.At)
				}
			}
		}
	}
}

// finish derives Stats from the walked state and the live board. It
// does not modify the walker, so it can be called repeatedly.
func (w *StatsWalker) Finish(b *Board, now time.Time, days int) Stats {
	if days <= 0 {
		days = 90
	}
	st := Stats{Board: b.ID, Generated: now, Days: days, Events: w.events}
	done := w.done

	// Headline counts from the live board.
	st.Blocked = b.BlockedCount()
	for _, t := range b.Live() {
		st.Live++
		if w.isWIP(t.Column) {
			st.InProgress++
		}
		if t.Column != done {
			switch _, s := DueInfo(t, now); s {
			case DueOverdue:
				st.Overdue++
			case DueToday:
				st.DueToday++
			}
		}
	}

	// Finished tasks within the window, newest first.
	cutoff := now.AddDate(0, 0, -days)
	for _, ft := range w.finished {
		if ft.DoneAt.After(cutoff) {
			st.Finished = append(st.Finished, ft)
		}
	}
	sort.Slice(st.Finished, func(i, j int) bool { return st.Finished[i].DoneAt.After(st.Finished[j].DoneAt) })
	cycles := make([]time.Duration, 0, len(st.Finished))
	for _, ft := range st.Finished {
		cycles = append(cycles, ft.Cycle)
	}
	st.CycleMean, st.CycleMedian, st.CycleP90 = Summarize(cycles)

	// Column stays.
	for _, c := range b.Columns {
		ds := w.stays[c.ID]
		mean, median, _ := Summarize(ds)
		st.Stays = append(st.Stays, ColumnStay{Column: c.ID, Name: c.Name, Samples: len(ds), Mean: mean, Median: median})
	}

	// Weekly throughput for the window.
	nWeeks := (days + 6) / 7
	start := weekOf(now).AddDate(0, 0, -7*(nWeeks-1))
	for i := 0; i < nWeeks; i++ {
		wk := start.AddDate(0, 0, 7*i)
		wc := WeekCount{Week: wk}
		if got := w.weeks[wk.Unix()]; got != nil {
			wc = *got
		}
		st.Weeks = append(st.Weeks, wc)
	}

	// WIP at the end of each day.
	deltas := append([]wipDelta(nil), w.wip...)
	sort.SliceStable(deltas, func(i, j int) bool { return deltas[i].at.Before(deltas[j].at) })
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
		for _, dl := range deltas {
			if dl.at.Before(end) {
				count += dl.n
			}
		}
		st.WIP = append(st.WIP, DayCount{Day: day, Count: max(0, count)})
	}

	// Aging live tasks (not done), oldest first.
	for _, t := range b.Live() {
		if t.Column == done {
			continue
		}
		since := t.CreatedAt
		if tr := w.tracks[t.ID]; tr != nil && !tr.since.IsZero() {
			since = tr.since
		}
		st.Aging = append(st.Aging, AgingTask{ID: t.ID, Title: t.Title, Column: ColName(b, t.Column), Since: since, Age: now.Sub(since)})
	}
	sort.Slice(st.Aging, func(i, j int) bool { return st.Aging[i].Age > st.Aging[j].Age })
	if len(st.Aging) > 10 {
		st.Aging = st.Aging[:10]
	}

	// Labels: open counts from the board, done counts and cycle times from
	// the finished sample.
	labels := map[string]*LabelStat{}
	get := func(l string) *LabelStat {
		ls := labels[l]
		if ls == nil {
			ls = &LabelStat{Label: l}
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
		_, ls.CycleMedian, _ = Summarize(labelCycles[l])
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

// computeStats derives analytics for a board from its full event history.
func ComputeStats(b *Board, events []Event, now time.Time, days int) Stats {
	w := NewStatsWalker(b)
	w.Feed(events)
	return w.Finish(b, now, days)
}

// summarize returns mean, median and 90th percentile of durations.
func Summarize(ds []time.Duration) (mean, median, p90 time.Duration) {
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
func HumanDuration(d time.Duration) string {
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
