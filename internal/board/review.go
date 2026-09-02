package board

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// reviewReport is a Markdown summary of a period, built from events.
func ReviewReport(b *Board, events []Event, now time.Time, days int) string {
	if days <= 0 {
		days = 7
	}
	since := now.AddDate(0, 0, -days)
	st := ComputeStats(b, events, now, days)
	done := ""
	first := ""
	if len(b.Columns) > 0 {
		done = b.DoneColumn().ID
		first = b.Columns[0].ID
	}
	title := func(id int) string {
		if t := b.Task(id); t != nil {
			return t.Title
		}
		return ""
	}

	var created, started, archived []string
	seenStarted := map[int]bool{}
	for _, e := range events {
		if e.Board != b.ID || !e.At.After(since) {
			continue
		}
		switch e.Kind {
		case EvTaskCreated:
			var t Task
			if err := json.Unmarshal(e.Data, &t); err == nil {
				created = append(created, fmt.Sprintf("- #%d %s", t.ID, t.Title))
			}
		case EvTaskMoved:
			if e.From == first && e.To != done && !seenStarted[e.Task] {
				seenStarted[e.Task] = true
				started = append(started, fmt.Sprintf("- #%d %s → %s", e.Task, title(e.Task), ColName(b, e.To)))
			}
		case EvTaskArchived:
			archived = append(archived, fmt.Sprintf("- #%d %s", e.Task, title(e.Task)))
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s · review of the last %d days\n\n", b.Name, days)
	fmt.Fprintf(&sb, "_%s to %s_\n\n", since.Format("Jan 2"), now.Format("Jan 2 2006"))

	fmt.Fprintf(&sb, "## Summary\n\n")
	fmt.Fprintf(&sb, "- Finished: **%d**\n", len(st.Finished))
	fmt.Fprintf(&sb, "- Added: **%d**\n", len(created))
	fmt.Fprintf(&sb, "- Started: **%d**\n", len(started))
	fmt.Fprintf(&sb, "- Open now: **%d** (%d in progress)\n", st.Live, st.InProgress)
	if len(st.Finished) > 0 {
		fmt.Fprintf(&sb, "- Cycle time: median **%s**, mean %s, p90 %s\n", HumanDuration(st.CycleMedian), HumanDuration(st.CycleMean), HumanDuration(st.CycleP90))
	}
	if st.Overdue > 0 || st.DueToday > 0 {
		fmt.Fprintf(&sb, "- Due: **%d overdue**, %d due today\n", st.Overdue, st.DueToday)
	}
	sb.WriteString("\n")

	sb.WriteString("## Finished\n\n")
	if len(st.Finished) == 0 {
		sb.WriteString("_nothing finished in this period_\n")
	}
	for _, ft := range st.Finished {
		labels := ""
		if len(ft.Labels) > 0 {
			labels = " `+" + strings.Join(ft.Labels, " +") + "`"
		}
		fmt.Fprintf(&sb, "- [x] #%d %s%s — %s, took %s\n", ft.ID, ft.Title, labels, ft.DoneAt.Format("Mon Jan 2"), HumanDuration(ft.Cycle))
	}
	sb.WriteString("\n")

	section := func(name string, lines []string) {
		fmt.Fprintf(&sb, "## %s\n\n", name)
		if len(lines) == 0 {
			sb.WriteString("_none_\n")
		}
		for _, l := range lines {
			sb.WriteString(l + "\n")
		}
		sb.WriteString("\n")
	}
	section("Started", started)
	section("Added", created)
	if len(archived) > 0 {
		section("Archived", archived)
	}

	var overdue []string
	live := b.Live()
	SortTasks(live, SortDue)
	for _, t := range live {
		if t.Column == done {
			continue
		}
		if label, state := DueInfo(t, now); state == DueOverdue || state == DueToday {
			overdue = append(overdue, fmt.Sprintf("- [ ] #%d %s — %s", t.ID, t.Title, label))
		}
	}
	section("Needs attention", overdue)

	if len(st.Aging) > 0 {
		sb.WriteString("## Oldest open tasks\n\n")
		for i, a := range st.Aging {
			if i == 5 {
				break
			}
			fmt.Fprintf(&sb, "- #%d %s — %s in %s\n", a.ID, a.Title, HumanDuration(a.Age), a.Column)
		}
		sb.WriteString("\n")
	}

	if len(st.Labels) > 0 {
		sb.WriteString("## Labels\n\n| Label | Open | Finished | Median cycle |\n| --- | ---: | ---: | ---: |\n")
		labels := append([]LabelStat(nil), st.Labels...)
		sort.SliceStable(labels, func(i, j int) bool { return labels[i].Done > labels[j].Done })
		for _, ls := range labels {
			cycle := ""
			if ls.Done > 0 {
				cycle = HumanDuration(ls.CycleMedian)
			}
			fmt.Fprintf(&sb, "| %s | %d | %d | %s |\n", ls.Label, ls.Open, ls.Done, cycle)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Throughput\n\n| Week | Added | Finished |\n| --- | ---: | ---: |\n")
	for _, w := range st.Weeks {
		fmt.Fprintf(&sb, "| %s | %d | %d |\n", w.Week.Format("Jan 2"), w.Created, w.Done)
	}
	return sb.String()
}
