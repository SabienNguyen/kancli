package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// viewState says which screen is in front of the user.
type viewState int

const (
	stateBoard viewState = iota
	stateForm
	stateColumnForm
	stateDetail
	stateConfirm
	statePrompt
	statePicker
)

const (
	minWidth     = 60
	minHeight    = 18
	maxUndo      = 100
	pollInterval = 2 * time.Second
)

// statusDuration is how long transient header messages stay visible.
var statusDuration = 4 * time.Second

// clearStatusMsg hides a transient status message once it has expired.
type clearStatusMsg struct{ id int }

// pollMsg asks the app to check the data file for external changes.
type pollMsg struct{}

// app is the root Bubble Tea model.
type app struct {
	cfg   config
	st    styles
	g     glyphs
	store *store
	file  *File
	board *Board

	cols    []column
	focused int
	state   viewState
	back    viewState // screen to return to after a dialog

	form    taskForm
	colForm columnForm
	detail  detailView
	confirm confirmDialog
	prompt  prompt
	pick    picker

	searching bool
	search    textinput.Model
	query     string
	sortMode  sortMode
	marks     map[int]bool
	compact   bool

	undoStack []undoEntry
	redoStack []undoEntry

	keys keyMap
	help help.Model

	width  int
	height int
	ready  bool

	statusMsg string
	statusID  int
	err       error
	dirty     bool
	quitting  bool
}

// newApp builds the root model for a loaded data file.
func newApp(cfg config, st styles, g glyphs, store *store, file *File) app {
	keys := defaultKeyMap()
	keys.applyKeyOverrides(cfg.Keys) //nolint:errcheck // validated by loadConfig
	search := textinput.New()
	search.Prompt = "/ "
	search.PromptStyle = st.searchPrompt
	search.Placeholder = "title, #12, @who, +label, p:high, due:today, col:done"
	search.PlaceholderStyle = st.muted
	search.CharLimit = 200

	m := app{
		cfg:     cfg,
		st:      st,
		g:       g,
		store:   store,
		file:    file,
		keys:    keys,
		help:    help.New(),
		search:  search,
		marks:   map[int]bool{},
		compact: cfg.Compact,
	}
	if s, ok := parseSortMode(cfg.Sort); ok {
		m.sortMode = s
	}
	if cfg.Board != "" {
		if b := file.Board(cfg.Board); b != nil {
			file.ActiveBoard = b.ID
		}
	}
	m.board = file.Active()
	m.rebuildColumns()
	m.refresh()
	return m
}

// Init implements tea.Model.
func (m app) Init() tea.Cmd {
	if !m.store.enabled() {
		return nil
	}
	return pollTick()
}

func pollTick() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return pollMsg{} })
}

// --- state helpers --------------------------------------------------------

// rebuildColumns recreates the UI columns from the board's column list,
// keeping cursor positions for columns that still exist.
func (m *app) rebuildColumns() {
	old := map[string]column{}
	for _, c := range m.cols {
		old[c.id] = c
	}
	m.cols = make([]column, 0, len(m.board.Columns))
	for _, c := range m.board.Columns {
		col, ok := old[c.ID]
		if !ok {
			col = newColumn(c.ID, m.st, m.keys)
		}
		m.cols = append(m.cols, col)
	}
	if m.focused >= len(m.cols) {
		m.focused = max(0, len(m.cols)-1)
	}
	for i := range m.cols {
		m.cols[i].setFocus(i == m.focused)
	}
	m.layout()
}

// refresh pushes filtered, sorted cards into every column.
func (m *app) refresh() tea.Cmd {
	q := parseQuery(m.query)
	now := timeNow()
	var cmds []tea.Cmd
	for i := range m.cols {
		col := m.board.Columns[i]
		tasks := m.board.TasksIn(col.ID)
		if !q.empty() {
			kept := tasks[:0]
			for _, t := range tasks {
				if q.matches(m.board, t, now) {
					kept = append(kept, t)
				}
			}
			tasks = kept
		}
		sortTasks(tasks, m.sortMode)
		d := cardDelegate{st: m.st, g: m.g, compact: m.compact, marks: m.marks, now: now}
		m.cols[i].configure(col, m.board.CountIn(col.ID), m.board.WIPExceeded(col.ID), d)
		cmds = append(cmds, m.cols[i].setTasks(tasks))
	}
	// Drop marks for tasks that no longer exist.
	for id := range m.marks {
		if t := m.board.Task(id); t == nil || t.Archived() {
			delete(m.marks, id)
		}
	}
	return tea.Batch(cmds...)
}

// focusColumn moves keyboard focus to another column.
func (m *app) focusColumn(i int) {
	if i < 0 || i >= len(m.cols) || i == m.focused {
		return
	}
	m.cols[m.focused].setFocus(false)
	m.focused = i
	m.cols[m.focused].setFocus(true)
	m.refresh()
}

func (m *app) col() *column {
	return &m.cols[m.focused]
}

// undoEntry is a snapshot of the board plus where the cursor was.
type undoEntry struct {
	board    []byte
	focused  int
	selected int
}

func (m *app) entry() (undoEntry, bool) {
	data, err := json.Marshal(m.board)
	if err != nil {
		return undoEntry{}, false
	}
	e := undoEntry{board: data, focused: m.focused}
	if len(m.cols) > 0 {
		e.selected = m.col().selectedID
	}
	return e, true
}

// snapshot records the board for undo and clears the redo stack.
func (m *app) snapshot() {
	e, ok := m.entry()
	if !ok {
		return
	}
	m.undoStack = append(m.undoStack, e)
	if len(m.undoStack) > maxUndo {
		m.undoStack = m.undoStack[1:]
	}
	m.redoStack = nil
}

