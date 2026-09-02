package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// cli runs the non-interactive subcommands.
type cli struct {
	env    *env
	stdout io.Writer
	stderr io.Writer
}

func (c *cli) run(cmd string, args []string) int {
	var err error
	switch cmd {
	case "add":
		err = c.add(args)
	case "list", "ls":
		err = c.list(args)
	case "show":
		err = c.show(args)
	case "move", "mv":
		err = c.move(args)
	case "done":
		err = c.done(args)
	case "archive":
		err = c.archive(args, true)
	case "restore":
		err = c.archive(args, false)
	case "rm", "delete":
		err = c.remove(args)
	case "due":
		err = c.due(args)
	case "stats":
		err = c.stats(args)
	case "review":
		err = c.review(args)
	case "log", "history":
		err = c.log(args)
	case "compact":
		err = c.compact(args)
	case "export":
		err = c.export(args)
	case "import":
		err = c.importCmd(args)
	case "boards", "board":
		err = c.boards(args)
	case "columns", "cols":
		err = c.columns(args)
	case "config":
		err = c.config(args)
	case "keys":
		err = c.keys(args)
	default:
		fmt.Fprintf(c.stderr, "kancli: unknown command %q (see `kancli help`)\n", cmd)
		return 2
	}
	if err != nil {
		fmt.Fprintf(c.stderr, "kancli %s: %v\n", cmd, err)
		if errors.Is(err, errUsage) {
			return 2
		}
		return 1
	}
	return 0
}

var errUsage = errors.New("usage error")

func usageErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUsage, fmt.Sprintf(format, args...))
}

func (c *cli) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("kancli "+name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	return fs
}

func (c *cli) save() error {
	if c.env.readOnly {
		return fmt.Errorf("the board is opened read-only with -as-of")
	}
	if err := c.env.store.save(c.env.file); err != nil {
		return err
	}
	return nil
}

// --- stats / review / log / compact -------------------------------------------

func (c *cli) stats(args []string) error {
	fs := c.flags("stats")
	var days int
	var asJSON, showSQL bool
	var query, format string
	fs.IntVar(&days, "days", 90, "window for throughput, WIP and cycle time")
	fs.BoolVar(&asJSON, "json", false, "print the statistics as JSON")
	fs.StringVar(&query, "q", "", "run this SQL with DuckDB over the tasks and events views")
	fs.StringVar(&format, "format", "box", "DuckDB output: box, json, csv or markdown")
	fs.BoolVar(&showSQL, "sql", false, "print the DuckDB view definitions and example queries")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	if showSQL || query != "" {
		state, cleanup, err := writeStateFile(c.env.file)
		if err != nil {
			return err
		}
		defer cleanup()
		views := sqlViews(state, c.env.store.eventFiles())
		if showSQL {
			fmt.Fprint(c.stdout, views)
			fmt.Fprint(c.stdout, "\n"+exampleQueries)
			return nil
		}
		bin, err := duckdbBinary()
		if err != nil {
			return err
		}
		out, err := runDuckDB(bin, views, query, format)
		fmt.Fprint(c.stdout, out)
		return err
	}
	st, err := c.env.store.boardStats(b, timeNow(), days)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(c.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	fmt.Fprint(c.stdout, formatStats(st))
	return nil
}

// formatStats renders statistics as plain text.
func formatStats(st boardStats) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Board %s · last %d days · %d events\n\n", st.Board, st.Days, st.Events)
	fmt.Fprintf(&sb, "Open %d · in progress %d · overdue %d · due today %d\n", st.Live, st.InProgress, st.Overdue, st.DueToday)
	if len(st.Finished) > 0 {
		fmt.Fprintf(&sb, "Finished %d · cycle time median %s, mean %s, p90 %s\n", len(st.Finished), humanDuration(st.CycleMedian), humanDuration(st.CycleMean), humanDuration(st.CycleP90))
	} else {
		sb.WriteString("Finished 0 · no cycle times yet\n")
	}
	sb.WriteString("\nThroughput (week starting: added / finished)\n")
	for _, w := range st.Weeks {
		fmt.Fprintf(&sb, "  %s  %3d / %-3d %s\n", w.Week.Format("Jan 02"), w.Created, w.Done, strings.Repeat("█", w.Done))
	}
	sb.WriteString("\nTime in column (mean / median, samples)\n")
	for _, s := range st.Stays {
		if s.Samples == 0 {
			fmt.Fprintf(&sb, "  %-16s no completed stays yet\n", s.Name)
			continue
		}
		fmt.Fprintf(&sb, "  %-16s %8s / %-8s %d\n", s.Name, humanDuration(s.Mean), humanDuration(s.Median), s.Samples)
	}
	if len(st.WIP) > 0 {
		last := st.WIP[len(st.WIP)-1]
		peak := 0
		for _, d := range st.WIP {
			peak = max(peak, d.Count)
		}
		fmt.Fprintf(&sb, "\nWork in progress: %d now, peak %d over %d days\n", last.Count, peak, len(st.WIP))
	}
	if len(st.Aging) > 0 {
		sb.WriteString("\nOldest open tasks\n")
		for _, a := range st.Aging {
			fmt.Fprintf(&sb, "  %8s  #%-4d %s (%s)\n", humanDuration(a.Age), a.ID, a.Title, a.Column)
		}
	}
	if len(st.Labels) > 0 {
		sb.WriteString("\nLabels (open / finished, median cycle)\n")
		for _, l := range st.Labels {
			cycle := ""
			if l.Done > 0 {
				cycle = humanDuration(l.CycleMedian)
			}
			fmt.Fprintf(&sb, "  %-16s %3d / %-3d %s\n", "+"+l.Label, l.Open, l.Done, cycle)
		}
	}
	return sb.String()
}

