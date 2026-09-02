# Column Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Columns can be added, edited, renamed, moved and deleted from the command line, and the app's full help (`?`) shows every key group, including the column keys, at any terminal width instead of truncating them.

**Architecture:** The board package already has the mutations (`AddColumn`, `UpdateColumn`, `RemoveColumn`, `MoveColumn`) and they are event-sourced and undoable, so the CLI work is thin: subcommands under the existing `kancli columns` command that resolve a column by id or name with `(*Board).Column`, call the mutation and `c.Save()`. The help fix replaces the bubbles `help.Model` full view (which truncates groups with `…` when the terminal is narrower than all groups side by side) with a small renderer that flows the groups into as many rows as the width needs.

**Tech Stack:** Go, Cobra (internal/cli), Bubble Tea + bubbles/help + lipgloss (internal/ui), teatest golden files under internal/ui/testdata/TestGolden (regenerate with `go test ./internal/ui -run TestGolden -update`).

**Spec:** No separate spec. Requirements are the Goal above plus the Global Constraints.

## Global Constraints

- Executors run with `model: "opus"`, one task per dispatch; the main session reviews every diff and re-runs the tests itself.
- Do not add event kinds or change on-disk formats. Moving a column N positions is N `MoveColumn` calls, each producing its own `column.moved` event; this keeps replay identical to the UI's `<`/`>` keys.
- `go test -race ./...` and `PATH=$HOME/go/bin:$PATH make lint` must pass after every task.
- Golden files under `internal/ui/testdata/TestGolden` may only change in Task 2, only via `-update`, and the diff must be reviewed line by line (only `help.golden` is expected to change).
- Commit after every task with a conventional message. Do not push.

---

### Task 1: `kancli columns add | edit | rename | move | rm`

**Files:**
- Modify: `internal/cli/root.go` (`columnsCmd`, ~line 564; model the subcommands on `boardsCmd` directly above it)
- Modify: `internal/cli/cli.go` (add the implementations after `columns()`, ~line 1186)
- Test: `internal/cli/cli_test.go` (append)

**Interfaces:**
- Consumes (all exist, use verbatim):
  ```go
  func (b *Board) Column(key string) *Column                              // id, else case-insensitive name; nil if none
  func (b *Board) AddColumn(name, color string, wip int) (*Column, error)
  func (b *Board) UpdateColumn(id, name, color string, wip int) error
  func (b *Board) RemoveColumn(id, moveTo string) error                   // moveTo "" lets the board pick the neighbour
  func (b *Board) MoveColumn(id string, delta int) bool                   // one step; false if it would leave the board
  func (b *Board) ColumnIndex(id string) int
  func (b *Board) AllIn(colID string) int                                 // tasks in the column, archived included
  func (c *cli) wrap(name string, fn func(*cobra.Command, []string) error) func(*cobra.Command, []string) error
  func (c *cli) Save() error
  func (e *Env) board() (*board.Board, error)
  func (c *cli) completeColumns(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)
  func positional(fns ...cobra.CompletionFunc) cobra.CompletionFunc
  func fixed(words ...string) cobra.CompletionFunc
  var board.ColumnPalette []string
  ```
- Produces: the subcommands and these methods on `*cli`:
  ```go
  func (c *cli) columnsAdd(name, color string, wip int) error
  func (c *cli) columnsEdit(key, name, color string, wip int, wipSet bool) error
  func (c *cli) columnsMove(key, where string) error
  func (c *cli) columnsRemove(key, to string) error
  ```

**Command surface (put this in `Use`/`Short`/`Example` exactly):**

| Command | Args / flags | Output on success |
|---|---|---|
| `columns add <name...>` | `--color N` (ANSI colour code, default: next palette entry), `--wip N` (0 = none) | `Added column "Review" (review)` |
| `columns edit <column>` | `--name S`, `--color N`, `--wip N`; only given flags change | `Updated column "QA" (review)` |
| `columns rename <column> <new name...>` | | `Renamed column "Review" to "QA"` |
| `columns move <column> <left\|right\|first\|last\|N>` | N is a 1-based position | `Moved column "QA" to position 2` |
| `columns rm <column>` | `--to <column>` destination for its tasks (default: the board picks the neighbour) | `Removed column "QA"; 3 tasks moved to To Do` or `Removed column "QA"` when it was empty |

Aliases: `rm` also `delete`; `add` also `new`. Errors are returned (Cobra prints them), e.g. `no column "qa" (see `kancli columns`)`, and for `move`: `position must be left, right, first, last or a number from 1 to 3`.

