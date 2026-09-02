package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// fileVersion is bumped whenever the on-disk format changes incompatibly.
const fileVersion = 1

// boardFile is the JSON document written to disk.
type boardFile struct {
	Version int        `json:"version"`
	Tasks   []taskJSON `json:"tasks"`
}

// taskJSON is the on-disk representation of a Task.
type taskJSON struct {
	ID          string    `json:"id"`
	Status      status    `json:"status"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toJSON(t Task) taskJSON {
	return taskJSON{
		ID:          t.id,
		Status:      t.status,
		Title:       t.title,
		Description: t.description,
		CreatedAt:   t.createdAt,
		UpdatedAt:   t.updatedAt,
	}
}

func (tj taskJSON) task() Task {
	t := Task{
		id:          tj.ID,
		status:      tj.Status,
		title:       tj.Title,
		description: tj.Description,
		createdAt:   tj.CreatedAt,
		updatedAt:   tj.UpdatedAt,
	}
	if t.id == "" {
		t.id = newID()
	}
	if t.createdAt.IsZero() {
		t.createdAt = time.Now()
	}
	if t.updatedAt.IsZero() {
		t.updatedAt = t.createdAt
	}
	return t
}

// store persists the board to a JSON file. A store with an empty path keeps
// everything in memory only.
type store struct {
	path string
}

func newStore(path string) store {
	return store{path: path}
}

// enabled reports whether the store writes to disk.
func (s store) enabled() bool { return s.path != "" }

// defaultStorePath returns the board file location: $KANCLI_FILE if set,
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

// load reads the tasks from disk. A missing file yields an empty board.
func (s store) load() ([]Task, error) {
	if !s.enabled() {
		return nil, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var f boardFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if f.Version > fileVersion {
		return nil, fmt.Errorf("%s was written by a newer kancli (version %d)", s.path, f.Version)
	}
	tasks := make([]Task, 0, len(f.Tasks))
	for _, tj := range f.Tasks {
		tasks = append(tasks, tj.task())
	}
	return tasks, nil
}

// save writes the tasks to disk atomically.
func (s store) save(tasks []Task) error {
	if !s.enabled() {
		return nil
	}
	f := boardFile{Version: fileVersion, Tasks: make([]taskJSON, 0, len(tasks))}
	for _, t := range tasks {
		f.Tasks = append(f.Tasks, toJSON(t))
	}
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
	return nil
}