// dropSnapshot discards the most recent snapshot after a no-op.
func (m *app) dropSnapshot() {
	if len(m.undoStack) > 0 {
		m.undoStack = m.undoStack[:len(m.undoStack)-1]
	}
}

func (m *app) restore(e undoEntry) bool {
	var b Board
	if err := json.Unmarshal(e.board, &b); err != nil {
		return false
	}
	*m.board = b
	m.rebuildColumns()
	if e.focused < len(m.cols) {
		m.focusColumn(e.focused)
		m.col().selectedID = e.selected
	}
	return true
}

// persist writes the file unless it has changed on disk behind our back.
func (m *app) persist(force bool) {
	if !force && m.store.changedOnDisk() {
		m.dirty = true
		m.err = fmt.Errorf("file changed on disk: R reloads (discarding your change), ctrl+s overwrites")
		return
	}
	m.err = m.store.save(m.file)
	m.dirty = m.err != nil
}

// changed is called after every mutation: refresh the view, save, and show
// a status message.
func (m *app) changed(format string, args ...any) tea.Cmd {
	cmd := m.refresh()
	m.persist(false)
	if format == "" {
		return cmd
	}
	return tea.Batch(cmd, m.setStatus(format, args...))
}

// setStatus shows a message in the header for a few seconds.
func (m *app) setStatus(format string, args ...any) tea.Cmd {
	m.statusMsg = fmt.Sprintf(format, args...)
	m.statusID++
	id := m.statusID
	return tea.Tick(statusDuration, func(time.Time) tea.Msg {
		return clearStatusMsg{id: id}
	})
}

// reload re-reads the data file, discarding in-memory changes.
func (m *app) reload() error {
	f, err := m.store.load()
	if err != nil {
		return err
	}
	current := m.board.ID
	m.file = f
	if b := f.Board(current); b != nil {
		f.ActiveBoard = b.ID
	}
	m.board = f.Active()
	m.undoStack, m.redoStack = nil, nil
	m.dirty = false
	m.err = nil
	m.rebuildColumns()
	m.refresh()
	return nil
}

// switchBoard makes another board active.
func (m *app) switchBoard(id string) tea.Cmd {
	b := m.file.Board(id)
	if b == nil {
		return nil
	}
	m.file.ActiveBoard = b.ID
	m.board = b
	m.focused = 0
	m.cols = nil
	m.marks = map[int]bool{}
	m.undoStack, m.redoStack = nil, nil
	m.rebuildColumns()
	m.persist(false)
	return tea.Batch(m.refresh(), m.setStatus("Opened board %q", b.Name))
}

// targets returns the marked tasks, or the selected one when nothing is
// marked.
func (m *app) targets() []int {
	if len(m.marks) > 0 {
		var ids []int
		for _, t := range m.board.Live() {
			if m.marks[t.ID] {
				ids = append(ids, t.ID)
			}
		}
		return ids
	}
	if t, ok := m.col().selected(); ok {
		return []int{t.ID}
	}
	return nil
}

func (m *app) taskTitle(id int) string {
	if t := m.board.Task(id); t != nil {
		return t.Title
	}
	return fmt.Sprintf("#%d", id)
}

func describeTargets(m *app, ids []int) string {
	if len(ids) == 1 {
		return fmt.Sprintf("%q", m.taskTitle(ids[0]))
	}
	return fmt.Sprintf("%d tasks", len(ids))
}

// --- layout ---------------------------------------------------------------

func (m *app) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.help.Width = m.width - m.st.help.GetHorizontalFrameSize() - 2
	headerH := lipgloss.Height(m.headerView())
	footerH := lipgloss.Height(m.footerView())
	colH := max(0, m.height-headerH-footerH)

	n := len(m.cols)
	if n == 0 {
		return
	}
	base := m.width / n
	extra := m.width % n
	for i := range m.cols {
		w := base
		if i < extra {
			w++
		}
		m.cols[i].setSize(w, colH)
	}
	switch m.state {
	case stateForm:
		m.form.setSize(m.width, m.height)
	case stateDetail:
		m.detail.setSize(m.width, m.height)
		m.renderDetail()
	case statePicker:
		m.pick.setSize(m.width, m.pickerHeight())
	case statePrompt:
		m.prompt.setSize(m.width)
	}
}

// pickerHeight is the room left for a full-screen list under the header.
func (m app) pickerHeight() int {
	return m.height - lipgloss.Height(m.headerView())
}

// columnAt maps an x coordinate to a column index.
func (m *app) columnAt(x int) int {
	for i := range m.cols {
		if x < m.cols[i].width {
			return i
		}
		x -= m.cols[i].width
	}
	return -1
}

// --- update ---------------------------------------------------------------