- [ ] **Step 1: Write the failing test** (append to `internal/cli/cli_test.go`)

```go
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
```

- [ ] **Step 2: Run it**: `go test ./internal/cli -run TestCLIColumnsManage` → FAIL (unknown command "add" for "kancli columns", or similar).

- [ ] **Step 3: Wire the subcommands** in `internal/cli/root.go`. Replace `columnsCmd` with:

```go
func (c *cli) columnsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "columns",
		Aliases: []string{"cols"},
		Short:   "List the columns of the board, or manage them with add, edit, rename, move and rm",
		GroupID: groupBoards,
		Args:    cobra.NoArgs,
		RunE:    c.wrap("columns", func(*cobra.Command, []string) error { return c.columns() }),
	}

	var addColor string
	var addWIP int
	add := &cobra.Command{
		Use:     "add <name...>",
		Aliases: []string{"new"},
		Short:   "Add a column at the right",
		Args:    cobra.MinimumNArgs(1),
		Example: "  kancli columns add Review --wip 2\n  kancli columns add \"Code Review\" --color 99",
		RunE: c.wrap("columns add", func(_ *cobra.Command, args []string) error {
			return c.columnsAdd(strings.Join(args, " "), addColor, addWIP)
		}),
	}
	add.Flags().StringVar(&addColor, "color", "", "ANSI colour code, e.g. 62 or 214 (default: next palette colour)")
	add.Flags().IntVar(&addWIP, "wip", 0, "work-in-progress limit shown in the header (0 = none)")
	_ = add.RegisterFlagCompletionFunc("color", fixed(board.ColumnPalette...))

	var editName, editColor string
	var editWIP int
	edit := &cobra.Command{
		Use:               "edit <column>",
		Short:             "Change a column's name, colour or WIP limit",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: positional(c.completeColumns),
		Example:           "  kancli columns edit review --name QA\n  kancli columns edit todo --wip 5",
		RunE: c.wrap("columns edit", func(cmd *cobra.Command, args []string) error {
			return c.columnsEdit(args[0], editName, editColor, editWIP, cmd.Flags().Changed("wip"))
		}),
	}
	edit.Flags().StringVar(&editName, "name", "", "new name")
	edit.Flags().StringVar(&editColor, "color", "", "ANSI colour code")
	edit.Flags().IntVar(&editWIP, "wip", 0, "work-in-progress limit (0 = none)")
	_ = edit.RegisterFlagCompletionFunc("color", fixed(board.ColumnPalette...))

	rename := &cobra.Command{
		Use:               "rename <column> <new name...>",
		Short:             "Rename a column",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: positional(c.completeColumns),
		RunE: c.wrap("columns rename", func(_ *cobra.Command, args []string) error {
			return c.columnsEdit(args[0], strings.Join(args[1:], " "), "", 0, false)
		}),
	}

	move := &cobra.Command{
		Use:               "move <column> <left|right|first|last|position>",
		Aliases:           []string{"mv"},
		Short:             "Move a column one step, to an end, or to a 1-based position",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: positional(c.completeColumns, fixed("left", "right", "first", "last")),
		Example:           "  kancli columns move review left\n  kancli columns move qa 2",
		RunE: c.wrap("columns move", func(_ *cobra.Command, args []string) error {
			return c.columnsMove(args[0], args[1])
		}),
	}

	var rmTo string
	rm := &cobra.Command{
		Use:               "rm <column>",
		Aliases:           []string{"delete"},
		Short:             "Delete a column and move its tasks to another",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: positional(c.completeColumns),
		Example:           "  kancli columns rm review --to done",
		RunE: c.wrap("columns rm", func(_ *cobra.Command, args []string) error {
			return c.columnsRemove(args[0], rmTo)
		}),
	}
	rm.Flags().StringVar(&rmTo, "to", "", "column that receives the tasks (default: the neighbour)")
	_ = rm.RegisterFlagCompletionFunc("to", c.completeColumns)

	cmd.AddCommand(add, edit, rename, move, rm)
	return cmd
}
```
Check that `fixed` accepts a variadic `[]string` spread (`fixed(board.ColumnPalette...)`); if its signature differs, adapt the call, not the helper. Make sure `board` is imported in root.go.

- [ ] **Step 4: Implement the methods** in `internal/cli/cli.go` after `columns()`:

