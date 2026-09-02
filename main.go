package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

// globalOpts are the flags accepted before a subcommand.
type globalOpts struct {
	file, board, theme, configPath, asOf string
	ascii, compact, demo, version        bool
}

// env is everything a command needs: config, store and loaded data.
type env struct {
	opts     globalOpts
	cfg      config
	st       styles
	g        glyphs
	store    *store
	file     *File
	readOnly bool
	asOf     time.Time
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `kancli — a kanban board for your terminal

Usage:
  kancli [flags]                  open the board
  kancli [flags] <command> [args] run a command without the UI

Commands:
  add <title...>       add a task (-d, -p, -due, -l, -a, -c)
  list                 list tasks (-c, -q, -json, -archived)
  show <id>            show one task
  move <id> <column>   move a task
  done <id...>         move tasks to the last column
  archive <id...>      archive tasks
  restore <id...>      restore archived tasks
  rm <id...>           delete tasks permanently
  due                  list overdue and due-today tasks (-days, -notify)
  stats                cycle time, throughput, WIP and aging (-days, -json, -q SQL)
  review               Markdown review of the last week (-days, -o)
  log                  recent events (-n, -task)
  export               write the board as json, csv, markdown or parquet (-f, -o)
  import <file>        read tasks from json, csv or markdown (-f, -c)
  boards               list boards; boards new|use|rename|rm <name>
  columns              list the columns of the board
  compact              fold the event log into a fresh snapshot
  config               show the config file location and an example
  keys                 list configurable key actions
  version              print the version

Add -as-of DATE before a command (or the UI) to see the board as it was.

Flags:
`)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var o globalOpts
	fs := flag.NewFlagSet("kancli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.file, "file", "", "data file (default: $KANCLI_FILE, config, or the user data dir)")
	fs.StringVar(&o.file, "f", "", "shorthand for -file")
	fs.StringVar(&o.board, "board", "", "board to open (name or id)")
	fs.StringVar(&o.board, "b", "", "shorthand for -board")
	fs.StringVar(&o.theme, "theme", "", "colour theme: default, high-contrast or mono")
	fs.StringVar(&o.configPath, "config", "", "config file (default: $KANCLI_CONFIG or the user config dir)")
	fs.BoolVar(&o.ascii, "ascii", false, "draw borders with ASCII characters")
	fs.BoolVar(&o.compact, "compact", false, "two-line cards")
	fs.BoolVar(&o.demo, "demo", false, "use sample data and don't save anything")
	fs.StringVar(&o.asOf, "as-of", "", "read-only view of the board at a date or time (2026-08-25, yesterday, -7d)")
	fs.BoolVar(&o.version, "version", false, "print the version and exit")
	fs.Usage = func() {
		usage(stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if o.version {
		fmt.Fprintln(stdout, "kancli", version)
		return 0
	}

	rest := fs.Args()
	if len(rest) > 0 && (rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help") {
		fs.Usage()
		return 0
	}
	if len(rest) > 0 && rest[0] == "version" {
		fmt.Fprintln(stdout, "kancli", version)
		return 0
	}

	actor := "ui"
	if len(rest) > 0 {
		actor = "cli"
	}
	e, err := newEnv(o, actor)
	if err != nil {
		fmt.Fprintf(stderr, "kancli: %v\n", err)
		return 1
	}

	if len(rest) > 0 {
		c := &cli{env: e, stdout: stdout, stderr: stderr}
		return c.run(rest[0], rest[1:])
	}

	if os.Getenv("KANCLI_DEBUG") != "" {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			fmt.Fprintf(stderr, "kancli: %v\n", err)
			return 1
		}
		defer f.Close()
	}
	a := newApp(e.cfg, e.st, e.g, e.store, e.file)
	if e.readOnly {
		a.readOnly, a.asOf = true, e.asOf
	}
	p := tea.NewProgram(a, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(stderr, "kancli: %v\n", err)
		return 1
	}
	return 0
}

// newEnv loads the config, resolves the data file and reads it. actor is
// recorded on every event this process writes.
func newEnv(o globalOpts, actor string) (*env, error) {
	cfgPath := o.configPath
	if cfgPath == "" {
		p, err := defaultConfigPath()
		if err == nil {
			cfgPath = p
		}
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	if o.theme != "" {
		cfg.Theme = o.theme
	}
	cfg.ASCII = cfg.ASCII || o.ascii
	cfg.Compact = cfg.Compact || o.compact
	if o.board != "" {
		cfg.Board = o.board
	}
	th, err := themeByName(cfg.Theme, cfg.ASCII)
	if err != nil {
		return nil, err
	}
	e := &env{opts: o, cfg: cfg, st: newStyles(th, cfg.ASCII), g: newGlyphs(cfg.ASCII)}

	if o.asOf != "" {
		t, err := parseAsOf(o.asOf, timeNow())
		if err != nil {
			return nil, err
		}
		e.asOf, e.readOnly = t, true
	}
	if o.demo {
		e.store = newStore("")
		e.store.actor = actor
		e.file = demoFile()
		e.file.rec.actor = "demo"
		if err := e.store.save(e.file); err != nil {
			return nil, err
		}
		if e.readOnly {
			return nil, fmt.Errorf("-as-of is not available in demo mode")
		}
		return e, nil
	}
	path := o.file
	if path == "" {
		path = os.Getenv("KANCLI_FILE")
	}
	if path == "" {
		path = cfg.File
	}
	if path == "" {
		path, err = defaultStorePath()
		if err != nil {
			return nil, err
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + path[1:]
		}
	}
	e.store = newStore(path)
	e.store.actor = actor
	if e.readOnly {
		e.file, err = e.store.loadAsOf(e.asOf)
		if err != nil {
			return nil, err
		}
		return e, nil
	}
	e.file, err = e.store.load()
	if err != nil {
		return nil, err
	}
	return e, nil
}

// parseAsOf accepts a date, a date-time, or a relative offset like -7d.
func parseAsOf(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "-") && len(s) > 1 {
		d, err := parseDue("+"+s[1:], now)
		if err == nil && d != "" {
			t, _ := time.ParseInLocation(dateLayout, d, time.Local)
			// +Nd added days; mirror it into the past.
			delta := t.Sub(today())
			return now.Add(-delta), nil
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	d, err := parseDue(s, now)
	if err != nil || d == "" {
		return time.Time{}, fmt.Errorf("cannot parse -as-of %q (use 2026-08-25, 2026-08-25 14:00, yesterday or -7d)", s)
	}
	t, _ := time.ParseInLocation(dateLayout, d, time.Local)
	// A bare date means the end of that day.
	return t.AddDate(0, 0, 1).Add(-time.Nanosecond), nil
}

// board returns the board selected by -board or the active one.
func (e *env) board() (*Board, error) {
	if e.cfg.Board != "" {
		b := e.file.Board(e.cfg.Board)
		if b == nil {
			return nil, fmt.Errorf("no board %q (see `kancli boards`)", e.cfg.Board)
		}
		return b, nil
	}
	return e.file.Active(), nil
}