// Update implements tea.Model.
func (m app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.layout()
		return m, nil

	case clearStatusMsg:
		if msg.id == m.statusID {
			m.statusMsg = ""
		}
		return m, nil

	case pollMsg:
		if m.store.changedOnDisk() && !m.dirty && m.state == stateBoard {
			if err := m.reload(); err != nil {
				m.err = err
			} else {
				return m, tea.Batch(pollTick(), m.setStatus("Reloaded: file changed on disk"))
			}
		}
		return m, pollTick()

	case formSubmitMsg:
		return m.handleFormSubmit(msg)

	case colFormSubmitMsg:
		return m.handleColumnFormSubmit(msg)

	case promptSubmitMsg:
		return m.handlePromptSubmit(msg)

	case formCancelMsg:
		m.state = m.back
		if m.state == stateDetail {
			m.renderDetail()
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		switch m.state {
		case stateForm:
			if msg.Type == tea.KeyCtrlC {
				return m.quit()
			}
			var cmd tea.Cmd
			m.form, cmd = m.form.Update(msg)
			return m, cmd
		case stateColumnForm:
			if msg.Type == tea.KeyCtrlC {
				return m.quit()
			}
			var cmd tea.Cmd
			m.colForm, cmd = m.colForm.Update(msg)
			return m, cmd
		case statePrompt:
			if msg.Type == tea.KeyCtrlC {
				return m.quit()
			}
			var cmd tea.Cmd
			m.prompt, cmd = m.prompt.Update(msg)
			return m, cmd
		case stateConfirm:
			return m.handleConfirmKey(msg)
		case stateDetail:
			return m.handleDetailKey(msg)
		case statePicker:
			return m.handlePickerKey(msg)
		default:
			if m.searching {
				return m.handleSearchKey(msg)
			}
			return m.handleBoardKey(msg)
		}
	}

	// Everything else (cursor blinks, list internals) belongs to the
	// active screen.
	var cmd tea.Cmd
	switch m.state {
	case stateForm:
		m.form, cmd = m.form.Update(msg)
	case stateColumnForm:
		m.colForm, cmd = m.colForm.Update(msg)
	case statePrompt:
		m.prompt, cmd = m.prompt.Update(msg)
	case stateDetail:
		m.detail, cmd = m.detail.update(msg)
	case statePicker:
		m.pick, cmd = m.pick.update(msg)
	default:
		if m.searching {
			m.search, cmd = m.search.Update(msg)
		} else if len(m.cols) > 0 {
			cmd = m.col().update(msg)
		}
	}
	return m, cmd
}

func (m app) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	return m, tea.Quit
}

func (m app) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m.quit()
	case tea.KeyEsc:
		m.searching = false
		m.query = ""
		m.search.SetValue("")
		m.search.Blur()
		m.layout()
		return m, m.refresh()
	case tea.KeyEnter:
		m.searching = false
		m.search.Blur()
		m.layout()
		return m, nil
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if v := m.search.Value(); v != m.query {
		m.query = v
		return m, tea.Batch(cmd, m.refresh())
	}
	return m, cmd
}

