package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func init() {
	statusDuration = 0
}

const (
	testWidth  = 120
	testHeight = 36
)

// newTestBoard returns a sized board backed by a temp file.
func newTestBoard(t *testing.T) (Board, store) {
	t.Helper()
	st := newStore(filepath.Join(t.TempDir(), "board.json"))
	m := newBoard(st, sampleTasks())
	mm, _ := m.Update(tea.WindowSizeMsg{Width: testWidth, Height: testHeight})
	return mm.(Board), st
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "shift+right":
		return tea.KeyMsg{Type: tea.KeyShiftRight}
	case "shift+left":
		return tea.KeyMsg{Type: tea.KeyShiftLeft}
	case "shift+up":
		return tea.KeyMsg{Type: tea.KeyShiftUp}
	case "shift+down":
		return tea.KeyMsg{Type: tea.KeyShiftDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// drain runs cmd and feeds the messages the board cares about back into
// it. Commands that block (cursor blinks) are abandoned after a short wait
// and status-clearing ticks are dropped so tests can inspect the message.
func drain(m Board, cmd tea.Cmd) Board {
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
	case formSubmitMsg, formCancelMsg, list.FilterMatchesMsg:
		mm, c := m.Update(msg)
		return drain(mm.(Board), c)
	}
	return m
}

// press sends one key and processes the resulting commands.
func press(m Board, k string) Board {
	mm, cmd := m.Update(keyMsg(k))
	return drain(mm.(Board), cmd)
}

// typeText sends each rune of s as a key press.
func typeText(m Board, s string) Board {
	for _, r := range s {
		m = press(m, string(r))
	}
	return m
}

func titles(c column) []string {
	var out []string
	for _, t := range c.tasks() {
		out = append(out, t.title)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestViewFitsTerminal(t *testing.T) {
	m, _ := newTestBoard(t)
	view := m.View()
	for _, want := range []string{"To Do", "In Progress", "Done", "buy milk", "write code", "stay cool", "Kancli"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q", want)
		}
	}
	lines := strings.Split(view, "\n")
	if len(lines) > testHeight {
		t.Errorf("view has %d lines, terminal has %d", len(lines), testHeight)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > testWidth {
			t.Errorf("line %d is %d wide, terminal is %d", i, w, testWidth)
		}
	}
}

func TestViewBeforeSizeAndWhenTooSmall(t *testing.T) {
	m := newBoard(store{}, nil)
	if got := m.View(); !strings.Contains(got, "Loading") {
		t.Errorf("unsized view = %q, want loading message", got)
	}
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	if got := mm.(Board).View(); !strings.Contains(got, "too small") {
		t.Errorf("tiny view = %q, want too-small message", got)
	}
}

func TestColumnNavigation(t *testing.T) {
	m, _ := newTestBoard(t)
	if m.focused != todo {
		t.Fatalf("initial focus = %v, want To Do", m.focused)
	}
	m = press(m, "l")
	if m.focused != inProgress {
		t.Errorf("after l focus = %v, want In Progress", m.focused)
	}
	m = press(m, "right")
	m = press(m, "right")
	if m.focused != todo {
		t.Errorf("focus should wrap around to To Do, got %v", m.focused)
	}
	m = press(m, "h")
	if m.focused != done {
		t.Errorf("after h from To Do focus = %v, want Done", m.focused)
	}
	m = press(m, "2")
	if m.focused != inProgress {
		t.Errorf("after 2 focus = %v, want In Progress", m.focused)
	}
	if !m.cols[inProgress].focused || m.cols[done].focused {
		t.Error("column focus flags do not match the board focus")
	}
}

func TestCursorMovement(t *testing.T) {
	m, _ := newTestBoard(t)
	m = press(m, "j")
	if got, _ := m.cols[todo].selected(); got.title != "eat sushi" {
		t.Errorf("after j selected = %q, want eat sushi", got.title)
	}
	m = press(m, "k")
	if got, _ := m.cols[todo].selected(); got.title != "buy milk" {
		t.Errorf("after k selected = %q, want buy milk", got.title)
	}
}

func TestMoveTaskBetweenColumns(t *testing.T) {
	m, st := newTestBoard(t)
	m = press(m, "L")
	if got := titles(m.cols[todo]); !equalStrings(got, []string{"eat sushi", "fold laundry"}) {
		t.Errorf("To Do after move = %v", got)
	}
	if got := titles(m.cols[inProgress]); !equalStrings(got, []string{"write code", "buy milk"}) {
		t.Errorf("In Progress after move = %v", got)
	}
	moved := m.cols[inProgress].tasks()[1]
	if moved.status != inProgress {
		t.Errorf("moved task status = %v, want In Progress", moved.status)
	}
	if sel, _ := m.cols[inProgress].selected(); sel.id != moved.id {
		t.Error("moved task should be selected in its new column")
	}

	saved, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, task := range saved {
		if task.title == "buy milk" && task.status == inProgress {
			found = true
		}
	}
	if !found {
		t.Error("move was not persisted")
	}

	// Moving left from the first column is a no-op with a status message.
	m = press(m, "H")
	if got := titles(m.cols[todo]); len(got) != 2 {
		t.Errorf("To Do changed on an impossible move: %v", got)
	}
	if m.statusMsg == "" {
		t.Error("expected a status message explaining the no-op")
	}

	// Moving right from Done is a no-op too.
	m = press(m, "3")
	m = press(m, "shift+right")
	if got := titles(m.cols[done]); !equalStrings(got, []string{"stay cool"}) {
		t.Errorf("Done changed on an impossible move: %v", got)
	}
	// And moving left from Done works.
	m = press(m, "shift+left")
	if got := titles(m.cols[inProgress]); !equalStrings(got, []string{"write code", "buy milk", "stay cool"}) {
		t.Errorf("In Progress after move left = %v", got)
	}
	if got := m.cols[done].count(); got != 0 {
		t.Errorf("Done has %d tasks, want 0", got)
	}
}

func TestReorderWithinColumn(t *testing.T) {
	m, st := newTestBoard(t)
	first := m.cols[todo].tasks()[0]
	m = press(m, "J")
	if got := titles(m.cols[todo]); !equalStrings(got, []string{"eat sushi", "buy milk", "fold laundry"}) {
		t.Errorf("after J order = %v", got)
	}
	if sel, _ := m.cols[todo].selected(); sel.id != first.id {
		t.Error("cursor should follow the moved task")
	}
	m = press(m, "K")
	if got := titles(m.cols[todo]); !equalStrings(got, []string{"buy milk", "eat sushi", "fold laundry"}) {
		t.Errorf("after K order = %v", got)
	}
	// Moving the top task up is a no-op.
	m = press(m, "K")
	if got := titles(m.cols[todo]); !equalStrings(got, []string{"buy milk", "eat sushi", "fold laundry"}) {
		t.Errorf("after K at top order = %v", got)
	}

	m = press(m, "J")
	saved, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].title != "eat sushi" || saved[1].title != "buy milk" {
		t.Errorf("order not persisted: %q, %q", saved[0].title, saved[1].title)
	}
}

func TestCreateTask(t *testing.T) {
	m, st := newTestBoard(t)
	m = press(m, "l")
	m = press(m, "n")
	if m.state != stateForm {
		t.Fatal("n should open the form")
	}
	if !strings.Contains(m.View(), "New task") {
		t.Error("form view should show the New task heading")
	}
	m = typeText(m, "ship it")
	m = press(m, "enter")
	m = typeText(m, "line one")
	m = press(m, "enter")
	m = typeText(m, "line two")
	m = press(m, "ctrl+s")
	if m.state != stateBoard {
		t.Fatal("ctrl+s should close the form")
	}
	got := m.cols[inProgress].tasks()
	if len(got) != 2 {
		t.Fatalf("In Progress has %d tasks, want 2", len(got))
	}
	created := got[1]
	if created.title != "ship it" || created.description != "line one\nline two" || created.status != inProgress {
		t.Errorf("created task = %+v", created)
	}
	if created.id == "" || created.createdAt.IsZero() {
		t.Error("created task is missing an id or timestamp")
	}
	if sel, _ := m.cols[inProgress].selected(); sel.id != created.id {
		t.Error("new task should be selected")
	}
	if !strings.Contains(m.cols[inProgress].view(), "line one …") {
		t.Error("card should show the first description line with an ellipsis")
	}

	saved, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 6 {
		t.Errorf("saved %d tasks, want 6", len(saved))
	}
}

func TestCreateTaskRequiresTitle(t *testing.T) {
	m, _ := newTestBoard(t)
	m = press(m, "n")
	m = press(m, "ctrl+s")
	if m.state != stateForm {
		t.Fatal("form should stay open without a title")
	}
	if m.form.err == "" || !strings.Contains(m.View(), "title is required") {
		t.Error("expected a validation error")
	}
	m = typeText(m, "x")
	if m.form.err != "" {
		t.Error("error should clear once a title is typed")
	}
	m = press(m, "esc")
	if m.state != stateBoard {
		t.Error("esc should close the form")
	}
	if got := m.cols[todo].count(); got != 3 {
		t.Errorf("cancelled form added a task: %d tasks", got)
	}
}

func TestEditTask(t *testing.T) {
	m, st := newTestBoard(t)
	original := m.cols[todo].tasks()[0]
	m = press(m, "e")
	if m.state != stateForm || m.form.mode != formEdit {
		t.Fatal("e should open the edit form")
	}
	if m.form.title.Value() != "buy milk" || m.form.desc.Value() != "strawberry milk" {
		t.Errorf("form not prefilled: %q / %q", m.form.title.Value(), m.form.desc.Value())
	}
	m = typeText(m, " today")
	m = press(m, "tab")
	m = typeText(m, "!")
	m = press(m, "ctrl+s")
	if m.state != stateBoard {
		t.Fatal("ctrl+s should close the form")
	}
	edited := m.cols[todo].tasks()[0]
	if edited.id != original.id {
		t.Error("editing changed the task id")
	}
	if edited.title != "buy milk today" || edited.description != "strawberry milk!" {
		t.Errorf("edited task = %q / %q", edited.title, edited.description)
	}
	if got := m.cols[todo].count(); got != 3 {
		t.Errorf("edit changed the task count to %d", got)
	}
	saved, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].title != "buy milk today" {
		t.Errorf("edit not persisted: %q", saved[0].title)
	}

	// Enter also opens the editor.
	m = press(m, "enter")
	if m.state != stateForm {
		t.Error("enter should open the edit form")
	}
}