func (c *cli) review(args []string) error {
	fs := c.flags("review")
	var days int
	var out string
	fs.IntVar(&days, "days", 7, "period to review")
	fs.StringVar(&out, "o", "", "write the Markdown to this file")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	events, err := c.env.store.events()
	if err != nil {
		return err
	}
	md := reviewReport(b, events, timeNow(), days)
	if out != "" {
		return os.WriteFile(out, []byte(md), 0o644)
	}
	_, err = io.WriteString(c.stdout, md)
	return err
}

func (c *cli) log(args []string) error {
	fs := c.flags("log")
	var n, task int
	var asJSON bool
	fs.IntVar(&n, "n", 20, "number of events to show")
	fs.IntVar(&task, "task", 0, "only events for this task id")
	fs.BoolVar(&asJSON, "json", false, "print raw events as JSON lines")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	events, err := c.env.store.events()
	if err != nil {
		return err
	}
	var picked []Event
	for _, e := range events {
		if e.Board != "" && e.Board != b.ID {
			continue
		}
		if task != 0 && e.Task != task {
			continue
		}
		picked = append(picked, e)
	}
	if n > 0 && len(picked) > n {
		picked = picked[len(picked)-n:]
	}
	if len(picked) == 0 {
		fmt.Fprintln(c.stdout, "No events.")
		return nil
	}
	for _, e := range picked {
		if asJSON {
			line, _ := json.Marshal(e)
			fmt.Fprintln(c.stdout, string(line))
			continue
		}
		actor := ""
		if e.Actor != "" {
			actor = " [" + e.Actor + "]"
		}
		fmt.Fprintf(c.stdout, "%s  %s%s\n", e.At.Local().Format("2006-01-02 15:04"), e.describe(c.env.file), actor)
	}
	return nil
}

func (c *cli) compact(args []string) error {
	if c.env.readOnly {
		return fmt.Errorf("the board is opened read-only with -as-of")
	}
	if !c.env.store.enabled() {
		return fmt.Errorf("nothing to compact in demo mode")
	}
	n := c.env.store.tailEvents()
	if err := c.env.store.compact(c.env.file); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Snapshot written to %s (%d event%s archived)\n", c.env.store.path, n, plural(n))
	return nil
}

// checkIDs fails before anything is changed when an id does not exist.
func checkIDs(b *Board, ids []int) error {
	for _, id := range ids {
		if b.Task(id) == nil {
			return fmt.Errorf("no task #%d", id)
		}
	}
	return nil
}

// flagsFirst moves flags (and their values) ahead of positional arguments
// so that `kancli add "title" -p high` works as well as `kancli add -p high
// "title"`.
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		name, _, inline := strings.Cut(name, "=")
		f := fs.Lookup(name)
		if f == nil {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		isBool := false
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = bf.IsBoolFlag()
		}
		if !inline && !isBool && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, rest...)
}