func (m app) handleBoardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Quit):
		return m.quit()

	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layout()
		return m, nil

	case key.Matches(msg, k.Back):
		if len(m.marks) > 0 {
			m.marks = map[int]bool{}
			return m, m.refresh()
		}
		if m.query != "" {
			m.query = ""
			m.search.SetValue("")
			m.layout()
			return m, m.refresh()
		}
		return m, nil

	case key.Matches(msg, k.Left):
		m.focusColumn((m.focused + len(m.cols) - 1) % max(1, len(m.cols)))
		return m, nil

	case key.Matches(msg, k.Right):
		m.focusColumn((m.focused + 1) % max(1, len(m.cols)))
		return m, nil

	case key.Matches(msg, k.Jump):
		if len(msg.Runes) == 1 {
			m.focusColumn(int(msg.Runes[0] - '1'))
		}
		return m, nil

	case key.Matches(msg, k.Search):
		m.searching = true
		m.search.SetValue(m.query)
		m.search.CursorEnd()
		m.layout()
		return m, m.search.Focus()

	case key.Matches(msg, k.Sort):
		m.sortMode = m.sortMode.next()
		return m, tea.Batch(m.refresh(), m.setStatus("Sorted by %s", m.sortMode))

	case key.Matches(msg, k.New):
		if len(m.board.Columns) == 0 {
			return m, m.setStatus("Add a column first (C)")
		}
		col := m.board.Columns[m.focused]
		m.form = newTaskForm(formCreate, Task{Column: col.ID}, col.Name, m.st, m.g)
		m.form.setSize(m.width, m.height)
		m.back = stateBoard
		m.state = stateForm
		return m, m.form.Init()

	case key.Matches(msg, k.Edit):
		t, ok := m.col().selected()
		if !ok {
			return m, nil
		}
		return m.openEditor(t, stateBoard)

	case key.Matches(msg, k.View):
		t, ok := m.col().selected()
		if !ok {
			return m, nil
		}
		return m.openDetail(t.ID)

	case key.Matches(msg, k.Mark):
		t, ok := m.col().selected()
		if !ok {
			return m, nil
		}
		if m.marks[t.ID] {
			delete(m.marks, t.ID)
		} else {
			m.marks[t.ID] = true
		}
		m.col().scroll(1)
		return m, m.refresh()

	case key.Matches(msg, k.Delete):
		return m.askDeleteTasks(m.targets(), stateBoard)

	case key.Matches(msg, k.Archive):
		ids := m.targets()
		if len(ids) == 0 {
			return m, nil
		}
		m.snapshot()
		for _, id := range ids {
			m.board.ArchiveTask(id)
		}
		m.marks = map[int]bool{}
		return m, m.changed("Archived %s", describeTargets(&m, ids))

	case key.Matches(msg, k.MoveLeft):
		return m.moveTargets(-1)

	case key.Matches(msg, k.MoveRight):
		return m.moveTargets(1)

	case key.Matches(msg, k.MoveUp):
		return m.reorderSelected(-1)

	case key.Matches(msg, k.MoveDown):
		return m.reorderSelected(1)

	case key.Matches(msg, k.Undo):
		if len(m.undoStack) == 0 {
			return m, m.setStatus("Nothing to undo")
		}
		cur, _ := m.entry()
		e := m.undoStack[len(m.undoStack)-1]
		m.dropSnapshot()
		if m.restore(e) {
			m.redoStack = append(m.redoStack, cur)
			cmd := m.refresh()
			m.persist(false)
			return m, tea.Batch(cmd, m.setStatus("Undid last change (%d left)", len(m.undoStack)))
		}
		return m, nil

	case key.Matches(msg, k.Redo):
		if len(m.redoStack) == 0 {
			return m, m.setStatus("Nothing to redo")
		}
		cur, _ := m.entry()
		e := m.redoStack[len(m.redoStack)-1]
		m.redoStack = m.redoStack[:len(m.redoStack)-1]
		if m.restore(e) {
			m.undoStack = append(m.undoStack, cur)
			cmd := m.refresh()
			m.persist(false)
			return m, tea.Batch(cmd, m.setStatus("Redid change"))
		}
		return m, nil

	case key.Matches(msg, k.Boards):
		m.openBoardPicker(m.board.ID)
		return m, nil

	case key.Matches(msg, k.ArchiveView):
		m.pick = newPicker(pickerArchive, "Archived tasks", archiveItems(m.board, m.g), m.st)
		m.pick.setSize(m.width, m.pickerHeight())
		m.state = statePicker
		return m, nil

	case key.Matches(msg, k.ArchiveDone):
		done := m.board.DoneColumn()
		if done == nil || m.board.CountIn(done.ID) == 0 {
			return m, m.setStatus("Nothing to archive")
		}
		n := m.board.CountIn(done.ID)
		m.confirm = newConfirm(confirmArchiveDone, "Archive all done tasks?", m.st,
			fmt.Sprintf("%d task%s in %s will be moved to the archive.", n, plural(n), done.Name))
		m.back = stateBoard
		m.state = stateConfirm
		return m, nil

	case key.Matches(msg, k.Reload):
		if m.dirty {
			m.confirm = newConfirm(confirmReload, "Reload from disk?", m.st,
				"Your unsaved change will be lost.")
			m.back = stateBoard
			m.state = stateConfirm
			return m, nil
		}
		if err := m.reload(); err != nil {
			m.err = err
			return m, nil
		}
		return m, m.setStatus("Reloaded %s", m.store.describe())

	case key.Matches(msg, k.Save):
		m.persist(true)
		if m.err != nil {
			return m, nil
		}
		return m, m.setStatus("Saved %s", m.store.describe())

	case key.Matches(msg, k.AddColumn):
		m.colForm = newColumnForm(nil, m.st)
		m.back = stateBoard
		m.state = stateColumnForm
		return m, m.colForm.Init()

	case key.Matches(msg, k.EditColumn):
		if len(m.board.Columns) == 0 {
			return m, nil
		}
		m.colForm = newColumnForm(&m.board.Columns[m.focused], m.st)
		m.back = stateBoard
		m.state = stateColumnForm
		return m, m.colForm.Init()

	case key.Matches(msg, k.DeleteColumn):
		if len(m.board.Columns) <= 1 {
			return m, m.setStatus("Cannot delete the only column")
		}
		col := m.board.Columns[m.focused]
		n := m.board.AllIn(col.ID)
		lines := []string{m.st.strong.Render(col.Name)}
		if n > 0 {
			neighbour := m.board.Columns[neighbourIndex(m.focused, len(m.board.Columns))]
			archived := n - m.board.CountIn(col.ID)
			msg := fmt.Sprintf("%d task%s will move to %s.", n, plural(n), neighbour.Name)
			if archived > 0 {
				msg = fmt.Sprintf("%d task%s (%d archived) will move to %s.", n, plural(n), archived, neighbour.Name)
			}
			lines = append(lines, msg)
		}
		m.confirm = newConfirm(confirmDeleteColumn, "Delete column?", m.st, lines...)
		m.confirm.sref = col.ID
		m.back = stateBoard
		m.state = stateConfirm
		return m, nil

	case key.Matches(msg, k.ColLeft):
		return m.moveColumn(-1)

	case key.Matches(msg, k.ColRight):
		return m.moveColumn(1)
	}

	if len(m.cols) == 0 {
		return m, nil
	}
	return m, m.col().update(msg)
}

// openBoardPicker shows the board list with the given board selected.
func (m *app) openBoardPicker(selectID string) {
	m.pick = newPicker(pickerBoards, "Boards", boardItems(m.file), m.st)
	m.pick.setSize(m.width, m.pickerHeight())
	m.pick.list.Select(max(0, indexOfBoard(m.file, selectID)))
	m.state = statePicker
}

func neighbourIndex(i, n int) int {
	if i > 0 {
		return i - 1
	}
	if n > 1 {
		return 1
	}
	return 0
}

func indexOfBoard(f *File, id string) int {
	for i, b := range f.Boards {
		if b.ID == id {
			return i
		}
	}
	return -1
}

func (m app) openEditor(t Task, back viewState) (tea.Model, tea.Cmd) {
	colName := t.Column
	if c := m.board.Column(t.Column); c != nil {
		colName = c.Name
	}
	m.form = newTaskForm(formEdit, t, colName, m.st, m.g)
	m.form.setSize(m.width, m.height)
	m.back = back
	m.state = stateForm
	return m, m.form.Init()
}

func (m app) openDetail(id int) (tea.Model, tea.Cmd) {
	m.detail = newDetailView(id, m.st, m.g)
	m.detail.setSize(m.width, m.height)
	m.renderDetail()
	m.state = stateDetail
	return m, nil
}

