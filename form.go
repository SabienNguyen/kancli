package main

import (
	"fmt"
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

// formSubmitMsg is sent when the user saves the task form.
type formSubmitMsg struct {
	task Task
	mode formMode
}

// formCancelMsg is sent when the user dismisses a form or dialog.
type formCancelMsg struct{}

const (
	fieldTitle = iota
	fieldDescription
	fieldPriority
	fieldDue
	fieldLabels
	fieldAssignee
	numFields
)

const (
	titleCharLimit       = 200
	descriptionCharLimit = 8000
	formMaxWidth         = 76
	formMinWidth         = 30
	formMaxDescHeight    = 8
	formMinDescHeight    = 1
	// formFixedRows is every row of the dialog except the description box.
	formFixedRows = 15
)

// taskForm creates or edits a single task.
type taskForm struct {
	mode     formMode
	task     Task
	colName  string
	title    textinput.Model
	desc     textarea.Model
	priority Priority
	due      textinput.Model
	labels   textinput.Model
	assignee textinput.Model
	field    int
	err      string
	st       styles
	g        glyphs
	keys     formKeyMap
	help     help.Model
}

func newTaskForm(mode formMode, t Task, colName string, st styles, g glyphs) taskForm {
	input := func(placeholder, value string, limit int) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.CharLimit = limit
		ti.SetValue(value)
		ti.CursorEnd()
		ti.PlaceholderStyle = st.muted
		return ti
	}
	ta := textarea.New()
	ta.Prompt = ""
	ta.Placeholder = "Add some details (optional)"
	ta.ShowLineNumbers = false
	ta.CharLimit = descriptionCharLimit
	ta.SetValue(t.Description)

	f := taskForm{
		mode:     mode,
		task:     t,
		colName:  colName,
		title:    input("What needs to be done?", t.Title, titleCharLimit),
		desc:     ta,
		priority: t.Priority,
		due:      input("today, fri, +3d, 2026-09-10", t.Due, 32),
		labels:   input("comma, separated", strings.Join(t.Labels, ", "), 200),
		assignee: input("who", t.Assignee, 60),
		st:       st,
		g:        g,
		keys:     formKeys,
		help:     help.New(),
	}
	f.title.Focus()
	f.setSize(formMaxWidth, 40)
	return f
}

// Init starts the cursor blinking in the focused field.
func (f taskForm) Init() tea.Cmd {
	return textinput.Blink
}

// setSize sizes the inputs so the dialog fits in a width x height area.
func (f *taskForm) setSize(width, height int) {
	inner := width - f.st.dialog.GetHorizontalFrameSize()
	inner = min(max(inner, formMinWidth), formMaxWidth)
	f.title.Width = inner - 1
	// Inline fields share their row with a 10-column label.
	for _, ti := range []*textinput.Model{&f.labels, &f.assignee} {
		ti.Width = max(8, inner-11)
	}
	// The due field is short so its parsed preview fits beside it.
	f.due.Width = min(28, max(8, inner-11))
	f.desc.SetWidth(inner)
	descHeight := height - f.st.dialog.GetVerticalFrameSize() - formFixedRows
	f.desc.SetHeight(min(max(descHeight, formMinDescHeight), formMaxDescHeight))
}

func (f taskForm) focusField(i int) (taskForm, tea.Cmd) {
	f.field = (i + numFields) % numFields
	f.title.Blur()
	f.desc.Blur()
	f.due.Blur()
	f.labels.Blur()
	f.assignee.Blur()
	switch f.field {
	case fieldTitle:
		return f, f.title.Focus()
	case fieldDescription:
		return f, f.desc.Focus()
	case fieldDue:
		return f, f.due.Focus()
	case fieldLabels:
		return f, f.labels.Focus()
	case fieldAssignee:
		return f, f.assignee.Focus()
	}
	return f, nil
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
			return f.focusField(f.field + 1)
		case key.Matches(msg, f.keys.Prev):
			return f.focusField(f.field - 1)
		case msg.Type == tea.KeyEnter && f.field != fieldDescription:
			// Enter walks through the fields; on the last one it saves.
			if f.field == numFields-1 {
				return f.submit()
			}
			return f.focusField(f.field + 1)
		}
		if f.field == fieldPriority {
			switch msg.String() {
			case "left", "h", "-", "shift+tab":
				f.priority = (f.priority + numPriorities - 1) % numPriorities
			case "right", "l", "+", " ", "=":
				f.priority = (f.priority + 1) % numPriorities
			case "0", "1", "2", "3", "4":
				f.priority = Priority(msg.String()[0] - '0')
			}
			return f, nil
		}
		var cmd tea.Cmd
		switch f.field {
		case fieldTitle:
			f.title, cmd = f.title.Update(msg)
		case fieldDescription:
			f.desc, cmd = f.desc.Update(msg)
		case fieldDue:
			f.due, cmd = f.due.Update(msg)
		case fieldLabels:
			f.labels, cmd = f.labels.Update(msg)
		case fieldAssignee:
			f.assignee, cmd = f.assignee.Update(msg)
		}
		if f.err != "" {
			f.err = ""
		}
		return f, cmd
	}

	// Non-key messages (cursor blinks) are addressed to a specific
	// component, so let every input see them.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	f.title, cmd = f.title.Update(msg)
	cmds = append(cmds, cmd)
	f.desc, cmd = f.desc.Update(msg)
	cmds = append(cmds, cmd)
	f.due, cmd = f.due.Update(msg)
	cmds = append(cmds, cmd)
	f.labels, cmd = f.labels.Update(msg)
	cmds = append(cmds, cmd)
	f.assignee, cmd = f.assignee.Update(msg)
	cmds = append(cmds, cmd)
	return f, tea.Batch(cmds...)
}

