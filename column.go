package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// card wraps a Task as a list item.
type card struct{ t Task }

// FilterValue implements list.Item. Filtering is done by the board, so
// this is only here to satisfy the interface.
func (c card) FilterValue() string { return c.t.Title }

// cardDelegate renders tasks as two- or three-line cards.
type cardDelegate struct {
	st      styles
	g       glyphs
	compact bool
	focused bool
	color   lipgloss.TerminalColor
	marks   map[int]bool
	now     time.Time
}

func (d cardDelegate) Height() int {
	if d.compact {
		return 2
	}
	return 3
}

func (d cardDelegate) Spacing() int { return 1 }

func (d cardDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

// Render draws one card.
func (d cardDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	c, ok := item.(card)
	if !ok {
		return
	}
	t := c.t
	th := d.st.th
	width := m.Width()
	if width <= 0 {
		return
	}
	selected := index == m.Index()

	// Styles for the three lines.
	var lineStyle, descStyle lipgloss.Style
	switch {
	case selected && d.focused:
		lineStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(d.color).Foreground(d.color).Padding(0, 0, 0, 1)
		if d.st.th.mono {
			lineStyle = lineStyle.Bold(true)
		}
		descStyle = lineStyle
	case selected:
		lineStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(th.muted).Foreground(th.text).Padding(0, 0, 0, 1)
		descStyle = lineStyle.Foreground(th.muted)
	default:
		lineStyle = lipgloss.NewStyle().Foreground(th.text).Padding(0, 0, 0, 2)
		descStyle = lineStyle.Foreground(th.muted)
	}
	inner := width - lineStyle.GetHorizontalFrameSize()
	if inner < 1 {
		return
	}

	// Line 1: mark, reference, priority and title.
	var head strings.Builder
	if d.marks[t.ID] {
		head.WriteString(d.st.mark.Render(d.g.mark) + " ")
	}
	head.WriteString(d.st.muted.Render(t.Ref()) + " ")
	if p := d.g.priority(t.Priority); p != "" {
		head.WriteString(lipgloss.NewStyle().Foreground(th.priorityColor(t.Priority)).Bold(true).Render(p) + " ")
	}
	prefixWidth := lipgloss.Width(head.String())
	title := ansi.Truncate(t.Title, max(1, inner-prefixWidth), d.g.ellipsis)
	line1 := lineStyle.Render(head.String() + title)

	// Line 2: description (skipped in compact mode).
	var lines []string
	lines = append(lines, line1)
	if !d.compact {
		desc := t.FirstLine()
		lines = append(lines, descStyle.Render(ansi.Truncate(desc, inner, d.g.ellipsis)))
	}

	// Meta line: due, assignee, labels, checklist, attachments, comments.
	meta := d.metaLine(t, inner)
	if d.compact && meta == "" {
		meta = descStyle.Render(ansi.Truncate(t.FirstLine(), inner, d.g.ellipsis))
	} else {
		meta = descStyle.Render(meta)
	}
	lines = append(lines, meta)

	fmt.Fprint(w, strings.Join(lines, "\n"))
}

// metaLine builds the styled metadata line for a card.
func (d cardDelegate) metaLine(t Task, width int) string {
	th := d.st.th
	var parts []string
	if label, state := dueInfo(t, d.now); label != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(th.dueColor(state)).Render(label))
	}
	if t.Assignee != "" {
		parts = append(parts, d.st.muted.Render("@"+t.Assignee))
	}
	for _, l := range t.Labels {
		parts = append(parts, d.st.chip.Render("+"+l))
	}
	if done, total := t.ChecklistProgress(); total > 0 {
		s := d.st.muted
		if done == total {
			s = d.st.success
		}
		parts = append(parts, s.Render(fmt.Sprintf("%s %d/%d", d.g.checked, done, total)))
	}
	if n := len(t.Attachments); n > 0 {
		parts = append(parts, d.st.muted.Render(fmt.Sprintf("%d link%s", n, plural(n))))
	}
	if n := len(t.Comments); n > 0 {
		parts = append(parts, d.st.muted.Render(fmt.Sprintf("%d comment%s", n, plural(n))))
	}
	if len(parts) == 0 {
		return ""
	}
	return ansi.Truncate(strings.Join(parts, d.st.muted.Render(" "+d.g.dot+" ")), width, d.g.ellipsis)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// column is one lane of the board, backed by a bubbles list used purely as
// a viewer: the board owns the data and pushes filtered, sorted cards in.
type column struct {
	id         string
	list       list.Model
	focused    bool
	width      int
	height     int
	selectedID int
	st         styles
	color      lipgloss.TerminalColor
	rows       int // rows per card including spacing
}