func parseIDs(args []string) ([]int, error) {
	if len(args) == 0 {
		return nil, usageErr("at least one task id is required")
	}
	ids := make([]int, 0, len(args))
	for _, a := range args {
		n, err := strconv.Atoi(strings.TrimPrefix(a, "#"))
		if err != nil {
			return nil, usageErr("%q is not a task id", a)
		}
		ids = append(ids, n)
	}
	return ids, nil
}

// --- add ------------------------------------------------------------------

func (c *cli) add(args []string) error {
	fs := c.flags("add")
	var desc, prio, due, labels, who, col string
	fs.StringVar(&desc, "d", "", "description")
	fs.StringVar(&desc, "desc", "", "description")
	fs.StringVar(&prio, "p", "", "priority: low, medium, high, urgent")
	fs.StringVar(&prio, "priority", "", "priority")
	fs.StringVar(&due, "due", "", "due date: 2026-09-10, today, tomorrow, fri, +3d")
	fs.StringVar(&labels, "l", "", "comma separated labels")
	fs.StringVar(&labels, "labels", "", "comma separated labels")
	fs.StringVar(&who, "a", "", "assignee")
	fs.StringVar(&who, "assignee", "", "assignee")
	fs.StringVar(&col, "c", "", "column (default: first)")
	fs.StringVar(&col, "column", "", "column")
	if err := fs.Parse(flagsFirst(fs, args)); err != nil {
		return errUsage
	}
	title := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if title == "" {
		return usageErr("kancli add [-d desc] [-p prio] [-due date] [-l labels] [-a who] [-c column] <title>")
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	t := Task{Title: title, Description: desc, Assignee: strings.TrimSpace(who), Labels: parseLabels(labels)}
	if prio != "" {
		if t.Priority, err = parsePriority(prio); err != nil {
			return err
		}
	}
	if t.Due, err = parseDue(due, timeNow()); err != nil {
		return err
	}
	if col != "" {
		cc := b.Column(col)
		if cc == nil {
			return fmt.Errorf("no column %q (see `kancli columns`)", col)
		}
		t.Column = cc.ID
	}
	added, err := b.AddTask(t)
	if err != nil {
		return err
	}
	if err := c.save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "%s added to %s\n", added.Ref(), b.Column(added.Column).Name)
	return nil
}

// --- list -----------------------------------------------------------------

