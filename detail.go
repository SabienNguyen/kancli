package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// detailView shows everything about one task and lets the user work its
// checklist, comments and attachments.
type detailView struct {
	taskID int
	vp     viewport.Model
	cursor int // index into checklist + attachments, -1 for none
	st     styles
	g      glyphs
	keys   detailKeyMap
	help   help.Model
	width  int
	height int
}

func newDetailView(id int, st styles, g glyphs) detailView {
	return detailView{taskID: id, vp: viewport.New(0, 0), cursor: -1, st: st, g: g, keys: detailKeys, help: help.New()}
}

func (d *detailView) setSize(width, height int) {
	d.width, d.height = width, height
	frame := d.st.dialog.GetHorizontalFrameSize()
	d.vp.Width = max(10, min(width, 100)-frame)
	d.vp.Height = max(3, height-d.st.dialog.GetVerticalFrameSize()-2)
	d.help.Width = d.vp.Width
}

// itemCount is the number of selectable checklist + attachment rows.
func (d detailView) itemCount(t Task) int {
	return len(t.Checklist) + len(t.Attachments)
}

// moveCursor steps through checklist items and attachments.
func (d *detailView) moveCursor(t Task, delta int) {
	n := d.itemCount(t)
	if n == 0 {
		d.cursor = -1
		return
	}
	d.cursor = ((d.cursor+delta)%n + n) % n
}

// render rebuilds the viewport content for a task.
func (d *detailView) render(t Task, b *Board, now time.Time) {
	st := d.st
	th := st.th
	w := d.vp.Width
	wrap := lipgloss.NewStyle().Width(w)
	var out []string

	col := "?"
	if c := b.Column(t.Column); c != nil {
		col = c.Name
	}
	out = append(out, st.dialogTitle.Render(t.Ref()+" "+t.Title))
	meta := []string{st.muted.Render("in ") + lipgloss.NewStyle().Foreground(th.columnColor(colColor(b, t.Column))).Bold(true).Render(col)}
	if t.Priority != priorityNone {
		meta = append(meta, lipgloss.NewStyle().Foreground(th.priorityColor(t.Priority)).Bold(true).
			Render(strings.TrimSpace(d.g.priority(t.Priority)+" "+t.Priority.String())))
	}
	if label, state := dueInfo(t, now); label != "" {
		meta = append(meta, lipgloss.NewStyle().Foreground(th.dueColor(state)).Render("due "+t.Due+" ("+label+")"))
	}
	if t.Assignee != "" {
		meta = append(meta, st.muted.Render("@"+t.Assignee))
	}
	for _, l := range t.Labels {
		meta = append(meta, st.chip.Render("+"+l))
	}
	out = append(out, wrap.Render(strings.Join(meta, st.muted.Render(" "+d.g.dot+" "))))
	out = append(out, st.muted.Render(fmt.Sprintf("created %s %s updated %s",
		relTime(t.CreatedAt, now), d.g.dot, relTime(t.UpdatedAt, now))))
	if t.Archived() {
		out = append(out, st.warning.Render("archived "+relTime(*t.ArchivedAt, now)))
	}
	out = append(out, "")

	if t.Description != "" {
		out = append(out, st.label.Render("Description"))
		out = append(out, wrap.Render(t.Description))
		out = append(out, "")
	}

	done, total := t.ChecklistProgress()
	title := "Checklist"
	if total > 0 {
		title = fmt.Sprintf("Checklist %d/%d", done, total)
	}
	out = append(out, st.label.Render(title))
	if total == 0 {
		out = append(out, st.muted.Render("  none · press t to add an item"))
	}
	for i, c := range t.Checklist {
		box := d.g.unchecked
		text := c.Text
		if c.Done {
			box = st.success.Render(d.g.checked)
			text = st.muted.Render(text)
		}
		line := "  " + box + " " + text
		if i == d.cursor {
			line = st.strong.Render("› ") + box + " " + text
		}
		out = append(out, wrap.Render(line))
	}
	out = append(out, "")

	out = append(out, st.label.Render("Attachments"))
	if len(t.Attachments) == 0 {
		out = append(out, st.muted.Render("  none · press A to attach a link or path"))
	}
	for i, a := range t.Attachments {
		line := "  " + st.chip.Render(a)
		if len(t.Checklist)+i == d.cursor {
			line = st.strong.Render("› ") + st.chip.Render(a)
		}
		out = append(out, wrap.Render(line))
	}
	out = append(out, "")

	if sim := similarTasks(b, t.Title, t.ID, 3); len(sim) > 0 {
		out = append(out, st.label.Render("Similar tasks"))
		for _, s := range sim {
			where := colName(b, s.Task.Column)
			if s.Task.Archived() {
				where = "archived"
			}
			out = append(out, wrap.Render(fmt.Sprintf("  %s %s %s", st.muted.Render(s.Task.Ref()), s.Task.Title, st.muted.Render("("+where+")"))))
		}
		out = append(out, "")
	}

	out = append(out, st.label.Render("Comments"))
	if len(t.Comments) == 0 {
		out = append(out, st.muted.Render("  none · press c to comment"))
	}
	for _, c := range t.Comments {
		out = append(out, st.muted.Render("  "+c.At.Format("2006-01-02 15:04")+" "+d.g.dot+" "+relTime(c.At, now)))
		out = append(out, wrap.Render("  "+c.Text))
	}
	out = append(out, "")

	if len(t.History) > 0 {
		out = append(out, st.label.Render("Activity"))
		for i := len(t.History) - 1; i >= 0; i-- {
			e := t.History[i]
			out = append(out, wrap.Render(st.muted.Render("  "+e.At.Format("2006-01-02 15:04")+"  ")+e.Text))
		}
	}
	d.vp.SetContent(strings.Join(out, "\n"))
}

func colName(b *Board, id string) string {
	if c := b.Column(id); c != nil {
		return c.Name
	}
	return id
}

func colColor(b *Board, id string) string {
	if c := b.Column(id); c != nil {
		return c.Color
	}
	return ""
}

func (d detailView) update(msg tea.Msg) (detailView, tea.Cmd) {
	var cmd tea.Cmd
	if k, ok := msg.(tea.KeyMsg); ok && !key.Matches(k, d.keys.Scroll) {
		return d, nil
	}
	d.vp, cmd = d.vp.Update(msg)
	return d, cmd
}

func (d detailView) view() string {
	footer := d.help.View(d.keys)
	body := lipgloss.JoinVertical(lipgloss.Left, d.vp.View(), "", footer)
	return d.st.dialog.Render(body)
}
