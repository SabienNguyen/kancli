package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// store persists the data file. A store with an empty path keeps everything
// in memory only.
type store struct {
	path    string
	modTime time.Time // modification time observed at the last load or save
}

func newStore(path string) *store {
	return &store{path: path}
}

// enabled reports whether the store writes to disk.
func (s *store) enabled() bool { return s != nil && s.path != "" }

// describe is shown in the header.
func (s *store) describe() string {
	if !s.enabled() {
		return "demo mode · changes are not saved"
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, s.path); err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "~" + string(filepath.Separator) + rel
		}
	}
	return s.path
}

// defaultStorePath returns the data file location: $KANCLI_FILE if set,
// otherwise kancli/board.json under $XDG_DATA_HOME or the OS config dir.
func defaultStorePath() (string, error) {
	if p := os.Getenv("KANCLI_FILE"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate data directory: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "kancli", "board.json"), nil
}

// load reads the data file. A missing file yields a fresh file with one
// empty board. Older formats are migrated in memory.
func (s *store) load() (*File, error) {
	if !s.enabled() {
		return newFile(), nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.modTime = time.Time{}
		return newFile(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if info, err := os.Stat(s.path); err == nil {
		s.modTime = info.ModTime()
	}
	f, err := decodeFile(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return f, nil
}

// decodeFile parses any supported file version.
func decodeFile(data []byte) (*File, error) {
	var probe struct {
		Version int             `json:"version"`
		Boards  json.RawMessage `json:"boards"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch {
	case probe.Version > fileVersion:
		return nil, fmt.Errorf("file was written by a newer kancli (version %d)", probe.Version)
	case probe.Version <= 1 && len(probe.Boards) == 0:
		// The original single-board format (or a file with no boards at all).
		return migrateV1(data)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	normalizeFile(&f)
	return &f, nil
}

// normalizeFile repairs anything a hand-edited file may be missing.
func normalizeFile(f *File) {
	f.Version = fileVersion
	boards := f.Boards[:0]
	for _, b := range f.Boards {
		if b != nil {
			boards = append(boards, b)
		}
	}
	f.Boards = boards
	if len(f.Boards) == 0 {
		f.Boards = append(f.Boards, newBoard("Main"))
	}
	seenBoards := map[string]bool{}
	for _, b := range f.Boards {
		if b.Name == "" {
			b.Name = "Board"
		}
		if b.ID == "" {
			b.ID = slug(b.Name)
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
}

func normalizeBoard(b *Board) {
	if len(b.Columns) == 0 {
		b.Columns = defaultColumns()
	}
	seenCols := map[string]bool{}
	for i := range b.Columns {
		c := &b.Columns[i]
		if c.Name == "" {
			c.Name = "Column"
		}
		if c.ID == "" {
			c.ID = slug(c.Name)
		}
		for seenCols[c.ID] {
			c.ID += "_2"
		}
		seenCols[c.ID] = true
		if c.Color == "" {
			c.Color = columnPalette[i%len(columnPalette)]
		}
	}
	seenTasks := map[int]bool{}
	maxID := 0
	now := time.Now()
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
		t.Labels = normalizeLabels(t.Labels)
		if t.Due != "" {
			if d, err := parseDue(t.Due, now); err == nil {
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
}

// migrateV1 converts the original single-board format, where each task had
// a string id and a status of todo/in_progress/done.
func migrateV1(data []byte) (*File, error) {
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
	f := newFile()
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
	normalizeFile(f)
	return f, nil
}

// changedOnDisk reports whether another program has written the file since
// this store last loaded or saved it.
func (s *store) changedOnDisk() bool {
	if !s.enabled() {
		return false
	}
	info, err := os.Stat(s.path)
	if err != nil {
		return !s.modTime.IsZero()
	}
	return !info.ModTime().Equal(s.modTime)
}

// save writes the file atomically and remembers its new modification time.
func (s *store) save(f *File) error {
	if !s.enabled() {
		return nil
	}
	f.Version = fileVersion
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode board: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".board-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	if info, err := os.Stat(s.path); err == nil {
		s.modTime = info.ModTime()
	}
	return nil
}