func (c *cli) list(args []string) error {
	fs := c.flags("list")
	var col, q, sortBy string
	var asJSON, archived, all bool
	fs.StringVar(&col, "c", "", "only this column")
	fs.StringVar(&col, "column", "", "only this column")
	fs.StringVar(&q, "q", "", "search query (same syntax as the UI)")
	fs.StringVar(&sortBy, "sort", "manual", "manual, priority, due, created, updated or title")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.BoolVar(&archived, "archived", false, "list archived tasks instead")
	fs.BoolVar(&all, "all", false, "include archived tasks")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	mode, ok := parseSortMode(sortBy)
	if !ok {
		return usageErr("unknown sort %q", sortBy)
	}
	var tasks []Task
	switch {
	case archived:
		tasks = b.ArchivedTasks()
	case all:
		tasks = append([]Task(nil), b.Tasks...)
	default:
		tasks = b.Live()
	}
	if col != "" {
		cc := b.Column(col)
		if cc == nil {
			return fmt.Errorf("no column %q", col)
		}
		tasks = filterTasks(tasks, func(t Task) bool { return t.Column == cc.ID })
	}
	if q != "" {
		pq := parseQuery(q)
		now := timeNow()
		tasks = filterTasks(tasks, func(t Task) bool { return pq.matches(b, t, now) })
	}
	sortTasks(tasks, mode)
	if asJSON {
		enc := json.NewEncoder(c.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}
	if len(tasks) == 0 {
		fmt.Fprintln(c.stdout, "No tasks.")
		return nil
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCOLUMN\tPRI\tDUE\tTITLE\tLABELS\tWHO")
	now := timeNow()
	for _, t := range tasks {
		colName := t.Column
		if cc := b.Column(t.Column); cc != nil {
			colName = cc.Name
		}
		if t.Archived() {
			colName += " (archived)"
		}
		dueLabel, _ := dueInfo(t, now)
		pri := ""
		if t.Priority != priorityNone {
			pri = t.Priority.String()
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, colName, pri, dueLabel, t.Title, strings.Join(t.Labels, ","), t.Assignee)
	}
	return tw.Flush()
}

func filterTasks(ts []Task, keep func(Task) bool) []Task {
	out := ts[:0:0]
	for _, t := range ts {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}

// --- show -----------------------------------------------------------------

func (c *cli) show(args []string) error {
	ids, err := parseIDs(args)
	if err != nil {
		return err
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	for i, id := range ids {
		t := b.Task(id)
		if t == nil {
			return fmt.Errorf("no task #%d", id)
		}
		if i > 0 {
			fmt.Fprintln(c.stdout)
		}
		fmt.Fprint(c.stdout, formatTask(b, *t, timeNow()))
	}
	return nil
}

// formatTask renders a task as plain text.
func formatTask(b *Board, t Task, now time.Time) string {
	var sb strings.Builder
	col := t.Column
	if cc := b.Column(t.Column); cc != nil {
		col = cc.Name
	}
	fmt.Fprintf(&sb, "%s %s\n", t.Ref(), t.Title)
	fmt.Fprintf(&sb, "  column:    %s\n", col)
	if t.Priority != priorityNone {
		fmt.Fprintf(&sb, "  priority:  %s\n", t.Priority)
	}
	if t.Due != "" {
		label, _ := dueInfo(t, now)
		fmt.Fprintf(&sb, "  due:       %s (%s)\n", t.Due, label)
	}
	if len(t.Labels) > 0 {
		fmt.Fprintf(&sb, "  labels:    %s\n", strings.Join(t.Labels, ", "))
	}
	if t.Assignee != "" {
		fmt.Fprintf(&sb, "  assignee:  %s\n", t.Assignee)
	}
	fmt.Fprintf(&sb, "  created:   %s\n", t.CreatedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(&sb, "  updated:   %s\n", t.UpdatedAt.Format("2006-01-02 15:04"))
	if t.Archived() {
		fmt.Fprintf(&sb, "  archived:  %s\n", t.ArchivedAt.Format("2006-01-02 15:04"))
	}
	if t.Description != "" {
		sb.WriteString("\n" + indent(t.Description) + "\n")
	}
	if len(t.Checklist) > 0 {
		done, total := t.ChecklistProgress()
		fmt.Fprintf(&sb, "\n  Checklist %d/%d\n", done, total)
		for _, item := range t.Checklist {
			box := "[ ]"
			if item.Done {
				box = "[x]"
			}
			fmt.Fprintf(&sb, "    %s %s\n", box, item.Text)
		}
	}
	if len(t.Attachments) > 0 {
		sb.WriteString("\n  Attachments\n")
		for _, a := range t.Attachments {
			fmt.Fprintf(&sb, "    %s\n", a)
		}
	}
	if len(t.Comments) > 0 {
		sb.WriteString("\n  Comments\n")
		for _, cm := range t.Comments {
			fmt.Fprintf(&sb, "    %s  %s\n", cm.At.Format("2006-01-02 15:04"), cm.Text)
		}
	}
	if len(t.History) > 0 {
		sb.WriteString("\n  Activity\n")
		for _, e := range t.History {
			fmt.Fprintf(&sb, "    %s  %s\n", e.At.Format("2006-01-02 15:04"), e.Text)
		}
	}
	return sb.String()
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

// --- move / done / archive / rm ---------------------------------------------

func (c *cli) move(args []string) error {
	if len(args) != 2 {
		return usageErr("kancli move <id> <column>")
	}
	ids, err := parseIDs(args[:1])
	if err != nil {
		return err
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.Column(args[1])
	if col == nil {
		return fmt.Errorf("no column %q (see `kancli columns`)", args[1])
	}
	if err := b.MoveTask(ids[0], col.ID); err != nil {
		return err
	}
	if err := c.save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "#%d moved to %s\n", ids[0], col.Name)
	return nil
}

func (c *cli) done(args []string) error {
	ids, err := parseIDs(args)
	if err != nil {
		return err
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.DoneColumn()
	if col == nil {
		return fmt.Errorf("the board has no columns")
	}
	if err := checkIDs(b, ids); err != nil {
		return err
	}
	for _, id := range ids {
		if err := b.MoveTask(id, col.ID); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "#%d moved to %s\n", id, col.Name)
	}
	return c.save()
}

func (c *cli) archive(args []string, archive bool) error {
	ids, err := parseIDs(args)
	if err != nil {
		return err
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	if err := checkIDs(b, ids); err != nil {
		return err
	}
	for _, id := range ids {
		if archive {
			b.ArchiveTask(id)
			fmt.Fprintf(c.stdout, "#%d archived\n", id)
		} else {
			b.RestoreTask(id)
			fmt.Fprintf(c.stdout, "#%d restored\n", id)
		}
	}
	return c.save()
}

func (c *cli) remove(args []string) error {
	ids, err := parseIDs(args)
	if err != nil {
		return err
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	if err := checkIDs(b, ids); err != nil {
		return err
	}
	for _, id := range ids {
		b.DeleteTask(id)
		fmt.Fprintf(c.stdout, "#%d deleted\n", id)
	}
	return c.save()
}

// --- due ------------------------------------------------------------------

func (c *cli) due(args []string) error {
	fs := c.flags("due")
	var days int
	var notify, quiet bool
	fs.IntVar(&days, "days", 0, "also include tasks due within N days")
	fs.BoolVar(&notify, "notify", false, "send a desktop notification when something is due")
	fs.BoolVar(&quiet, "q", false, "print nothing, only exit 1 when something is due")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	now := timeNow()
	done := b.DoneColumn()
	var lines []string
	overdue, todayN, soon := 0, 0, 0
	tasks := b.Live()
	sortTasks(tasks, sortDue)
	for _, t := range tasks {
		if done != nil && t.Column == done.ID {
			continue
		}
		due, ok := t.DueDate()
		if !ok {
			continue
		}
		label, state := dueInfo(t, now)
		include := false
		switch state {
		case dueOverdue:
			overdue++
			include = true
		case dueToday:
			todayN++
			include = true
		default:
			if days > 0 && daysBetween(now, due) <= days {
				soon++
				include = true
			}
		}
		if include {
			lines = append(lines, fmt.Sprintf("%-14s %s %s", label, t.Ref(), t.Title))
		}
	}
	if len(lines) == 0 {
		if !quiet {
			fmt.Fprintln(c.stdout, "Nothing due.")
		}
		return nil
	}
	if !quiet {
		for _, l := range lines {
			fmt.Fprintln(c.stdout, l)
		}
	}
	if notify {
		summary := summarizeDue(overdue, todayN, soon)
		body := strings.Join(lines, "\n")
		if len(lines) > 5 {
			body = strings.Join(lines[:5], "\n") + fmt.Sprintf("\n… and %d more", len(lines)-5)
		}
		if err := notifyDesktop("kancli: "+summary, body); err != nil {
			return err
		}
	}
	if quiet {
		return fmt.Errorf("%s", summarizeDue(overdue, todayN, soon))
	}
	return nil
}

func summarizeDue(overdue, todayN, soon int) string {
	var parts []string
	if overdue > 0 {
		parts = append(parts, fmt.Sprintf("%d overdue", overdue))
	}
	if todayN > 0 {
		parts = append(parts, fmt.Sprintf("%d due today", todayN))
	}
	if soon > 0 {
		parts = append(parts, fmt.Sprintf("%d due soon", soon))
	}
	return strings.Join(parts, ", ")
}

// --- export / import --------------------------------------------------------

func formatFromPath(path, explicit string) string {
	if explicit != "" {
		return strings.ToLower(explicit)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		return "csv"
	case ".md", ".markdown":
		return "md"
	case ".parquet":
		return "parquet"
	default:
		return "json"
	}
}

func (c *cli) export(args []string) error {
	fs := c.flags("export")
	var format, out string
	var all bool
	var events bool
	fs.StringVar(&format, "f", "", "json, csv, md or parquet (default from -o extension, else json)")
	fs.StringVar(&format, "format", "", "json, csv, md or parquet")
	fs.StringVar(&out, "o", "", "output file (default: stdout; required for parquet)")
	fs.BoolVar(&all, "all", false, "json only: export every board, not just the current one")
	fs.BoolVar(&events, "events", false, "parquet only: export the event log instead of tasks")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	if formatFromPath(out, format) == "parquet" {
		if out == "" {
			return usageErr("parquet export needs -o FILE")
		}
		bin, err := duckdbBinary()
		if err != nil {
			return err
		}
		state, cleanup, err := writeStateFile(c.env.file)
		if err != nil {
			return err
		}
		defer cleanup()
		views := sqlViews(state, c.env.store.eventFiles())
		source := fmt.Sprintf("SELECT * FROM tasks WHERE board = %s", sqlLiteral(b.ID))
		if events {
			source = fmt.Sprintf("SELECT * FROM events WHERE board = %s OR board IS NULL", sqlLiteral(b.ID))
		}
		_, err = runDuckDB(bin, views, fmt.Sprintf("COPY (%s) TO %s (FORMAT PARQUET)", source, sqlLiteral(out)), "csv")
		if err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "Wrote %s\n", out)
		return nil
	}
	w := c.stdout
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	switch formatFromPath(out, format) {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if all {
			return enc.Encode(c.env.file)
		}
		return enc.Encode(b)
	case "csv":
		return writeCSV(w, b)
	case "md", "markdown":
		_, err := io.WriteString(w, formatMarkdown(b))
		return err
	default:
		return usageErr("unknown format %q", format)
	}
}

var csvHeader = []string{"id", "column", "title", "description", "priority", "due", "labels", "assignee", "created_at", "updated_at", "archived_at"}

func writeCSV(w io.Writer, b *Board) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, t := range b.Tasks {
		col := t.Column
		if cc := b.Column(t.Column); cc != nil {
			col = cc.Name
		}
		archived := ""
		if t.Archived() {
			archived = t.ArchivedAt.Format(time.RFC3339)
		}
		pri := ""
		if t.Priority != priorityNone {
			pri = t.Priority.String()
		}
		rec := []string{strconv.Itoa(t.ID), col, t.Title, t.Description, pri, t.Due,
			strings.Join(t.Labels, ","), t.Assignee, t.CreatedAt.Format(time.RFC3339), t.UpdatedAt.Format(time.RFC3339), archived}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// formatMarkdown renders a board as a Markdown document.
func formatMarkdown(b *Board) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n", b.Name)
	done := b.DoneColumn()
	for _, col := range b.Columns {
		tasks := b.TasksIn(col.ID)
		fmt.Fprintf(&sb, "\n## %s (%d)\n\n", col.Name, len(tasks))
		for _, t := range tasks {
			box := "[ ]"
			if done != nil && col.ID == done.ID {
				box = "[x]"
			}
			fmt.Fprintf(&sb, "- %s %s %s", box, t.Ref(), t.Title)
			var meta []string
			if t.Priority != priorityNone {
				meta = append(meta, "!"+t.Priority.String())
			}
			if t.Due != "" {
				meta = append(meta, "due:"+t.Due)
			}
			for _, l := range t.Labels {
				meta = append(meta, "+"+l)
			}
			if t.Assignee != "" {
				meta = append(meta, "@"+t.Assignee)
			}
			if len(meta) > 0 {
				sb.WriteString(" `" + strings.Join(meta, " ") + "`")
			}
			sb.WriteString("\n")
			if t.Description != "" {
				for _, line := range strings.Split(t.Description, "\n") {
					sb.WriteString("  " + line + "\n")
				}
			}
			for _, item := range t.Checklist {
				box := "[ ]"
				if item.Done {
					box = "[x]"
				}
				fmt.Fprintf(&sb, "  - %s %s\n", box, item.Text)
			}
		}
	}
	return sb.String()
}

func (c *cli) importCmd(args []string) error {
	fs := c.flags("import")
	var format, col string
	fs.StringVar(&format, "f", "", "json, csv or md (default from the file extension)")
	fs.StringVar(&format, "format", "", "json, csv or md")
	fs.StringVar(&col, "c", "", "default column for tasks without one")
	fs.StringVar(&col, "column", "", "default column")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 1 {
		return usageErr("kancli import [-f json|csv|md] [-c column] <file>")
	}
	path := fs.Arg(0)
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	defCol := ""
	if col != "" {
		cc := b.Column(col)
		if cc == nil {
			return fmt.Errorf("no column %q", col)
		}
		defCol = cc.ID
	}
	var tasks []Task
	switch formatFromPath(path, format) {
	case "json":
		tasks, err = tasksFromJSON(data)
	case "csv":
		tasks, err = tasksFromCSV(data)
	case "md", "markdown":
		tasks, err = tasksFromMarkdown(data)
	default:
		return usageErr("unknown format %q", format)
	}
	if err != nil {
		return err
	}
	n := 0
	for _, t := range tasks {
		if cc := b.Column(t.Column); cc != nil {
			t.Column = cc.ID
		} else if defCol != "" {
			t.Column = defCol
		} else {
			t.Column = ""
		}
		t.ID = 0
		t.History = nil
		if _, err := b.AddTask(t); err != nil {
			return fmt.Errorf("task %q: %w", t.Title, err)
		}
		n++
	}
	if err := c.save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Imported %d task%s into %s\n", n, plural(n), b.Name)
	return nil
}

// tasksFromJSON accepts a kancli file, a board, a task array or one task.
func tasksFromJSON(data []byte) ([]Task, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err == nil {
		if _, ok := probe["boards"]; ok {
			f, err := decodeFile(data)
			if err != nil {
				return nil, err
			}
			var out []Task
			for _, b := range f.Boards {
				for _, t := range b.Tasks {
					if cc := b.Column(t.Column); cc != nil {
						t.Column = cc.Name
					}
					out = append(out, t)
				}
			}
			return out, nil
		}
		if _, ok := probe["tasks"]; ok {
			var b Board
			if err := json.Unmarshal(data, &b); err != nil {
				return nil, err
			}
			for i := range b.Tasks {
				if cc := b.Column(b.Tasks[i].Column); cc != nil {
					b.Tasks[i].Column = cc.Name
				}
			}
			return b.Tasks, nil
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return []Task{t}, nil
	}
	var ts []Task
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil, fmt.Errorf("expected a kancli file, a board, a task or a task list: %w", err)
	}
	return ts, nil
}

// tasksFromCSV reads the columns written by export; only title is required.
func tasksFromCSV(data []byte) ([]Task, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	idx := map[string]int{}
	for i, h := range records[0] {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := idx["title"]; !ok {
		return nil, fmt.Errorf("csv needs a title column")
	}
	get := func(rec []string, name string) string {
		i, ok := idx[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	var out []Task
	for _, rec := range records[1:] {
		title := get(rec, "title")
		if title == "" {
			continue
		}
		t := Task{Title: title, Description: get(rec, "description"), Column: get(rec, "column"),
			Labels: parseLabels(get(rec, "labels")), Assignee: get(rec, "assignee")}
		if p := get(rec, "priority"); p != "" {
			if t.Priority, err = parsePriority(p); err != nil {
				return nil, err
			}
		}
		if d := get(rec, "due"); d != "" {
			if t.Due, err = parseDue(d, timeNow()); err != nil {
				return nil, err
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// tasksFromMarkdown reads "## Column" headings and "- [ ] title" items.
// Indented lines under an item become its description; indented "- [ ]"
// lines become checklist items. Backtick metadata like `!high due:fri
// +label @who` is parsed.
func tasksFromMarkdown(data []byte) ([]Task, error) {
	var out []Task
	col := ""
	var cur *Task
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			col = strings.TrimSpace(trimmed[3:])
			if i := strings.LastIndex(col, " ("); i > 0 && strings.HasSuffix(col, ")") {
				col = col[:i]
			}
			cur = nil
		case strings.HasPrefix(trimmed, "# "):
			continue
		case strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			t := Task{Column: col}
			text := strings.TrimSpace(trimmed[2:])
			text = strings.TrimPrefix(strings.TrimPrefix(text, "[ ] "), "[x] ")
			text = strings.TrimPrefix(text, "[X] ")
			if i := strings.LastIndex(text, " `"); i >= 0 && strings.HasSuffix(text, "`") {
				applyMeta(&t, text[i+2:len(text)-1])
				text = text[:i]
			}
			// Strip a leading "#12 " reference.
			if strings.HasPrefix(text, "#") {
				if sp := strings.Index(text, " "); sp > 0 {
					if _, err := strconv.Atoi(text[1:sp]); err == nil {
						text = text[sp+1:]
					}
				}
			}
			t.Title = strings.TrimSpace(text)
			if t.Title == "" {
				continue
			}
			out = append(out, t)
			cur = &out[len(out)-1]
		case cur != nil && trimmed != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			if strings.HasPrefix(trimmed, "- ") {
				item := strings.TrimSpace(trimmed[2:])
				done := strings.HasPrefix(item, "[x] ") || strings.HasPrefix(item, "[X] ")
				item = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(item, "[ ] "), "[x] "), "[X] ")
				cur.Checklist = append(cur.Checklist, ChecklistItem{Text: item, Done: done})
			} else if cur.Description == "" {
				cur.Description = trimmed
			} else {
				cur.Description += "\n" + trimmed
			}
		case trimmed == "":
			cur = nil
		}
	}
	return out, sc.Err()
}

func applyMeta(t *Task, meta string) {
	for _, tok := range strings.Fields(meta) {
		switch {
		case strings.HasPrefix(tok, "!"):
			if p, err := parsePriority(tok[1:]); err == nil {
				t.Priority = p
			}
		case strings.HasPrefix(tok, "due:"):
			if d, err := parseDue(tok[4:], timeNow()); err == nil {
				t.Due = d
			}
		case strings.HasPrefix(tok, "+"):
			t.Labels = append(t.Labels, tok[1:])
		case strings.HasPrefix(tok, "@"):
			t.Assignee = tok[1:]
		}
	}
}

// --- boards / columns / config / keys ------------------------------------

func (c *cli) boards(args []string) error {
	f := c.env.file
	if len(args) == 0 {
		for _, b := range f.Boards {
			mark := " "
			if b.ID == f.ActiveBoard {
				mark = "*"
			}
			n := len(b.Live())
			fmt.Fprintf(c.stdout, "%s %-20s %d task%s\n", mark, b.Name, n, plural(n))
		}
		return nil
	}
	switch args[0] {
	case "new", "add":
		if len(args) < 2 {
			return usageErr("kancli boards new <name>")
		}
		b, err := f.AddBoard(strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		f.Activate(b.ID) //nolint:errcheck // just created
		if err := c.save(); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "Created board %q\n", b.Name)
	case "use", "switch":
		if len(args) < 2 {
			return usageErr("kancli boards use <name>")
		}
		b := f.Board(strings.Join(args[1:], " "))
		if b == nil {
			return fmt.Errorf("no board %q", strings.Join(args[1:], " "))
		}
		f.Activate(b.ID) //nolint:errcheck // board exists
		if err := c.save(); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "Switched to %q\n", b.Name)
	case "rename":
		if len(args) != 3 {
			return usageErr("kancli boards rename <old> <new>")
		}
		b := f.Board(args[1])
		if b == nil {
			return fmt.Errorf("no board %q", args[1])
		}
		if err := f.RenameBoard(b.ID, args[2]); err != nil {
			return err
		}
		if err := c.save(); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "Renamed to %q\n", b.Name)
	case "rm", "delete":
		if len(args) < 2 {
			return usageErr("kancli boards rm <name>")
		}
		b := f.Board(strings.Join(args[1:], " "))
		if b == nil {
			return fmt.Errorf("no board %q", strings.Join(args[1:], " "))
		}
		if err := f.RemoveBoard(b.ID); err != nil {
			return err
		}
		if err := c.save(); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "Deleted board %q\n", b.Name)
	default:
		return usageErr("kancli boards [new|use|rename|rm] ...")
	}
	return nil
}

func (c *cli) columns(args []string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTASKS\tWIP\tCOLOUR")
	for _, col := range b.Columns {
		wip := ""
		if col.WIPLimit > 0 {
			wip = strconv.Itoa(col.WIPLimit)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", col.ID, col.Name, b.CountIn(col.ID), wip, col.Color)
	}
	return tw.Flush()
}

func (c *cli) config(args []string) error {
	path := c.env.opts.configPath
	if path == "" {
		path, _ = defaultConfigPath()
	}
	fmt.Fprintf(c.stdout, "Config file: %s\n", path)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintln(c.stdout, "(exists)")
	} else {
		fmt.Fprintln(c.stdout, "(not created yet)")
	}
	fmt.Fprintf(c.stdout, "Data file:   %s\n", c.env.store.describe())
	fmt.Fprintf(c.stdout, "Theme:       %s\n\nExample config:\n%s", c.env.st.th.name, exampleConfig)
	return nil
}

func (c *cli) keys(args []string) error {
	k := defaultKeyMap()
	k.applyKeyOverrides(c.env.cfg.Keys) //nolint:errcheck // validated on load
	acts := k.actions()
	names := actionNames()
	sort.Strings(names)
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ACTION\tKEYS\tDESCRIPTION")
	for _, n := range names {
		b := acts[n]
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n, helpLabel(b.Keys()), b.Help().Desc)
	}
	return tw.Flush()
}