func (m *app) renderDetail() {
	t := m.board.Task(m.detail.taskID)
	if t == nil {
		m.state = stateBoard
		return
	}
	if n := m.detail.itemCount(*t); m.detail.cursor >= n {
		m.detail.cursor = n - 1
	}
	m.detail.render(*t, m.board, timeNow())
}

func (m app) askDeleteTasks(ids []int, back viewState) (tea.Model, tea.Cmd) {
	if len(ids) == 0 {
		return m, nil
	}
	var lines []string
	for i, id := range ids {
		if i == 5 {
			lines = append(lines, m.st.muted.Render(fmt.Sprintf("… and %d more", len(ids)-5)))
			break
		}
		lines = append(lines, m.st.strong.Render(fmt.Sprintf("#%d %s", id, m.taskTitle(id))))
	}
	lines = append(lines, m.st.muted.Render("Tip: a archives instead of deleting."))
	m.confirm = newConfirm(confirmDeleteTasks, "Delete task?", m.st, lines...)
	if len(ids) > 1 {
		m.confirm.title = fmt.Sprintf("Delete %d tasks?", len(ids))
	}
	m.confirm.taskIDs = ids
	m.back = back
	m.state = stateConfirm
	return m, nil
}

// moveTargets moves the marked or selected tasks one column left or right.
func (m app) moveTargets(delta int) (tea.Model, tea.Cmd) {
	ids := m.targets()
	if len(ids) == 0 {
		return m, nil
	}
	var cmds []tea.Cmd
	m.snapshot()
	moved := 0
	var lastCol *Column
	for _, id := range ids {
		t := m.board.Task(id)
		if t == nil {
			continue
		}
		i := m.board.ColumnIndex(t.Column) + delta
		if i < 0 || i >= len(m.board.Columns) {
			continue
		}
		target := m.board.Columns[i]
		if err := m.board.MoveTask(id, target.ID); err == nil {
			moved++
			lastCol = &m.board.Columns[i]
		}
	}
	if moved == 0 {
		m.dropSnapshot()
		if delta > 0 {
			return m, m.setStatus("%s is already in the last column", describeTargets(&m, ids))
		}
		return m, m.setStatus("%s is already in the first column", describeTargets(&m, ids))
	}
	if len(ids) == 1 {
		// Keep the moved task selected in its new column.
		for i := range m.cols {
			if m.cols[i].id == lastCol.ID {
				m.cols[i].selectedID = ids[0]
			}
		}
	}
	m.marks = map[int]bool{}
	cmds = append(cmds, m.changed("Moved %s to %s", describeTargets(&m, ids), lastCol.Name))
	if m.board.WIPExceeded(lastCol.ID) {
		cmds = append(cmds, m.setStatus("%s is over its WIP limit (%d/%d)", lastCol.Name, m.board.CountIn(lastCol.ID), lastCol.WIPLimit))
	}
	return m, tea.Batch(cmds...)
}

// reorderSelected moves the selected task up or down within its column.
func (m app) reorderSelected(delta int) (tea.Model, tea.Cmd) {
	if m.sortMode != sortManual {
		return m, m.setStatus("Switch to manual sort (s) to reorder tasks")
	}
	if m.query != "" {
		return m, m.setStatus("Clear the search (esc) to reorder tasks")
	}
	t, ok := m.col().selected()
	if !ok {
		return m, nil
	}
	m.snapshot()
	if !m.board.ReorderTask(t.ID, delta) {
		m.dropSnapshot()
		return m, nil
	}
	m.col().selectedID = t.ID
	return m, m.changed("")
}

func (m app) moveColumn(delta int) (tea.Model, tea.Cmd) {
	if len(m.board.Columns) == 0 {
		return m, nil
	}
	col := m.board.Columns[m.focused]
	m.snapshot()
	if !m.board.MoveColumn(col.ID, delta) {
		m.dropSnapshot()
		return m, nil
	}
	m.focused += delta
	m.rebuildColumns()
	return m, m.changed("Moved column %s", col.Name)
}

func (m app) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m.quit()
	case key.Matches(msg, confirmKeys.No):
		m.state = m.back
		return m, nil
	case !key.Matches(msg, confirmKeys.Yes):
		return m, nil
	}
	c := m.confirm
	m.state = m.back
	switch c.kind {
	case confirmDeleteTasks:
		m.snapshot()
		for _, id := range c.taskIDs {
			m.board.DeleteTask(id)
		}
		m.marks = map[int]bool{}
		if m.state == stateDetail {
			m.state = stateBoard
		}
		return m, m.changed("Deleted %s", describeTargets(&m, c.taskIDs))
	case confirmDeleteColumn:
		i := m.board.ColumnIndex(c.sref)
		moveTo := ""
		if i >= 0 && m.board.AllIn(c.sref) > 0 {
			moveTo = m.board.Columns[neighbourIndex(i, len(m.board.Columns))].ID
		}
		name := m.board.Columns[i].Name
		m.snapshot()
		if err := m.board.RemoveColumn(c.sref, moveTo); err != nil {
			m.dropSnapshot()
			return m, m.setStatus("%v", err)
		}
		m.rebuildColumns()
		return m, m.changed("Deleted column %s", name)
	case confirmDeleteBoard:
		name := c.sref
		if b := m.file.Board(c.sref); b != nil {
			name = b.Name
		}
		if err := m.file.RemoveBoard(c.sref); err != nil {
			return m, m.setStatus("%v", err)
		}
		cmd := m.switchBoard(m.file.ActiveBoard)
		m.openBoardPicker(m.board.ID)
		return m, tea.Batch(cmd, m.setStatus("Deleted board %q", name))
	case confirmArchiveDone:
		m.snapshot()
		n := m.board.ArchiveDone()
		return m, m.changed("Archived %d task%s", n, plural(n))
	case confirmDeleteArchived:
		m.snapshot()
		for _, id := range c.taskIDs {
			m.board.DeleteTask(id)
		}
		cmd := m.changed("Deleted %s permanently", describeTargets(&m, c.taskIDs))
		m.pick = newPicker(pickerArchive, "Archived tasks", archiveItems(m.board, m.g), m.st)
		m.pick.setSize(m.width, m.pickerHeight())
		return m, cmd
	case confirmReload:
		if err := m.reload(); err != nil {
			m.err = err
			return m, nil
		}
		return m, m.setStatus("Reloaded %s", m.store.describe())
	}
	return m, nil
}

