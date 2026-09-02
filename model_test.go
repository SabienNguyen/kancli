package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func assertFits(t *testing.T, m app, label string) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) > m.height {
		t.Errorf("%s: view has %d lines for a %d-row terminal", label, len(lines), m.height)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("%s: line %d is %d wide for a %d-column terminal", label, i, w, m.width)
		}
	}
}

func TestEveryScreenFitsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{60, 18}, {80, 24}, {120, 40}} {
		m, _ := newTestApp(t)
		mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = mm.(app)
		assertFits(t, m, "board")
		m = press(m, "?")
		assertFits(t, m, "full help")
		m = press(m, "?", "n")
		assertFits(t, m, "form")
		m = press(m, "esc", "enter")
		assertFits(t, m, "detail")
		m = press(m, "esc", "C")
		assertFits(t, m, "column form")
		m = press(m, "esc", "b")
		assertFits(t, m, "boards")
		m = press(m, "esc", "z")
		assertFits(t, m, "archive")
		m = press(m, "esc", "d")
		assertFits(t, m, "confirm")
		m = press(m, "esc", "/")
		m = typeText(m, "p:high")
		assertFits(t, m, "search")
		m = press(m, "enter")
		assertFits(t, m, "search applied")
		m = press(m, "esc", "space", "space")
		assertFits(t, m, "marks")
		m = press(m, "esc", "b", "n")
		assertFits(t, m, "prompt")
		m = press(m, "esc", "esc", "enter", "c")
		assertFits(t, m, "comment prompt")
	}
	m := newApp(config{}, styles{}, glyphs{}, newStore(""), sampleFile())
	if !strings.Contains(m.View(), "Loading") {
		t.Error("unsized app should show a loading message")
	}
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	if !strings.Contains(mm.(app).View(), "too small") {
		t.Error("tiny terminal should show the too-small message")
	}
}

func TestNavigationAndMarks(t *testing.T) {
	m, _ := newTestApp(t)
	m = press(m, "l")
	if m.focused != 1 {
		t.Errorf("focus = %d, want 1", m.focused)
	}
	m = press(m, "l", "l")
	if m.focused != 0 {
		t.Errorf("focus should wrap, got %d", m.focused)
	}
	m = press(m, "3")
	if m.focused != 2 {
		t.Errorf("jump focus = %d", m.focused)
	}
	m = press(m, "h", "j")
	if sel, _ := m.col().selected(); sel.ID != 6 {
		t.Errorf("after j selected = %d, want 6", sel.ID)
	}
	m = press(m, "1", "space", "space")
	if len(m.marks) != 2 || !m.marks[1] || !m.marks[2] {
		t.Errorf("marks = %v", m.marks)
	}
	if !strings.Contains(m.View(), "2 marked") {
		t.Error("header should count marks")
	}
	m = press(m, "esc")
	if len(m.marks) != 0 {
		t.Error("esc should clear marks")
	}
}

func TestMoveAndUndo(t *testing.T) {
	m, st := newTestApp(t)
	m = press(m, "L")
	if got := idsOf(m.board.TasksIn("in_progress")); !slices.Equal(got, []int{5, 6, 1}) {
		t.Errorf("In Progress = %v", got)
	}
	if !strings.Contains(m.statusMsg, "WIP limit") {
		t.Errorf("status = %q, want a WIP warning", m.statusMsg)
	}
	f, _ := st.load()
	if f.Active().Task(1).Column != "in_progress" {
		t.Error("move not persisted")
	}
	m = press(m, "u")
	if got := idsOf(m.board.TasksIn("todo")); !slices.Equal(got, []int{1, 2, 3, 4}) {
		t.Errorf("after undo To Do = %v", got)
	}
	m = press(m, "U")
	if m.board.Task(1).Column != "in_progress" {
		t.Error("redo failed")
	}
	// Bulk move with marks.
	m = press(m, "1", "space", "space", "L")
	if got := idsOf(m.board.TasksIn("todo")); !slices.Equal(got, []int{4}) {
		t.Errorf("after bulk move To Do = %v", got)
	}
	if len(m.marks) != 0 {
		t.Error("marks should clear after a bulk move")
	}
	// Moving left from the first column is refused with a message.
	m = press(m, "H")
	if got := m.board.Task(4).Column; got != "todo" {
		t.Errorf("task 4 moved to %s", got)
	}
	if m.statusMsg == "" {
		t.Error("expected a status message")
	}
}

