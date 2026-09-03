package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/ui"
)

// Main parses args, runs a subcommand or launches the UI, and returns the
// process exit code: 0 on success, 1 on failure, 2 on a usage error.
func Main(version string, args []string, stdout, stderr io.Writer, launch func(*Env) error) int {
	c := &cli{stdout: stdout, stderr: stderr, launch: launch}
	root := c.root(version)
	root.SetArgs(normalizeArgs(args))
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if c.env != nil {
		// Closing checkpoints the write-ahead log, so a clean exit leaves
		// board.db alone on disk.
		if cerr := c.env.Store.Close(); cerr != nil && err == nil {
			fmt.Fprintf(stderr, "kancli: %v\n", cerr)
		}
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errSilentExit):
		return 1
	case !c.ran:
		// Cobra rejected the command line before any command ran.
		fmt.Fprintf(stderr, "kancli: %v\nRun 'kancli --help' for usage.\n", err)
		return 2
	case c.envErr:
		fmt.Fprintf(stderr, "kancli: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "kancli %s: %v\n", c.name, err)
	if errors.Is(err, errUsage) {
		return 2
	}
	return 1
}

// normalizeArgs accepts the single-dash long flags earlier releases used
// (-file, -json, -as-of) by rewriting them to the --long form. Single
// letters (-p) and values that look like flags (-7d) are left alone.
func normalizeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			return append(out, args[i:]...)
		}
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			name, _, _ := strings.Cut(a[1:], "=")
			if len(name) >= 2 && isFlagName(name) {
				a = "-" + a
			}
		}
		out = append(out, a)
	}
	return out
}

func isFlagName(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r == '-' || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}
	return true
}

const (
	groupTasks   = "tasks"
	groupBoards  = "boards"
	groupInsight = "insight"
	groupData    = "data"
	groupSetup   = "setup"
)

// root builds the command tree. Every leaf goes through c.wrap so the
// environment is loaded once, after all flags are known.
func (c *cli) root(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "kancli",
		Short: "A kanban board for your terminal",
		Long: `kancli is a kanban board for your terminal.

Run it without a command to open the board. Every command also works
without the UI, so tasks can be scripted, piped and scheduled.

Add --as-of DATE before a command (or the UI) to see the board as it was.
Flags may also be written with a single dash (-json, -as-of).`,
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c.ran, c.name = true, "ui"
			if err := c.prepare("ui"); err != nil {
				return err
			}
			if c.launch == nil {
				return usageErr("no command given (see `kancli help`)")
			}
			return c.launch(c.env)
		},
	}
	root.SetVersionTemplate("kancli {{.Version}}\n")
	root.CompletionOptions.HiddenDefaultCmd = false

	pf := root.PersistentFlags()
	o := &c.opts
	pf.StringVar(&o.file, "file", "", "data file (default: $KANCLI_FILE, config, or the user data dir)")
	pf.StringVarP(&o.board, "board", "b", "", "board to open (name or id)")
	pf.StringVar(&o.theme, "theme", "", "colour theme: default, high-contrast or mono")
	pf.StringVar(&o.configPath, "config", "", "config file (default: $KANCLI_CONFIG or the user config dir)")
	pf.BoolVar(&o.ascii, "ascii", false, "draw borders with ASCII characters")
	pf.BoolVar(&o.compact, "compact", false, "two-line cards")
	pf.BoolVar(&o.demo, "demo", false, "use sample data and don't save anything")
	pf.StringVar(&o.asOf, "as-of", "", "read-only view of the board at a date or time (2026-08-25, yesterday, -7d)")
	pf.BoolVar(&o.noAnim, "no-animations", false, "disable card animations in the UI")
	_ = root.RegisterFlagCompletionFunc("board", c.completeBoards)
	_ = root.RegisterFlagCompletionFunc("theme", cobra.FixedCompletions(ui.ThemeNames, cobra.ShellCompDirectiveNoFileComp))
	_ = root.MarkPersistentFlagFilename("file", "json")
	_ = root.MarkPersistentFlagFilename("config", "json")

	root.AddGroup(
		&cobra.Group{ID: groupTasks, Title: "Task commands:"},
		&cobra.Group{ID: groupBoards, Title: "Board commands:"},
		&cobra.Group{ID: groupInsight, Title: "Insight commands:"},
		&cobra.Group{ID: groupData, Title: "Data commands:"},
		&cobra.Group{ID: groupSetup, Title: "Setup commands:"},
	)
	root.SetHelpCommandGroupID(groupSetup)
	root.SetCompletionCommandGroupID(groupSetup)

	root.AddCommand(
		c.addCmd(), c.listCmd(), c.showCmd(), c.moveCmd(), c.doneCmd(),
		c.archiveCmd(true), c.archiveCmd(false), c.rmCmd(), c.linkCmd(), c.unlinkCmd(), c.dueCmd(),
		c.statsCmd(), c.reviewCmd(), c.logCmd(),
		c.exportCmd(), c.importCmd(), c.compactCmd(),
		c.boardsCmd(), c.columnsCmd(),
		c.configCmd(), c.keysCmd(), c.versionCmd(version),
	)
	return root
}

