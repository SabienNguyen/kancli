package board

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// fileVersion is bumped whenever the on-disk format changes incompatibly.
const FileVersion = 2

// File is the whole data file: every board plus which one is active.
type File struct {
	Version     int       `json:"version"`
	ActiveBoard string    `json:"active_board"`
	Boards      []*Board  `json:"boards"`
	LastSeq     int64     `json:"last_seq,omitempty"`   // last event folded into this snapshot
	SnapshotAt  time.Time `json:"snapshot_at,omitzero"` // when the snapshot was written

	rec *recorder
}

// Board is one kanban board with its columns and tasks.
type Board struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Columns     []Column `json:"columns"`
	Tasks       []Task   `json:"tasks"`
	NextID      int      `json:"next_id"`

	rec   *recorder
	clock func() time.Time
	// stamp is the clock reading of the mutation in progress, so the event
	// it emits carries exactly the time written into the task.
	stamp time.Time

	// byID maps task id to position in Tasks. It is rebuilt whenever gen
	// changes; mutations bump gen with touch().
	byID    map[int]int
	byIDGen uint64
	gen     uint64
}

// touch marks the task slice as changed so the id index is rebuilt.
func (b *Board) touch() { b.gen++ }

// reindex rebuilds the id index.
func (b *Board) reindex() {
	b.byID = make(map[int]int, len(b.Tasks))
	for i := range b.Tasks {
		b.byID[b.Tasks[i].ID] = i
	}
	b.byIDGen = b.gen
}

// Column is one lane on a board. The last column is treated as "done".
type Column struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color,omitempty"`
	WIPLimit int    `json:"wip_limit,omitempty"`
}

// Priority orders tasks from none (0) to urgent.
type Priority int

const (
	PriorityNone Priority = iota
	PriorityLow
	PriorityMedium
	PriorityHigh
	PriorityUrgent
	NumPriorities
)

var priorityNames = [NumPriorities]string{"none", "low", "medium", "high", "urgent"}

func (p Priority) String() string {
	if p < 0 || p >= NumPriorities {
		return "none"
	}
	return priorityNames[p]
}

func ParsePriority(s string) (Priority, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, n := range priorityNames {
		if n == s || (s != "" && strings.HasPrefix(n, s)) {
			return Priority(i), nil
		}
	}
	return PriorityNone, fmt.Errorf("unknown priority %q (use none, low, medium, high or urgent)", s)
}