func TestReorderRespectsSortAndSearch(t *testing.T) {
	m, _ := newTestApp(t)
	m = press(m, "J")
	if got := idsOf(m.board.TasksIn("todo")); !slices.Equal(got, []int{2, 1, 3, 4}) {
		t.Errorf("after J = %v", got)
	}
	if sel, _ := m.col().selected(); sel.ID != 1 {
		t.Errorf("cursor should follow the task, selected %d", sel.ID)
	}
	m = press(m, "K", "s")
	if m.sortMode != sortPriority {
		t.Errorf("sort = %v", m.sortMode)
	}
	if got := visibleTitles(m, 0); got[0] != "Write the release notes" {
		t.Errorf("priority sort order = %v", got)
	}
	m = press(m, "J")
	if !strings.Contains(m.statusMsg, "manual") {
		t.Errorf("reorder while sorted should be refused, status = %q", m.statusMsg)
	}
	if got := idsOf(m.board.TasksIn("todo")); !slices.Equal(got, []int{1, 2, 3, 4}) {
		t.Errorf("board order changed while sorted: %v", got)
	}
}

func TestSearchFiltersEveryColumn(t *testing.T) {
	m, _ := newTestApp(t)
	m = press(m, "/")
	if !m.searching {
		t.Fatal("/ should start a search")
	}
	m = typeText(m, "@sam")
	if got := visibleTitles(m, 0); !slices.Equal(got, []string{"Write the release notes"}) {
		t.Errorf("To Do matches = %v", got)
	}
	if got := visibleTitles(m, 1); !slices.Equal(got, []string{"Ship the CLI subcommands"}) {
		t.Errorf("In Progress matches = %v", got)
	}
	if len(visibleTitles(m, 2)) != 0 {
		t.Error("Done should have no matches")
	}
	m = press(m, "enter")
	if m.searching || m.query != "@sam" {
		t.Error("enter should keep the query")
	}
	// Actions work on the filtered selection.
	m = press(m, "L")
	if m.board.Task(1).Column != "in_progress" {
		t.Error("move on filtered selection failed")
	}
	m = press(m, "esc")
	if m.query != "" || len(visibleTitles(m, 0)) != 3 {
		t.Error("esc should clear the search")
	}
	// Typing board keys while searching is literal.
	m = press(m, "/")
	m = typeText(m, "d")
	if m.state != stateBoard || m.board.Task(2) == nil {
		t.Error("typing d in the search must not delete")
	}
}

func TestCreateEditTask(t *testing.T) {
	m, st := newTestApp(t)
	m = press(m, "l", "n")
	if m.state != stateForm {
		t.Fatal("n should open the form")
	}
	m = typeText(m, "Ship it")
	m = press(m, "enter")
	m = typeText(m, "line one")
	m = press(m, "enter")
	m = typeText(m, "line two")
	m = press(m, "tab", "right", "right", "right") // priority: high
	m = press(m, "tab")
	m = typeText(m, "+2d")
	m = press(m, "tab")
	m = typeText(m, "Ops, Docs")
	m = press(m, "tab")
	m = typeText(m, "sam")
	m = press(m, "ctrl+s")
	if m.state != stateBoard {
		t.Fatalf("ctrl+s should close the form, err=%q", m.form.err)
	}
	got := m.board.TasksIn("in_progress")
	created := got[len(got)-1]
	if created.Title != "Ship it" || created.Description != "line one\nline two" || created.Priority != priorityHigh ||
		created.Due != today().AddDate(0, 0, 2).Format(dateLayout) || !slices.Equal(created.Labels, []string{"docs", "ops"}) || created.Assignee != "sam" {
		t.Errorf("created = %+v", created)
	}
	if sel, _ := m.col().selected(); sel.ID != created.ID {
		t.Error("new task should be selected")
	}
	f, _ := st.load()
	if f.Active().Task(created.ID) == nil {
		t.Error("new task not persisted")
	}

	// Validation.
	m = press(m, "n", "ctrl+s")
	if m.state != stateForm || !strings.Contains(m.form.err, "title") {
		t.Errorf("empty title should be rejected: %q", m.form.err)
	}
	m = typeText(m, "x")
	m = press(m, "tab", "tab", "tab")
	m = typeText(m, "someday")
	m = press(m, "ctrl+s")
	if m.state != stateForm || !strings.Contains(m.form.err, "date") {
		t.Errorf("bad date should be rejected: %q", m.form.err)
	}
	m = press(m, "esc")
	if m.state != stateBoard {
		t.Error("esc should cancel")
	}

	// Edit keeps the id and records history.
	m = press(m, "1", "e")
	if m.form.title.Value() != "Write the release notes" {
		t.Errorf("edit form not prefilled: %q", m.form.title.Value())
	}
	m = typeText(m, " now")
	m = press(m, "ctrl+s")
	if got := m.board.Task(1); got.Title != "Write the release notes now" || !strings.Contains(got.History[len(got.History)-1].Text, "renamed") {
		t.Errorf("edit = %+v", got)
	}
}