func TestDeleteTask(t *testing.T) {
	m, st := newTestBoard(t)
	m = press(m, "d")
	if m.state != stateConfirmDelete {
		t.Fatal("d should ask for confirmation")
	}
	if !strings.Contains(m.View(), "Delete task?") || !strings.Contains(m.View(), "buy milk") {
		t.Error("confirmation should name the task")
	}
	m = press(m, "n")
	if m.state != stateBoard || m.cols[todo].count() != 3 {
		t.Fatal("n should cancel the delete")
	}
	m = press(m, "d")
	m = press(m, "y")
	if got := titles(m.cols[todo]); !equalStrings(got, []string{"eat sushi", "fold laundry"}) {
		t.Errorf("after delete = %v", got)
	}
	if sel, ok := m.cols[todo].selected(); !ok || sel.title != "eat sushi" {
		t.Errorf("cursor should land on the next task, got %v", sel.title)
	}
	saved, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 4 {
		t.Errorf("saved %d tasks, want 4", len(saved))
	}

	// Deleting the last task in a column leaves an empty column that still
	// renders and ignores further deletes.
	m = press(m, "3")
	m = press(m, "d")
	m = press(m, "y")
	if m.cols[done].count() != 0 {
		t.Error("Done should be empty")
	}
	m = press(m, "d")
	if m.state != stateBoard {
		t.Error("delete on an empty column should do nothing")
	}
	if !strings.Contains(m.View(), "No tasks") {
		t.Error("empty column should say so")
	}
}

