package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

// keyMap holds the board key bindings.
type KeyMap struct {
	Up, Down, Left, Right, Jump                            key.Binding
	New, Edit, View, Delete, Archive, Mark                 key.Binding
	MoveLeft, MoveRight, MoveUp, MoveDown                  key.Binding
	Search, Sort, Undo, Redo                               key.Binding
	Boards, ArchiveView, ArchiveDone, Reload, Save, Stats  key.Binding
	AddColumn, EditColumn, DeleteColumn, ColLeft, ColRight key.Binding
	Help, Quit, Back                                       key.Binding
}

// ShortHelp is the one-line help shown under the board.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.View, k.Edit, k.MoveRight, k.Search, k.Undo, k.Help, k.Quit}
}

// FullHelp is the expanded help toggled with "?".
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right, k.Jump, k.Back},
		{k.New, k.View, k.Edit, k.Delete, k.Archive, k.Mark},
		{k.MoveLeft, k.MoveRight, k.MoveUp, k.MoveDown, k.Search, k.Sort},
		{k.Undo, k.Redo, k.Boards, k.ArchiveView, k.ArchiveDone, k.Stats, k.Reload},
		{k.AddColumn, k.EditColumn, k.DeleteColumn, k.ColLeft, k.ColRight, k.Save},
		{k.Help, k.Quit},
	}
}

func bind(help, desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:           bind("↑/k", "up", "up", "k"),
		Down:         bind("↓/j", "down", "down", "j"),
		Left:         bind("←/h", "prev column", "left", "h"),
		Right:        bind("→/l", "next column", "right", "l"),
		Jump:         bind("1-9", "jump to column", "1", "2", "3", "4", "5", "6", "7", "8", "9"),
		Back:         bind("esc", "clear marks/search", "esc"),
		New:          bind("n", "new task", "n"),
		View:         bind("enter/v", "view", "enter", "v"),
		Edit:         bind("e", "edit", "e"),
		Delete:       bind("d", "delete", "d"),
		Archive:      bind("a", "archive", "a"),
		Mark:         bind("space", "mark", " "),
		MoveLeft:     bind("H/⇧←", "move left", "H", "shift+left", "["),
		MoveRight:    bind("L/⇧→", "move right", "L", "shift+right", "]"),
		MoveUp:       bind("K/⇧↑", "move up", "K", "shift+up"),
		MoveDown:     bind("J/⇧↓", "move down", "J", "shift+down"),
		Search:       bind("/", "search", "/"),
		Sort:         bind("s", "sort", "s"),
		Undo:         bind("u", "undo", "u"),
		Redo:         bind("U", "redo", "U", "ctrl+r"),
		Boards:       bind("b", "boards", "b"),
		ArchiveView:  bind("z", "archived tasks", "z"),
		ArchiveDone:  bind("Z", "archive all done", "Z"),
		Stats:        bind("S", "stats", "S"),
		Reload:       bind("R", "reload file", "R"),
		Save:         bind("ctrl+s", "write snapshot", "ctrl+s"),
		AddColumn:    bind("C", "add column", "C"),
		EditColumn:   bind("E", "edit column", "E"),
		DeleteColumn: bind("D", "delete column", "D"),
		ColLeft:      bind("<", "column left", "<"),
		ColRight:     bind(">", "column right", ">"),
		Help:         bind("?", "help", "?"),
		Quit:         bind("q", "quit", "q", "ctrl+c"),
	}
}

// actions maps config key names to bindings.
func (k *KeyMap) Actions() map[string]*key.Binding {
	return map[string]*key.Binding{
		"up": &k.Up, "down": &k.Down, "left": &k.Left, "right": &k.Right, "jump": &k.Jump, "back": &k.Back,
		"new": &k.New, "view": &k.View, "edit": &k.Edit, "delete": &k.Delete, "archive": &k.Archive, "mark": &k.Mark,
		"move_left": &k.MoveLeft, "move_right": &k.MoveRight, "move_up": &k.MoveUp, "move_down": &k.MoveDown,
		"search": &k.Search, "sort": &k.Sort, "undo": &k.Undo, "redo": &k.Redo,
		"boards": &k.Boards, "archive_view": &k.ArchiveView, "archive_done": &k.ArchiveDone, "reload": &k.Reload, "save": &k.Save, "stats": &k.Stats,
		"add_column": &k.AddColumn, "edit_column": &k.EditColumn, "delete_column": &k.DeleteColumn,
		"column_left": &k.ColLeft, "column_right": &k.ColRight,
		"help": &k.Help, "quit": &k.Quit,
	}
}