func (m app) handleFormSubmit(msg formSubmitMsg) (tea.Model, tea.Cmd) {
	m.state = m.back
	t := msg.task
	m.snapshot()
	var what string
	switch msg.mode {
	case formEdit:
		if err := m.board.UpdateTask(t); err != nil {
			m.dropSnapshot()
			return m, m.setStatus("%v", err)
		}
		what = "Updated"
	default:
		added, err := m.board.AddTask(t)
		if err != nil {
			m.dropSnapshot()
			return m, m.setStatus("%v", err)
		}
		for i := range m.cols {
			if m.cols[i].id == added.Column {
				m.cols[i].selectedID = added.ID
			}
		}
		t = *added
		what = "Added"
	}
	if m.state == stateDetail {
		m.renderDetail()
	}
	return m, m.changed("%s %s %q", what, t.Ref(), t.Title)
}

func (m app) handleColumnFormSubmit(msg colFormSubmitMsg) (tea.Model, tea.Cmd) {
	m.state = m.back
	m.snapshot()
	if msg.id == "" {
		col, err := m.board.AddColumn(msg.name, msg.color, msg.wip)
		if err != nil {
			m.dropSnapshot()
			return m, m.setStatus("%v", err)
		}
		m.rebuildColumns()
		m.focusColumn(m.board.ColumnIndex(col.ID))
		return m, m.changed("Added column %s", col.Name)
	}
	if err := m.board.UpdateColumn(msg.id, msg.name, msg.color, msg.wip); err != nil {
		m.dropSnapshot()
		return m, m.setStatus("%v", err)
	}
	m.rebuildColumns()
	return m, m.changed("Updated column %s", msg.name)
}

func (m app) handlePromptSubmit(msg promptSubmitMsg) (tea.Model, tea.Cmd) {
	m.state = m.back
	text := strings.TrimSpace(msg.text)
	switch msg.kind {
	case promptNewBoard:
		b, err := m.file.AddBoard(text)
		if err != nil {
			return m, m.setStatus("%v", err)
		}
		cmd := m.switchBoard(b.ID)
		m.state = stateBoard
		return m, tea.Batch(cmd, m.setStatus("Created board %q", b.Name))
	case promptRenameBoard:
		b := m.file.Board(msg.sref)
		if b == nil || text == "" {
			return m, nil
		}
		if other := m.file.Board(text); other != nil && other.ID != b.ID {
			return m, m.setStatus("A board named %q already exists", text)
		}
		b.Name = text
		m.persist(false)
		m.openBoardPicker(b.ID)
		return m, m.setStatus("Renamed board to %q", text)
	case promptComment:
		if text == "" {
			m.renderDetail()
			return m, nil
		}
		m.snapshot()
		if err := m.board.AddComment(msg.ref, text); err != nil {
			return m, m.setStatus("%v", err)
		}
		m.renderDetail()
		m.detail.vp.GotoBottom()
		return m, m.changed("Comment added")
	case promptChecklistItem:
		if text == "" {
			m.renderDetail()
			return m, nil
		}
		m.snapshot()
		if err := m.board.AddChecklistItem(msg.ref, text); err != nil {
			return m, m.setStatus("%v", err)
		}
		if t := m.board.Task(msg.ref); t != nil {
			m.detail.cursor = len(t.Checklist) - 1
		}
		m.renderDetail()
		return m, m.changed("Checklist item added")
	case promptAttachment:
		if text == "" {
			m.renderDetail()
			return m, nil
		}
		m.snapshot()
		if err := m.board.AddAttachment(msg.ref, text); err != nil {
			return m, m.setStatus("%v", err)
		}
		m.renderDetail()
		return m, m.changed("Attachment added")
	}
	return m, nil
}

func (m app) openPrompt(kind promptKind, title, initial string, ref int, sref string, back viewState) (tea.Model, tea.Cmd) {
	m.prompt = newPrompt(kind, title, initial, m.st)
	m.prompt.setSize(m.width)
	m.prompt.ref = ref
	m.prompt.sref = sref
	m.back = back
	m.state = statePrompt
	return m, m.prompt.Init()
}

