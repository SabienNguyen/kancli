package ui

import (
	"fmt"

	"github.com/SabienNguyen/kancli/internal/board"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- prompt ---------------------------------------------------------------

// promptKind says what a single-line prompt is collecting.
type promptKind int

const (
	promptNewBoard promptKind = iota
	promptRenameBoard
	promptComment
	promptChecklistItem
	promptAttachment
	promptLink
)

// promptSubmitMsg carries the text entered into a prompt.
type promptSubmitMsg struct {
	kind promptKind
	text string
	ref  int    // task id for task prompts
	sref string // board id for board prompts
}

// prompt is a one-line text dialog.
type prompt struct {
	kind  promptKind
	title string
	input textinput.Model
	ref   int
	sref  string
	st    Styles
}

func newPrompt(kind promptKind, title, initial string, st Styles) prompt {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.PromptStyle = st.searchPrompt
	ti.CharLimit = 500
	ti.Width = 60
	ti.PlaceholderStyle = st.muted
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.Focus()
	return prompt{kind: kind, title: title, input: ti, st: st}
}

func (p prompt) Init() tea.Cmd { return textinput.Blink }

// setSize keeps the dialog inside a terminal of the given width.
func (p *prompt) setSize(width int) {
	inner := width - p.st.dialog.GetHorizontalFrameSize() - lipgloss.Width(p.input.Prompt) - 1
	p.input.Width = min(60, max(10, inner))
}

func (p prompt) Update(msg tea.Msg) (prompt, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyEsc:
			return p, func() tea.Msg { return formCancelMsg{} }
		case tea.KeyEnter:
			m := promptSubmitMsg{kind: p.kind, text: p.input.Value(), ref: p.ref, sref: p.sref}
			return p, func() tea.Msg { return m }
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

func (p prompt) View() string {
	body := lipgloss.JoinVertical(lipgloss.Left,
		p.st.dialogTitle.Render(p.title),
		"",
		p.input.View(),
		"",
		p.st.muted.Render("enter to save • esc to cancel"),
	)
	return p.st.dialog.Render(body)
}

// --- confirm --------------------------------------------------------------

// confirmKind says what a yes/no dialog will do.
type confirmKind int

const (
	confirmDeleteTasks confirmKind = iota
	confirmDeleteColumn
	confirmDeleteBoard
	confirmArchiveDone
	confirmDeleteArchived
)

// confirmDialog asks a yes/no question.
type confirmDialog struct {
	kind    confirmKind
	title   string
	lines   []string
	taskIDs []int
	sref    string
	st      Styles
	help    help.Model
}

func newConfirm(kind confirmKind, title string, st Styles, lines ...string) confirmDialog {
	return confirmDialog{kind: kind, title: title, lines: lines, st: st, help: help.New()}
}

func (c confirmDialog) View() string {
	parts := []string{c.st.dialogTitle.Render(c.title), ""}
	parts = append(parts, c.lines...)
	parts = append(parts, "", c.help.ShortHelpView(confirmKeys.ShortHelp()))
	return c.st.dialog.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// --- picker ---------------------------------------------------------------

// pickerKind says what a picker lists.
type pickerKind int

const (
	pickerBoards pickerKind = iota
	pickerArchive
)

// pickItem is a generic list row.
type pickItem struct {
	id    string
	num   int
	title string
	desc  string
}

func (p pickItem) Title() string       { return p.title }
func (p pickItem) Description() string { return p.desc }
func (p pickItem) FilterValue() string { return p.title }

// picker is a full-screen list used for boards and archived tasks.
type picker struct {
	kind pickerKind
	list list.Model
	st   Styles
	keys pickerKeyMap
	help help.Model
}

func newPicker(kind pickerKind, title string, items []pickItem, st Styles) picker {
	d := list.NewDefaultDelegate()
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(st.th.accent).BorderForeground(st.th.accent)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.Foreground(st.th.accent).BorderForeground(st.th.accent)
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	l := list.New(li, d, 0, 0)
	l.Title = title
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()
	l.KeyMap.NextPage = bind("pgdn", "next page", "pgdown")
	l.KeyMap.PrevPage = bind("pgup", "prev page", "pgup")
	l.KeyMap.ShowFullHelp.SetEnabled(false)
	l.KeyMap.CloseFullHelp.SetEnabled(false)
	l.Styles.Title = lipgloss.NewStyle().Padding(0, 1).Bold(true).Background(st.th.accent).Foreground(st.th.onColor)
	if st.th.mono {
		l.Styles.Title = lipgloss.NewStyle().Padding(0, 1).Bold(true).Reverse(true)
	}
	l.Styles.NoItems = st.muted.Padding(0, 0, 0, 2)
	if kind == pickerArchive {
		l.SetStatusBarItemName("archived task", "archived tasks")
	} else {
		l.SetStatusBarItemName("board", "boards")
	}
	return picker{kind: kind, list: l, st: st, keys: pickerKeys, help: help.New()}
}

func (p *picker) setSize(width, height int) {
	frame := p.st.column.GetHorizontalFrameSize()
	p.list.SetSize(max(10, width-frame), max(5, height-p.st.column.GetVerticalFrameSize()-2))
	p.help.Width = width - frame
}

func (p picker) selected() (pickItem, bool) {
	it, ok := p.list.SelectedItem().(pickItem)
	return it, ok
}

func (p picker) filtering() bool { return p.list.SettingFilter() }

func (p picker) update(msg tea.Msg) (picker, tea.Cmd) {
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return p, cmd
}

func (p picker) view() string {
	var bindings []key.Binding
	if p.kind == pickerBoards {
		bindings = []key.Binding{p.keys.Select, p.keys.New, p.keys.Rename, p.keys.Delete, p.keys.Back}
	} else {
		bindings = []key.Binding{p.keys.Restore, p.keys.Delete, p.keys.Back}
	}
	footer := p.help.ShortHelpView(bindings)
	body := lipgloss.JoinVertical(lipgloss.Left, p.list.View(), "", footer)
	s := p.st.column.BorderForeground(p.st.th.accent)
	return s.Render(body)
}

func boardItems(f *board.File) []pickItem {
	items := make([]pickItem, 0, len(f.Boards))
	for _, b := range f.Boards {
		live := len(b.Live())
		desc := fmt.Sprintf("%d task%s, %d column%s", live, board.Plural(live), len(b.Columns), board.Plural(len(b.Columns)))
		if b.ID == f.ActiveBoard {
			desc += " (current)"
		}
		items = append(items, pickItem{id: b.ID, title: b.Name, desc: desc})
	}
	return items
}

func archiveItems(b *board.Board, g Glyphs) []pickItem {
	var items []pickItem
	for _, t := range b.ArchivedTasks() {
		col := t.Column
		if c := b.Column(t.Column); c != nil {
			col = c.Name
		}
		items = append(items, pickItem{
			num:   t.ID,
			title: t.Ref() + " " + t.Title,
			desc:  "archived " + board.RelTime(*t.ArchivedAt, board.Now()) + " " + g.dot + " from " + col,
		})
	}
	return items
}