func newColumn(id string, st styles, keys keyMap) column {
	l := list.New(nil, cardDelegate{st: st}, 0, 0)
	l.KeyMap.CursorUp = keys.Up
	l.KeyMap.CursorDown = keys.Down
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(true)
	l.DisableQuitKeybindings()
	l.KeyMap.NextPage = bind("pgdn", "next page", "pgdown")
	l.KeyMap.PrevPage = bind("pgup", "prev page", "pgup")
	l.KeyMap.ShowFullHelp.SetEnabled(false)
	l.KeyMap.CloseFullHelp.SetEnabled(false)
	l.Styles.NoItems = st.muted.Padding(0, 0, 0, 2)
	l.Styles.PaginationStyle = st.muted.Padding(0, 0, 0, 2)
	l.Styles.ActivePaginationDot = lipgloss.NewStyle().Foreground(st.th.accent).SetString("•")
	l.Styles.InactivePaginationDot = st.muted.SetString("•")
	return column{id: id, list: l, st: st, color: st.th.muted, rows: 4}
}

// configure refreshes the column header and delegate from board state.
func (c *column) configure(col Column, count int, exceeded bool, d cardDelegate) {
	c.color = c.st.th.columnColor(col.Color)
	d.color = c.color
	d.focused = c.focused
	c.rows = d.Height() + d.Spacing()
	c.list.SetDelegate(d)

	title := fmt.Sprintf("%s %d", col.Name, count)
	if col.WIPLimit > 0 {
		title = fmt.Sprintf("%s %d/%d", col.Name, count, col.WIPLimit)
	}
	c.list.Title = title
	c.list.SetStatusBarItemName("task", "tasks")
	ts := lipgloss.NewStyle().Padding(0, 1).Bold(true).
		Background(c.color).Foreground(c.st.th.onColor)
	if exceeded {
		ts = ts.Background(c.st.th.err)
	}
	if c.st.th.mono {
		ts = lipgloss.NewStyle().Padding(0, 1).Bold(true).Reverse(true)
	}
	c.list.Styles.Title = ts
}

func (c *column) setFocus(focused bool) {
	c.focused = focused
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
	s := c.st.column
	if c.focused {
		s = s.BorderForeground(c.color)
		if c.st.th.mono {
			s = s.Border(lipgloss.ThickBorder())
			if c.st.ascii {
				s = s.Border(lipgloss.Border{
					Top: "=", Bottom: "=", Left: "#", Right: "#",
					TopLeft: "#", TopRight: "#", BottomLeft: "#", BottomRight: "#",
				})
			}
		}
	}
	return s.
		Width(max(0, c.width-s.GetHorizontalBorderSize())).
		Height(max(0, c.height-s.GetVerticalBorderSize()))
}

// setTasks replaces the visible cards, keeping the previously selected
// task under the cursor when it is still visible.
func (c *column) setTasks(tasks []Task) tea.Cmd {
	items := make([]list.Item, len(tasks))
	sel := -1
	for i, t := range tasks {
		items[i] = card{t}
		if t.ID == c.selectedID {
			sel = i
		}
	}
	prev := c.list.Index()
	cmd := c.list.SetItems(items)
	switch {
	case len(items) == 0:
		c.list.Select(0)
	case sel >= 0:
		c.list.Select(sel)
	case prev >= len(items):
		c.list.Select(len(items) - 1)
	default:
		c.list.Select(prev)
	}
	c.remember()
	return cmd
}

// remember records which task is under the cursor.
func (c *column) remember() {
	if t, ok := c.selected(); ok {
		c.selectedID = t.ID
	}
}

// selected returns the task under the cursor, if any.
func (c column) selected() (Task, bool) {
	cd, ok := c.list.SelectedItem().(card)
	return cd.t, ok
}

func (c column) count() int { return len(c.list.Items()) }

func (c *column) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.list, cmd = c.list.Update(msg)
	c.remember()
	return cmd
}

// scroll moves the cursor by delta rows.
func (c *column) scroll(delta int) {
	for ; delta < 0; delta++ {
		c.list.CursorUp()
	}
	for ; delta > 0; delta-- {
		c.list.CursorDown()
	}
	c.remember()
}

// cardAt maps a row inside the column (0 = top border) to the visible card
// index at that row.
func (c column) cardAt(row int) (int, bool) {
	s := c.style()
	top := s.GetVerticalFrameSize()/2 + lipgloss.Height(c.list.Styles.Title.Render("x")) + c.list.Styles.TitleBar.GetVerticalPadding()
	rel := row - top
	if rel < 0 {
		return 0, false
	}
	perPage := c.list.Paginator.PerPage
	i := rel / c.rows
	if rel%c.rows >= c.rows-1 || i >= perPage {
		return 0, false
	}
	idx := c.list.Paginator.Page*perPage + i
	if idx >= c.count() {
		return 0, false
	}
	return idx, true
}

func (c column) view() string {
	s := c.style()
	listWidth := max(0, c.width-s.GetHorizontalFrameSize())
	inner := lipgloss.NewStyle().MaxWidth(listWidth).Render(c.list.View())
	return s.Render(inner)
}
