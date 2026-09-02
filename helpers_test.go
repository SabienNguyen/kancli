package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func init() {
	statusDuration = 0
}

const (
	testWidth  = 120
	testHeight = 36
)

func testStyles() (styles, glyphs) {
	th, _ := themeByName("default", false)
	return newStyles(th, false), newGlyphs(false)
}

// newTestApp returns a sized app with the demo data backed by a temp file.
func newTestApp(t *testing.T) (app, *store) {
	t.Helper()
	st := newStore(filepath.Join(t.TempDir(), "board.json"))
	f := sampleFile()
	if err := st.save(f); err != nil {
		t.Fatal(err)
	}
	s, g := testStyles()
	m := newApp(config{}, s, g, st, f)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: testWidth, Height: testHeight})
	return mm.(app), st
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
func drain(m app, cmd tea.Cmd) app {
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
		return drain(mm.(app), c)
	}
	return m
}

// press sends one key and processes the resulting commands.
func press(m app, keys ...string) app {
	for _, k := range keys {
		mm, cmd := m.Update(keyMsg(k))
		m = drain(mm.(app), cmd)
	}
	return m
}

// typeText sends each rune of s as a key press.
func typeText(m app, s string) app {
	for _, r := range s {
		m = press(m, string(r))
	}
	return m
}

func visibleTitles(m app, col int) []string {
	var out []string
	for _, it := range m.cols[col].list.Items() {
		out = append(out, it.(card).t.Title)
	}
	return out
}