// MarshalText implements encoding.TextMarshaler.
func (p Priority) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (p *Priority) UnmarshalText(b []byte) error {
	if len(strings.TrimSpace(string(b))) == 0 {
		*p = PriorityNone
		return nil
	}
	v, err := ParsePriority(string(b))
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// ChecklistItem is one line of a task's checklist.
type ChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Comment is a timestamped note on a task.
type Comment struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// HistoryEntry is one line of a task's activity history.
type HistoryEntry struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Task is a single card.
type Task struct {
	ID          int             `json:"id"`
	Column      string          `json:"column"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Priority    Priority        `json:"priority,omitempty"`
	Due         string          `json:"due,omitempty"` // YYYY-MM-DD
	Labels      []string        `json:"labels,omitempty"`
	Assignee    string          `json:"assignee,omitempty"`
	Checklist   []ChecklistItem `json:"checklist,omitempty"`
	Attachments []string        `json:"attachments,omitempty"`
	Links       []Link          `json:"links,omitempty"`
	Comments    []Comment       `json:"comments,omitempty"`
	History     []HistoryEntry  `json:"history,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	ArchivedAt  *time.Time      `json:"archived_at,omitempty"`
}

// Archived reports whether the task has been archived off the board.
func (t Task) Archived() bool { return t.ArchivedAt != nil }

// DueDate parses the task's due date, if it has one.
func (t Task) DueDate() (time.Time, bool) {
	if t.Due == "" {
		return time.Time{}, false
	}
	d, err := time.ParseInLocation(DateLayout, t.Due, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}

// ChecklistProgress returns done and total checklist counts.
func (t Task) ChecklistProgress() (int, int) {
	done := 0
	for _, c := range t.Checklist {
		if c.Done {
			done++
		}
	}
	return done, len(t.Checklist)
}

// Ref is the task's user-visible reference, e.g. "#12".
func (t Task) Ref() string { return fmt.Sprintf("#%d", t.ID) }

// FirstLine returns the first non-empty line of the description, with an
// ellipsis when more follows.
func (t Task) FirstLine() string {
	first, rest, multi := strings.Cut(t.Description, "\n")
	first = strings.TrimSpace(first)
	if multi && strings.TrimSpace(rest) != "" {
		return first + " …"
	}
	return first
}

func (t *Task) logAt(at time.Time, format string, args ...any) {
	t.History = append(t.History, HistoryEntry{At: at, Text: fmt.Sprintf(format, args...)})
}

// defaultColumns are used for new boards.
func DefaultColumns() []Column {
	return []Column{
		{ID: "todo", Name: "To Do", Color: "62"},
		{ID: "in_progress", Name: "In Progress", Color: "214"},
		{ID: "done", Name: "Done", Color: "35"},
	}
}

// columnPalette is cycled through when new columns are created without a
// colour.
var ColumnPalette = []string{"62", "214", "35", "205", "39", "208", "99", "171", "43", "167"}

// newFile creates a data file with one empty board.
func NewFile() *File {
	b := NewBoard("Main")
	f := &File{Version: FileVersion, ActiveBoard: b.ID, Boards: []*Board{b}}
	f.Attach()
	return f
}

func NewBoard(name string) *Board {
	return &Board{ID: Slug(name), Name: name, Columns: DefaultColumns(), NextID: 1}
}

// slug turns a name into a lowercase identifier.
func Slug(name string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('_')
			lastDash = true
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		s = "item"
	}
	return s
}

// uniqueID appends a counter to base until it is not in taken.
func uniqueID(base string, taken func(string) bool) string {
	id := base
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s_%d", base, n)
	}
	return id
}

// --- File -----------------------------------------------------------------

// Active returns the active board, falling back to the first one.
func (f *File) Active() *Board {
	if b := f.Board(f.ActiveBoard); b != nil {
		return b
	}
	if len(f.Boards) == 0 {
		f.Boards = append(f.Boards, NewBoard("Main"))
	}
	f.ActiveBoard = f.Boards[0].ID
	return f.Boards[0]
}

// Board finds a board by ID or (case-insensitive) name.
func (f *File) Board(key string) *Board {
	for _, b := range f.Boards {
		if b.ID == key {
			return b
		}
	}
	for _, b := range f.Boards {
		if strings.EqualFold(b.Name, key) {
			return b
		}
	}
	return nil
}

// AddBoard creates a new empty board and returns it.
func (f *File) AddBoard(name string) (*Board, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("board name is required")
	}
	if f.Board(name) != nil {
		return nil, fmt.Errorf("a board named %q already exists", name)
	}
	b := NewBoard(name)
	b.ID = uniqueID(b.ID, func(id string) bool { return f.Board(id) != nil })
	f.Boards = append(f.Boards, b)
	f.Attach()
	f.emit(Event{Kind: EvBoardAdded, Board: b.ID, Data: MustJSON(Board{ID: b.ID, Name: b.Name, Columns: b.Columns, NextID: b.NextID})})
	return b, nil
}

// RenameBoard changes a board's name.
func (f *File) RenameBoard(id, name string) error {
	b := f.Board(id)
	if b == nil {
		return fmt.Errorf("no board %q", id)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("board name is required")
	}
	if other := f.Board(name); other != nil && other != b {
		return fmt.Errorf("a board named %q already exists", name)
	}
	b.Name = name
	f.emit(Event{Kind: EvBoardRenamed, Board: b.ID, Text: name})
	return nil
}

