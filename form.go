package main

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type formMode int

const (
	formCreate formMode = iota
	formEdit
)

// formSubmitMsg is sent when the user saves the form.
type formSubmitMsg struct {
	task Task
	mode formMode
}

// formCancelMsg is sent when the user dismisses the form.
type formCancelMsg struct{}

const (
	fieldTitle = iota
	fieldDescription
	numFields
)

const (
	titleCharLimit       = 120
	descriptionCharLimit = 4000
	formMaxWidth         = 72
	formMinWidth         = 24
	formMaxDescHeight    = 10
	formMinDescHeight    = 2
)

// taskForm creates or edits a single task.
type taskForm struct {
	mode  formMode
	task  Task
	title textinput.Model
	desc  textarea.Model
	field int
	err   string
	keys  formKeyMap
	help  help.Model
}

func newTaskForm(mode formMode, t Task) taskForm {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "What needs to be done?"
	ti.CharLimit = titleCharLimit
	ti.SetValue(t.title)
	ti.CursorEnd()

	ta := textarea.New()
	ta.Prompt = ""
	ta.Placeholder = "Add some details (optional)"
	ta.ShowLineNumbers = false
	ta.CharLimit = descriptionCharLimit
	ta.SetValue(t.description)

	f := taskForm{
		mode:  mode,
		task:  t,
		title: ti,
		desc:  ta,
		keys:  formKeys,
		help:  help.New(),
	}
	f.title.Focus()
	f.setSize(formMaxWidth, formMaxDescHeight)
	return f
}

// Init starts the cursor blinking in the focused field.
func (f taskForm) Init() tea.Cmd {
	return textinput.Blink
}

// setSize sizes the inputs so the dialog fits in a width x height area.
func (f *taskForm) setSize(width, height int) {
	inner := width - dialogStyle.GetHorizontalFrameSize()
	inner = min(max(inner, formMinWidth), formMaxWidth)
	f.title.Width = inner - 1
	f.desc.SetWidth(inner)

	// Everything except the description is a fixed number of rows: the
	// heading, two labels, the title input, the footer and three spacers.
	const fixedRows = 8
	descHeight := height - dialogStyle.GetVerticalFrameSize() - fixedRows
	f.desc.SetHeight(min(max(descHeight, formMinDescHeight), formMaxDescHeight))
}

func (f taskForm) focusField(i int) (taskForm, tea.Cmd) {
	f.field = i
	f.title.Blur()
	f.desc.Blur()
	switch i {
	case fieldTitle:
		return f, f.title.Focus()
	default:
		return f, f.desc.Focus()
	}
}

func (f taskForm) Update(msg tea.Msg) (taskForm, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, f.keys.Cancel):
			return f, func() tea.Msg { return formCancelMsg{} }
		case key.Matches(msg, f.keys.Submit):
			return f.submit()
		case key.Matches(msg, f.keys.Next):
			return f.focusField((f.field + 1) % numFields)
		case key.Matches(msg, f.keys.Prev):
			return f.focusField((f.field + numFields - 1) % numFields)
		case msg.Type == tea.KeyEnter && f.field == fieldTitle:
			return f.focusField(fieldDescription)
		}
		var cmd tea.Cmd
		if f.field == fieldTitle {
			f.title, cmd = f.title.Update(msg)
		} else {
			f.desc, cmd = f.desc.Update(msg)
		}
		if f.err != "" && strings.TrimSpace(f.title.Value()) != "" {
			f.err = ""
		}
		return f, cmd
	}

	// Non-key messages (cursor blinks and the like) are addressed to a
	// specific component, so let both see them.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	f.title, cmd = f.title.Update(msg)
	cmds = append(cmds, cmd)
	f.desc, cmd = f.desc.Update(msg)
	cmds = append(cmds, cmd)
	return f, tea.Batch(cmds...)
}

func (f taskForm) submit() (taskForm, tea.Cmd) {
	title := strings.TrimSpace(f.title.Value())
	if title == "" {
		f.err = "A title is required."
		return f.focusField(fieldTitle)
	}
	t := f.task
	t.title = title
	t.description = strings.TrimSpace(f.desc.Value())
	t.updatedAt = time.Now()
	mode := f.mode
	return f, func() tea.Msg { return formSubmitMsg{task: t, mode: mode} }
}

func (f taskForm) heading() string {
	if f.mode == formEdit {
		return "Edit task"
	}
	return "New task"
}

func (f taskForm) View() string {
	titleLabel, descLabel := labelStyle, labelStyle
	if f.field == fieldTitle {
		titleLabel = focusedLabelStyle
	} else {
		descLabel = focusedLabelStyle
	}

	footer := f.help.View(f.keys)
	if f.err != "" {
		footer = errorStyle.Render(f.err)
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		dialogTitleStyle.Render(f.heading())+mutedStyle.Render(" · "+f.task.status.String()),
		"",
		titleLabel.Render("Title"),
		f.title.View(),
		"",
		descLabel.Render("Description"),
		f.desc.View(),
		"",
		footer,
	)
	return dialogStyle.Render(body)
}
