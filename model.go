package main

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// viewState says which screen is in front of the user.
type viewState int

const (
	stateBoard viewState = iota
	stateForm
	stateConfirmDelete
)

const (
	minWidth  = 60
	minHeight = 16
)

// statusDuration is how long transient header messages stay visible.
var statusDuration = 4 * time.Second

// clearStatusMsg hides a transient status message once it has expired.
type clearStatusMsg struct{ id int }

// Board is the root model of the application.
type Board struct {
	store   store
	cols    [numStatuses]column
	focused status
	state   viewState
	form    taskForm
	pending Task // task awaiting delete confirmation

	keys keyMap
	help help.Model

	width  int
	height int
	ready  bool

	statusMsg string
	statusID  int
	err       error
	quitting  bool
}

// newBoard builds a board holding the given tasks. Tasks keep their column
// and relative order.
func newBoard(st store, tasks []Task) Board {
	h := help.New()
	m := Board{
		store: st,
		keys:  keys,
		help:  h,
	}
	for _, s := range allStatuses {
		m.cols[s] = newColumn(s)
	}
	byStatus := map[status][]Task{}
	for _, t := range tasks {
		if !t.status.valid() {
			t.status = todo
		}
		byStatus[t.status] = append(byStatus[t.status], t)
	}
	for _, s := range allStatuses {
		m.cols[s].setTasks(byStatus[s])
	}
	m.cols[m.focused].focus()
	return m
}

// Init implements tea.Model.
func (m Board) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m Board) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case formSubmitMsg:
		return m.handleFormSubmit(msg)

	case formCancelMsg:
		m.state = stateBoard
		return m, nil

	case list.FilterMatchesMsg:
		return m, m.cols[m.focused].update(msg)

	case tea.KeyMsg:
		switch m.state {
		case stateForm:
			if msg.Type == tea.KeyCtrlC {
				m.quitting = true
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.form, cmd = m.form.Update(msg)
			return m, cmd
		case stateConfirmDelete:
			return m.handleConfirmKey(msg)
		default:
			return m.handleBoardKey(msg)
		}
	}

	// Everything else (cursor blinks, timers) belongs to the active screen.
	if m.state == stateForm {
		var cmd tea.Cmd
		m.form, cmd = m.form.Update(msg)
		return m, cmd
	}
	return m, m.cols[m.focused].update(msg)
}

func (m Board) handleBoardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	col := &m.cols[m.focused]

	// While a filter is being typed every key belongs to the list, except
	// ctrl+c which must always quit.
	if col.filtering() {
		if msg.Type == tea.KeyCtrlC {
			m.quitting = true
			return m, tea.Quit
		}
		return m, col.update(msg)
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit

	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layout()
		return m, nil

	case key.Matches(msg, m.keys.Left):
		m.focusColumn(m.focused.prev())
		return m, nil

	case key.Matches(msg, m.keys.Right):
		m.focusColumn(m.focused.next())
		return m, nil

	case key.Matches(msg, m.keys.Jump):
		n := int(msg.Runes[0] - '1')
		m.focusColumn(status(n))
		return m, nil

	case key.Matches(msg, m.keys.New):
		m.form = newTaskForm(formCreate, newTask(m.focused, "", ""))
		m.form.setSize(m.width, m.height)
		m.state = stateForm
		return m, m.form.Init()

	case key.Matches(msg, m.keys.Edit):
		t, ok := col.selected()
		if !ok {
			return m, nil
		}
		m.form = newTaskForm(formEdit, t)
		m.form.setSize(m.width, m.height)
		m.state = stateForm
		return m, m.form.Init()

	case key.Matches(msg, m.keys.Delete):
		t, ok := col.selected()
		if !ok {
			return m, nil
		}
		m.pending = t
		m.state = stateConfirmDelete
		return m, nil

	case key.Matches(msg, m.keys.MoveLeft):
		return m.moveSelected(-1)

	case key.Matches(msg, m.keys.MoveRight):
		return m.moveSelected(1)

	case key.Matches(msg, m.keys.MoveUp):
		return m.reorderSelected(-1)

	case key.Matches(msg, m.keys.MoveDown):
		return m.reorderSelected(1)
	}

	return m, col.update(msg)
}

func (m Board) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, confirmKeys.Yes):
		m.state = stateBoard
		title := m.pending.title
		cmd := m.cols[m.pending.status].remove(m.pending.id)
		m.pending = Task{}
		return m, tea.Batch(cmd, m.persist(), m.setStatus("Deleted %q", title))
	case key.Matches(msg, confirmKeys.No):
		m.state = stateBoard
		m.pending = Task{}
		return m, nil
	case msg.Type == tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m Board) handleFormSubmit(msg formSubmitMsg) (tea.Model, tea.Cmd) {
	m.state = stateBoard
	t := msg.task
	if !t.status.valid() {
		t.status = m.focused
	}
	var cmd tea.Cmd
	var what string
	switch msg.mode {
	case formEdit:
		cmd = m.cols[t.status].replace(t)
		what = "Updated"
	default:
		cmd = m.cols[t.status].add(t)
		what = "Added"
	}
	return m, tea.Batch(cmd, m.persist(), m.setStatus("%s %q", what, t.title))
}