// actionNames lists the configurable actions in a stable order.
func ActionNames() []string {
	var k KeyMap
	names := make([]string, 0, 32)
	for n := range k.Actions() {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// applyKeyOverrides replaces the keys of any action named in overrides.
func (k *KeyMap) ApplyOverrides(overrides map[string][]string) error {
	acts := k.Actions()
	for action, keys := range overrides {
		b, ok := acts[action]
		if !ok {
			return fmt.Errorf("unknown key action %q", action)
		}
		if len(keys) == 0 {
			b.SetEnabled(false)
			continue
		}
		b.SetKeys(keys...)
		b.SetHelp(HelpLabel(keys), b.Help().Desc)
	}
	return nil
}

// helpLabel builds the key column of the help text from a key list.
func HelpLabel(keys []string) string {
	shown := keys
	if len(shown) > 2 {
		shown = shown[:2]
	}
	out := make([]string, len(shown))
	for i, k := range shown {
		switch k {
		case " ":
			out[i] = "space"
		default:
			out[i] = k
		}
	}
	return strings.Join(out, "/")
}

// formKeyMap holds the form key bindings.
type formKeyMap struct {
	Next, Prev, Submit, Cancel key.Binding
}

func (k formKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Next, k.Submit, k.Cancel}
}

func (k formKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Next, k.Prev}, {k.Submit, k.Cancel}}
}

var formKeys = formKeyMap{
	Next:   bind("tab", "next field", "tab"),
	Prev:   bind("shift+tab", "prev field", "shift+tab"),
	Submit: bind("ctrl+s", "save", "ctrl+s"),
	Cancel: bind("esc", "cancel", "esc"),
}

// detailKeyMap holds the task detail view bindings.
type detailKeyMap struct {
	Scroll, Item, Toggle, RemoveItem, AddItem, Comment, Attach, Open key.Binding
	Link, Go, Edit, MoveLeft, MoveRight, Archive, Delete, Back       key.Binding
}

func (k detailKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Edit, k.Comment, k.AddItem, k.Link, k.Go, k.Toggle, k.Back}
}

func (k detailKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Scroll, k.Item, k.Toggle, k.RemoveItem},
		{k.AddItem, k.Comment, k.Attach, k.Open},
		{k.Link, k.Go, k.Edit, k.MoveLeft, k.MoveRight, k.Archive, k.Delete, k.Back},
	}
}

var detailKeys = detailKeyMap{
	Scroll:     bind("j/k", "scroll", "j", "k", "down", "up", "pgdown", "pgup"),
	Item:       bind("tab", "next item", "tab", "shift+tab"),
	Toggle:     bind("space", "toggle item", " ", "x"),
	RemoveItem: bind("X", "remove item", "X"),
	AddItem:    bind("t", "add checklist item", "t"),
	Comment:    bind("c", "comment", "c"),
	Attach:     bind("A", "attach link", "A"),
	Open:       bind("o", "open attachment", "o"),
	Link:       bind("l", "link task", "l"),
	Go:         bind("g", "go to linked task", "g"),
	Edit:       bind("e", "edit", "e"),
	MoveLeft:   bind("H", "move left", "H", "shift+left", "["),
	MoveRight:  bind("L", "move right", "L", "shift+right", "]"),
	Archive:    bind("a", "archive", "a"),
	Delete:     bind("d", "delete", "d"),
	Back:       bind("esc", "back", "esc", "q"),
}

// confirmKeyMap holds the confirmation dialog bindings.
type confirmKeyMap struct {
	Yes, No key.Binding
}

func (k confirmKeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Yes, k.No} }
func (k confirmKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Yes, k.No}}
}

var confirmKeys = confirmKeyMap{
	Yes: bind("y/enter", "confirm", "y", "enter"),
	No:  bind("n/esc", "cancel", "n", "esc"),
}

// pickerKeyMap holds the board picker / archive list bindings.
type pickerKeyMap struct {
	Select, New, Rename, Describe, Delete, Restore, Back key.Binding
}

func (k pickerKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Select, k.New, k.Rename, k.Describe, k.Delete, k.Back}
}

func (k pickerKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Select, k.New, k.Rename, k.Describe}, {k.Delete, k.Restore, k.Back}}
}

var pickerKeys = pickerKeyMap{
	Select:   bind("enter", "open", "enter"),
	New:      bind("n", "new board", "n"),
	Rename:   bind("r", "rename", "r"),
	Describe: bind("e", "describe", "e"),
	Delete:   bind("d", "delete", "d"),
	Restore:  bind("enter", "restore", "enter"),
	Back:     bind("esc", "back", "esc", "q"),
}
