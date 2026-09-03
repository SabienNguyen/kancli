package ui

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/config"
	"github.com/SabienNguyen/kancli/internal/store"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func init() {
	StatusDuration = 0
}

const (
	testWidth  = 120
	testHeight = 36
)

func testStyles() (Styles, Glyphs) {
	th, _ := ThemeByName("default", false)
	return NewStyles(th, false), NewGlyphs(false)
}

// newStore returns a store that is closed when the test ends. A store that
// stays open holds board.db, and on Windows an open handle stops the temp
// directory from being removed.
func newStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st := store.New(path)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newTestApp returns a sized app with the demo data backed by a temp file.
func newTestApp(t *testing.T) (App, *store.Store) {
	t.Helper()
	st := newStore(t, filepath.Join(t.TempDir(), "board.json"))
	f := board.SampleFile()
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	s, g := testStyles()
	m := New(config.Config{}, s, g, st, f)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: testWidth, Height: testHeight})
	return mm.(App), st
}

func keyMsg(k string) tea.KeyMsg {
	special := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"ctrl+s": tea.KeyCtrlS, "ctrl+c": tea.KeyCtrlC, "ctrl+r": tea.KeyCtrlR,
		"shift+right": tea.KeyShiftRight, "shift+left": tea.KeyShiftLeft, "shift+up": tea.KeyShiftUp, "shift+down": tea.KeyShiftDown,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "backspace": tea.KeyBackspace,
	}
	if t, ok := special[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	if k == "space" {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// drain runs cmd and feeds the messages the app cares about back into it.
// Commands that block (cursor blinks, timers) are abandoned after a short
// wait; status-clearing ticks are dropped so tests can inspect messages.
func drain(m App, cmd tea.Cmd) App {
	if cmd == nil {
		return m
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return m
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = drain(m, c)
		}
		return m
	case formSubmitMsg, formCancelMsg, colFormSubmitMsg, promptSubmitMsg, list.FilterMatchesMsg:
		mm, c := m.Update(msg)
		return drain(mm.(App), c)
	}
	return m
}

// press sends one key and processes the resulting commands.
func press(m App, keys ...string) App {
	for _, k := range keys {
		mm, cmd := m.Update(keyMsg(k))
		m = drain(mm.(App), cmd)
	}
	return m
}

// typeText sends each rune of s as a key press.
func typeText(m App, s string) App {
	for _, r := range s {
		m = press(m, string(r))
	}
	return m
}

func visibleTitles(m App, col int) []string {
	var out []string
	for _, it := range m.cols[col].list.Items() {
		out = append(out, it.(card).t.Title)
	}
	return out
}

// stateOf renders the tasks and columns of every board for comparisons.
func stateOf(f *board.File) string {
	data, _ := json.Marshal(f.Boards)
	return string(data)
}

func idsOf(ts []board.Task) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