func TestDeleteArchiveAndArchiveView(t *testing.T) {
	m, _ := newTestApp(t)
	m = press(m, "d")
	if m.state != stateConfirm || !strings.Contains(m.View(), "Write the release notes") {
		t.Fatal("d should confirm with the task title")
	}
	m = press(m, "n")
	if m.state != stateBoard || m.board.Task(1) == nil {
		t.Fatal("n should cancel")
	}
	m = press(m, "d", "y")
	if m.board.Task(1) != nil {
		t.Error("task not deleted")
	}
	m = press(m, "u")
	if m.board.Task(1) == nil {
		t.Error("undo should bring the task back")
	}
	m = press(m, "a")
	if !m.board.Task(1).Archived() {
		t.Error("a should archive")
	}
	m = press(m, "z")
	if m.state != statePicker || !strings.Contains(m.View(), "#1 Write the release notes") {
		t.Fatal("z should list archived tasks")
	}
	m = press(m, "enter")
	if m.board.Task(1).Archived() {
		t.Error("enter should restore")
	}
	m = press(m, "esc", "3", "Z", "y")
	if m.board.CountIn("done") != 0 || len(m.board.ArchivedTasks()) != 2 {
		t.Error("Z should archive every done task")
	}
	m = press(m, "z", "d", "y")
	if len(m.board.ArchivedTasks()) != 1 {
		t.Error("d in the archive should delete permanently")
	}
}

func TestDetailView(t *testing.T) {
	m, _ := newTestApp(t)
	m = press(m, "enter")
	if m.state != stateDetail {
		t.Fatal("enter should open the detail view")
	}
	v := m.View()
	for _, want := range []string{"#1 Write the release notes", "Checklist 1/3", "Draft", "github.com", "Activity"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail view missing %q", want)
		}
	}
	m = press(m, "tab", "tab", "space") // second checklist item
	if !m.board.Task(1).Checklist[1].Done {
		t.Error("space should toggle the item under the cursor")
	}
	m = press(m, "t")
	m = typeText(m, "Ship")
	m = press(m, "enter")
	if len(m.board.Task(1).Checklist) != 4 || m.state != stateDetail {
		t.Error("t should add a checklist item and return to the detail view")
	}
	m = press(m, "X")
	if len(m.board.Task(1).Checklist) != 3 {
		t.Error("X should remove the new item")
	}
	m = press(m, "c")
	m = typeText(m, "looks good")
	m = press(m, "enter")
	if got := m.board.Task(1).Comments; len(got) != 1 || got[0].Text != "looks good" {
		t.Errorf("comments = %v", got)
	}
	m = press(m, "A")
	m = typeText(m, "notes.txt")
	m = press(m, "enter")
	if got := m.board.Task(1).Attachments; len(got) != 2 {
		t.Errorf("attachments = %v", got)
	}
	m = press(m, "L")
	if m.board.Task(1).Column != "in_progress" || m.focused != 1 {
		t.Error("L in the detail view should move the task and follow it")
	}
	if !strings.Contains(m.statusMsg, "Moved #1 to In Progress") {
		t.Errorf("status = %q", m.statusMsg)
	}
	m = press(m, "e")
	if m.state != stateForm || m.form.mode != formEdit {
		t.Fatal("e should open the editor")
	}
	m = press(m, "esc")
	if m.state != stateDetail {
		t.Error("cancelling the editor should return to the detail view")
	}
	m = press(m, "d", "y")
	if m.state != stateBoard || m.board.Task(1) != nil {
		t.Error("delete from the detail view should return to the board")
	}
}

