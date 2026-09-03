package ui

import (
	"bytes"
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// TestGolden drives the real program with teatest and compares each screen
// against testdata/TestGolden/<name>.golden. Regenerate the files after an
// intentional change with:
//
//	go test ./internal/ui -run TestGolden -update
func TestGolden(t *testing.T) {
	cases := []struct {
		name  string
		theme string
		ascii bool
		cfg   config.Config
		keys  []string
		text  string
		after []string
		// want is a string the final screen must contain. Cases whose
		// last key starts an asynchronous command wait for it so the
		// screen has settled before the program is told to quit.
		want string
	}{
		{name: "board"},
		{name: "board-compact", cfg: config.Config{Compact: true}},
		{name: "board-mono-ascii", theme: "mono", ascii: true},
		{name: "board-narrow", cfg: config.Config{Sort: "priority"}},
		{name: "help", keys: []string{"?"}},
		{name: "search", keys: []string{"/"}, text: "p:high", after: []string{"enter"}},
		{name: "marks", keys: []string{" ", "j", " "}},
		{name: "detail", keys: []string{"enter"}},
		{name: "detail-links", keys: []string{"enter", "l"}, text: "blocked-by #2", after: []string{"enter"}, want: "Linked"},
		{name: "form", keys: []string{"n"}, text: "Write golden tests"},
		{name: "edit", keys: []string{"e"}},
		{name: "column-form", keys: []string{"C"}},
		{name: "boards", keys: []string{"b"}},
		{name: "prompt", keys: []string{"b", "n"}},
		{name: "confirm", keys: []string{"d"}},
		{name: "archive", keys: []string{"z"}},
		{name: "stats", keys: []string{"S"}},
		{name: "goals-board", keys: []string{"b", "j", "enter"}},
		{name: "goals-detail", keys: []string{"b", "j", "enter", "enter"}},
		{name: "moved", keys: []string{"L"}},
		{name: "sorted", keys: []string{"s", "s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := 120, 36
			if c.name == "board-narrow" {
				w, h = 72, 22
			}
			tm := goldenApp(t, w, h, c.theme, c.ascii, c.cfg)
			for _, k := range c.keys {
				tm.Send(keyMsg(k))
			}
			if c.text != "" {
				tm.Type(c.text)
			}
			for _, k := range c.after {
				tm.Send(keyMsg(k))
			}
			if c.want != "" {
				teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
					return bytes.Contains(b, []byte(c.want))
				}, teatest.WithCheckInterval(time.Millisecond), teatest.WithDuration(5*time.Second))
			}
			tm.Send(tea.QuitMsg{})
			fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(App)
			assertFits(t, fm, c.name)
			teatest.RequireEqualOutput(t, []byte(fm.View()))
		})
	}
}

// goldenApp starts the program on the demo board with a frozen clock so
// relative dates, week buckets and history are the same on every run.
func goldenApp(t *testing.T, w, h int, theme string, ascii bool, cfg config.Config) *teatest.TestModel {
	t.Helper()
	// The clock is fixed in the local zone so every derived date (due
	// labels, week buckets, history lines) is the same in any time zone.
	fixed := time.Date(2026, 8, 25, 10, 30, 0, 0, time.Local)
	prevNow := board.Now
	board.Now = func() time.Time { return fixed }
	t.Cleanup(func() { board.Now = prevNow })

	// Status messages must survive until the screen is captured: the
	// package-wide test default expires them immediately, which would
	// race the final render.
	prevStatus := StatusDuration
	StatusDuration = time.Hour
	t.Cleanup(func() { StatusDuration = prevStatus })

	if theme == "" {
		theme = "default"
	}
	th, err := ThemeByName(theme, ascii)
	if err != nil {
		t.Fatal(err)
	}
	cfg.NoAnimations = true
	cfg.Images = "off"
	cfg.ASCII = ascii
	f := board.DemoFile()
	st := newStore(t, "")
	m := New(cfg, NewStyles(th, ascii), NewGlyphs(ascii), st, f)
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(w, h))
}