// DescribeBoard sets a board's description; empty clears it.
func (f *File) DescribeBoard(id, text string) error {
	b := f.Board(id)
	if b == nil {
		return fmt.Errorf("no board %q", id)
	}
	text = strings.TrimSpace(text)
	b.Description = text
	f.emit(Event{Kind: EvBoardDescribed, Board: b.ID, Text: text})
	return nil
}

// Activate makes a board the active one.
func (f *File) Activate(id string) error {
	b := f.Board(id)
	if b == nil {
		return fmt.Errorf("no board %q", id)
	}
	if f.ActiveBoard != b.ID {
		f.ActiveBoard = b.ID
		f.emit(Event{Kind: EvBoardActivated, To: b.ID})
	}
	return nil
}

// RemoveBoard deletes a board. The last board cannot be removed.
func (f *File) RemoveBoard(id string) error {
	if len(f.Boards) <= 1 {
		return fmt.Errorf("cannot delete the only board")
	}
	for i, b := range f.Boards {
		if b.ID == id {
			f.Boards = append(f.Boards[:i], f.Boards[i+1:]...)
			if f.ActiveBoard == id {
				f.ActiveBoard = f.Boards[0].ID
			}
			f.emit(Event{Kind: EvBoardRemoved, Board: id})
			return nil
		}
	}
	return fmt.Errorf("no board %q", id)
}

// --- Board ----------------------------------------------------------------

// Column finds a column by ID, or by case-insensitive name or prefix.
func (b *Board) Column(key string) *Column {
	for i := range b.Columns {
		if b.Columns[i].ID == key {
			return &b.Columns[i]
		}
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return nil
	}
	for i := range b.Columns {
		if strings.ToLower(b.Columns[i].Name) == key {
			return &b.Columns[i]
		}
	}
	var match *Column
	for i := range b.Columns {
		if strings.HasPrefix(strings.ToLower(b.Columns[i].Name), key) || strings.HasPrefix(b.Columns[i].ID, key) {
			if match != nil {
				return nil // ambiguous
			}
			match = &b.Columns[i]
		}
	}
	return match
}

// ColumnIndex returns the position of a column or -1.
func (b *Board) ColumnIndex(id string) int {
	for i := range b.Columns {
		if b.Columns[i].ID == id {
			return i
		}
	}
	return -1
}

// DoneColumn is the last column.
func (b *Board) DoneColumn() *Column {
	if len(b.Columns) == 0 {
		return nil
	}
	return &b.Columns[len(b.Columns)-1]
}

// Task finds a task by ID.
func (b *Board) Task(id int) *Task {
	if i := b.taskIndex(id); i >= 0 {
		return &b.Tasks[i]
	}
	return nil
}

// taskIndex returns the position of a task, using the id index. The index
// is verified against the slice so direct edits to Tasks stay safe.
func (b *Board) taskIndex(id int) int {
	if b.byID == nil || b.byIDGen != b.gen || len(b.byID) != len(b.Tasks) {
		b.reindex()
	}
	if i, ok := b.byID[id]; ok {
		if i < len(b.Tasks) && b.Tasks[i].ID == id {
			return i
		}
		b.reindex()
		if i, ok := b.byID[id]; ok {
			return i
		}
	}
	return -1
}

// TasksIn returns the live (unarchived) tasks of a column in board order.
func (b *Board) TasksIn(colID string) []Task {
	var out []Task
	for _, t := range b.Tasks {
		if t.Column == colID && !t.Archived() {
			out = append(out, t)
		}
	}
	return out
}

// CountIn returns the number of live tasks in a column.
func (b *Board) CountIn(colID string) int {
	n := 0
	for _, t := range b.Tasks {
		if t.Column == colID && !t.Archived() {
			n++
		}
	}
	return n
}