func (m app) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.detail.keys
	t := m.board.Task(m.detail.taskID)
	if t == nil {
		m.state = stateBoard
		return m, nil
	}
	id := t.ID
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m.quit()
	case key.Matches(msg, k.Back):
		m.state = stateBoard
		return m, nil
	case key.Matches(msg, k.Item):
		delta := 1
		if msg.String() == "shift+tab" {
			delta = -1
		}
		m.detail.moveCursor(*t, delta)
		m.renderDetail()
		return m, nil
	case key.Matches(msg, k.Toggle):
		i := m.detail.cursor
		if i < 0 || i >= len(t.Checklist) {
			return m, m.setStatus("Select a checklist item with tab first")
		}
		m.snapshot()
		m.board.ToggleChecklistItem(id, i)
		m.renderDetail()
		return m, m.changed("")
	case key.Matches(msg, k.RemoveItem):
		i := m.detail.cursor
		switch {
		case i >= 0 && i < len(t.Checklist):
			m.snapshot()
			m.board.RemoveChecklistItem(id, i)
		case i >= len(t.Checklist) && i < len(t.Checklist)+len(t.Attachments):
			m.snapshot()
			m.board.RemoveAttachment(id, i-len(t.Checklist))
		default:
			return m, m.setStatus("Select an item with tab first")
		}
		m.renderDetail()
		return m, m.changed("Removed item")
	case key.Matches(msg, k.AddItem):
		return m.openPrompt(promptChecklistItem, "Add checklist item", "", id, "", stateDetail)
	case key.Matches(msg, k.Comment):
		return m.openPrompt(promptComment, "Add comment", "", id, "", stateDetail)
	case key.Matches(msg, k.Attach):
		return m.openPrompt(promptAttachment, "Attach a link or file path", "", id, "", stateDetail)
	case key.Matches(msg, k.Open):
		if len(t.Attachments) == 0 {
			return m, m.setStatus("No attachments")
		}
		i := m.detail.cursor - len(t.Checklist)
		if i < 0 || i >= len(t.Attachments) {
			i = 0
		}
		ref := t.Attachments[i]
		if err := openExternal(ref); err != nil {
			return m, m.setStatus("%v", err)
		}
		return m, m.setStatus("Opened %s", ref)
	case key.Matches(msg, k.Edit):
		return m.openEditor(*t, stateDetail)
	case key.Matches(msg, k.MoveLeft), key.Matches(msg, k.MoveRight):
		delta := 1
		if key.Matches(msg, k.MoveLeft) {
			delta = -1
		}
		i := m.board.ColumnIndex(t.Column) + delta
		if i < 0 || i >= len(m.board.Columns) {
			return m, nil
		}
		m.snapshot()
		target := m.board.Columns[i]
		ref := t.Ref()
		m.board.MoveTask(id, target.ID) //nolint:errcheck // column exists
		m.focusColumn(i)
		m.cols[i].selectedID = id
		m.renderDetail()
		return m, m.changed("Moved %s to %s", ref, target.Name)
	case key.Matches(msg, k.Archive):
		m.snapshot()
		m.board.ArchiveTask(id)
		m.state = stateBoard
		return m, m.changed("Archived %s", t.Ref())
	case key.Matches(msg, k.Delete):
		return m.askDeleteTasks([]int{id}, stateDetail)
	}
	var cmd tea.Cmd
	m.detail, cmd = m.detail.update(msg)
	return m, cmd
}

func (m app) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pick.filtering() {
		var cmd tea.Cmd
		m.pick, cmd = m.pick.update(msg)
		return m, cmd
	}
	k := m.pick.keys
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m.quit()
	case key.Matches(msg, k.Back):
		m.state = stateBoard
		return m, nil
	}
	item, ok := m.pick.selected()
	if m.pick.kind == pickerBoards {
		switch {
		case key.Matches(msg, k.Select):
			if !ok {
				return m, nil
			}
			m.state = stateBoard
			return m, m.switchBoard(item.id)
		case key.Matches(msg, k.New):
			return m.openPrompt(promptNewBoard, "New board name", "", 0, "", statePicker)
		case key.Matches(msg, k.Rename):
			if !ok {
				return m, nil
			}
			return m.openPrompt(promptRenameBoard, "Rename board", item.title, 0, item.id, statePicker)
		case key.Matches(msg, k.Delete):
			if !ok {
				return m, nil
			}
			if len(m.file.Boards) <= 1 {
				return m, m.setStatus("Cannot delete the only board")
			}
			b := m.file.Board(item.id)
			n := len(b.Tasks)
			m.confirm = newConfirm(confirmDeleteBoard, "Delete board?", m.st,
				m.st.strong.Render(b.Name), fmt.Sprintf("%d task%s will be deleted permanently.", n, plural(n)))
			m.confirm.sref = item.id
			m.back = statePicker
			m.state = stateConfirm
			return m, nil
		}
	} else {
		switch {
		case key.Matches(msg, k.Restore):
			if !ok {
				return m, nil
			}
			m.snapshot()
			m.board.RestoreTask(item.num)
			m.pick = newPicker(pickerArchive, "Archived tasks", archiveItems(m.board, m.g), m.st)
			m.pick.setSize(m.width, m.pickerHeight())
			return m, m.changed("Restored #%d", item.num)
		case key.Matches(msg, k.Delete):
			if !ok {
				return m, nil
			}
			m.confirm = newConfirm(confirmDeleteArchived, "Delete permanently?", m.st, m.st.strong.Render(item.title))
			m.confirm.taskIDs = []int{item.num}
			m.back = statePicker
			m.state = stateConfirm
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.pick, cmd = m.pick.update(msg)
	return m, cmd
}

func (m app) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateDetail:
		var cmd tea.Cmd
		m.detail.vp, cmd = m.detail.vp.Update(msg)
		return m, cmd
	case statePicker:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.pick.list.CursorUp()
		case tea.MouseButtonWheelDown:
			m.pick.list.CursorDown()
		}
		return m, nil
	case stateBoard:
	default:
		return m, nil
	}
	if len(m.cols) == 0 {
		return m, nil
	}
	ci := m.columnAt(msg.X)
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if ci < 0 {
			ci = m.focused
		}
		delta := 1
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -1
		}
		m.cols[ci].scroll(delta)
		return m, nil
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress || ci < 0 {
			return m, nil
		}
		headerH := lipgloss.Height(m.headerView())
		row := msg.Y - headerH
		prevFocus := m.focused
		m.focusColumn(ci)
		col := m.col()
		if idx, ok := col.cardAt(row); ok {
			if idx == col.list.Index() && prevFocus == ci {
				if t, ok := col.selected(); ok {
					return m.openDetail(t.ID)
				}
			}
			col.list.Select(idx)
			col.remember()
		}
		return m, nil
	}
	return m, nil
}