func TestFilterCapturesKeysAndMoves(t *testing.T) {
	m, _ := newTestBoard(t)
	m = press(m, "/")
	if !m.cols[todo].filtering() {
		t.Fatal("/ should start filtering")
	}
	// While typing a filter, board keys are literal input.
	m = typeText(m, "sushi")
	if m.state != stateBoard || m.cols[todo].count() != 3 {
		t.Fatal("filter text should not trigger board actions")
	}
	m = press(m, "enter")
	if !m.cols[todo].list.IsFiltered() {
		t.Fatal("enter should apply the filter")
	}
	if got := len(m.cols[todo].list.VisibleItems()); got != 1 {
		t.Fatalf("filter shows %d tasks, want 1", got)
	}
	// Board actions use the visible selection, not the raw index.
	m = press(m, "L")
	if got := titles(m.cols[inProgress]); !equalStrings(got, []string{"write code", "eat sushi"}) {
		t.Errorf("In Progress after filtered move = %v", got)
	}
	if got := titles(m.cols[todo]); !equalStrings(got, []string{"buy milk", "fold laundry"}) {
		t.Errorf("To Do after filtered move = %v", got)
	}
	// Reordering is refused while filtered.
	m = press(m, "J")
	if got := titles(m.cols[todo]); !equalStrings(got, []string{"buy milk", "fold laundry"}) {
		t.Errorf("reorder changed order while filtered: %v", got)
	}
	// Leaving the column clears its filter.
	m = press(m, "l")
	if m.cols[todo].hasFilter() {
		t.Error("blurring a column should clear its filter")
	}
}

