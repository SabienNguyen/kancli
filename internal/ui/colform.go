package ui

import (
	"strconv"
	"strings"

	"github.com/SabienNguyen/kancli/internal/board"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// colFormSubmitMsg is sent when the column form is saved.
type colFormSubmitMsg struct {
	id    string // empty when creating
	name  string
	color string
	wip   int
}

const (
	colFieldName = iota
	colFieldColor
	colFieldWIP
	numColFields
)

// columnForm creates or edits a column.
type columnForm struct {
	id       string
	name     textinput.Model
	colorIdx int
	custom   string // colour not in the palette (kept as-is)
	wip      textinput.Model
	field    int
	err      string
	st       Styles
	keys     formKeyMap
	help     help.Model
}

func newColumnForm(col *board.Column, st Styles) columnForm {
	name := textinput.New()
	name.Prompt = ""
	name.Placeholder = "Column name"
	name.CharLimit = 40
	name.PlaceholderStyle = st.muted
	wip := textinput.New()
	wip.Prompt = ""
	wip.Placeholder = "0 = no limit"
	wip.CharLimit = 4
	wip.PlaceholderStyle = st.muted
	f := columnForm{name: name, wip: wip, st: st, keys: formKeys, help: help.New(), colorIdx: -1}
	if col != nil {
		f.id = col.ID
		f.name.SetValue(col.Name)
		if col.WIPLimit > 0 {
			f.wip.SetValue(strconv.Itoa(col.WIPLimit))
		}
		for i, c := range board.ColumnPalette {
			if c == col.Color {
				f.colorIdx = i
			}
		}
		if f.colorIdx < 0 {
			f.custom = col.Color
		}
	}
	f.name.CursorEnd()
	f.name.Focus()
	f.name.Width = 40
	f.wip.Width = 12
	return f
}

func (f columnForm) Init() tea.Cmd { return textinput.Blink }

func (f columnForm) color() string {
	if f.colorIdx >= 0 && f.colorIdx < len(board.ColumnPalette) {
		return board.ColumnPalette[f.colorIdx]
	}
	return f.custom
}

func (f columnForm) focusField(i int) (columnForm, tea.Cmd) {
	f.field = (i + numColFields) % numColFields
	f.name.Blur()
	f.wip.Blur()
	switch f.field {
	case colFieldName:
		return f, f.name.Focus()
	case colFieldWIP:
		return f, f.wip.Focus()
	}
	return f, nil
}

func (f columnForm) Update(msg tea.Msg) (columnForm, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, f.keys.Cancel):
			return f, func() tea.Msg { return formCancelMsg{} }
		case key.Matches(msg, f.keys.Submit):
			return f.submit()
		case key.Matches(msg, f.keys.Next):
			return f.focusField(f.field + 1)
		case key.Matches(msg, f.keys.Prev):
			return f.focusField(f.field - 1)
		case msg.Type == tea.KeyEnter:
			if f.field == numColFields-1 {
				return f.submit()
			}
			return f.focusField(f.field + 1)
		}
		if f.field == colFieldColor {
			n := len(board.ColumnPalette)
			switch msg.String() {
			case "left", "h", "-":
				f.colorIdx = (f.colorIdx + n - 1 + n) % n
				f.custom = ""
			case "right", "l", "+", " ", "=":
				f.colorIdx = (f.colorIdx + 1) % n
				f.custom = ""
			}
			return f, nil
		}
		var cmd tea.Cmd
		if f.field == colFieldName {
			f.name, cmd = f.name.Update(msg)
		} else {
			f.wip, cmd = f.wip.Update(msg)
		}
		f.err = ""
		return f, cmd
	}
	var cmds []tea.Cmd
	var cmd tea.Cmd
	f.name, cmd = f.name.Update(msg)
	cmds = append(cmds, cmd)
	f.wip, cmd = f.wip.Update(msg)
	cmds = append(cmds, cmd)
	return f, tea.Batch(cmds...)
}

func (f columnForm) submit() (columnForm, tea.Cmd) {
	name := strings.TrimSpace(f.name.Value())
	if name == "" {
		f.err = "A name is required."
		return f.focusField(colFieldName)
	}
	wip := 0
	if v := strings.TrimSpace(f.wip.Value()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			f.err = "The WIP limit must be a whole number."
			return f.focusField(colFieldWIP)
		}
		wip = n
	}
	msg := colFormSubmitMsg{id: f.id, name: name, color: f.color(), wip: wip}
	return f, func() tea.Msg { return msg }
}

func (f columnForm) View() string {
	label := func(field int, text string) string {
		if f.field == field {
			return f.st.focusedLabel.Render(text)
		}
		return f.st.label.Render(text)
	}
	heading := "New column"
	if f.id != "" {
		heading = "Edit column"
	}
	var swatches []string
	for i, c := range board.ColumnPalette {
		s := lipgloss.NewStyle().Background(lipgloss.Color(c)).Foreground(f.st.th.onColor)
		if f.st.th.mono {
			s = lipgloss.NewStyle()
		}
		if i == f.colorIdx {
			swatches = append(swatches, s.Bold(true).Render(" ● "))
		} else {
			swatches = append(swatches, s.Render("   "))
		}
	}
	colorLine := strings.Join(swatches, " ")
	if f.custom != "" {
		colorLine = lipgloss.NewStyle().Background(lipgloss.Color(f.custom)).Render("   ") + f.st.muted.Render("  custom "+f.custom+"  →/← to pick")
	} else if f.field == colFieldColor {
		colorLine += f.st.muted.Render("  ←/→")
	}
	footer := f.help.View(f.keys)
	if f.err != "" {
		footer = f.st.err.Render(f.err)
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		f.st.dialogTitle.Render(heading),
		"",
		label(colFieldName, "Name"),
		f.name.View(),
		"",
		label(colFieldColor, "Colour"),
		colorLine,
		"",
		label(colFieldWIP, "WIP limit")+"  "+f.wip.View(),
		"",
		footer,
	)
	return f.st.dialog.Render(body)
}