```go
func (c *cli) columnsAdd(name, color string, wip int) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col, err := b.AddColumn(name, color, wip)
	if err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Added column %q (%s)\n", col.Name, col.ID)
	return nil
}

// columnsEdit changes only what was given: an empty name or colour keeps
// the current one, and wip is applied only when wipSet is true so that
// "--wip 0" can clear a limit.
func (c *cli) columnsEdit(key, name, color string, wip int, wipSet bool) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.Column(key)
	if col == nil {
		return fmt.Errorf("no column %q (see `kancli columns`)", key)
	}
	old := *col
	if name == "" {
		name = col.Name
	}
	if color == "" {
		color = col.Color
	}
	if !wipSet {
		wip = col.WIPLimit
	}
	if err := b.UpdateColumn(col.ID, name, color, wip); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	if name != old.Name && color == old.Color && wip == old.WIPLimit {
		fmt.Fprintf(c.stdout, "Renamed column %q to %q\n", old.Name, name)
	} else {
		fmt.Fprintf(c.stdout, "Updated column %q (%s)\n", name, col.ID)
	}
	return nil
}

func (c *cli) columnsMove(key, where string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.Column(key)
	if col == nil {
		return fmt.Errorf("no column %q (see `kancli columns`)", key)
	}
	id, name := col.ID, col.Name
	n := len(b.Columns)
	from := b.ColumnIndex(id)
	var to int
	switch strings.ToLower(where) {
	case "left":
		to = from - 1
	case "right":
		to = from + 1
	case "first":
		to = 0
	case "last":
		to = n - 1
	default:
		pos, err := strconv.Atoi(where)
		if err != nil || pos < 1 || pos > n {
			return fmt.Errorf("position must be left, right, first, last or a number from 1 to %d", n)
		}
		to = pos - 1
	}
	if to < 0 || to >= n {
		return fmt.Errorf("column %q is already at that end", name)
	}
	step := 1
	if to < from {
		step = -1
	}
	for i := from; i != to; i += step {
		if !b.MoveColumn(id, step) {
			return fmt.Errorf("cannot move column %q", name)
		}
	}
	if err := c.Save(); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "Moved column %q to position %d\n", name, to+1)
	return nil
}

func (c *cli) columnsRemove(key, to string) error {
	b, err := c.env.board()
	if err != nil {
		return err
	}
	col := b.Column(key)
	if col == nil {
		return fmt.Errorf("no column %q (see `kancli columns`)", key)
	}
	id, name := col.ID, col.Name
	moveTo := ""
	if to != "" {
		dest := b.Column(to)
		if dest == nil {
			return fmt.Errorf("no column %q to move tasks to (see `kancli columns`)", to)
		}
		moveTo = dest.ID
	}
	moved := b.AllIn(id)
	if err := b.RemoveColumn(id, moveTo); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	if moved == 0 {
		fmt.Fprintf(c.stdout, "Removed column %q\n", name)
		return nil
	}
	dest := moveTo
	if dest == "" {
		// The board chose the neighbour; find where the tasks went.
		dest = destinationOf(b, id)
	}
	fmt.Fprintf(c.stdout, "Removed column %q; %d task%s moved to %s\n", name, moved, board.Plural(moved), board.ColName(b, dest))
	return nil
}
```
`board.Plural` is used elsewhere in cli.go (`link` output); confirm its name and signature (search `Plural(`) and adapt if it differs. Implement `destinationOf` by reading `RemoveColumn` in `internal/board/data.go` (~line 842) to see which neighbour it picks when `moveTo == ""` and mirroring that choice (it is a few lines: the column to the left if there is one, else the right — verify). If `RemoveColumn` already returns or records the destination in some way, use that instead and delete `destinationOf`.

Note: `col` is a pointer into `b.Columns`, which `MoveColumn`/`RemoveColumn` reorder, which is why `id`/`name` are copied before mutating.

- [ ] **Step 5: Run the test**: `go test -race ./internal/cli -run TestCLIColumnsManage -v` → PASS. Then `go test -race ./...` and `PATH=$HOME/go/bin:$PATH make lint`.

- [ ] **Step 6: Commit**
```bash
git add internal/cli/root.go internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat(cli): add, edit, rename, move and delete columns"
```

---

### Task 2: Full help that wraps instead of truncating

**Files:**
- Create: `internal/ui/helpview.go`
- Modify: `internal/ui/model.go` (`footerView`, ~line 1769; the `m.help.Width` line ~412 stays)
- Test: `internal/ui/helpview_test.go` (create)
- Regenerate: `internal/ui/testdata/TestGolden/help.golden` (only this golden should change)

