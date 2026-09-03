package cli

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/SabienNguyen/kancli/internal/config"
	"github.com/SabienNguyen/kancli/internal/desktop"
	"github.com/SabienNguyen/kancli/internal/store"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/ui"
)

// cli runs the non-interactive subcommands.
type cli struct {
	env    *Env
	opts   Options
	stdout io.Writer
	stderr io.Writer
	launch func(*Env) error

	ran    bool   // a command body started running
	name   string // command being run, for error messages
	envErr bool   // the failure came from loading the environment
}

// Flag values for the commands that take more than positional arguments.
type (
	addOpts struct {
		desc, prio, due, labels, who, col string
	}
	listOpts struct {
		col, query, sortBy    string
		asJSON, archived, all bool
	}
	statsOpts struct {
		days            int
		asJSON, showSQL bool
		query, format   string
	}
	exportOpts struct {
		format, out string
		all, events bool
	}
)

var errUsage = errors.New("usage error")

// errSilentExit makes a command exit 1 without printing anything.
var errSilentExit = errors.New("silent exit")

func usageErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUsage, fmt.Sprintf(format, args...))
}

func (c *cli) Save() error {
	if c.env.ReadOnly {
		return fmt.Errorf("the board is opened read-only with -as-of")
	}
	if err := c.env.Store.Save(c.env.File); err != nil {
		return err
	}
	return nil
}

// --- stats / review / log / compact -------------------------------------------

func (c *cli) stats(o statsOpts) error {
	days, asJSON, showSQL, query, format := o.days, o.asJSON, o.showSQL, o.query, o.format
	b, err := c.env.board()
	if err != nil {
		return err
	}
	if showSQL || query != "" {
		state, cleanup, err := store.WriteStateFile(c.env.File)
		if err != nil {
			return err
		}
		defer cleanup()
		eventsFile, eventsCleanup, err := store.WriteEventsFile(c.env.Store)
		if err != nil {
			return err
		}
		defer eventsCleanup()
		views := store.SQLViews(state, eventFiles(eventsFile), store.DoneColumns(c.env.File))
		if showSQL {
			fmt.Fprint(c.stdout, views)
			fmt.Fprint(c.stdout, "\n"+store.ExampleQueries)
			return nil
		}
		bin, err := store.DuckDBBinary()
		if err != nil {
			return err
		}
		out, err := store.RunDuckDB(bin, views, query, format)
		fmt.Fprint(c.stdout, out)
		return err
	}
	st, err := c.env.Store.BoardStats(b, board.Now(), days)
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
func formatStats(st board.Stats) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Board %s · last %d days · %d events\n\n", st.Board, st.Days, st.Events)
	fmt.Fprintf(&sb, "Open %d · in progress %d · overdue %d · due today %d\n", st.Live, st.InProgress, st.Overdue, st.DueToday)
	if len(st.Finished) > 0 {
		fmt.Fprintf(&sb, "Finished %d · cycle time median %s, mean %s, p90 %s\n", len(st.Finished), board.HumanDuration(st.CycleMedian), board.HumanDuration(st.CycleMean), board.HumanDuration(st.CycleP90))
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
		fmt.Fprintf(&sb, "  %-16s %8s / %-8s %d\n", s.Name, board.HumanDuration(s.Mean), board.HumanDuration(s.Median), s.Samples)
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
			fmt.Fprintf(&sb, "  %8s  #%-4d %s (%s)\n", board.HumanDuration(a.Age), a.ID, a.Title, a.Column)
		}
	}
	if len(st.Labels) > 0 {
		sb.WriteString("\nLabels (open / finished, median cycle)\n")
		for _, l := range st.Labels {
			cycle := ""
			if l.Done > 0 {
				cycle = board.HumanDuration(l.CycleMedian)
			}
			fmt.Fprintf(&sb, "  %-16s %3d / %-3d %s\n", "+"+l.Label, l.Open, l.Done, cycle)
		}
	}
	return sb.String()
}

func (c *cli) review(days int, out string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	events, err := c.env.Store.Events()
	if err != nil {
		return err
	}
	md := board.ReviewReport(b, events, board.Now(), days)
	if out != "" {
		return os.WriteFile(out, []byte(md), 0o644)
	}
	_, err = io.WriteString(c.stdout, md)
	return err
}