// focusColumn moves keyboard focus to another column.
func (m *Board) focusColumn(s status) {
	if !s.valid() || s == m.focused {
		return
	}
	m.cols[m.focused].blur()
	m.focused = s
	m.cols[m.focused].focus()
}

// moveSelected moves the selected task one column to the left or right.
func (m Board) moveSelected(delta int) (tea.Model, tea.Cmd) {
	src := &m.cols[m.focused]
	t, ok := src.selected()
	if !ok {
		return m, nil
	}
	target := m.focused + status(delta)
	if !target.valid() {
		if delta > 0 {
			return m, m.setStatus("%q is already done", t.title)
		}
		return m, m.setStatus("%q is already in %s", t.title, todo)
	}
	removeCmd := src.remove(t.id)
	t.status = target
	t.updatedAt = time.Now()
	addCmd := m.cols[target].add(t)
	return m, tea.Batch(removeCmd, addCmd, m.persist(),
		m.setStatus("Moved %q to %s", t.title, target))
}

// reorderSelected moves the selected task up or down within its column.
func (m Board) reorderSelected(delta int) (tea.Model, tea.Cmd) {
	col := &m.cols[m.focused]
	if col.hasFilter() {
		return m, m.setStatus("Clear the filter to reorder tasks")
	}
	cmd, moved := col.moveSelected(delta)
	if !moved {
		return m, nil
	}
	return m, tea.Batch(cmd, m.persist())
}

// allTasks returns every task on the board in column order.
func (m Board) allTasks() []Task {
	var out []Task
	for _, s := range allStatuses {
		out = append(out, m.cols[s].tasks()...)
	}
	return out
}

// persist writes the board to disk. Saving is synchronous so successive
// changes can never be written out of order; errors are shown in the header.
func (m *Board) persist() tea.Cmd {
	if err := m.store.save(m.allTasks()); err != nil {
		m.err = err
		return nil
	}
	m.err = nil
	return nil
}

// setStatus shows a message in the header for a few seconds.
func (m *Board) setStatus(format string, args ...any) tea.Cmd {
	m.statusMsg = fmt.Sprintf(format, args...)
	m.statusID++
	id := m.statusID
	return tea.Tick(statusDuration, func(time.Time) tea.Msg {
		return clearStatusMsg{id: id}
	})
}

// layout recomputes the column sizes from the terminal size.
func (m *Board) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.help.Width = m.width - helpStyle.GetHorizontalFrameSize()
	headerH := lipgloss.Height(m.headerView())
	footerH := lipgloss.Height(m.footerView())
	colH := max(0, m.height-headerH-footerH)

	base := m.width / numStatuses
	extra := m.width % numStatuses
	for i := range m.cols {
		w := base
		if i < extra {
			w++
		}
		m.cols[i].setSize(w, colH)
	}
	if m.state == stateForm {
		m.form.setSize(m.width, m.height)
	}
}

// View implements tea.Model.
func (m Board) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "Loading…"
	}
	if m.width < minWidth || m.height < minHeight {
		msg := fmt.Sprintf("Terminal too small.\nNeed at least %dx%d, have %dx%d.", minWidth, minHeight, m.width, m.height)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, mutedStyle.Render(msg))
	}

	switch m.state {
	case stateForm:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.form.View())
	case stateConfirmDelete:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.confirmView())
	}

	columns := make([]string, 0, numStatuses)
	for _, s := range allStatuses {
		columns = append(columns, m.cols[s].view())
	}
	board := lipgloss.JoinHorizontal(lipgloss.Top, columns...)
	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), board, m.footerView())
}

func (m Board) headerView() string {
	left := " " + appTitleStyle.Render("Kancli")

	var right string
	switch {
	case m.err != nil:
		right = errorStyle.Render("save failed: " + m.err.Error())
	case m.statusMsg != "":
		right = statusMsgStyle.Render(m.statusMsg)
	case !m.store.enabled():
		right = mutedStyle.Render("demo mode · changes are not saved")
	default:
		right = mutedStyle.Render(m.store.path)
	}
	right += " "

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Not enough room for both; the status is more useful than the name.
		return lipgloss.NewStyle().MaxWidth(m.width).Render(" " + right)
	}
	return left + lipgloss.NewStyle().Width(gap).Render("") + right
}

func (m Board) footerView() string {
	return helpStyle.Render(m.help.View(m.keys))
}

func (m Board) confirmView() string {
	body := lipgloss.JoinVertical(lipgloss.Left,
		dialogTitleStyle.Render("Delete task?"),
		"",
		highlightStyle.Render(m.pending.title),
		mutedStyle.Render("from "+m.pending.status.String()),
		"",
		m.help.ShortHelpView(confirmKeys.ShortHelp()),
	)
	return dialogStyle.Render(body)
}
