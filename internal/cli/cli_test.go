package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SabienNguyen/kancli/internal/store"

	"github.com/SabienNguyen/kancli/internal/board"
)

// runCLI runs the binary's argument parser against a temp data file.
// newStore returns a store that is closed when the test ends, so it never
// holds board.db past the temp directory's cleanup.
func newStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st := store.New(path)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func runCLI(t *testing.T, path string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main("test", append([]string{"-file", path, "-config", filepath.Join(t.TempDir(), "none.json")}, args...), &out, &errb, nil)
	return out.String(), errb.String(), code
}

func TestCLIAddListMoveDone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	out, errs, code := runCLI(t, path, "add", "-p", "high", "-due", "tomorrow", "-l", "Docs,ops", "-a", "sam", "Write", "docs")
	if code != 0 || !strings.Contains(out, "Added task #1 to To Do") {
		t.Fatalf("add: %d %q %q", code, out, errs)
	}
	runCLI(t, path, "add", "-c", "in", "Second task")
	out, _, _ = runCLI(t, path, "list")
	if !strings.Contains(out, "Write docs") || !strings.Contains(out, "docs,ops") || !strings.Contains(out, "In Progress") {
		t.Errorf("list output:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "list", "-q", "@sam", "-json")
	var tasks []board.Task
	if err := json.Unmarshal([]byte(out), &tasks); err != nil || len(tasks) != 1 || tasks[0].Priority != board.PriorityHigh {
		t.Errorf("json list = %s (%v)", out, err)
	}
	if out, _, code := runCLI(t, path, "move", "1", "done"); code != 0 || !strings.Contains(out, "moved to Done") {
		t.Errorf("move: %q", out)
	}
	if _, errs, code := runCLI(t, path, "move", "1", "nowhere"); code == 0 || !strings.Contains(errs, "no column") {
		t.Errorf("bad move should fail: %q", errs)
	}
	runCLI(t, path, "done", "2")
	out, _, _ = runCLI(t, path, "list", "-c", "done")
	if strings.Count(out, "\n") != 3 { // header + 2 rows
		t.Errorf("done column should have 2 tasks:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "show", "1")
	if !strings.Contains(out, "priority:  high") || !strings.Contains(out, "Moved from To Do to Done") {
		t.Errorf("show:\n%s", out)
	}
	runCLI(t, path, "archive", "1")
	out, _, _ = runCLI(t, path, "list", "-archived")
	if !strings.Contains(out, "Write docs") {
		t.Errorf("archived list:\n%s", out)
	}
	runCLI(t, path, "restore", "1")
	if out, _, code := runCLI(t, path, "rm", "1", "2"); code != 0 || !strings.Contains(out, "#2 deleted") {
		t.Errorf("rm: %q", out)
	}
	out, _, _ = runCLI(t, path, "list")
	if !strings.Contains(out, "No tasks") {
		t.Errorf("list after rm:\n%s", out)
	}
	if _, _, code := runCLI(t, path, "bogus"); code != 2 {
		t.Error("unknown command should exit 2")
	}
	if out, _, code := runCLI(t, path, "version"); code != 0 || !strings.HasPrefix(out, "kancli ") {
		t.Errorf("version: %q", out)
	}
}

func TestCLIDue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	runCLI(t, path, "add", "-due", "yesterday", "Late")
	runCLI(t, path, "add", "-due", "today", "Now")
	runCLI(t, path, "add", "-due", "+3d", "Soon")
	runCLI(t, path, "add", "Whenever")
	out, _, _ := runCLI(t, path, "due")
	if !strings.Contains(out, "1d overdue") || !strings.Contains(out, "today") || strings.Contains(out, "Soon") {
		t.Errorf("due:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "due", "-days", "5")
	if !strings.Contains(out, "Soon") {
		t.Errorf("due -days:\n%s", out)
	}
	if out, errs, code := runCLI(t, path, "due", "-q"); code != 1 || out != "" || errs != "" {
		t.Errorf("due -q should be silent with exit 1: %d %q %q", code, out, errs)
	}
	runCLI(t, path, "done", "1", "2")
	if out, _, code := runCLI(t, path, "due"); code != 0 || !strings.Contains(out, "Nothing due") {
		t.Errorf("due after done: %q", out)
	}
}

func TestCLIExportImport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.json")
	runCLI(t, path, "add", "-p", "urgent", "-l", "a,b", "-d", "line one\nline two", "Alpha")
	runCLI(t, path, "add", "-c", "done", "Beta")

	md, _, _ := runCLI(t, path, "export", "-f", "md")
	for _, want := range []string{"# Main", "## To Do (1)", "- [ ] #1 Alpha `!urgent +a +b`", "  line one", "## Done (1)", "- [x] #2 Beta"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	csvOut := filepath.Join(dir, "out.csv")
	if _, errs, code := runCLI(t, path, "export", "-o", csvOut); code != 0 {
		t.Fatalf("csv export: %s", errs)
	}
	data, _ := os.ReadFile(csvOut)
	if !strings.HasPrefix(string(data), "id,column,title") || !strings.Contains(string(data), "Alpha") {
		t.Errorf("csv:\n%s", data)
	}
	jsonOut, _, _ := runCLI(t, path, "export")
	var b board.Board
	if err := json.Unmarshal([]byte(jsonOut), &b); err != nil || len(b.Tasks) != 2 {
		t.Errorf("json export: %v", err)
	}

	// Import each format into a fresh board.
	other := filepath.Join(dir, "other.json")
	if out, errs, code := runCLI(t, other, "import", csvOut); code != 0 || !strings.Contains(out, "Imported 2 tasks") {
		t.Fatalf("csv import: %q %q", out, errs)
	}
	mdFile := filepath.Join(dir, "board.md")
	os.WriteFile(mdFile, []byte(md), 0o644)
	if out, _, code := runCLI(t, other, "import", mdFile); code != 0 || !strings.Contains(out, "Imported 2") {
		t.Fatalf("md import: %q", out)
	}
	jsonFile := filepath.Join(dir, "board-export.json")
	os.WriteFile(jsonFile, []byte(jsonOut), 0o644)
	if out, _, code := runCLI(t, other, "import", jsonFile); code != 0 || !strings.Contains(out, "Imported 2") {
		t.Fatalf("json import: %q", out)
	}
	out, _, _ := runCLI(t, other, "list", "-json")
	var tasks []board.Task
	json.Unmarshal([]byte(out), &tasks)
	if len(tasks) != 6 {
		t.Fatalf("imported %d tasks, want 6", len(tasks))
	}
	urgent, done := 0, 0
	for _, task := range tasks {
		if task.Priority == board.PriorityUrgent {
			urgent++
		}
		if task.Column == "done" {
			done++
		}
	}
	if urgent != 3 || done != 3 {
		t.Errorf("imports lost metadata: urgent=%d done=%d", urgent, done)
	}
}

func TestCLIBoardsColumnsConfigKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	if out, _, code := runCLI(t, path, "boards", "new", "Work"); code != 0 || !strings.Contains(out, "Created") {
		t.Fatalf("boards new: %q", out)
	}
	runCLI(t, path, "add", "On work")
	runCLI(t, path, "boards", "use", "main")
	runCLI(t, path, "add", "On main")
	out, _, _ := runCLI(t, path, "boards")
	if !strings.Contains(out, "* Main") || !strings.Contains(out, "  Work") {
		t.Errorf("boards:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "-b", "work", "list")
	if !strings.Contains(out, "On work") || strings.Contains(out, "On main") {
		t.Errorf("-b list:\n%s", out)
	}
	runCLI(t, path, "boards", "rename", "Work", "Office")
	runCLI(t, path, "boards", "rm", "Office")
	out, _, _ = runCLI(t, path, "boards")
	if strings.Contains(out, "Office") || strings.Contains(out, "Work") {
		t.Errorf("board not deleted:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "columns")
	if !strings.Contains(out, "in_progress") {
		t.Errorf("columns:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "config")
	if !strings.Contains(out, "Config file:") || !strings.Contains(out, `"theme"`) {
		t.Errorf("config:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "keys")
	if !strings.Contains(out, "move_right") {
		t.Errorf("keys:\n%s", out)
	}
	if _, errs, code := runCLI(t, path, "-theme", "neon", "list"); code != 1 || !strings.Contains(errs, "unknown theme") {
		t.Errorf("bad theme: %d %q", code, errs)
	}
}

func TestMarkdownImportParsing(t *testing.T) {
	src := `# Board

## To Do (2)

- [ ] #1 First ` + "`!high due:2030-01-02 +a @me`" + `
  some description
  more
  - [x] step one
  - [ ] step two
- Second item

## Done

- [x] Finished
`
	tasks, err := tasksFromMarkdown([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("parsed %d tasks: %+v", len(tasks), tasks)
	}
	f := tasks[0]
	if f.Title != "First" || f.Priority != board.PriorityHigh || f.Due != "2030-01-02" || f.Assignee != "me" || f.Column != "To Do" {
		t.Errorf("first = %+v", f)
	}
	if f.Description != "some description\nmore" || len(f.Checklist) != 2 || !f.Checklist[0].Done {
		t.Errorf("first body = %q %+v", f.Description, f.Checklist)
	}
	if tasks[2].Column != "Done" || tasks[2].Title != "Finished" {
		t.Errorf("third = %+v", tasks[2])
	}
}

func TestCLIValidatesIDsBeforeChanging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	runCLI(t, path, "add", "One")
	runCLI(t, path, "add", "Two")
	for _, cmd := range []string{"rm", "done", "archive"} {
		out, errs, code := runCLI(t, path, cmd, "1", "99")
		if code != 1 || out != "" || !strings.Contains(errs, "no task #99") {
			t.Errorf("%s: code=%d out=%q err=%q", cmd, code, out, errs)
		}
	}
	out, _, _ := runCLI(t, path, "list")
	if !strings.Contains(out, "One") || !strings.Contains(out, "To Do") || strings.Contains(out, "archived") {
		t.Errorf("a failed command changed the board:\n%s", out)
	}
}

func TestCLIAddFlagsAfterTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	out, errs, code := runCLI(t, path, "add", "Write", "the", "notes", "-p", "high", "-due", "tomorrow", "-l", "docs", "-a", "sam", "-c", "done")
	if code != 0 {
		t.Fatalf("add: %q %q", out, errs)
	}
	out, _, _ = runCLI(t, path, "list", "-json")
	var tasks []board.Task
	json.Unmarshal([]byte(out), &tasks)
	if len(tasks) != 1 || tasks[0].Title != "Write the notes" || tasks[0].Priority != board.PriorityHigh ||
		tasks[0].Assignee != "sam" || tasks[0].Column != "done" || len(tasks[0].Labels) != 1 {
		t.Errorf("flags after the title were not honoured: %+v", tasks)
	}
}

func TestCLIStatsLogReviewCompactAsOf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	runCLI(t, path, "add", "-l", "bug", "Fix it")
	runCLI(t, path, "add", "Ship it")
	runCLI(t, path, "move", "1", "in")
	runCLI(t, path, "done", "1")

	out, errs, code := runCLI(t, path, "stats")
	if code != 0 || !strings.Contains(out, "Finished 1") || !strings.Contains(out, "Throughput") || !strings.Contains(out, "+bug") {
		t.Errorf("stats: %d %q %q", code, out, errs)
	}
	out, _, _ = runCLI(t, path, "stats", "-json")
	var st board.Stats
	if err := json.Unmarshal([]byte(out), &st); err != nil || len(st.Finished) != 1 || st.Live != 2 {
		t.Errorf("stats -json: %v live=%d finished=%d", err, st.Live, len(st.Finished))
	}
	out, _, _ = runCLI(t, path, "stats", "-sql")
	if !strings.Contains(out, "CREATE OR REPLACE VIEW events") || !strings.Contains(out, "Throughput per ISO week") {
		t.Errorf("stats -sql:\n%s", out)
	}
	t.Setenv("KANCLI_DUCKDB", "/definitely/missing/duckdb")
	if _, errs, code := runCLI(t, path, "stats", "-q", "SELECT 1"); code != 1 || !strings.Contains(errs, "duckdb") {
		t.Errorf("stats -q without duckdb: %d %q", code, errs)
	}
	if _, errs, code := runCLI(t, path, "export", "-f", "parquet", "-o", filepath.Join(t.TempDir(), "x.parquet")); code != 1 || !strings.Contains(errs, "duckdb") {
		t.Errorf("parquet without duckdb: %d %q", code, errs)
	}

	out, _, _ = runCLI(t, path, "log")
	for _, want := range []string{`created #1 "Fix it" in To Do`, "moved #1 from To Do to In Progress", "moved #1 from In Progress to Done", "[cli]"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	out, _, _ = runCLI(t, path, "log", "-task", "2", "-json")
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, `"kind":"task.created"`) {
		t.Errorf("log -task -json:\n%s", out)
	}

	out, _, _ = runCLI(t, path, "review", "-days", "1")
	if !strings.Contains(out, "- Finished: **1**") || !strings.Contains(out, "- [x] #1 Fix it") || !strings.Contains(out, "- #2 Ship it") {
		t.Errorf("review:\n%s", out)
	}
	mdFile := filepath.Join(t.TempDir(), "review.md")
	runCLI(t, path, "review", "-o", mdFile)
	if data, err := os.ReadFile(mdFile); err != nil || !strings.Contains(string(data), "# Main") {
		t.Errorf("review -o: %v", err)
	}

	out, _, code = runCLI(t, path, "compact")
	if code != 0 || !strings.Contains(out, "Snapshot written") || !strings.Contains(out, "folded") {
		t.Errorf("compact: %q", out)
	}
	st2 := newStore(t, path)
	if _, err := st2.Load(); err != nil || st2.TailEvents() != 0 {
		t.Errorf("after compact tail=%d err=%v", st2.TailEvents(), err)
	}

	// As-of views are read-only and reflect history.
	out, errs, code = runCLI(t, path, "-as-of", "-30d", "list")
	if code != 0 || !strings.Contains(out, "No tasks") {
		t.Errorf("as-of before the tasks: %d %q %q", code, out, errs)
	}
	if _, errs, code := runCLI(t, path, "-as-of", "today", "add", "nope"); code != 1 || !strings.Contains(errs, "read-only") {
		t.Errorf("as-of should refuse writes: %d %q", code, errs)
	}
	out, _, _ = runCLI(t, path, "list")
	if strings.Contains(out, "nope") {
		t.Error("read-only add leaked into the board")
	}
}

func TestCLILinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	runCLI(t, path, "add", "Design")
	runCLI(t, path, "add", "Build")
	runCLI(t, path, "add", "Ship")
	if out, errs, code := runCLI(t, path, "link", "2", "blocked-by", "1"); code != 0 || !strings.Contains(out, "#2 blocked-by #1") {
		t.Fatalf("link: %d %q %q", code, out, errs)
	}
	runCLI(t, path, "link", "3", "subtask-of", "2")
	if _, errs, code := runCLI(t, path, "link", "1", "blocked-by", "2"); code != 1 || !strings.Contains(errs, "cycle") {
		t.Errorf("cycle should fail: %d %q", code, errs)
	}
	out, _, _ := runCLI(t, path, "show", "2")
	for _, want := range []string{"blocked by  #1 Design", "subtask     #3 Ship", "status:    blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("show missing %q:\n%s", want, out)
		}
	}
	out, _, _ = runCLI(t, path, "list", "-q", "blocked:yes")
	if !strings.Contains(out, "Build") || strings.Contains(out, "Design") {
		t.Errorf("blocked query:\n%s", out)
	}
	runCLI(t, path, "done", "1")
	out, _, _ = runCLI(t, path, "list", "-q", "blocked:yes")
	if !strings.Contains(out, "No tasks") {
		t.Errorf("finishing the blocker should unblock:\n%s", out)
	}
	if out, _, code := runCLI(t, path, "unlink", "1", "2"); code != 0 || !strings.Contains(out, "removed 1 link") {
		t.Errorf("unlink: %q", out)
	}
	if _, _, code := runCLI(t, path, "unlink", "1", "2"); code != 1 {
		t.Error("unlinking unlinked tasks should fail")
	}
	out, _, _ = runCLI(t, path, "log", "-n", "3")
	if !strings.Contains(out, "unlinked #1 blocks #2") {
		t.Errorf("log:\n%s", out)
	}
}

func TestCLIColumnsManage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	runCLI(t, path, "add", "one")
	runCLI(t, path, "add", "-c", "done", "two")

	out, errs, code := runCLI(t, path, "columns", "add", "Review", "--wip", "2", "--color", "99")
	if code != 0 || !strings.Contains(out, `Added column "Review" (review)`) {
		t.Fatalf("columns add: %d %q %q", code, out, errs)
	}
	out, _, _ = runCLI(t, path, "columns")
	if !strings.Contains(out, "review") || !strings.Contains(out, "99") {
		t.Errorf("new column missing from list:\n%s", out)
	}
	if _, errs, code := runCLI(t, path, "columns", "add", "review"); code == 0 || !strings.Contains(errs, "already exists") {
		t.Errorf("duplicate name should fail: %d %q", code, errs)
	}

	out, errs, code = runCLI(t, path, "columns", "edit", "review", "--name", "QA", "--wip", "0")
	if code != 0 || !strings.Contains(out, `Updated column "QA" (review)`) {
		t.Fatalf("columns edit: %d %q %q", code, out, errs)
	}
	out, _, _ = runCLI(t, path, "columns")
	if !strings.Contains(out, "QA") || strings.Contains(out, "Review") {
		t.Errorf("edit not applied:\n%s", out)
	}

	out, errs, code = runCLI(t, path, "columns", "rename", "qa", "Code", "Review")
	if code != 0 || !strings.Contains(out, `Renamed column "QA" to "Code Review"`) {
		t.Fatalf("columns rename: %d %q %q", code, out, errs)
	}

	// Positions: To Do, In Progress, Done, Code Review -> move to 2, then first, then right.
	out, errs, code = runCLI(t, path, "columns", "move", "review", "2")
	if code != 0 || !strings.Contains(out, `Moved column "Code Review" to position 2`) {
		t.Fatalf("columns move: %d %q %q", code, out, errs)
	}
	runCLI(t, path, "columns", "move", "review", "first")
	runCLI(t, path, "columns", "move", "review", "right")
	out, _, _ = runCLI(t, path, "columns")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 || !strings.HasPrefix(lines[2], "review") {
		t.Errorf("after moves, want review second:\n%s", out)
	}
	if _, errs, code := runCLI(t, path, "columns", "move", "review", "9"); code == 0 || !strings.Contains(errs, "1 to 4") {
		t.Errorf("out-of-range position should fail: %d %q", code, errs)
	}
	if _, errs, code := runCLI(t, path, "columns", "move", "review", "sideways"); code == 0 || !strings.Contains(errs, "left, right, first, last") {
		t.Errorf("bad direction should fail: %d %q", code, errs)
	}

	// Removing "done" moves its task somewhere explicit.
	out, errs, code = runCLI(t, path, "columns", "rm", "done", "--to", "review")
	if code != 0 || !strings.Contains(out, `Removed column "Done"; 1 task moved to Code Review`) {
		t.Fatalf("columns rm: %d %q %q", code, out, errs)
	}
	out, _, _ = runCLI(t, path, "list")
	if !strings.Contains(out, "two") || !strings.Contains(out, "Code Review") {
		t.Errorf("task should now sit in Code Review:\n%s", out)
	}
	if _, errs, code := runCLI(t, path, "columns", "rm", "nope"); code == 0 || !strings.Contains(errs, `no column "nope"`) {
		t.Errorf("unknown column should fail: %d %q", code, errs)
	}
	for _, id := range []string{"review", "in_progress"} {
		runCLI(t, path, "columns", "rm", id)
	}
	if _, errs, code := runCLI(t, path, "columns", "rm", "todo"); code == 0 || !strings.Contains(errs, "only column") {
		t.Errorf("last column must stay: %d %q", code, errs)
	}
}

func TestCLIBoardsDescribe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	out, errs, code := runCLI(t, path, "boards", "new", "Work", "--desc", "Client projects")
	if code != 0 || !strings.Contains(out, "Created") {
		t.Fatalf("boards new --desc: %d %q %q", code, out, errs)
	}
	out, _, _ = runCLI(t, path, "boards")
	if !strings.Contains(out, "Client projects") {
		t.Errorf("boards list should show the description:\n%s", out)
	}
	out, errs, code = runCLI(t, path, "boards", "describe", "work", "Invoices", "and", "projects")
	if code != 0 || !strings.Contains(out, `Described "Work": Invoices and projects`) {
		t.Fatalf("describe: %d %q %q", code, out, errs)
	}
	out, _, _ = runCLI(t, path, "boards")
	if !strings.Contains(out, "Invoices and projects") || strings.Contains(out, "Client projects") {
		t.Errorf("description not replaced:\n%s", out)
	}
	out, errs, code = runCLI(t, path, "boards", "describe", "work")
	if code != 0 || !strings.Contains(out, `Cleared the description of "Work"`) {
		t.Fatalf("clear: %d %q %q", code, out, errs)
	}
	if _, errs, code := runCLI(t, path, "boards", "describe", "nope", "x"); code == 0 || !strings.Contains(errs, `no board "nope"`) {
		t.Errorf("unknown board: %d %q", code, errs)
	}
	out, _, _ = runCLI(t, path, "log")
	if !strings.Contains(out, "described board") {
		t.Errorf("log should show the description event:\n%s", out)
	}
}

func TestCLIGoalBoards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	out, errs, code := runCLI(t, path, "boards", "new", "Roadmap", "--goals")
	if code != 0 || !strings.Contains(out, "Created board") {
		t.Fatalf("boards new --goals: %d %q %q", code, out, errs)
	}
	out, _, _ = runCLI(t, path, "boards")
	if !strings.Contains(out, "goals") || !strings.Contains(out, "* Roadmap") {
		t.Fatalf("boards list should mark the goal board:\n%s", out)
	}
	out, errs, code = runCLI(t, path, "-b", "roadmap", "add", "Ship 1.0")
	if code != 0 || !strings.Contains(out, "Added goal #1") {
		t.Fatalf("add on a goal board: %d %q %q", code, out, errs)
	}
	runCLI(t, path, "boards", "use", "main")
	if out, _, _ := runCLI(t, path, "add", "Fix login"); !strings.Contains(out, "Added task #1") {
		t.Fatalf("add on a task board: %q", out)
	}
	runCLI(t, path, "add", "Write docs")

	// A link from either side, stored on whichever side the words name.
	if out, errs, code := runCLI(t, path, "link", "1", "subtask-of", "roadmap#1"); code != 0 || !strings.Contains(out, "#1 subtask-of roadmap#1") {
		t.Fatalf("link to another board: %d %q %q", code, out, errs)
	}
	if out, errs, code := runCLI(t, path, "link", "roadmap#1", "parent-of", "2"); code != 0 || !strings.Contains(out, "roadmap#1 parent-of #2") {
		t.Fatalf("link from another board: %d %q %q", code, out, errs)
	}
	for _, id := range []string{"1", "2"} {
		out, _, _ := runCLI(t, path, "show", id)
		if !strings.Contains(out, "subtask of  roadmap#1 Ship 1.0") {
			t.Errorf("show %s should name the foreign parent:\n%s", id, out)
		}
	}
	out, _, _ = runCLI(t, path, "-b", "roadmap", "show", "1")
	if !strings.Contains(out, "0/2") || !strings.Contains(out, "subtask     main#1 Fix login") {
		t.Errorf("goal should count both tickets:\n%s", out)
	}
	runCLI(t, path, "done", "1")
	if out, _, _ := runCLI(t, path, "-b", "roadmap", "show", "1"); !strings.Contains(out, "1/2") {
		t.Errorf("finishing a ticket should show 1/2:\n%s", out)
	}
	out, _, _ = runCLI(t, path, "list", "-q", "parent:roadmap#1")
	if !strings.Contains(out, "Fix login") || !strings.Contains(out, "Write docs") {
		t.Errorf("parent query across boards:\n%s", out)
	}
	runCLI(t, path, "rm", "2")
	if out, _, _ := runCLI(t, path, "-b", "roadmap", "show", "1"); !strings.Contains(out, "1/1") {
		t.Errorf("deleting a ticket should drop its link:\n%s", out)
	}
	if out, errs, code := runCLI(t, path, "unlink", "1", "roadmap#1"); code != 0 || !strings.Contains(out, "removed 1 link") {
		t.Fatalf("unlink across boards: %d %q %q", code, out, errs)
	}
	if out, _, _ := runCLI(t, path, "-b", "roadmap", "show", "1"); strings.Contains(out, "Fix login") {
		t.Errorf("the last link should be gone:\n%s", out)
	}

	// Error cases.
	if _, errs, code := runCLI(t, path, "link", "1", "subtask-of", "nowhere#1"); code == 0 || !strings.Contains(errs, `no board "nowhere"`) {
		t.Errorf("unknown board: %d %q", code, errs)
	}
	if _, errs, code := runCLI(t, path, "link", "1", "subtask-of", "roadmap#99"); code == 0 || !strings.Contains(errs, "#99") {
		t.Errorf("unknown task: %d %q", code, errs)
	}

	// Back to a ticket board.
	if out, errs, code := runCLI(t, path, "boards", "kind", "roadmap", "tasks"); code != 0 || !strings.Contains(out, `Made "Roadmap" a task board`) {
		t.Fatalf("boards kind: %d %q %q", code, out, errs)
	}
	if out, _, _ := runCLI(t, path, "boards"); strings.Contains(out, "goals") {
		t.Errorf("boards list still says goals:\n%s", out)
	}
	if out, errs, code := runCLI(t, path, "boards", "kind", "roadmap", "goals"); code != 0 || !strings.Contains(out, `Made "Roadmap" a goal board`) {
		t.Fatalf("boards kind goals: %d %q %q", code, out, errs)
	}
}