func (c *cli) log(n, task int, asJSON bool) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	events, err := c.env.Store.Events()
	if err != nil {
		return err
	}
	var picked []board.Event
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
		fmt.Fprintf(c.stdout, "%s  %s%s\n", e.At.Local().Format("2006-01-02 15:04"), e.Describe(c.env.File), actor)
	}
	return nil
}

func (c *cli) Compact() error {
	if c.env.ReadOnly {
		return fmt.Errorf("the board is opened read-only with -as-of")
	}
	if !c.env.Store.Enabled() {
		return fmt.Errorf("nothing to compact in demo mode")
	}
	n := c.env.Store.TailEvents()
	if err := c.env.Store.Compact(c.env.File); errors.Is(err, store.ErrStale) {
		f, err := c.env.Store.Load()
		if err != nil {
			return err
		}
		c.env.File = f
		n = c.env.Store.TailEvents()
		if err := c.env.Store.Compact(f); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Snapshot written to %s (%d event%s folded)\n", c.env.Store.Path(), n, board.Plural(n))
	return nil
}

// eventFiles wraps the exported event log for SQLViews, which takes a list
// so an empty export can define the events view as empty.
func eventFiles(path string) []string {
	if path == "" {
		return nil
	}
	return []string{path}
}

// checkIDs fails before anything is changed when an id does not exist.
func checkIDs(b *board.Board, ids []int) error {
	for _, id := range ids {
		if b.Task(id) == nil {
			return fmt.Errorf("no %s #%d", b.Noun(), id)
		}
	}
	return nil
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

func (c *cli) add(o addOpts, title string) error {
	desc, prio, due, labels, who, col := o.desc, o.prio, o.due, o.labels, o.who, o.col
	if title == "" {
		return usageErr("a title is required")
	}
	b, err := c.env.board()
	if err != nil {
		return err
	}
	t := board.Task{Title: title, Description: desc, Assignee: strings.TrimSpace(who), Labels: board.ParseLabels(labels)}
	if prio != "" {
		if t.Priority, err = board.ParsePriority(prio); err != nil {
			return err
		}
	}
	if t.Due, err = board.ParseDue(due, board.Now()); err != nil {
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
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Added %s %s to %s\n", b.Noun(), added.Ref(), b.Column(added.Column).Name)
	return nil
}

// --- list -----------------------------------------------------------------

func (c *cli) list(o listOpts) error {
	col, q, sortBy, asJSON, archived, all := o.col, o.query, o.sortBy, o.asJSON, o.archived, o.all
	b, err := c.env.board()
	if err != nil {
		return err
	}
	mode, ok := board.ParseSortMode(sortBy)
	if !ok {
		return usageErr("unknown sort %q", sortBy)
	}
	var tasks []board.Task
	switch {
	case archived:
		tasks = b.ArchivedTasks()
	case all:
		tasks = append([]board.Task(nil), b.Tasks...)
	default:
		tasks = b.Live()
	}
	if col != "" {
		cc := b.Column(col)
		if cc == nil {
			return fmt.Errorf("no column %q", col)
		}
		tasks = filterTasks(tasks, func(t board.Task) bool { return t.Column == cc.ID })
	}
	if q != "" {
		pq := board.ParseQuery(q)
		now := board.Now()
		tasks = filterTasks(tasks, func(t board.Task) bool { return pq.Matches(b, t, now) })
	}
	board.SortTasks(tasks, mode)
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
	now := board.Now()
	for _, t := range tasks {
		colName := board.ColName(b, t.Column)
		if t.Archived() {
			colName += " (archived)"
		}
		dueLabel, _ := board.DueInfo(t, now)
		pri := ""
		if t.Priority != board.PriorityNone {
			pri = t.Priority.String()
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, colName, pri, dueLabel, t.Title, strings.Join(t.Labels, ","), t.Assignee)
	}
	return tw.Flush()
}

func filterTasks(ts []board.Task, keep func(board.Task) bool) []board.Task {
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
			return fmt.Errorf("no %s #%d", b.Noun(), id)
		}
		if i > 0 {
			fmt.Fprintln(c.stdout)
		}
		fmt.Fprint(c.stdout, formatTask(c.env.File, b, *t, board.Now()))
	}
	return nil
}

// formatTask renders a task as plain text. f resolves the boards of links
// that leave b; it may be nil.
func formatTask(f *board.File, b *board.Board, t board.Task, now time.Time) string {
	var sb strings.Builder
	col := t.Column
	if cc := b.Column(t.Column); cc != nil {
		col = cc.Name
	}
	fmt.Fprintf(&sb, "%s %s\n", t.Ref(), t.Title)
	fmt.Fprintf(&sb, "  column:    %s\n", col)
	if t.Priority != board.PriorityNone {
		fmt.Fprintf(&sb, "  priority:  %s\n", t.Priority)
	}
	if t.Due != "" {
		label, _ := board.DueInfo(t, now)
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
	if done, total := b.SubtaskProgress(t.ID); total > 0 {
		fmt.Fprintf(&sb, "  subtasks:  %d/%d\n", done, total)
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
	if rels := b.Relations(t.ID); len(rels) > 0 {
		sb.WriteString("\n  Links\n")
		for _, r := range rels {
			ref := board.Ref{Board: r.Board, ID: r.Task.ID}
			fmt.Fprintf(&sb, "    %-11s %s %s (%s)\n", r.Label, ref, r.Task.Title, board.ColName(relBoard(f, b, r), r.Task.Column))
		}
	}
	if b.IsBlocked(t.ID) {
		sb.WriteString("  status:    blocked\n")
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

// relBoard is the board a relation's task lives on, for its column name.
func relBoard(f *board.File, b *board.Board, r board.Relation) *board.Board {
	if r.Board == "" || f == nil {
		return b
	}
	if ob := f.Board(r.Board); ob != nil {
		return ob
	}
	return b
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
	if err := c.Save(); err != nil {
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
	return c.Save()
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
	return c.Save()
}

// refBoard is the board a ref names, or cur when it names none.
func (c *cli) refBoard(cur *board.Board, r board.Ref) (*board.Board, error) {
	if r.Board == "" || r.Board == cur.ID {
		return cur, nil
	}
	ob := c.env.File.Board(r.Board)
	if ob == nil {
		return nil, fmt.Errorf("no board %q", r.Board)
	}
	return ob, nil
}

// relTo rewrites a ref read against cur so it can be stored on on.
func relTo(cur, on *board.Board, r board.Ref) board.Ref {
	id := r.Board
	if id == "" {
		id = cur.ID
	}
	if id == on.ID {
		return board.Ref{ID: r.ID}
	}
	return board.Ref{Board: id, ID: r.ID}
}

func (c *cli) link(args []string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	from, err := board.ParseRef(args[0], b, c.env.File)
	if err != nil {
		return err
	}
	to, err := board.ParseRef(args[2], b, c.env.File)
	if err != nil {
		return err
	}
	// Inverse words ("blocked-by", "parent-of") swap the two ends, so the
	// link is stored on whichever side the normalised relation starts from.
	first, kind, _, err := board.ParseLinkSpec(0, args[1], 1)
	if err != nil {
		return err
	}
	src, dst := from, to
	if first == 1 {
		src, dst = to, from
	}
	srcBoard, err := c.refBoard(b, src)
	if err != nil {
		return err
	}
	if err := srcBoard.AddLinkTo(src.ID, kind, relTo(b, srcBoard, dst)); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "%s %s %s\n", from, strings.ReplaceAll(strings.ToLower(args[1]), "_", "-"), to)
	return nil
}

func (c *cli) unlink(args []string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	from, err := board.ParseRef(args[0], b, c.env.File)
	if err != nil {
		return err
	}
	to, err := board.ParseRef(args[1], b, c.env.File)
	if err != nil {
		return err
	}
	fromBoard, err := c.refBoard(b, from)
	if err != nil {
		return err
	}
	toBoard, err := c.refBoard(b, to)
	if err != nil {
		return err
	}
	for _, side := range []struct {
		b *board.Board
		r board.Ref
	}{{fromBoard, from}, {toBoard, to}} {
		if side.b.Task(side.r.ID) == nil {
			return fmt.Errorf("no %s %s", side.b.Noun(), side.r)
		}
	}
	n := unlinkOneWay(fromBoard, from.ID, toBoard, to.ID) + unlinkOneWay(toBoard, to.ID, fromBoard, from.ID)
	if n == 0 {
		return fmt.Errorf("%s and %s are not linked", from, to)
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "removed %d link%s between %s and %s\n", n, board.Plural(n), from, to)
	return nil
}

// unlinkOneWay drops the links a task stores towards another task, which
// may live on another board, and returns how many were removed.
func unlinkOneWay(src *board.Board, id int, dst *board.Board, dstID int) int {
	t := src.Task(id)
	if t == nil {
		return 0
	}
	want := board.Ref{ID: dstID}
	if dst.ID != src.ID {
		want.Board = dst.ID
	}
	n := 0
	for _, l := range append([]board.Link(nil), t.Links...) {
		lb := l.Board
		if lb == "" {
			lb = src.ID
		}
		if l.Task == dstID && lb == dst.ID && src.RemoveLinkTo(id, l.Kind, want) {
			n++
		}
	}
	return n
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
	return c.Save()
}

// --- due ------------------------------------------------------------------

func (c *cli) due(days int, notify, quiet bool) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	now := board.Now()
	done := b.DoneColumn()
	var lines []string
	overdue, todayN, soon := 0, 0, 0
	tasks := b.Live()
	board.SortTasks(tasks, board.SortDue)
	for _, t := range tasks {
		if done != nil && t.Column == done.ID {
			continue
		}
		due, ok := t.DueDate()
		if !ok {
			continue
		}
		label, state := board.DueInfo(t, now)
		include := false
		switch state {
		case board.DueOverdue:
			overdue++
			include = true
		case board.DueToday:
			todayN++
			include = true
		default:
			if days > 0 && board.DaysBetween(now, due) <= days {
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
		if err := desktop.Notify("kancli: "+summary, body); err != nil {
			return err
		}
	}
	if quiet {
		return errSilentExit
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

func (c *cli) export(o exportOpts) error {
	format, out, all, events := o.format, o.out, o.all, o.events
	b, err := c.env.board()
	if err != nil {
		return err
	}
	if formatFromPath(out, format) == "parquet" {
		if out == "" {
			return usageErr("parquet export needs -o FILE")
		}
		bin, err := store.DuckDBBinary()
		if err != nil {
			return err
		}
		state, cleanup, err := store.WriteStateFile(c.env.File)
		if err != nil {
			return err
		}
		defer cleanup()
		eventsFile, eventsCleanup, err := store.WriteEventsFile(c.env.Store)
		if err != nil {
			return err
		}
		defer eventsCleanup()
		views := store.SQLViews(state, eventFiles(eventsFile), store.DoneColumns(c.env.File))
		source := fmt.Sprintf("SELECT * FROM tasks WHERE board = %s", store.SQLLiteral(b.ID))
		if events {
			source = fmt.Sprintf("SELECT * FROM events WHERE board = %s OR board IS NULL", store.SQLLiteral(b.ID))
		}
		_, err = store.RunDuckDB(bin, views, fmt.Sprintf("COPY (%s) TO %s (FORMAT PARQUET)", source, store.SQLLiteral(out)), "csv")
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
			return enc.Encode(c.env.File)
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

func writeCSV(w io.Writer, b *board.Board) error {
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
		if t.Priority != board.PriorityNone {
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
func formatMarkdown(b *board.Board) string {
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
			if t.Priority != board.PriorityNone {
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

func (c *cli) importTasks(format, col, path string) error {
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
	var tasks []board.Task
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
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Imported %d task%s into %s\n", n, board.Plural(n), b.Name)
	return nil
}

// tasksFromJSON accepts a kancli file, a board, a task array or one task.
func tasksFromJSON(data []byte) ([]board.Task, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err == nil {
		if _, ok := probe["boards"]; ok {
			f, err := board.Decode(data)
			if err != nil {
				return nil, err
			}
			var out []board.Task
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
			var b board.Board
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
		var t board.Task
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return []board.Task{t}, nil
	}
	var ts []board.Task
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil, fmt.Errorf("expected a kancli file, a board, a task or a task list: %w", err)
	}
	return ts, nil
}

// tasksFromCSV reads the columns written by export; only title is required.
func tasksFromCSV(data []byte) ([]board.Task, error) {
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
	var out []board.Task
	for _, rec := range records[1:] {
		title := get(rec, "title")
		if title == "" {
			continue
		}
		t := board.Task{Title: title, Description: get(rec, "description"), Column: get(rec, "column"),
			Labels: board.ParseLabels(get(rec, "labels")), Assignee: get(rec, "assignee")}
		if p := get(rec, "priority"); p != "" {
			if t.Priority, err = board.ParsePriority(p); err != nil {
				return nil, err
			}
		}
		if d := get(rec, "due"); d != "" {
			if t.Due, err = board.ParseDue(d, board.Now()); err != nil {
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
func tasksFromMarkdown(data []byte) ([]board.Task, error) {
	var out []board.Task
	col := ""
	var cur *board.Task
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
			t := board.Task{Column: col}
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
				cur.Checklist = append(cur.Checklist, board.ChecklistItem{Text: item, Done: done})
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

func applyMeta(t *board.Task, meta string) {
	for _, tok := range strings.Fields(meta) {
		switch {
		case strings.HasPrefix(tok, "!"):
			if p, err := board.ParsePriority(tok[1:]); err == nil {
				t.Priority = p
			}
		case strings.HasPrefix(tok, "due:"):
			if d, err := board.ParseDue(tok[4:], board.Now()); err == nil {
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

func (c *cli) boardsList() error {
	f := c.env.File
	// The kind column only appears once a goal board exists, so a file of
	// ticket boards lists exactly as it did before.
	kindWidth := 0
	for _, b := range f.Boards {
		if b.IsGoals() {
			kindWidth = 7
			break
		}
	}
	for _, b := range f.Boards {
		mark := " "
		if b.ID == f.ActiveBoard {
			mark = "*"
		}
		kind := ""
		if b.IsGoals() {
			kind = "goals"
		}
		n := len(b.Live())
		suffix := ""
		if b.Description != "" {
			suffix = "  " + b.Description
		}
		// A negative width left-aligns; 0 drops the column entirely.
		fmt.Fprintf(c.stdout, "%s %-20s %*s%d task%s%s\n", mark, b.Name, -kindWidth, kind, n, board.Plural(n), suffix)
	}
	return nil
}

func (c *cli) boardsNew(name, desc string, goals bool) error {
	f := c.env.File
	b, err := f.AddBoard(name)
	if err != nil {
		return err
	}
	f.Activate(b.ID) //nolint:errcheck // just created
	if goals {
		if err := f.SetBoardKind(b.ID, board.BoardKindGoals); err != nil {
			return err
		}
	}
	if desc != "" {
		if err := f.DescribeBoard(b.ID, desc); err != nil {
			return err
		}
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Created board %q\n", b.Name)
	return nil
}

func (c *cli) boardsKind(key, kind string) error {
	f := c.env.File
	b := f.Board(key)
	if b == nil {
		return fmt.Errorf("no board %q", key)
	}
	if err := f.SetBoardKind(b.ID, kind); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Made %q a %s board\n", b.Name, b.Noun())
	return nil
}

func (c *cli) boardsDescribe(key, text string) error {
	f := c.env.File
	b := f.Board(key)
	if b == nil {
		return fmt.Errorf("no board %q", key)
	}
	if err := f.DescribeBoard(b.ID, text); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	if b.Description == "" {
		fmt.Fprintf(c.stdout, "Cleared the description of %q\n", b.Name)
		return nil
	}
	fmt.Fprintf(c.stdout, "Described %q: %s\n", b.Name, b.Description)
	return nil
}

func (c *cli) boardsUse(name string) error {
	f := c.env.File
	b := f.Board(name)
	if b == nil {
		return fmt.Errorf("no board %q", name)
	}
	f.Activate(b.ID) //nolint:errcheck // board exists
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Switched to %q\n", b.Name)
	return nil
}

func (c *cli) boardsRename(from, to string) error {
	f := c.env.File
	b := f.Board(from)
	if b == nil {
		return fmt.Errorf("no board %q", from)
	}
	if err := f.RenameBoard(b.ID, to); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Renamed to %q\n", b.Name)
	return nil
}

func (c *cli) boardsRemove(name string) error {
	f := c.env.File
	b := f.Board(name)
	if b == nil {
		return fmt.Errorf("no board %q", name)
	}
	if err := f.RemoveBoard(b.ID); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Deleted board %q\n", b.Name)
	return nil
}

func (c *cli) columns() error {
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

func (c *cli) columnsAdd(name, color string, wip int) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col, err := b.AddColumn(name, color, wip)
	if err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Added column %q (%s)\n", col.Name, col.ID)
	return nil
}

// columnsEdit changes only what was given: an empty name or colour keeps
// the current one, and wip is applied only when wipSet is true so that
// "--wip 0" can clear a limit.
func (c *cli) columnsEdit(key, name, color string, wip int, wipSet bool) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.Column(key)
	if col == nil {
		return fmt.Errorf("no column %q (see `kancli columns`)", key)
	}
	old := *col
	if name == "" {
		name = col.Name
	}
	if color == "" {
		color = col.Color
	}
	if !wipSet {
		wip = col.WIPLimit
	}
	if err := b.UpdateColumn(col.ID, name, color, wip); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	if name != old.Name && color == old.Color && wip == old.WIPLimit {
		fmt.Fprintf(c.stdout, "Renamed column %q to %q\n", old.Name, name)
	} else {
		fmt.Fprintf(c.stdout, "Updated column %q (%s)\n", name, old.ID)
	}
	return nil
}

func (c *cli) columnsMove(key, where string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.Column(key)
	if col == nil {
		return fmt.Errorf("no column %q (see `kancli columns`)", key)
	}
	// col points into b.Columns, which MoveColumn reorders, so copy first.
	id, name := col.ID, col.Name
	n := len(b.Columns)
	from := b.ColumnIndex(id)
	var to int
	switch strings.ToLower(where) {
	case "left":
		to = from - 1
	case "right":
		to = from + 1
	case "first":
		to = 0
	case "last":
		to = n - 1
	default:
		pos, err := strconv.Atoi(where)
		if err != nil || pos < 1 || pos > n {
			return fmt.Errorf("position must be left, right, first, last or a number from 1 to %d", n)
		}
		to = pos - 1
	}
	if to < 0 || to >= n {
		return fmt.Errorf("column %q is already at that end", name)
	}
	step := 1
	if to < from {
		step = -1
	}
	for i := from; i != to; i += step {
		if !b.MoveColumn(id, step) {
			return fmt.Errorf("cannot move column %q", name)
		}
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Moved column %q to position %d\n", name, to+1)
	return nil
}

func (c *cli) columnsRemove(key, to string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.Column(key)
	if col == nil {
		return fmt.Errorf("no column %q (see `kancli columns`)", key)
	}
	// col points into b.Columns, which RemoveColumn cuts, so copy first.
	id, name := col.ID, col.Name
	moveTo := ""
	if to != "" {
		dest := b.Column(to)
		if dest == nil {
			return fmt.Errorf("no column %q to move tasks to (see `kancli columns`)", to)
		}
		moveTo = dest.ID
	} else if i := b.ColumnIndex(id); len(b.Columns) > 1 {
		// board.RemoveColumn drops the tasks when no destination is given,
		// so pick the neighbour here: the one to the left, else the right.
		if i > 0 {
			moveTo = b.Columns[i-1].ID
		} else {
			moveTo = b.Columns[i+1].ID
		}
	}
	moved := b.AllIn(id)
	if err := b.RemoveColumn(id, moveTo); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	if moved == 0 {
		fmt.Fprintf(c.stdout, "Removed column %q\n", name)
		return nil
	}
	fmt.Fprintf(c.stdout, "Removed column %q; %d task%s moved to %s\n", name, moved, board.Plural(moved), board.ColName(b, moveTo))
	return nil
}

func (c *cli) config() error {
	path := c.env.Opts.configPath
	if path == "" {
		path, _ = config.DefaultPath()
	}
	fmt.Fprintf(c.stdout, "Config file: %s\n", path)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintln(c.stdout, "(exists)")
	} else {
		fmt.Fprintln(c.stdout, "(not created yet)")
	}
	fmt.Fprintf(c.stdout, "Data file:   %s\n", c.env.Store.Describe())
	fmt.Fprintf(c.stdout, "Theme:       %s\n\nExample config:\n%s", c.env.Styles.ThemeName(), config.Example)
	return nil
}

func (c *cli) keys() error {
	k := ui.DefaultKeyMap()
	k.ApplyOverrides(c.env.Cfg.Keys) //nolint:errcheck // validated on load
	acts := k.Actions()
	names := ui.ActionNames()
	sort.Strings(names)
	tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ACTION\tKEYS\tDESCRIPTION")
	for _, n := range names {
		b := acts[n]
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n, ui.HelpLabel(b.Keys()), b.Help().Desc)
	}
	return tw.Flush()
}
