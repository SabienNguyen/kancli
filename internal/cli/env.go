package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/config"
	"github.com/SabienNguyen/kancli/internal/store"
	"github.com/SabienNguyen/kancli/internal/ui"
)

// Options are the flags accepted before a subcommand.
type Options struct {
	file, board, theme, configPath, asOf string
	ascii, compact, demo, noAnim         bool
}

// Env is everything a command needs: config, store and loaded data.
type Env struct {
	Opts     Options
	Cfg      config.Config
	Styles   ui.Styles
	Glyphs   ui.Glyphs
	Store    *store.Store
	File     *board.File
	ReadOnly bool
	AsOf     time.Time
}

// NewEnv loads the config, resolves the data file and reads it. actor is
// recorded on every event this process writes.
func NewEnv(o Options, actor string) (*Env, error) {
	cfgPath := o.configPath
	if cfgPath == "" {
		p, err := config.DefaultPath()
		if err == nil {
			cfgPath = p
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if o.theme != "" {
		cfg.Theme = o.theme
	}
	cfg.ASCII = cfg.ASCII || o.ascii
	cfg.Compact = cfg.Compact || o.compact
	cfg.NoAnimations = cfg.NoAnimations || o.noAnim
	if o.board != "" {
		cfg.Board = o.board
	}
	th, err := ui.ThemeByName(cfg.Theme, cfg.ASCII)
	if err != nil {
		return nil, err
	}
	e := &Env{Opts: o, Cfg: cfg, Styles: ui.NewStyles(th, cfg.ASCII), Glyphs: ui.NewGlyphs(cfg.ASCII)}

	if o.asOf != "" {
		t, err := parseAsOf(o.asOf, board.Now())
		if err != nil {
			return nil, err
		}
		e.AsOf, e.ReadOnly = t, true
	}
	if o.demo {
		e.Store = store.New("")
		e.Store.SetActor(actor)
		e.File = board.DemoFile()
		e.File.SetActor("demo")
		if err := e.Store.Save(e.File); err != nil {
			return nil, err
		}
		if e.ReadOnly {
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
		path, err = store.DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + path[1:]
		}
	}
	e.Store = store.New(path)
	e.Store.SetActor(actor)
	if e.ReadOnly {
		e.File, err = e.Store.LoadAsOf(e.AsOf)
		if err != nil {
			return nil, err
		}
		return e, nil
	}
	e.File, err = e.Store.Load()
	if err != nil {
		return nil, err
	}
	return e, nil
}

// parseAsOf accepts a date, a date-time, or a relative offset like -7d.
func parseAsOf(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "-") && len(s) > 1 {
		d, err := board.ParseDue("+"+s[1:], now)
		if err == nil && d != "" {
			t, _ := time.ParseInLocation(board.DateLayout, d, time.Local)
			// +Nd added days; mirror it into the past.
			delta := t.Sub(board.Today())
			return now.Add(-delta), nil
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	d, err := board.ParseDue(s, now)
	if err != nil || d == "" {
		return time.Time{}, fmt.Errorf("cannot parse -as-of %q (use 2026-08-25, 2026-08-25 14:00, yesterday or -7d)", s)
	}
	t, _ := time.ParseInLocation(board.DateLayout, d, time.Local)
	// A bare date means the end of that day.
	return t.AddDate(0, 0, 1).Add(-time.Nanosecond), nil
}

// board returns the board selected by -board or the active one.
func (e *Env) board() (*board.Board, error) {
	if e.Cfg.Board != "" {
		b := e.File.Board(e.Cfg.Board)
		if b == nil {
			return nil, fmt.Errorf("no board %q (see `kancli boards`)", e.Cfg.Board)
		}
		return b, nil
	}
	return e.File.Active(), nil
}