// wrap loads the environment before a command body runs and records which
// command failed so Main can report it.
func (c *cli) wrap(name string, fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		c.ran, c.name = true, name
		if err := c.prepare("cli"); err != nil {
			return err
		}
		return fn(cmd, args)
	}
}

// prepare builds the environment once. actor is recorded on every event
// this process writes.
func (c *cli) prepare(actor string) error {
	if c.env != nil {
		return nil
	}
	e, err := NewEnv(c.opts, actor)
	if err != nil {
		c.envErr = true
		return err
	}
	c.env = e
	if up, ok := e.Store.Upgraded(); ok {
		fmt.Fprintf(c.stderr, "kancli: moved your board into %s (format v%d → v%d); the old files are in %s\n",
			e.Store.Path(), up.From, up.To, up.Backup)
	}
	for _, w := range e.Cfg.Warnings {
		fmt.Fprintln(c.stderr, "kancli:", w)
	}
	return nil
}

// --- completion -------------------------------------------------------------

// quietEnv loads the board for shell completion without printing errors.
func (c *cli) quietEnv() (*board.Board, bool) {
	if err := c.prepare("cli"); err != nil {
		return nil, false
	}
	b, err := c.env.board()
	if err != nil {
		return nil, false
	}
	return b, true
}

func (c *cli) completeTasks(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	b, ok := c.quietEnv()
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, t := range b.Live() {
		out = append(out, strconv.Itoa(t.ID)+"\t"+t.Title)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func (c *cli) completeColumns(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	b, ok := c.quietEnv()
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, col := range b.Columns {
		out = append(out, col.ID+"\t"+col.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func (c *cli) completeBoards(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	if err := c.prepare("cli"); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, b := range c.env.File.Boards {
		out = append(out, b.ID+"\t"+b.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// positional completes argument i of a command with fn and nothing after.
func positional(fns ...cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) < len(fns) {
			return fns[len(args)](cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func fixed(words ...string) cobra.CompletionFunc {
	return cobra.FixedCompletions(words, cobra.ShellCompDirectiveNoFileComp)
}

// --- task commands ----------------------------------------------------------

func (c *cli) addCmd() *cobra.Command {
	var o addOpts
	cmd := &cobra.Command{
		Use:     "add <title...>",
		Short:   "Add a task",
		GroupID: groupTasks,
		Args:    cobra.MinimumNArgs(1),
		Example: `  kancli add "Write the release notes" -p high --due fri -l docs -a sam
  kancli add -c "in progress" "Ship the CLI"`,
		RunE: c.wrap("add", func(_ *cobra.Command, args []string) error {
			return c.add(o, strings.TrimSpace(strings.Join(args, " ")))
		}),
	}
	f := cmd.Flags()
	f.StringVarP(&o.desc, "desc", "d", "", "description (Markdown)")
	f.StringVarP(&o.prio, "priority", "p", "", "priority: low, medium, high, urgent")
	f.StringVar(&o.due, "due", "", "due date: 2026-09-10, today, tomorrow, fri, +3d")
	f.StringVarP(&o.labels, "labels", "l", "", "comma separated labels")
	f.StringVarP(&o.who, "assignee", "a", "", "assignee")
	f.StringVarP(&o.col, "column", "c", "", "column (default: first)")
	_ = cmd.RegisterFlagCompletionFunc("column", c.completeColumns)
	_ = cmd.RegisterFlagCompletionFunc("priority", fixed("low", "medium", "high", "urgent"))
	_ = cmd.RegisterFlagCompletionFunc("due", fixed("today", "tomorrow", "mon", "tue", "wed", "thu", "fri", "sat", "sun", "+1d", "+3d", "+7d"))
	return cmd
}

func (c *cli) listCmd() *cobra.Command {
	var o listOpts
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tasks",
		GroupID: groupTasks,
		Args:    cobra.NoArgs,
		Example: `  kancli list -q "+bug due:week"
  kancli list --json | jq '.[].title'`,
		RunE: c.wrap("list", func(*cobra.Command, []string) error { return c.list(o) }),
	}
	f := cmd.Flags()
	f.StringVarP(&o.col, "column", "c", "", "only this column")
	f.StringVarP(&o.query, "query", "q", "", "search query (same syntax as the UI)")
	f.StringVar(&o.sortBy, "sort", "manual", "manual, priority, due, created, updated or title")
	f.BoolVar(&o.asJSON, "json", false, "print JSON")
	f.BoolVar(&o.archived, "archived", false, "list archived tasks instead")
	f.BoolVar(&o.all, "all", false, "include archived tasks")
	_ = cmd.RegisterFlagCompletionFunc("column", c.completeColumns)
	_ = cmd.RegisterFlagCompletionFunc("sort", fixed("manual", "priority", "due", "created", "updated", "title"))
	return cmd
}

func (c *cli) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "show <id...>",
		Short:             "Show one or more tasks",
		GroupID:           groupTasks,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeTasks,
		RunE:              c.wrap("show", func(_ *cobra.Command, args []string) error { return c.show(args) }),
	}
}

func (c *cli) moveCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "move <id> <column>",
		Aliases:           []string{"mv"},
		Short:             "Move a task to a column",
		GroupID:           groupTasks,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: positional(c.completeTasks, c.completeColumns),
		RunE:              c.wrap("move", func(_ *cobra.Command, args []string) error { return c.move(args) }),
	}
}

func (c *cli) doneCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "done <id...>",
		Short:             "Move tasks to the last column",
		GroupID:           groupTasks,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeTasks,
		RunE:              c.wrap("done", func(_ *cobra.Command, args []string) error { return c.done(args) }),
	}
}

func (c *cli) archiveCmd(archive bool) *cobra.Command {
	name, short := "archive", "Archive tasks"
	if !archive {
		name, short = "restore", "Restore archived tasks"
	}
	return &cobra.Command{
		Use:               name + " <id...>",
		Short:             short,
		GroupID:           groupTasks,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeTasks,
		RunE:              c.wrap(name, func(_ *cobra.Command, args []string) error { return c.archive(args, archive) }),
	}
}

func (c *cli) rmCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "rm <id...>",
		Aliases:           []string{"delete"},
		Short:             "Delete tasks permanently",
		GroupID:           groupTasks,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: c.completeTasks,
		RunE:              c.wrap("rm", func(_ *cobra.Command, args []string) error { return c.remove(args) }),
	}
}

func (c *cli) linkCmd() *cobra.Command {
	kinds := fixed("blocks", "blocked-by", "subtask-of", "parent-of", "relates")
	return &cobra.Command{
		Use:               "link <id> <blocks|blocked-by|subtask-of|parent-of|relates> <id>",
		Short:             "Link two tasks",
		GroupID:           groupTasks,
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: positional(c.completeTasks, kinds, c.completeTasks),
		Example:           "  kancli link 12 blocked-by 7\n  kancli link 3 subtask-of 2",
		RunE:              c.wrap("link", func(_ *cobra.Command, args []string) error { return c.link(args) }),
	}
}

func (c *cli) unlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "unlink <id> <id>",
		Short:             "Remove every link between two tasks",
		GroupID:           groupTasks,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: positional(c.completeTasks, c.completeTasks),
		RunE:              c.wrap("unlink", func(_ *cobra.Command, args []string) error { return c.unlink(args) }),
	}
}

