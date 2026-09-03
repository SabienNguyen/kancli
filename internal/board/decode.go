package board

import (
	"encoding/json"
	"fmt"
	"time"
)

// --- decoding ----------------------------------------------------------------

// Decode parses any supported file version.
func Decode(data []byte) (*File, error) {
	f, _, err := DecodeVersion(data)
	return f, err
}

// DecodeVersion is Decode plus the version number the bytes were written
// with (0 or 1 for the original format). Callers use it to notice an
// upgrade before writing anything.
func DecodeVersion(data []byte) (f *File, version int, err error) {
	var probe struct {
		Version int             `json:"version"`
		Boards  json.RawMessage `json:"boards"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, 0, err
	}
	switch {
	case probe.Version > FileVersion:
		return nil, probe.Version, fmt.Errorf("file was written by a newer kancli (version %d)", probe.Version)
	case probe.Version <= 1 && len(probe.Boards) == 0:
		// The original single-board format (or a file with no boards at all).
		f, err := MigrateV1(data)
		return f, probe.Version, err
	}
	f = &File{}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, probe.Version, err
	}
	NormalizeFile(f)
	return f, probe.Version, nil
}

// normalizeFile repairs anything a hand-edited file may be missing.
func NormalizeFile(f *File) {
	f.Version = FileVersion
	boards := f.Boards[:0]
	for _, b := range f.Boards {
		if b != nil {
			boards = append(boards, b)
		}
	}
	f.Boards = boards
	if len(f.Boards) == 0 {
		f.Boards = append(f.Boards, NewBoard("Main"))
	}
	seenBoards := map[string]bool{}
	for _, b := range f.Boards {
		if b.Name == "" {
			b.Name = "Board"
		}
		if b.ID == "" {
			b.ID = Slug(b.Name)
		}
		for seenBoards[b.ID] {
			b.ID += "_2"
		}
		seenBoards[b.ID] = true
		normalizeBoard(b)
	}
	if f.Board(f.ActiveBoard) == nil {
		f.ActiveBoard = f.Boards[0].ID
	}
	f.Attach()
}

func normalizeBoard(b *Board) {
	if len(b.Columns) == 0 {
		b.Columns = DefaultColumns()
	}
	seenCols := map[string]bool{}
	for i := range b.Columns {
		c := &b.Columns[i]
		if c.Name == "" {
			c.Name = "Column"
		}
		if c.ID == "" {
			c.ID = Slug(c.Name)
		}
		for seenCols[c.ID] {
			c.ID += "_2"
		}
		seenCols[c.ID] = true
		if c.Color == "" {
			c.Color = ColumnPalette[i%len(ColumnPalette)]
		}
	}
	seenTasks := map[int]bool{}
	maxID := 0
	now := Now()
	for i := range b.Tasks {
		t := &b.Tasks[i]
		if t.ID <= 0 || seenTasks[t.ID] {
			t.ID = 0 // assigned below once we know the max
		} else {
			seenTasks[t.ID] = true
			maxID = max(maxID, t.ID)
		}
		if b.Column(t.Column) == nil {
			t.Column = b.Columns[0].ID
		} else {
			t.Column = b.Column(t.Column).ID
		}
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now
		}
		if t.UpdatedAt.IsZero() {
			t.UpdatedAt = t.CreatedAt
		}
		t.Labels = NormalizeLabels(t.Labels)
		if t.Due != "" {
			if d, err := ParseDue(t.Due, now); err == nil {
				t.Due = d
			} else {
				t.Due = ""
			}
		}
	}
	for i := range b.Tasks {
		if b.Tasks[i].ID == 0 {
			maxID++
			b.Tasks[i].ID = maxID
		}
	}
	if b.NextID <= maxID {
		b.NextID = maxID + 1
	}
	// Task ids may have been reassigned above, so the index is stale.
	b.invalidateIndex()
	b.touch()
}

// migrateV1 converts the original single-board format, where each task had
// a string id and a status of todo/in_progress/done.
func MigrateV1(data []byte) (*File, error) {
	var old struct {
		Tasks []struct {
			Status      string    `json:"status"`
			Title       string    `json:"title"`
			Description string    `json:"description"`
			CreatedAt   time.Time `json:"created_at"`
			UpdatedAt   time.Time `json:"updated_at"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}
	f := NewFile()
	b := f.Boards[0]
	for _, ot := range old.Tasks {
		col := ot.Status
		if b.Column(col) == nil {
			col = b.Columns[0].ID
		}
		t := Task{
			ID:          b.NextID,
			Column:      col,
			Title:       ot.Title,
			Description: ot.Description,
			CreatedAt:   ot.CreatedAt,
			UpdatedAt:   ot.UpdatedAt,
		}
		b.NextID++
		b.Tasks = append(b.Tasks, t)
	}
	NormalizeFile(f)
	return f, nil
}