**Interfaces:**
- Consumes: `KeyMap.FullHelp() [][]key.Binding` (internal/ui/keys.go:28), `KeyMap.ShortHelp()`, `m.help` (a `bubbles/help.Model`, whose `Styles.FullKey`, `Styles.FullDesc` and `Styles.Ellipsis` are lipgloss styles), `m.st.help` (padding style, theme.go:161), `assertFits` in model_test.go.
- Produces, verbatim:
  ```go
  // fullHelp lays out key groups in as many rows as width needs. Each
  // group is one aligned block (keys left, descriptions right); blocks sit
  // side by side separated by a gutter and wrap to a new row when the next
  // block would not fit. If the result is taller than maxHeight the blank
  // lines between rows go first, then the tail is cut and an ellipsis line
  // is appended.
  func fullHelp(groups [][]key.Binding, width, maxHeight int, keyStyle, descStyle, ellipsis lipgloss.Style) string
  ```

- [ ] **Step 1: Write the failing test** (`internal/ui/helpview_test.go`)

```go
package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestFullHelpWrapsToWidth(t *testing.T) {
	groups := defaultKeyMap().FullHelp()
	plain := lipgloss.NewStyle()
	descs := []string{"add column", "edit column", "delete column", "column left", "column right", "help", "quit", "up", "search"}
	for _, width := range []int{60, 80, 120, 200} {
		out := fullHelp(groups, width, 40, plain, plain, plain)
		for i, line := range strings.Split(out, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: line %d is %d wide: %q", width, i, w, line)
			}
		}
		for _, d := range descs {
			if !strings.Contains(out, d) {
				t.Errorf("width %d: %q missing:\n%s", width, d, out)
			}
		}
	}
	// Wider terminals use fewer rows.
	narrow := strings.Count(fullHelp(groups, 60, 40, plain, plain, plain), "\n")
	wide := strings.Count(fullHelp(groups, 200, 40, plain, plain, plain), "\n")
	if wide >= narrow {
		t.Errorf("200 columns should need fewer lines than 60: %d vs %d", wide, narrow)
	}
	// A tight height cap is respected and signalled.
	capped := fullHelp(groups, 60, 6, plain, plain, plain)
	if n := strings.Count(capped, "\n") + 1; n > 6 {
		t.Errorf("capped help has %d lines, want <= 6", n)
	}
	if !strings.Contains(capped, "…") {
		t.Errorf("capped help should end with an ellipsis:\n%s", capped)
	}
}
```
`defaultKeyMap()` is whatever internal/ui/keys.go names its constructor for the default `KeyMap` (search `func.*KeyMap` in keys.go); use the real name.

- [ ] **Step 2: Run it**: `go test ./internal/ui -run TestFullHelpWrapsToWidth` → FAIL (undefined `fullHelp`).

- [ ] **Step 3: Implement `internal/ui/helpview.go`**

```go
package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

const helpGutter = 4

// fullHelp lays out key groups in as many rows as width needs. Each
// group is one aligned block (keys left, descriptions right); blocks sit
// side by side separated by a gutter and wrap to a new row when the next
// block would not fit. If the result is taller than maxHeight the blank
// lines between rows go first, then the tail is cut and an ellipsis line
// is appended.
func fullHelp(groups [][]key.Binding, width, maxHeight int, keyStyle, descStyle, ellipsis lipgloss.Style) string {
	var blocks []string
	for _, g := range groups {
		if b := helpBlock(g, keyStyle, descStyle); b != "" {
			blocks = append(blocks, b)
		}
	}
	var rows []string
	var row []string
	used := 0
	for _, b := range blocks {
		w := lipgloss.Width(b)
		if len(row) > 0 && used+helpGutter+w > width {
			rows = append(rows, joinBlocks(row))
			row, used = nil, 0
		}
		if len(row) > 0 {
			used += helpGutter
		}
		row = append(row, b)
		used += w
	}
	if len(row) > 0 {
		rows = append(rows, joinBlocks(row))
	}

	out := strings.Join(rows, "\n\n")
	if maxHeight > 0 && lipgloss.Height(out) > maxHeight {
		out = strings.Join(rows, "\n")
	}
	if maxHeight > 0 && lipgloss.Height(out) > maxHeight {
		lines := strings.Split(out, "\n")
		keep := max(0, maxHeight-1)
		out = strings.Join(append(lines[:keep], ellipsis.Render("…")), "\n")
	}
	return out
}

// helpBlock renders one group as aligned "key  desc" lines, skipping
// disabled or empty bindings.
func helpBlock(g []key.Binding, keyStyle, descStyle lipgloss.Style) string {
	type item struct{ k, d string }
	var items []item
	keyW := 0
	for _, b := range g {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		if h.Key == "" && h.Desc == "" {
			continue
		}
		items = append(items, item{h.Key, h.Desc})
		keyW = max(keyW, lipgloss.Width(h.Key))
	}
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		pad := strings.Repeat(" ", keyW-lipgloss.Width(it.k))
		lines = append(lines, keyStyle.Render(it.k)+pad+" "+descStyle.Render(it.d))
	}
	return strings.Join(lines, "\n")
}

// joinBlocks puts blocks side by side, top-aligned, with the gutter.
func joinBlocks(blocks []string) string {
	gutter := strings.Repeat(" ", helpGutter)
	parts := make([]string, 0, len(blocks)*2-1)
	for i, b := range blocks {
		if i > 0 {
			parts = append(parts, gutter)
		}
		parts = append(parts, b)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}
```

