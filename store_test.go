package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "board.json")
	st := newStore(path)
	f, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Boards) != 1 || len(f.Boards[0].Tasks) != 0 {
		t.Fatal("missing file should yield one empty board")
	}
	want := sampleFile()
	if err := st.save(want); err != nil {
		t.Fatal(err)
	}
	got, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Boards[0].Tasks) != len(want.Boards[0].Tasks) {
		t.Fatalf("loaded %d tasks, want %d", len(got.Boards[0].Tasks), len(want.Boards[0].Tasks))
	}
	a, b := want.Boards[0].Tasks[0], got.Boards[0].Tasks[0]
	if a.ID != b.ID || a.Title != b.Title || a.Priority != b.Priority || a.Due != b.Due || len(a.Checklist) != len(b.Checklist) {
		t.Errorf("task changed in round trip:\n%+v\n%+v", a, b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %d entries", len(entries))
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"priority": "high"`) || !strings.Contains(string(data), `"version": 2`) {
		t.Errorf("unexpected file contents:\n%s", data)
	}
	if st.changedOnDisk() {
		t.Error("file should not count as changed right after save")
	}
	time.Sleep(10 * time.Millisecond)
	os.Chtimes(path, time.Now(), time.Now().Add(time.Second))
	if !st.changedOnDisk() {
		t.Error("external modification should be detected")
	}
}

func TestStoreMigratesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	v1 := `{"version":1,"tasks":[
		{"id":"abc","status":"todo","title":"buy milk","description":"strawberry","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
		{"id":"def","status":"in_progress","title":"write code"},
		{"id":"ghi","status":"done","title":"stay cool"}]}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := newStore(path).load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	if len(b.Tasks) != 3 || b.Tasks[0].ID != 1 || b.Tasks[2].ID != 3 {
		t.Errorf("migrated tasks = %+v", b.Tasks)
	}
	if b.Tasks[1].Column != "in_progress" || b.Tasks[2].Column != "done" || b.Tasks[0].Description != "strawberry" {
		t.Errorf("columns not preserved: %+v", b.Tasks)
	}
	if b.NextID != 4 {
		t.Errorf("next id = %d", b.NextID)
	}
}

func TestStoreRepairsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	if _, err := newStore(write("corrupt.json", "{nope")).load(); err == nil {
		t.Error("corrupt file should fail")
	}
	if _, err := newStore(write("newer.json", `{"version": 99}`)).load(); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer file error = %v", err)
	}
	f, err := newStore(write("sparse.json", `{"version":2,"boards":[{"name":"X","tasks":[
		{"title":"a","column":"todo"},{"id":5,"title":"b","column":"bogus","due":"garbage"},{"id":5,"title":"c"}]}]}`)).load()
	if err != nil {
		t.Fatal(err)
	}
	b := f.Active()
	if b.ID != "x" || len(b.Columns) != 3 {
		t.Errorf("board not normalised: %+v", b)
	}
	ids := idsOf(b.Tasks)
	if ids[1] != 5 || ids[0] == 0 || ids[2] == 0 || ids[0] == ids[2] || ids[2] == 5 {
		t.Errorf("ids not repaired: %v", ids)
	}
	if b.Tasks[1].Column != "todo" || b.Tasks[1].Due != "" {
		t.Errorf("bad column/due not repaired: %+v", b.Tasks[1])
	}
	if b.NextID <= 5 {
		t.Errorf("next id = %d", b.NextID)
	}
}

func TestDefaultPaths(t *testing.T) {
	t.Setenv("KANCLI_FILE", "/tmp/custom.json")
	if p, _ := defaultStorePath(); p != "/tmp/custom.json" {
		t.Errorf("KANCLI_FILE path = %q", p)
	}
	t.Setenv("KANCLI_FILE", "")
	t.Setenv("XDG_DATA_HOME", "/data")
	if p, _ := defaultStorePath(); p != filepath.Join("/data", "kancli", "board.json") {
		t.Errorf("XDG path = %q", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	if p, _ := defaultConfigPath(); p != filepath.Join("/cfg", "kancli", "config.json") {
		t.Errorf("config path = %q", p)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if c, err := loadConfig(p); err != nil || c.Theme != "" {
		t.Errorf("missing config = %+v, %v", c, err)
	}
	os.WriteFile(p, []byte(`{"theme":"mono","ascii":true,"sort":"due","keys":{"quit":["x"]}}`), 0o644)
	c, err := loadConfig(p)
	if err != nil || c.Theme != "mono" || !c.ASCII || c.Sort != "due" || c.Keys["quit"][0] != "x" {
		t.Errorf("config = %+v, %v", c, err)
	}
	os.WriteFile(p, []byte(`{"theme":"neon"}`), 0o644)
	if _, err := loadConfig(p); err == nil {
		t.Error("unknown theme should fail")
	}
	os.WriteFile(p, []byte(`{"keys":{"fly":["f"]}}`), 0o644)
	if _, err := loadConfig(p); err == nil {
		t.Error("unknown key action should fail")
	}
	k := defaultKeyMap()
	if err := k.applyKeyOverrides(map[string][]string{"quit": {"x"}, "help": {}}); err != nil {
		t.Fatal(err)
	}
	if k.Quit.Keys()[0] != "x" || k.Help.Enabled() {
		t.Error("overrides not applied")
	}
}

func TestThemes(t *testing.T) {
	for _, name := range themeNames {
		if _, err := themeByName(name, true); err != nil {
			t.Errorf("theme %s: %v", name, err)
		}
	}
	if _, err := themeByName("neon", false); err == nil {
		t.Error("unknown theme should fail")
	}
	th, _ := themeByName("mono", true)
	if !th.mono || th.border.TopLeft != "+" {
		t.Error("mono/ascii theme misconfigured")
	}
}

func TestDecodeFileWithoutVersionOrWithNullBoard(t *testing.T) {
	f, err := decodeFile([]byte(`{"active_board":"work","boards":[{"id":"work","name":"Work","tasks":[{"id":1,"title":"keep","column":"todo"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Boards) != 1 || f.Boards[0].Name != "Work" || len(f.Boards[0].Tasks) != 1 {
		t.Errorf("version-less v2 file was not decoded as v2: %+v", f.Boards[0])
	}
	f, err = decodeFile([]byte(`{"version":2,"boards":[null,{"name":"Real"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Boards) != 1 || f.Boards[0].Name != "Real" {
		t.Errorf("null board not dropped: %+v", f.Boards)
	}
	f, err = decodeFile([]byte(`{"version":2,"boards":[null]}`))
	if err != nil || len(f.Boards) != 1 {
		t.Errorf("all-null boards should yield a default board: %v %v", f, err)
	}
}
