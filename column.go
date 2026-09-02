package main

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// column is one lane of the board, backed by a bubbles list.
type column struct {
	status  status
	list    list.Model
	focused bool
	width   int
	height  int
}

func newColumn(st status) column {
	l := list.New(nil, newDelegate(st, false), 0, 0)
	l.Title = st.String()
	l.SetShowHelp(false)
	l.SetStatusBarItemName("task", "tasks")
	l.DisableQuitKeybindings()
	// The list's default paging keys overlap with the board's column and
	// delete bindings, so narrow them to the dedicated paging keys.
	l.KeyMap.NextPage = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "next page"))
	l.KeyMap.PrevPage = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "prev page"))
	l.KeyMap.ShowFullHelp.SetEnabled(false)
	l.KeyMap.CloseFullHelp.SetEnabled(false)
	l.Styles.Title = l.Styles.Title.
		Background(statusColor(st)).
		Foreground(statusTitleForeground(st)).
		Bold(true)
	l.Styles.NoItems = mutedStyle.Padding(0, 0, 0, 2)
	l.FilterInput.Prompt = "/ "
	l.FilterInput.PromptStyle = lipgloss.NewStyle().Foreground(statusColor(st))
	return column{status: st, list: l}
}

// newDelegate renders cards, highlighting the selection only in the
// focused column so the active cursor is easy to spot.
func newDelegate(st status, focused bool) list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	if focused {
		c := statusColor(st)
		d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(c).BorderForeground(c)
		d.Styles.SelectedDesc = d.Styles.SelectedDesc.Foreground(c).BorderForeground(c)
		return d
	}
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.
		Foreground(d.Styles.NormalTitle.GetForeground()).
		BorderForeground(colorMuted)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.
		Foreground(d.Styles.NormalDesc.GetForeground()).
		BorderForeground(colorMuted)
	return d
}

func (c *column) focus() {
	c.focused = true
	c.list.SetDelegate(newDelegate(c.status, true))
}

// blur drops focus and clears any active filter so hidden cards never
// linger in an unfocused column.
func (c *column) blur() {
	c.focused = false
	c.list.ResetFilter()
	c.list.SetDelegate(newDelegate(c.status, false))
}

// filtering reports whether the user is currently typing a filter, in
// which case all keys belong to the list.
func (c column) filtering() bool {
	return c.list.SettingFilter()
}

// hasFilter reports whether a filter is being typed or has been applied.
func (c column) hasFilter() bool {
	return c.list.SettingFilter() || c.list.IsFiltered()
}

func (c *column) setSize(width, height int) {
	c.width, c.height = width, height
	s := c.style()
	c.list.SetSize(
		max(0, width-s.GetHorizontalFrameSize()),
		max(0, height-s.GetVerticalFrameSize()),
	)
}

func (c column) style() lipgloss.Style {
	s := columnStyle
	if c.focused {
		s = s.BorderForeground(statusColor(c.status))
	}
	return s.
		Width(max(0, c.width-s.GetHorizontalBorderSize())).
		Height(max(0, c.height-s.GetVerticalBorderSize()))
}

// tasks returns every task in the column in display order.
func (c column) tasks() []Task {
	items := c.list.Items()
	out := make([]Task, 0, len(items))
	for _, it := range items {
		if t, ok := it.(Task); ok {
			out = append(out, t)
		}
	}
	return out
}

func (c *column) setTasks(tasks []Task) tea.Cmd {
	items := make([]list.Item, len(tasks))
	for i, t := range tasks {
		t.status = c.status
		items[i] = t
	}
	cmd := c.list.SetItems(items)
	c.clampCursor()
	return cmd
}

func (c column) count() int {
	return len(c.list.Items())
}

// selected returns the task under the cursor, if any.
func (c column) selected() (Task, bool) {
	t, ok := c.list.SelectedItem().(Task)
	return t, ok
}

func (c column) indexOf(id string) int {
	for i, it := range c.list.Items() {
		if t, ok := it.(Task); ok && t.id == id {
			return i
		}
	}
	return -1
}

// add appends a task to the column and, when no filter is active, moves the
// cursor onto it.
func (c *column) add(t Task) tea.Cmd {
	t.status = c.status
	cmd := c.list.InsertItem(c.count(), t)
	if !c.hasFilter() {
		c.list.Select(c.count() - 1)
	}
	return cmd
}

// replace updates the task with the same ID in place.
func (c *column) replace(t Task) tea.Cmd {
	i := c.indexOf(t.id)
	if i < 0 {
		return c.add(t)
	}
	t.status = c.status
	return c.list.SetItem(i, t)
}

// remove deletes the task with the given ID.
func (c *column) remove(id string) tea.Cmd {
	i := c.indexOf(id)
	if i < 0 {
		return nil
	}
	c.list.RemoveItem(i)
	var cmd tea.Cmd
	if c.hasFilter() {
		// RemoveItem only patches the filtered view by index; re-run the
		// filter so the visible cards stay correct.
		cmd = c.list.SetItems(c.list.Items())
	}
	c.clampCursor()
	return cmd
}

// moveSelected shifts the selected task up (-1) or down (+1) within the
// column. Reordering is disabled while a filter hides cards.
func (c *column) moveSelected(delta int) (tea.Cmd, bool) {
	if c.hasFilter() {
		return nil, false
	}
	items := c.list.Items()
	i := c.list.Index()
	j := i + delta
	if i < 0 || i >= len(items) || j < 0 || j >= len(items) {
		return nil, false
	}
	items[i], items[j] = items[j], items[i]
	cmd := c.list.SetItems(items)
	c.list.Select(j)
	return cmd, true
}

// clampCursor keeps the cursor on a real card after items are removed.
func (c *column) clampCursor() {
	if c.list.Paginator.PerPage <= 0 {
		return
	}
	n := len(c.list.VisibleItems())
	switch {
	case n == 0:
		c.list.Select(0)
	case c.list.Index() >= n:
		c.list.Select(n - 1)
	}
}

func (c *column) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.list, cmd = c.list.Update(msg)
	return cmd
}

func (c column) view() string {
	return c.style().Render(c.list.View())
}