- [ ] **Step 4: Run it**: `go test ./internal/ui -run TestFullHelpWrapsToWidth` → PASS.

- [ ] **Step 5: Use it in `footerView`** (internal/ui/model.go ~line 1769):

```go
func (m App) footerView() string {
	inner := max(1, m.width-m.st.help.GetHorizontalFrameSize())
	var body string
	if m.help.ShowAll {
		// Leave at least a third of the screen to the board.
		body = fullHelp(m.keys.FullHelp(), inner, max(3, m.height*2/3), m.help.Styles.FullKey, m.help.Styles.FullDesc, m.help.Styles.Ellipsis)
	} else {
		body = m.help.View(m.keys)
	}
	return m.st.help.Render(lipgloss.NewStyle().MaxWidth(inner).Render(body))
}
```
`m.keys` must be a `KeyMap` (or expose `FullHelp()`); check its type at the `help.View(m.keys)` call and adapt the receiver expression only.

- [ ] **Step 6: Regenerate and inspect the golden**

Run: `go test ./internal/ui -run TestGolden -update && git diff --stat internal/ui/testdata`
Expected: only `help.golden` changed. Open the new `help.golden` and confirm the footer shows all six groups including `C add column … > column right` and `? help`, `q quit`, with no `…`. If any other golden changed, revert it (`git checkout -- <file>`) and investigate why the short help changed; the short help path must be untouched.

- [ ] **Step 7: Run everything**: `go test -race ./...` (this includes `TestEveryScreenFitsTheTerminal` at 60x18, 80x24, 120x40 which now exercises the wrapped help via `assertFits`) and `PATH=$HOME/go/bin:$PATH make lint`.

- [ ] **Step 8: Commit**
```bash
git add internal/ui/helpview.go internal/ui/helpview_test.go internal/ui/model.go internal/ui/testdata/TestGolden/help.golden
git commit -m "feat(ui): full help wraps to the terminal width so every key group shows"
```

---

### Task 3: Docs

**Files:**
- Modify: `README.md` (the non-interactive CLI section; find the `kancli columns` line, ~line 281, and the paragraph about completion)
- Modify: `CHANGELOG.md` (`## Unreleased` → `### Added`)

- [ ] **Step 1: README.** After the existing `kancli columns` example line add:
```
kancli columns add Review --wip 2   # add a column at the right
kancli columns edit review --name QA --color 99
kancli columns move qa 2            # left | right | first | last | position
kancli columns rm qa --to done      # delete, moving its tasks
```
and in the key table area, no change (column keys are already listed). In the "full help" mention (search for "`?`" in README), if it says the help is a single line or similar, reword to: "`?` toggles the full key reference; it wraps to your terminal width."

- [ ] **Step 2: CHANGELOG.** Under `### Added`, after the "Safer upgrades" bullet:
```markdown
- Columns from the command line: `kancli columns add | edit | rename |
  move | rm`, with completion of column names and palette colours.
- The full help (`?`) wraps to the terminal width, so the column keys and
  `?`/`q` are no longer cut off on narrower terminals.
```

- [ ] **Step 3: Commit**
```bash
git add README.md CHANGELOG.md
git commit -m "docs: column commands and wrapped help"
```