func (c *cli) dueCmd() *cobra.Command {
	var days int
	var notify, quiet bool
	cmd := &cobra.Command{
		Use:     "due",
		Short:   "List overdue and due-today tasks",
		GroupID: groupTasks,
		Args:    cobra.NoArgs,
		Long: `List overdue and due-today tasks.

With --quiet nothing is printed and the exit code is 1 when something is
due, which makes it easy to use from shell prompts and cron.`,
		RunE: c.wrap("due", func(*cobra.Command, []string) error { return c.due(days, notify, quiet) }),
	}
	f := cmd.Flags()
	f.IntVar(&days, "days", 0, "also include tasks due within N days")
	f.BoolVar(&notify, "notify", false, "send a desktop notification when something is due")
	f.BoolVarP(&quiet, "quiet", "q", false, "print nothing, only exit 1 when something is due")
	return cmd
}

// --- insight ----------------------------------------------------------------

func (c *cli) statsCmd() *cobra.Command {
	var o statsOpts
	cmd := &cobra.Command{
		Use:     "stats",
		Short:   "Cycle time, throughput, WIP and aging",
		GroupID: groupInsight,
		Args:    cobra.NoArgs,
		Example: `  kancli stats --json
  kancli stats -q "SELECT * FROM cycle_times"   # any SQL, via DuckDB
  kancli stats --sql > kancli.sql`,
		RunE: c.wrap("stats", func(*cobra.Command, []string) error { return c.stats(o) }),
	}
	f := cmd.Flags()
	f.IntVar(&o.days, "days", 90, "window for throughput, WIP and cycle time")
	f.BoolVar(&o.asJSON, "json", false, "print the statistics as JSON")
	f.StringVarP(&o.query, "query", "q", "", "run this SQL with DuckDB over the tasks and events views")
	f.StringVar(&o.format, "format", "box", "DuckDB output: box, json, csv or markdown")
	f.BoolVar(&o.showSQL, "sql", false, "print the DuckDB view definitions and example queries")
	_ = cmd.RegisterFlagCompletionFunc("format", fixed("box", "json", "csv", "markdown"))
	return cmd
}