func TestHelpToggleAndQuit(t *testing.T) {
	m, _ := newTestBoard(t)
	short := lipgloss.Height(m.footerView())
	m = press(m, "?")
	if !m.help.ShowAll {
		t.Error("? should expand help")
	}
	if full := lipgloss.Height(m.footerView()); full <= short {
		t.Errorf("full help height %d should exceed short help height %d", full, short)
	}
	if lines := strings.Split(m.View(), "\n"); len(lines) > testHeight {
		t.Errorf("view with full help has %d lines, terminal has %d", len(lines), testHeight)
	}

	mm, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q should return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q should quit")
	}
	if mm.(Board).View() != "" {
		t.Error("view should be blank while quitting")
	}
}

func TestSaveErrorIsShown(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "board.json")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newBoard(newStore(blocked), sampleTasks())
	mm, _ := m.Update(tea.WindowSizeMsg{Width: testWidth, Height: testHeight})
	m = press(mm.(Board), "L")
	if m.err == nil {
		t.Fatal("expected a save error")
	}
	if !strings.Contains(m.View(), "save failed") {
		t.Error("header should show the save error")
	}
}

func TestDemoModeDoesNotPersist(t *testing.T) {
	m := newBoard(store{}, sampleTasks())
	mm, _ := m.Update(tea.WindowSizeMsg{Width: testWidth, Height: testHeight})
	m = mm.(Board)
	if !strings.Contains(m.View(), "demo mode") {
		t.Error("header should mention demo mode")
	}
	m = press(m, "L")
	if m.err != nil {
		t.Errorf("demo mode returned an error: %v", m.err)
	}
	if got := titles(m.cols[inProgress]); !equalStrings(got, []string{"write code", "buy milk"}) {
		t.Errorf("In Progress after move = %v", got)
	}
}