func (f taskForm) submit() (taskForm, tea.Cmd) {
	title := strings.TrimSpace(f.title.Value())
	if title == "" {
		f.err = "A title is required."
		return f.focusField(fieldTitle)
	}
	due, err := parseDue(f.due.Value(), timeNow())
	if err != nil {
		f.err = err.Error()
		return f.focusField(fieldDue)
	}
	t := f.task
	t.Title = title
	t.Description = strings.TrimSpace(f.desc.Value())
	t.Priority = f.priority
	t.Due = due
	t.Labels = parseLabels(f.labels.Value())
	t.Assignee = strings.TrimSpace(f.assignee.Value())
	mode := f.mode
	return f, func() tea.Msg { return formSubmitMsg{task: t, mode: mode} }
}

func (f taskForm) heading() string {
	if f.mode == formEdit {
		return "Edit " + f.task.Ref()
	}
	return "New task"
}

func (f taskForm) labelFor(field int, text string) string {
	if f.field == field {
		return f.st.focusedLabel.Render(text)
	}
	return f.st.label.Render(text)
}

func (f taskForm) View() string {
	footer := f.help.View(f.keys)
	if f.err != "" {
		footer = f.st.err.Render(f.err)
	}

	// Priority selector.
	var prio []string
	for p := Priority(0); p < numPriorities; p++ {
		name := p.String()
		if p == f.priority {
			s := lipgloss.NewStyle().Bold(true).Foreground(f.st.th.priorityColor(p))
			if f.st.th.mono {
				s = s.Reverse(true)
			}
			prio = append(prio, s.Render("["+name+"]"))
		} else {
			prio = append(prio, f.st.muted.Render(" "+name+" "))
		}
	}
	prioLine := strings.Join(prio, "")
	if f.field == fieldPriority {
		prioLine += f.st.muted.Render("  ←/→ to change")
	}

	// Due date preview.
	dueLine := f.due.View()
	if v := strings.TrimSpace(f.due.Value()); v != "" {
		if d, err := parseDue(v, timeNow()); err != nil {
			dueLine += "  " + f.st.err.Render("?")
		} else if d != "" {
			t, _ := time.ParseInLocation(dateLayout, d, time.Local)
			label, state := dueInfo(Task{Due: d}, timeNow())
			dueLine += "  " + lipgloss.NewStyle().Foreground(f.st.th.dueColor(state)).
				Render(fmt.Sprintf("%s (%s)", t.Format("Mon Jan 2"), label))
		}
	}

	inner := f.desc.Width()
	clip := lipgloss.NewStyle().MaxWidth(inner)
	prioLine = clip.Render(prioLine)
	dueLine = clip.Render(dueLine)
	body := lipgloss.JoinVertical(lipgloss.Left,
		clip.Render(f.st.dialogTitle.Render(f.heading())+f.st.muted.Render(" "+f.g.dot+" "+f.colName)),
		"",
		f.labelFor(fieldTitle, "Title"),
		f.title.View(),
		"",
		f.labelFor(fieldDescription, "Description"),
		f.desc.View(),
		"",
		clip.Render(f.labelFor(fieldPriority, "Priority")+"  "+prioLine),
		clip.Render(f.labelFor(fieldDue, "Due")+"       "+dueLine),
		f.labelFor(fieldLabels, "Labels")+"    "+f.labels.View(),
		f.labelFor(fieldAssignee, "Assignee")+"  "+f.assignee.View(),
		"",
		clip.Render(footer),
	)
	return f.st.dialog.Render(body)
}