func (c *cli) reviewCmd() *cobra.Command {
	var days int
	var out string
	cmd := &cobra.Command{
		Use:     "review",
		Short:   "Markdown review of the last week",
		GroupID: groupInsight,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("review", func(*cobra.Command, []string) error { return c.review(days, out) }),
	}
	cmd.Flags().IntVar(&days, "days", 7, "period to review")
	cmd.Flags().StringVarP(&out, "output", "o", "", "write the Markdown to this file")
	_ = cmd.MarkFlagFilename("output", "md")
	return cmd
}

func (c *cli) logCmd() *cobra.Command {
	var n, task int
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "log",
		Aliases: []string{"history"},
		Short:   "Recent events",
		GroupID: groupInsight,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("log", func(*cobra.Command, []string) error { return c.log(n, task, asJSON) }),
	}
	f := cmd.Flags()
	f.IntVarP(&n, "count", "n", 20, "number of events to show")
	f.IntVar(&task, "task", 0, "only events for this task id")
	f.BoolVar(&asJSON, "json", false, "print raw events as JSON lines")
	return cmd
}

// --- data -------------------------------------------------------------------

func (c *cli) exportCmd() *cobra.Command {
	var o exportOpts
	cmd := &cobra.Command{
		Use:     "export",
		Short:   "Write the board as json, csv, markdown or parquet",
		GroupID: groupData,
		Args:    cobra.NoArgs,
		Example: `  kancli export -f md
  kancli export -o tasks.parquet          # via DuckDB
  kancli export -o events.parquet --events`,
		RunE: c.wrap("export", func(*cobra.Command, []string) error { return c.export(o) }),
	}
	f := cmd.Flags()
	f.StringVarP(&o.format, "format", "f", "", "json, csv, md or parquet (default from -o extension, else json)")
	f.StringVarP(&o.out, "output", "o", "", "output file (default: stdout; required for parquet)")
	f.BoolVar(&o.all, "all", false, "json only: export every board, not just the current one")
	f.BoolVar(&o.events, "events", false, "parquet only: export the event log instead of tasks")
	_ = cmd.RegisterFlagCompletionFunc("format", fixed("json", "csv", "md", "parquet"))
	_ = cmd.MarkFlagFilename("output")
	return cmd
}