// Live returns all unarchived tasks in board order.
func (b *Board) Live() []Task {
	var out []Task
	for _, t := range b.Tasks {
		if !t.Archived() {
			out = append(out, t)
		}
	}
	return out
}

// ArchivedTasks returns archived tasks, most recently archived first.
func (b *Board) ArchivedTasks() []Task {
	var out []Task
	for _, t := range b.Tasks {
		if t.Archived() {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ArchivedAt.After(*out[j].ArchivedAt) })
	return out
}

// WIPExceeded reports whether a column holds more tasks than its limit.
func (b *Board) WIPExceeded(colID string) bool {
	c := b.Column(colID)
	return c != nil && c.WIPLimit > 0 && b.CountIn(colID) > c.WIPLimit
}

// Labels returns every label in use, sorted.
func (b *Board) Labels() []string {
	seen := map[string]bool{}
	for _, t := range b.Tasks {
		for _, l := range t.Labels {
			seen[l] = true
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// AddTask assigns an ID and timestamps and appends the task to the board.
func (b *Board) AddTask(t Task) (*Task, error) {
	t.Title = strings.TrimSpace(t.Title)
	if t.Title == "" {
		return nil, fmt.Errorf("a title is required")
	}
	if t.Column == "" && len(b.Columns) > 0 {
		t.Column = b.Columns[0].ID
	}
	col := b.Column(t.Column)
	if col == nil {
		return nil, fmt.Errorf("no column %q", t.Column)
	}
	t.Column = col.ID
	if b.NextID < 1 {
		b.NextID = 1
	}
	for b.Task(b.NextID) != nil {
		b.NextID++
	}
	t.ID = b.NextID
	b.NextID++
	now := b.now()
	t.CreatedAt, t.UpdatedAt = now, now
	t.Labels = NormalizeLabels(t.Labels)
	t.History = nil
	t.logAt(now, "Created in %s", col.Name)
	b.Tasks = append(b.Tasks, t)
	b.touch()
	added := &b.Tasks[len(b.Tasks)-1]
	added.Links = nil // links are only ever created through AddLink
	b.emit(Event{Kind: EvTaskCreated, Task: t.ID, To: t.Column, Data: MustJSON(*added)})
	b.linkMentions(t.ID, t.Description)
	return b.Task(t.ID), nil
}

// UpdateTask replaces the editable fields of a task and records what
// changed.
func (b *Board) UpdateTask(u Task) error {
	t := b.Task(u.ID)
	if t == nil {
		return fmt.Errorf("no task #%d", u.ID)
	}
	u.Title = strings.TrimSpace(u.Title)
	if u.Title == "" {
		return fmt.Errorf("a title is required")
	}
	u.Labels = NormalizeLabels(u.Labels)
	var changes []string
	if u.Title != t.Title {
		changes = append(changes, fmt.Sprintf("renamed from %q", t.Title))
	}
	if u.Description != t.Description {
		changes = append(changes, "description edited")
	}
	if u.Priority != t.Priority {
		changes = append(changes, "priority "+u.Priority.String())
	}
	if u.Due != t.Due {
		if u.Due == "" {
			changes = append(changes, "due date cleared")
		} else {
			changes = append(changes, "due "+u.Due)
		}
	}
	if strings.Join(u.Labels, ",") != strings.Join(t.Labels, ",") {
		changes = append(changes, "labels "+strings.Join(u.Labels, ", "))
	}
	if u.Assignee != t.Assignee {
		if u.Assignee == "" {
			changes = append(changes, "unassigned")
		} else {
			changes = append(changes, "assigned to "+u.Assignee)
		}
	}
	descChanged := u.Description != t.Description
	t.Title, t.Description, t.Priority, t.Due = u.Title, u.Description, u.Priority, u.Due
	t.Labels, t.Assignee = u.Labels, u.Assignee
	if len(changes) > 0 {
		t.UpdatedAt = b.now()
		t.logAt(t.UpdatedAt, "Edited: %s", strings.Join(changes, "; "))
		b.emit(Event{Kind: EvTaskUpdated, Task: t.ID, Text: strings.Join(changes, "; "), Data: MustJSON(*t)})
	}
	if descChanged {
		b.linkMentions(t.ID, t.Description)
	}
	return nil
}

// MoveTask moves a task to the end of another column.
func (b *Board) MoveTask(id int, colID string) error {
	i := b.taskIndex(id)
	if i < 0 {
		return fmt.Errorf("no task #%d", id)
	}
	col := b.Column(colID)
	if col == nil {
		return fmt.Errorf("no column %q", colID)
	}
	t := b.Tasks[i]
	from := b.Column(t.Column)
	if from != nil && from.ID == col.ID {
		return nil
	}
	fromID := t.Column
	t.Column = col.ID
	t.UpdatedAt = b.now()
	if from != nil {
		t.logAt(t.UpdatedAt, "Moved from %s to %s", from.Name, col.Name)
	} else {
		t.logAt(t.UpdatedAt, "Moved to %s", col.Name)
	}
	b.Tasks = append(b.Tasks[:i], b.Tasks[i+1:]...)
	b.Tasks = append(b.Tasks, t)
	b.touch()
	b.emit(Event{Kind: EvTaskMoved, Task: t.ID, From: fromID, To: col.ID})
	return nil
}

// ReorderTask swaps a task with its neighbour (delta -1 or +1) within its
// column. It reports whether anything moved.
func (b *Board) ReorderTask(id int, delta int) bool {
	i := b.taskIndex(id)
	if i < 0 {
		return false
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for j := i + step; j >= 0 && j < len(b.Tasks); j += step {
		if b.Tasks[j].Column == b.Tasks[i].Column && !b.Tasks[j].Archived() {
			b.Tasks[i], b.Tasks[j] = b.Tasks[j], b.Tasks[i]
			b.touch()
			b.emit(Event{Kind: EvTaskReordered, Task: id, Index: step})
			return true
		}
	}
	return false
}

// DeleteTask removes a task permanently.
func (b *Board) DeleteTask(id int) bool {
	i := b.taskIndex(id)
	if i < 0 {
		return false
	}
	col := b.Tasks[i].Column
	b.Tasks = append(b.Tasks[:i], b.Tasks[i+1:]...)
	b.touch()
	b.dropLinksTo(id)
	b.emit(Event{Kind: EvTaskDeleted, Task: id, From: col})
	return true
}

// ArchiveTask hides a task from the board without deleting it.
func (b *Board) ArchiveTask(id int) bool {
	t := b.Task(id)
	if t == nil || t.Archived() {
		return false
	}
	now := b.now()
	t.ArchivedAt = &now
	t.UpdatedAt = now
	t.logAt(now, "Archived")
	b.emit(Event{Kind: EvTaskArchived, Task: id, From: t.Column})
	return true
}

// RestoreTask brings an archived task back to its column.
func (b *Board) RestoreTask(id int) bool {
	t := b.Task(id)
	if t == nil || !t.Archived() {
		return false
	}
	t.ArchivedAt = nil
	t.UpdatedAt = b.now()
	if b.Column(t.Column) == nil && len(b.Columns) > 0 {
		t.Column = b.Columns[0].ID
	}
	t.logAt(t.UpdatedAt, "Restored")
	// Put it at the end of its column.
	i := b.taskIndex(id)
	tt := b.Tasks[i]
	b.Tasks = append(b.Tasks[:i], b.Tasks[i+1:]...)
	b.Tasks = append(b.Tasks, tt)
	b.touch()
	b.emit(Event{Kind: EvTaskRestored, Task: id, To: tt.Column})
	return true
}

// ArchiveDone archives every task in the last column and returns how many.
func (b *Board) ArchiveDone() int {
	done := b.DoneColumn()
	if done == nil {
		return 0
	}
	n := 0
	for i := range b.Tasks {
		if b.Tasks[i].Column == done.ID && !b.Tasks[i].Archived() {
			b.ArchiveTask(b.Tasks[i].ID)
			n++
		}
	}
	return n
}

// AddComment appends a comment to a task.
func (b *Board) AddComment(id int, text string) error {
	t := b.Task(id)
	if t == nil {
		return fmt.Errorf("no task #%d", id)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("comment is empty")
	}
	now := b.now()
	t.Comments = append(t.Comments, Comment{At: now, Text: text})
	t.UpdatedAt = now
	t.logAt(now, "Commented")
	b.emit(Event{Kind: EvCommentAdded, Task: id, Text: text})
	b.linkMentions(id, text)
	return nil
}

// AddChecklistItem appends an item to a task's checklist.
func (b *Board) AddChecklistItem(id int, text string) error {
	t := b.Task(id)
	if t == nil {
		return fmt.Errorf("no task #%d", id)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("checklist item is empty")
	}
	t.Checklist = append(t.Checklist, ChecklistItem{Text: text})
	t.UpdatedAt = b.now()
	b.emit(Event{Kind: EvChecklistAdded, Task: id, Text: text})
	return nil
}

// ToggleChecklistItem flips the done state of checklist item i.
func (b *Board) ToggleChecklistItem(id, i int) bool {
	t := b.Task(id)
	if t == nil || i < 0 || i >= len(t.Checklist) {
		return false
	}
	t.Checklist[i].Done = !t.Checklist[i].Done
	t.UpdatedAt = b.now()
	if t.Checklist[i].Done {
		t.logAt(t.UpdatedAt, "Checked %q", t.Checklist[i].Text)
	} else {
		t.logAt(t.UpdatedAt, "Unchecked %q", t.Checklist[i].Text)
	}
	b.emit(Event{Kind: EvChecklistToggled, Task: id, Index: i})
	return true
}

// RemoveChecklistItem deletes checklist item i.
func (b *Board) RemoveChecklistItem(id, i int) bool {
	t := b.Task(id)
	if t == nil || i < 0 || i >= len(t.Checklist) {
		return false
	}
	t.Checklist = append(t.Checklist[:i], t.Checklist[i+1:]...)
	t.UpdatedAt = b.now()
	b.emit(Event{Kind: EvChecklistRemoved, Task: id, Index: i})
	return true
}

// AddAttachment records a link or file path on a task.
func (b *Board) AddAttachment(id int, ref string) error {
	t := b.Task(id)
	if t == nil {
		return fmt.Errorf("no task #%d", id)
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("attachment is empty")
	}
	t.Attachments = append(t.Attachments, ref)
	t.UpdatedAt = b.now()
	t.logAt(t.UpdatedAt, "Attached %s", ref)
	b.emit(Event{Kind: EvAttachmentAdded, Task: id, Text: ref})
	return nil
}

// RemoveAttachment deletes attachment i.
func (b *Board) RemoveAttachment(id, i int) bool {
	t := b.Task(id)
	if t == nil || i < 0 || i >= len(t.Attachments) {
		return false
	}
	t.Attachments = append(t.Attachments[:i], t.Attachments[i+1:]...)
	t.UpdatedAt = b.now()
	b.emit(Event{Kind: EvAttachmentRemoved, Task: id, Index: i})
	return true
}

// AddColumn appends a column. An empty colour picks the next palette entry.
func (b *Board) AddColumn(name, color string, wip int) (*Column, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("column name is required")
	}
	for _, c := range b.Columns {
		if strings.EqualFold(c.Name, name) {
			return nil, fmt.Errorf("a column named %q already exists", name)
		}
	}
	if color == "" {
		color = ColumnPalette[len(b.Columns)%len(ColumnPalette)]
	}
	id := uniqueID(Slug(name), func(id string) bool { return b.ColumnIndex(id) >= 0 })
	b.Columns = append(b.Columns, Column{ID: id, Name: name, Color: color, WIPLimit: max(0, wip)})
	col := &b.Columns[len(b.Columns)-1]
	b.emit(Event{Kind: EvColumnAdded, To: id, Data: MustJSON(*col)})
	return col, nil
}

// UpdateColumn renames, recolours or re-limits a column.
func (b *Board) UpdateColumn(id, name, color string, wip int) error {
	c := b.Column(id)
	if c == nil {
		return fmt.Errorf("no column %q", id)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("column name is required")
	}
	for _, other := range b.Columns {
		if other.ID != c.ID && strings.EqualFold(other.Name, name) {
			return fmt.Errorf("a column named %q already exists", name)
		}
	}
	c.Name, c.Color, c.WIPLimit = name, color, max(0, wip)
	b.emit(Event{Kind: EvColumnUpdated, To: c.ID, Data: MustJSON(*c)})
	return nil
}

// AllIn returns how many tasks, archived ones included, belong to a column.
func (b *Board) AllIn(colID string) int {
	n := 0
	for _, t := range b.Tasks {
		if t.Column == colID {
			n++
		}
	}
	return n
}

// RemoveColumn deletes a column, moving its tasks (archived ones included)
// to moveTo. With an empty moveTo the tasks are deleted permanently. The
// last remaining column cannot be removed.
func (b *Board) RemoveColumn(id, moveTo string) error {
	i := b.ColumnIndex(id)
	if i < 0 {
		return fmt.Errorf("no column %q", id)
	}
	if len(b.Columns) <= 1 {
		return fmt.Errorf("cannot delete the only column")
	}
	// Copy the target's id and name: a pointer into b.Columns would move to
	// a different column once the deleted one is cut out of the slice.
	to, toName := "", ""
	if moveTo != "" {
		target := b.Column(moveTo)
		if target == nil || target.ID == id {
			return fmt.Errorf("no column %q to move tasks to", moveTo)
		}
		to, toName = target.ID, target.Name
	}
	kept := b.Tasks[:0]
	now := b.now()
	for _, t := range b.Tasks {
		if t.Column != id {
			kept = append(kept, t)
			continue
		}
		if to == "" {
			continue
		}
		t.Column = to
		if !t.Archived() {
			t.logAt(now, "Moved to %s (column %s deleted)", toName, b.Columns[i].Name)
		}
		kept = append(kept, t)
	}
	b.Tasks = kept
	b.touch()
	b.Columns = append(b.Columns[:i], b.Columns[i+1:]...)
	b.emit(Event{Kind: EvColumnRemoved, From: id, To: to})
	return nil
}

// MoveColumn shifts a column left (-1) or right (+1).
func (b *Board) MoveColumn(id string, delta int) bool {
	i := b.ColumnIndex(id)
	j := i + delta
	if i < 0 || j < 0 || j >= len(b.Columns) {
		return false
	}
	b.Columns[i], b.Columns[j] = b.Columns[j], b.Columns[i]
	b.emit(Event{Kind: EvColumnMoved, From: id, Index: delta})
	return true
}

// Replace swaps in a whole board state (used by undo) and records it as a
// single event so replay reproduces it.
func (b *Board) Replace(nb Board) {
	rec, clock, gen := b.rec, b.clock, b.gen
	*b = nb
	b.rec, b.clock = rec, clock
	b.byID, b.gen = nil, gen+1
	b.emit(Event{Kind: EvBoardRestored, Data: MustJSON(nb)})
}

// normalizeLabels trims, lowercases, de-duplicates and sorts labels.
func NormalizeLabels(labels []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range labels {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// parseLabels splits a comma separated label list.
func ParseLabels(s string) []string {
	return NormalizeLabels(strings.Split(s, ","))
}