// --- view -----------------------------------------------------------------

// View implements tea.Model.
func (m app) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "Loading…"
	}
	if m.width < minWidth || m.height < minHeight {
		msg := fmt.Sprintf("Terminal too small.\nNeed at least %dx%d, have %dx%d.", minWidth, minHeight, m.width, m.height)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.st.muted.Render(msg))
	}

	center := func(s string) string {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s)
	}
	// Dialogs keep a one-line status strip at the bottom so messages and
	// errors stay visible.
	dialog := func(s string) string {
		var note string
		switch {
		case m.err != nil:
			note = m.st.err.Render(m.err.Error())
		case m.statusMsg != "":
			note = m.st.success.Render(m.statusMsg)
		}
		if note == "" {
			return center(s)
		}
		body := lipgloss.Place(m.width, m.height-1, lipgloss.Center, lipgloss.Center, s)
		return lipgloss.JoinVertical(lipgloss.Left, body, lipgloss.NewStyle().MaxWidth(m.width).Render(" "+note))
	}
	switch m.state {
	case stateForm:
		return dialog(m.form.View())
	case stateColumnForm:
		return dialog(m.colForm.View())
	case statePrompt:
		return dialog(m.prompt.View())
	case stateConfirm:
		return dialog(m.confirm.View())
	case stateDetail:
		return dialog(m.detail.view())
	case statePicker:
		return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), m.pick.view())
	}

	columns := make([]string, 0, len(m.cols))
	for _, c := range m.cols {
		columns = append(columns, c.view())
	}
	board := lipgloss.JoinHorizontal(lipgloss.Top, columns...)
	if len(m.cols) == 0 {
		board = center(m.st.muted.Render("No columns. Press C to add one."))
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), board, m.footerView())
}

// dueSummary counts overdue and due-today tasks for the header badge.
func (m app) dueSummary() string {
	now := timeNow()
	overdue, todayN := 0, 0
	done := m.board.DoneColumn()
	for _, t := range m.board.Live() {
		if done != nil && t.Column == done.ID {
			continue
		}
		switch _, s := dueInfo(t, now); s {
		case dueOverdue:
			overdue++
		case dueToday:
			todayN++
		}
	}
	var parts []string
	if overdue > 0 {
		parts = append(parts, m.st.err.Render(fmt.Sprintf("%d overdue", overdue)))
	}
	if todayN > 0 {
		parts = append(parts, m.st.warning.Render(fmt.Sprintf("%d due today", todayN)))
	}
	return strings.Join(parts, m.st.muted.Render(" "+m.g.dot+" "))
}

func (m app) headerView() string {
	left := " " + m.st.appTitle.Render("Kancli") + m.st.muted.Render(" "+m.g.dot+" ") + m.st.strong.Render(m.board.Name)
	if len(m.marks) > 0 {
		left += m.st.muted.Render(fmt.Sprintf("  %d marked", len(m.marks)))
	}

	var right string
	switch {
	case m.err != nil:
		right = m.st.err.Render(m.err.Error())
	case m.statusMsg != "":
		right = m.st.success.Render(m.statusMsg)
	default:
		var parts []string
		if s := m.dueSummary(); s != "" {
			parts = append(parts, s)
		}
		if m.sortMode != sortManual {
			parts = append(parts, m.st.muted.Render("sort: "+m.sortMode.String()))
		}
		parts = append(parts, m.st.muted.Render(m.store.describe()))
		right = strings.Join(parts, m.st.muted.Render(" "+m.g.dot+" "))
	}
	right += " "

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 && m.err == nil && m.statusMsg == "" {
		// Not enough room: drop the file path, then the sort mode.
		var parts []string
		if s := m.dueSummary(); s != "" {
			parts = append(parts, s)
		}
		right = strings.Join(parts, m.st.muted.Render(" "+m.g.dot+" ")) + " "
		gap = m.width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	var line string
	if gap < 1 {
		line = lipgloss.NewStyle().MaxWidth(m.width).Render(" " + right)
	} else {
		line = left + lipgloss.NewStyle().Width(gap).Render("") + right
	}
	if m.searching || m.query != "" {
		bar := " " + m.search.View()
		if !m.searching {
			bar = " " + m.st.searchPrompt.Render("/ ") + m.query
		}
		matches := 0
		for _, c := range m.cols {
			matches += c.count()
		}
		bar += m.st.muted.Render(fmt.Sprintf("  %d match%s", matches, map[bool]string{true: "", false: "es"}[matches == 1]))
		if !m.searching {
			bar += m.st.muted.Render("  esc clears")
		}
		line = lipgloss.JoinVertical(lipgloss.Left, line, lipgloss.NewStyle().MaxWidth(m.width).Render(bar))
	}
	return line
}

func (m app) footerView() string {
	inner := max(1, m.width-m.st.help.GetHorizontalFrameSize())
	return m.st.help.Render(lipgloss.NewStyle().MaxWidth(inner).Render(m.help.View(m.keys)))
}
