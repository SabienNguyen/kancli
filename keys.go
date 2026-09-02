package main

import "github.com/charmbracelet/bubbles/key"

// keyMap holds the board key bindings.
type keyMap struct {
	Up        key.Binding
	Down      key.Binding
	Left      key.Binding
	Right     key.Binding
	Jump      key.Binding
	New       key.Binding
	Edit      key.Binding
	Delete    key.Binding
	MoveLeft  key.Binding
	MoveRight key.Binding
	MoveUp    key.Binding
	MoveDown  key.Binding
	Filter    key.Binding
	Help      key.Binding
	Quit      key.Binding
}

// ShortHelp is the one-line help shown under the board.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.Edit, k.Delete, k.MoveRight, k.Filter, k.Help, k.Quit}
}

// FullHelp is the expanded help toggled with "?".
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right, k.Jump},
		{k.New, k.Edit, k.Delete, k.Filter},
		{k.MoveLeft, k.MoveRight, k.MoveUp, k.MoveDown},
		{k.Help, k.Quit},
	}
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Left: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("←/h", "prev column"),
	),
	Right: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("→/l", "next column"),
	),
	Jump: key.NewBinding(
		key.WithKeys("1", "2", "3"),
		key.WithHelp("1-3", "jump to column"),
	),
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e", "enter"),
		key.WithHelp("e/enter", "edit"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
	MoveLeft: key.NewBinding(
		key.WithKeys("H", "shift+left", "["),
		key.WithHelp("H/⇧←", "move left"),
	),
	MoveRight: key.NewBinding(
		key.WithKeys("L", "shift+right", "]"),
		key.WithHelp("L/⇧→", "move right"),
	),
	MoveUp: key.NewBinding(
		key.WithKeys("K", "shift+up"),
		key.WithHelp("K/⇧↑", "move up"),
	),
	MoveDown: key.NewBinding(
		key.WithKeys("J", "shift+down"),
		key.WithHelp("J/⇧↓", "move down"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}

// formKeyMap holds the task form key bindings.
type formKeyMap struct {
	Next   key.Binding
	Prev   key.Binding
	Submit key.Binding
	Cancel key.Binding
}

// ShortHelp is the help line shown under the form.
func (k formKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Next, k.Submit, k.Cancel}
}

// FullHelp is unused by the form but required by help.KeyMap.
func (k formKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Next, k.Prev}, {k.Submit, k.Cancel}}
}

var formKeys = formKeyMap{
	Next: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	Prev: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "prev field"),
	),
	Submit: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "save"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	),
}

// confirmKeyMap holds the delete confirmation bindings.
type confirmKeyMap struct {
	Yes key.Binding
	No  key.Binding
}

// ShortHelp is the help line shown under the confirmation dialog.
func (k confirmKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Yes, k.No}
}

// FullHelp is unused by the dialog but required by help.KeyMap.
func (k confirmKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Yes, k.No}}
}

var confirmKeys = confirmKeyMap{
	Yes: key.NewBinding(
		key.WithKeys("y", "enter"),
		key.WithHelp("y/enter", "delete"),
	),
	No: key.NewBinding(
		key.WithKeys("n", "esc"),
		key.WithHelp("n/esc", "keep"),
	),
}
