package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
	"github.com/SabienNguyen/kancli/internal/kitty"

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
	links  []board.Relation
	vp     viewport.Model
	cursor int // index into checklist + attachments, -1 for none
	st     Styles
	g      Glyphs
	keys   detailKeyMap
	help   help.Model
	width  int
	height int

	md     *markdown
	images bool                    // inline previews of image attachments
	pics   map[string]*kitty.Image // decoded previews by path and width
	picErr map[string]string
}

func newDetailView(id int, st Styles, g Glyphs, images bool) detailView {
	return detailView{taskID: id, vp: viewport.New(0, 0), cursor: -1, st: st, g: g, keys: detailKeys, help: help.New(),
		md: &markdown{mono: st.th.mono}, images: images, pics: map[string]*kitty.Image{}, picErr: map[string]string{}}
}

// preview returns the placeholder lines for an image attachment, loading
// and caching it on first use.
func (d *detailView) preview(ref string, width int) ([]string, string) {
	key := fmt.Sprintf("%s@%d", ref, width)
	if msg, ok := d.picErr[key]; ok {
		return nil, msg
	}
	im, ok := d.pics[key]
	if !ok {
		var err error
		im, err = kitty.Load(ref, min(width-2, 60), 12)
		if err != nil {
			d.picErr[key] = err.Error()
			return nil, err.Error()
		}
		d.pics[key] = im
	}
	lines := im.Lines()
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return lines, ""
}

func (d *detailView) setSize(width, height int) {
	d.width, d.height = width, height
	frame := d.st.dialog.GetHorizontalFrameSize()
	d.vp.Width = max(10, min(width, 100)-frame)
	d.vp.Height = max(3, height-d.st.dialog.GetVerticalFrameSize()-2)
	d.help.Width = d.vp.Width
}

// itemCount is the number of selectable checklist, attachment and link
// rows. The board is needed because incoming links live on other tasks.
func (d detailView) itemCount(t board.Task) int {
	return len(t.Checklist) + len(t.Attachments) + len(d.links)
}

// linkAt returns the relation under the cursor, if the cursor is on one.
func (d detailView) linkAt(t board.Task) (board.Relation, bool) {
	i := d.cursor - len(t.Checklist) - len(t.Attachments)
	if i < 0 || i >= len(d.links) {
		return board.Relation{}, false
	}
	return d.links[i], true
}

// moveCursor steps through checklist items and attachments.
func (d *detailView) moveCursor(t board.Task, delta int) {
	n := d.itemCount(t)
	if n == 0 {
		d.cursor = -1
		return
	}
	d.cursor = ((d.cursor+delta)%n + n) % n
}

// render rebuilds the viewport content for a task.
func (d *detailView) render(t board.Task, b *board.Board, now time.Time) {
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
	if t.Priority != board.PriorityNone {
		meta = append(meta, lipgloss.NewStyle().Foreground(th.priorityColor(t.Priority)).Bold(true).
			Render(strings.TrimSpace(d.g.priority(t.Priority)+" "+t.Priority.String())))
	}
	if label, state := board.DueInfo(t, now); label != "" {
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
		board.RelTime(t.CreatedAt, now), d.g.dot, board.RelTime(t.UpdatedAt, now))))
	if t.Archived() {
		out = append(out, st.warning.Render("archived "+board.RelTime(*t.ArchivedAt, now)))
	}
	out = append(out, "")

	if t.Description != "" {
		out = append(out, st.label.Render("Description"))
		out = append(out, d.md.render(t.Description, w))
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
		if d.images && kitty.IsImage(a) {
			lines, errMsg := d.preview(a, w)
			if errMsg != "" {
				out = append(out, st.muted.Render("    (no preview: "+errMsg+")"))
			}
			out = append(out, lines...)
		}
	}
	out = append(out, "")

	d.links = b.Relations(t.ID)
	out = append(out, st.label.Render("Links"))
	if len(d.links) == 0 {
		out = append(out, st.muted.Render("  none · press l to link (blocks, blocked-by, subtask-of, parent-of, relates)"))
	}
	for i, r := range d.links {
		where := board.ColName(b, r.Task.Column)
		if r.Task.Archived() {
			where = "archived"
		}
		label := st.muted.Render(r.Label)
		if r.Label == "blocked by" && !r.Task.Archived() && !isDone(b, r.Task) {
			label = st.err.Render(r.Label)
		}
		line := fmt.Sprintf("  %s %s %s %s", label, st.muted.Render(r.Task.Ref()), r.Task.Title, st.muted.Render("("+where+")"))
		if len(t.Checklist)+len(t.Attachments)+i == d.cursor {
			line = st.strong.Render("› ") + strings.TrimPrefix(line, "  ")
		}
		out = append(out, wrap.Render(line))
	}
	if done, total := b.SubtaskProgress(t.ID); total > 0 {
		out = append(out, st.muted.Render(fmt.Sprintf("  %d of %d subtask%s finished", done, total, board.Plural(total))))
	}
	out = append(out, "")

	if sim := board.SimilarTasks(b, t.Title, t.ID, 3); len(sim) > 0 {
		out = append(out, st.label.Render("Similar tasks"))
		for _, s := range sim {
			where := board.ColName(b, s.Task.Column)
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
		out = append(out, st.muted.Render("  "+c.At.Format("2006-01-02 15:04")+" "+d.g.dot+" "+board.RelTime(c.At, now)))
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

func isDone(b *board.Board, t board.Task) bool {
	done := b.DoneColumn()
	return done != nil && t.Column == done.ID
}

func colColor(b *board.Board, id string) string {
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