func TestColumnsAndBoards(t *testing.T) {
	m, _ := newTestApp(t)
	m = press(m, "C")
	m = typeText(m, "Review")
	m = press(m, "tab", "right", "tab")
	m = typeText(m, "2")
	m = press(m, "ctrl+s")
	if len(m.board.Columns) != 4 || m.board.Columns[3].Name != "Review" || m.board.Columns[3].WIPLimit != 2 {
		t.Fatalf("columns = %+v", m.board.Columns)
	}
	if m.focused != 3 || len(m.cols) != 4 {
		t.Errorf("new column should be focused: focused=%d cols=%d", m.focused, len(m.cols))
	}
	m = press(m, "<")
	if m.board.Columns[2].Name != "Review" || m.focused != 2 {
		t.Error("< should move the column left")
	}
	m = press(m, "E")
	m = press(m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	m = typeText(m, "QA")
	m = press(m, "ctrl+s")
	if m.board.Columns[2].Name != "QA" {
		t.Errorf("rename failed: %+v", m.board.Columns[2])
	}
	// Deleting a column keeps its archived tasks.
	m = press(m, "2", "j", "a", "1")
	m = press(m, "2", "D")
	if !strings.Contains(m.View(), "(1 archived)") {
		t.Error("confirm dialog should mention archived tasks")
	}
	m = press(m, "y")
	if len(m.board.ArchivedTasks()) != 1 || m.board.ArchivedTasks()[0].Column != "todo" {
		t.Errorf("archived task lost on column delete: %+v", m.board.ArchivedTasks())
	}
	m = press(m, "u", "u")
	m = press(m, "2", "D", "y")
	if len(m.board.Columns) != 3 || m.board.CountIn("todo") != 6 {
		t.Errorf("deleting In Progress should move its tasks to To Do: %v", idsOf(m.board.TasksIn("todo")))
	}
	m = press(m, "u")
	if len(m.board.Columns) != 4 || len(m.cols) != 4 {
		t.Error("undo should restore the column")
	}

	m = press(m, "b")
	if m.state != statePicker {
		t.Fatal("b should open the board picker")
	}
	m = press(m, "n")
	m = typeText(m, "Work")
	m = press(m, "enter")
	if m.board.Name != "Work" || m.state != stateBoard || len(m.board.Tasks) != 0 {
		t.Errorf("new board not opened: %s", m.board.Name)
	}
	m = press(m, "b", "j", "enter")
	if m.board.Name != "Work" {
		m = press(m, "b", "k", "enter")
	}
	m = press(m, "b")
	m = press(m, "r")
	m = press(m, "backspace", "backspace", "backspace", "backspace")
	m = typeText(m, "Office")
	m = press(m, "enter")
	if m.file.Board("Office") == nil {
		t.Error("rename failed")
	}
	m = press(m, "d", "y")
	if len(m.file.Boards) != 1 || m.board.Name != "Demo" {
		t.Errorf("delete board failed: %d boards, on %s", len(m.file.Boards), m.board.Name)
	}
}

func TestExternalChangesAreMerged(t *testing.T) {
	m, st := newTestApp(t)
	// Another process appends an event.
	other := newStore(st.path)
	other.actor = "cli"
	f, err := other.load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Active().AddTask(Task{Title: "From elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if err := other.save(f); err != nil {
		t.Fatal(err)
	}

	// A poll picks it up.
	if !st.changedOnDisk() {
		t.Fatal("appended events should be detected")
	}
	mm, _ := m.Update(pollMsg{})
	m = mm.(app)
	if m.board.Task(9) == nil || m.board.Task(9).Title != "From elsewhere" {
		t.Fatal("poll should have replayed the external event")
	}

	// Another external event followed by a local change: both survive.
	f.Active().AddTask(Task{Title: "Another"}) //nolint:errcheck // test data
	if err := other.save(f); err != nil {
		t.Fatal(err)
	}
	m = press(m, "L")
	if m.err != nil {
		t.Fatalf("local change failed: %v", m.err)
	}
	if m.board.Task(10) == nil || m.board.Task(1).Column != "in_progress" {
		t.Errorf("merge lost a change: external=%v local=%v", m.board.Task(10) != nil, m.board.Task(1).Column)
	}
	// The other process sees the local move after reloading.
	f, err = other.load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Active().Task(1).Column != "in_progress" {
		t.Error("the other process should replay the local move")
	}

	// ctrl+s writes a snapshot and archives the tail.
	m = press(m, "ctrl+s")
	if m.err != nil || st.tailEvents() != 0 {
		t.Errorf("compact: err=%v tail=%d", m.err, st.tailEvents())
	}
	if segs, _ := filepath.Glob(filepath.Join(st.archiveDir, "*.jsonl")); len(segs) == 0 {
		t.Error("compaction should archive the tail log")
	}
	f, _ = newStore(st.path).load()
	if f.Active().Task(1).Column != "in_progress" || f.Active().Task(10) == nil {
		t.Error("snapshot should carry the merged state")
	}
}

func TestMouse(t *testing.T) {
	m, _ := newTestApp(t)
	colW := m.cols[0].width
	// Click the second card in the second column.
	headerH := lipgloss.Height(m.headerView())
	y := headerH + 3 + m.cols[1].rows // top border + title + padding, then one card down
	mm, _ := m.Update(tea.MouseMsg{X: colW + 2, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = mm.(app)
	if m.focused != 1 {
		t.Fatalf("click should focus column 1, got %d", m.focused)
	}
	if sel, _ := m.col().selected(); sel.ID != 6 {
		t.Errorf("click should select task 6, got %d", sel.ID)
	}
	mm, _ = m.Update(tea.MouseMsg{X: colW + 2, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = mm.(app)
	if m.state != stateDetail {
		t.Error("clicking the selected card should open it")
	}
	m = press(m, "esc")
	mm, _ = m.Update(tea.MouseMsg{X: 2, Y: y, Button: tea.MouseButtonWheelDown})
	m = mm.(app)
	if sel, _ := m.cols[0].selected(); sel.ID != 2 {
		t.Errorf("wheel should scroll the column under the pointer, selected %d", sel.ID)
	}
}

func TestQuitAndHelp(t *testing.T) {
	m, _ := newTestApp(t)
	m = press(m, "?")
	if !m.help.ShowAll {
		t.Error("? should expand help")
	}
	mm, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || mm.(app).View() != "" {
		t.Error("q should return QuitMsg and blank the view")
	}
	m = press(m, "n")
	_, cmd = m.Update(keyMsg("ctrl+c"))
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c should quit from inside the form")
	}
}

func TestDemoModeAndThemes(t *testing.T) {
	for _, name := range themeNames {
		th, _ := themeByName(name, name == "mono")
		m := newApp(config{Compact: true}, newStyles(th, name == "mono"), newGlyphs(name == "mono"), newStore(""), sampleFile())
		mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m = mm.(app)
		assertFits(t, m, name)
		if !strings.Contains(m.View(), "demo mode") {
			t.Errorf("%s: header should mention demo mode", name)
		}
		m = press(m, "L")
		if m.err != nil {
			t.Errorf("%s: demo mode returned an error: %v", name, m.err)
		}
	}
}

func TestConfiguredCursorKeys(t *testing.T) {
	st := newStore("")
	s, g := testStyles()
	m := newApp(config{Keys: map[string][]string{"down": {"w"}, "up": {"e"}, "edit": {"E"}}}, s, g, st, sampleFile())
	mm, _ := m.Update(tea.WindowSizeMsg{Width: testWidth, Height: testHeight})
	m = mm.(app)
	m = press(m, "w")
	if sel, _ := m.col().selected(); sel.ID != 2 {
		t.Errorf("configured down key did not move the cursor, selected %d", sel.ID)
	}
	m = press(m, "j")
	if sel, _ := m.col().selected(); sel.ID != 2 {
		t.Errorf("default down key should be replaced, selected %d", sel.ID)
	}
	m = press(m, "e")
	if sel, _ := m.col().selected(); sel.ID != 1 || m.state != stateBoard {
		t.Errorf("configured up key failed: selected %d state %v", sel.ID, m.state)
	}
}

func TestStatsScreenAndRelevance(t *testing.T) {
	s, g := testStyles()
	st := newStore("")
	f := demoFile()
	if err := st.save(f); err != nil {
		t.Fatal(err)
	}
	m := newApp(config{}, s, g, st, f)
	for _, size := range [][2]int{{60, 18}, {110, 40}} {
		mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		a := press(mm.(app), "S")
		if a.state != stateStats {
			t.Fatal("S should open the stats screen")
		}
		assertFits(t, a, "stats")
		if size[1] >= 40 {
			v := a.View()
			for _, want := range []string{"Stats", "finished", "Finished per week", "Work in progress", "Mean time in column"} {
				if !strings.Contains(v, want) {
					t.Errorf("stats view missing %q", want)
				}
			}
		}
		a = press(a, "w")
		if a.stats.days != 365 {
			t.Errorf("w should cycle the window, got %d", a.stats.days)
		}
		a = press(a, "j", "esc")
		if a.state != stateBoard {
			t.Error("esc should return to the board")
		}
	}

	// Free-text search ranks by relevance: the title hit comes first.
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	a := press(mm.(app), "/")
	a = typeText(a, "release")
	if got := visibleTitles(a, 0); len(got) == 0 || got[0] != "Write the release notes" {
		t.Errorf("relevance order = %v", got)
	}
	if !strings.Contains(a.View(), "match") {
		t.Error("search bar should show the match count")
	}
	a = press(a, "esc")

	// Creating a near-duplicate warns.
	a = press(a, "n")
	a = typeText(a, "Write release notes")
	a = press(a, "ctrl+s")
	if !strings.Contains(a.statusMsg, "Similar to #1") {
		t.Errorf("status = %q", a.statusMsg)
	}
	a = press(a, "enter")
	if !strings.Contains(a.View(), "Similar tasks") {
		t.Error("detail view should list similar tasks")
	}
}

func TestReadOnlyAsOfView(t *testing.T) {
	m, st := newTestApp(t)
	m = press(m, "L", "ctrl+s")
	if m.err != nil {
		t.Fatal(m.err)
	}
	past, err := st.loadAsOf(timeNow().Add(-10 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	s, g := testStyles()
	ro := newApp(config{}, s, g, newStore(st.path), past)
	ro.readOnly, ro.asOf = true, timeNow().Add(-10*24*time.Hour)
	mm, _ := ro.Update(tea.WindowSizeMsg{Width: testWidth, Height: testHeight})
	ro = mm.(app)
	if !strings.Contains(ro.View(), "read-only") {
		t.Error("header should show the read-only banner")
	}
	before := stateOf(ro.file)
	for _, k := range []string{"L", "d", "n", "a", "u", "C", "D", "Z", "ctrl+s"} {
		ro = press(ro, k)
		if ro.state != stateBoard {
			ro = press(ro, "esc")
		}
	}
	if stateOf(ro.file) != before {
		t.Error("read-only view was mutated")
	}
	if !strings.Contains(ro.statusMsg, "Read-only") {
		t.Errorf("status = %q", ro.statusMsg)
	}
	ro = press(ro, "enter", "c")
	if ro.state != stateDetail || !strings.Contains(ro.statusMsg, "Read-only") {
		t.Error("detail view mutations should be blocked")
	}
	if tail := newStore(st.path); tail.enabled() {
		f2, _ := tail.load()
		if f2.Active().Task(1).Column != "in_progress" {
			t.Error("the live board should be untouched by the read-only session")
		}
	}
}
