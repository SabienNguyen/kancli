package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "board.json")
	st := newStore(path)

	empty, err := st.load()
	if err != nil {
		t.Fatalf("load missing file: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("missing file yielded %d tasks", len(empty))
	}

	want := []Task{
		newTask(todo, "one", "first"),
		newTask(inProgress, "two", "line 1\nline 2"),
		newTask(done, "three", ""),
	}
	if err := st.save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d tasks, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if g.id != w.id || g.status != w.status || g.title != w.title || g.description != w.description {
			t.Errorf("task %d = %+v, want %+v", i, g, w)
		}
		if !g.createdAt.Equal(w.createdAt) || !g.updatedAt.Equal(w.updatedAt) {
			t.Errorf("task %d timestamps changed", i)
		}
	}

	// No temp files left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only board.json in the data dir, found %d entries", len(entries))
	}

	// The file is readable JSON with string statuses.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "in_progress"`) {
		t.Errorf("status not written as a string:\n%s", data)
	}
}

func TestStoreDisabled(t *testing.T) {
	st := store{}
	if st.enabled() {
		t.Error("empty store should be disabled")
	}
	if err := st.save(sampleTasks()); err != nil {
		t.Errorf("save on disabled store: %v", err)
	}
	got, err := st.load()
	if err != nil || got != nil {
		t.Errorf("load on disabled store = %v, %v", got, err)
	}
}

func TestStoreRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()

	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newStore(corrupt).load(); err == nil {
		t.Error("corrupt file should fail to load")
	}

	newer := filepath.Join(dir, "newer.json")
	if err := os.WriteFile(newer, []byte(`{"version": 99, "tasks": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newStore(newer).load(); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer file error = %v", err)
	}

	badStatus := filepath.Join(dir, "status.json")
	if err := os.WriteFile(badStatus, []byte(`{"version": 1, "tasks": [{"title": "x", "status": "blocked"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newStore(badStatus).load(); err == nil {
		t.Error("unknown status should fail to load")
	}
}

func TestStoreFillsMissingFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	if err := os.WriteFile(path, []byte(`{"version": 1, "tasks": [{"title": "bare", "status": "done"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := newStore(path).load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d tasks", len(got))
	}
	if got[0].id == "" || got[0].createdAt.IsZero() || got[0].updatedAt.IsZero() {
		t.Errorf("missing fields were not filled in: %+v", got[0])
	}
}

func TestStoreReassignsDuplicateIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	data := `{"version": 1, "tasks": [
		{"id": "same", "title": "first", "status": "todo"},
		{"id": "same", "title": "second", "status": "todo"},
		{"id": "other", "title": "third", "status": "done"}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := newStore(path).load()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].id != "same" || got[2].id != "other" {
		t.Errorf("unique ids should be kept: %q, %q", got[0].id, got[2].id)
	}
	if got[1].id == "same" || got[1].id == "" {
		t.Errorf("duplicate id was not reassigned: %q", got[1].id)
	}
}

func TestDefaultStorePath(t *testing.T) {
	t.Setenv("KANCLI_FILE", "/tmp/custom.json")
	if p, err := defaultStorePath(); err != nil || p != "/tmp/custom.json" {
		t.Errorf("KANCLI_FILE path = %q, %v", p, err)
	}
	t.Setenv("KANCLI_FILE", "")
	t.Setenv("XDG_DATA_HOME", "/data")
	if p, err := defaultStorePath(); err != nil || p != filepath.Join("/data", "kancli", "board.json") {
		t.Errorf("XDG path = %q, %v", p, err)
	}
}

func TestStatusJSON(t *testing.T) {
	for _, s := range allStatuses {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %v: %v", s, err)
		}
		var back status
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		if back != s {
			t.Errorf("round trip of %v gave %v", s, back)
		}
	}
	if _, err := json.Marshal(status(7)); err == nil {
		t.Error("invalid status should not marshal")
	}
	if todo.prev() != done || done.next() != todo || inProgress.next() != done {
		t.Error("next/prev should wrap around the board")
	}
}

func TestTaskDescriptionPreview(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"one line":         "one line",
		"first\nsecond":    "first …",
		"first\n\n  \n":    "first",
		"  padded  \nmore": "padded …",
	}
	for in, want := range cases {
		if got := (Task{description: in}).Description(); got != want {
			t.Errorf("Description(%q) = %q, want %q", in, got, want)
		}
	}
	tk := newTask(todo, "  spaced  ", "  desc  ")
	if tk.title != "spaced" || tk.description != "desc" {
		t.Errorf("newTask should trim: %q / %q", tk.title, tk.description)
	}
	if time.Since(tk.createdAt) > time.Minute {
		t.Error("createdAt should be now")
	}
}