func (c *cli) importCmd() *cobra.Command {
	var format, col string
	cmd := &cobra.Command{
		Use:     "import <file>",
		Short:   "Read tasks from json, csv or markdown",
		GroupID: groupData,
		Args:    cobra.ExactArgs(1),
		Long:    "Read tasks from a json, csv or markdown file. Use - to read standard input.",
		RunE:    c.wrap("import", func(_ *cobra.Command, args []string) error { return c.importTasks(format, col, args[0]) }),
	}
	f := cmd.Flags()
	f.StringVarP(&format, "format", "f", "", "json, csv or md (default from the file extension)")
	f.StringVarP(&col, "column", "c", "", "default column for tasks without one")
	_ = cmd.RegisterFlagCompletionFunc("format", fixed("json", "csv", "md"))
	_ = cmd.RegisterFlagCompletionFunc("column", c.completeColumns)
	return cmd
}

func (c *cli) compactCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "compact",
		Short:   "Fold the event log into a fresh snapshot",
		GroupID: groupData,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("compact", func(*cobra.Command, []string) error { return c.Compact() }),
	}
}

// --- boards / columns -------------------------------------------------------

func (c *cli) boardsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "boards",
		Aliases: []string{"board"},
		Short:   "List boards, or manage them with new, use, rename, describe and rm",
		GroupID: groupBoards,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("boards", func(*cobra.Command, []string) error { return c.boardsList() }),
	}
	var newDesc string
	boardsNew := &cobra.Command{
		Use:     "new <name>",
		Aliases: []string{"add"},
		Short:   "Create a board and switch to it",
		Args:    cobra.MinimumNArgs(1),
		RunE: c.wrap("boards new", func(_ *cobra.Command, args []string) error {
			return c.boardsNew(strings.Join(args, " "), newDesc)
		}),
	}
	boardsNew.Flags().StringVar(&newDesc, "desc", "", "description")

	cmd.AddCommand(
		boardsNew,
		&cobra.Command{
			Use:               "use <name>",
			Aliases:           []string{"switch"},
			Short:             "Switch to a board",
			Args:              cobra.MinimumNArgs(1),
			ValidArgsFunction: positional(c.completeBoards),
			RunE:              c.wrap("boards use", func(_ *cobra.Command, args []string) error { return c.boardsUse(strings.Join(args, " ")) }),
		},
		&cobra.Command{
			Use:               "rename <old> <new>",
			Short:             "Rename a board",
			Args:              cobra.ExactArgs(2),
			ValidArgsFunction: positional(c.completeBoards),
			RunE:              c.wrap("boards rename", func(_ *cobra.Command, args []string) error { return c.boardsRename(args[0], args[1]) }),
		},
		&cobra.Command{
			Use:               "describe <board> <text...>",
			Short:             "Set a board's description (empty text clears it)",
			Args:              cobra.MinimumNArgs(1),
			ValidArgsFunction: positional(c.completeBoards),
			Example:           "  kancli boards describe work \"Client projects and invoices\"\n  kancli boards describe work \"\"",
			RunE: c.wrap("boards describe", func(_ *cobra.Command, args []string) error {
				return c.boardsDescribe(args[0], strings.Join(args[1:], " "))
			}),
		},
		&cobra.Command{
			Use:               "rm <name>",
			Aliases:           []string{"delete"},
			Short:             "Delete a board and everything on it",
			Args:              cobra.MinimumNArgs(1),
			ValidArgsFunction: positional(c.completeBoards),
			RunE:              c.wrap("boards rm", func(_ *cobra.Command, args []string) error { return c.boardsRemove(strings.Join(args, " ")) }),
		},
	)
	return cmd
}

func (c *cli) columnsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "columns",
		Aliases: []string{"cols"},
		Short:   "List the columns of the board, or manage them with add, edit, rename, move and rm",
		GroupID: groupBoards,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("columns", func(*cobra.Command, []string) error { return c.columns() }),
	}

	var addColor string
	var addWIP int
	add := &cobra.Command{
		Use:     "add <name...>",
		Aliases: []string{"new"},
		Short:   "Add a column at the right",
		Args:    cobra.MinimumNArgs(1),
		Example: "  kancli columns add Review --wip 2\n  kancli columns add \"Code Review\" --color 99",
		RunE: c.wrap("columns add", func(_ *cobra.Command, args []string) error {
			return c.columnsAdd(strings.Join(args, " "), addColor, addWIP)
		}),
	}
	add.Flags().StringVar(&addColor, "color", "", "ANSI colour code, e.g. 62 or 214 (default: next palette colour)")
	add.Flags().IntVar(&addWIP, "wip", 0, "work-in-progress limit shown in the header (0 = none)")
	_ = add.RegisterFlagCompletionFunc("color", fixed(board.ColumnPalette...))

	var editName, editColor string
	var editWIP int
	edit := &cobra.Command{
		Use:               "edit <column>",
		Short:             "Change a column's name, colour or WIP limit",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: positional(c.completeColumns),
		Example:           "  kancli columns edit review --name QA\n  kancli columns edit todo --wip 5",
		RunE: c.wrap("columns edit", func(cmd *cobra.Command, args []string) error {
			return c.columnsEdit(args[0], editName, editColor, editWIP, cmd.Flags().Changed("wip"))
		}),
	}
	edit.Flags().StringVar(&editName, "name", "", "new name")
	edit.Flags().StringVar(&editColor, "color", "", "ANSI colour code")
	edit.Flags().IntVar(&editWIP, "wip", 0, "work-in-progress limit (0 = none)")
	_ = edit.RegisterFlagCompletionFunc("color", fixed(board.ColumnPalette...))

	rename := &cobra.Command{
		Use:               "rename <column> <new name...>",
		Short:             "Rename a column",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: positional(c.completeColumns),
		RunE: c.wrap("columns rename", func(_ *cobra.Command, args []string) error {
			return c.columnsEdit(args[0], strings.Join(args[1:], " "), "", 0, false)
		}),
	}

	move := &cobra.Command{
		Use:               "move <column> <left|right|first|last|position>",
		Aliases:           []string{"mv"},
		Short:             "Move a column one step, to an end, or to a 1-based position",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: positional(c.completeColumns, fixed("left", "right", "first", "last")),
		Example:           "  kancli columns move review left\n  kancli columns move qa 2",
		RunE: c.wrap("columns move", func(_ *cobra.Command, args []string) error {
			return c.columnsMove(args[0], args[1])
		}),
	}

	var rmTo string
	rm := &cobra.Command{
		Use:               "rm <column>",
		Aliases:           []string{"delete"},
		Short:             "Delete a column and move its tasks to another",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: positional(c.completeColumns),
		Example:           "  kancli columns rm review --to done",
		RunE: c.wrap("columns rm", func(_ *cobra.Command, args []string) error {
			return c.columnsRemove(args[0], rmTo)
		}),
	}
	rm.Flags().StringVar(&rmTo, "to", "", "column that receives the tasks (default: the neighbour)")
	_ = rm.RegisterFlagCompletionFunc("to", c.completeColumns)

	cmd.AddCommand(add, edit, rename, move, rm)
	return cmd
}

// --- setup ------------------------------------------------------------------

func (c *cli) configCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "config",
		Short:   "Show the config file location and an example",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("config", func(*cobra.Command, []string) error { return c.config() }),
	}
}

func (c *cli) keysCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "keys",
		Short:   "List configurable key actions",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("keys", func(*cobra.Command, []string) error { return c.keys() }),
	}
}

func (c *cli) versionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the version",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			c.ran = true
			fmt.Fprintln(c.stdout, "kancli", version)
			return nil
		},
	}
}
