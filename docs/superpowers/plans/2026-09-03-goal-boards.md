# Goal Boards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Links can point at tasks on other boards, written `work#12`, and a board can be marked as a goal board whose cards roll up progress from the tickets they link, with a small visible tag and "goal" wording but otherwise identical UI.

**Architecture:** `Link.Board` (additive) plus a back-reference from each `Board` to its `File` lets the existing relation code follow links across boards without changing call sites. `Board.Kind` (additive) plus a `board.kind` event marks goal boards. A single `board.ParseRef` turns `#12` / `work#12` / `Work#12` into a (board, id) pair for the prompt, the CLI, mentions and search.

**Tech Stack:** Go, existing packages. `go test -race ./...`, `PATH=$HOME/go/bin:$PATH make lint`.

**Spec:** `docs/superpowers/specs/2026-09-03-goal-boards-design.md`.

## Global Constraints

- Executors run with `model: "opus"`, one task per dispatch; the main session reviews and re-runs tests.
- Never rename or remove JSON fields or event kinds. Adding is fine.
- Goldens under `internal/ui/testdata` change only in Task 4, only via `-update`, and only the files named there.
- Commit after every task. Do not push.

---

### Task 1: Cross-board links in the board package

**Files:** `internal/board/links.go`, `internal/board/data.go` (`DeleteTask` cleanup, `File.Attach`, `File.RemoveBoard`), `internal/board/events.go` (link events carry the board), `internal/board/query.go` (filters accept refs), tests in `internal/board/links_test.go` (create if absent) and `replay_prop_test.go`.

**Interfaces (verbatim):**
```go
type Link struct {
	Kind  LinkKind `json:"kind"`
	Task  int      `json:"task"`
	Board string   `json:"board,omitempty"` // other task's board id; "" = same board
}
// Ref names a task, possibly on another board.
type Ref struct{ Board string; ID int } // Board "" = the current board
// ParseRef reads "#12", "12", "work#12" or "Work#12". cur resolves a bare
// number; f resolves board ids and names (nil f: only bare numbers).
func ParseRef(s string, cur *Board, f *File) (Ref, error)
func (r Ref) String() string           // "#12" or "work#12"
func (b *Board) Resolve(l Link) (*Board, *Task)   // follows l.Board through b.file; nil,nil when unresolvable
func (f *File) TaskAt(boardID string, id int) (*Board, *Task)
// AddLink/RemoveLink keep their signatures for same-board links and gain:
func (b *Board) AddLinkTo(from int, kind LinkKind, to Ref) error
func (b *Board) RemoveLinkTo(from int, kind LinkKind, to Ref) bool
// Relation gains:
Board string // id of the other task's board ("" when it is this board)
```
Behaviour:
- `File.Attach` sets `b.file = f` on every board (unexported field, excluded from JSON, copied in `Replace`/`EmptyBase`/`EvBoardRestored` like `rec`).
- `Resolve`: `l.Board == "" || l.Board == b.ID` → local lookup; else `b.file.TaskAt(l.Board, l.Task)`; nil file → nil.
- `Relations`, `Blockers`, `Subtasks`, `SubtaskProgress`, `Parent`, `IsBlocked`, `BlockedCount`, `isFinished` (finished = last column of the task's OWN board or archived), `reaches` (cycle check walks across boards with a `(board,id)` seen set) all use `Resolve`. Reverse relations (who links to me) scan every board in `b.file` when it is set, else only `b`.
- `AddLinkTo` validates both ends exist, refuses self links and cycles, normalises `to.Board == b.ID` to `""`, stores the link on `from`, emits `Event{Kind: EvLinkAdded, Task: from, Index: to.ID, Text: string(kind), To: to.Board}`. `AddLink(from, kind, to)` = `AddLinkTo(from, kind, Ref{ID: to})`. Same for remove. `Apply` reads `e.To` back into `Link.Board`.
- Cleanup: `DeleteTask`/`dropLinksTo` also removes links on other boards that point at `(b.ID, id)` (via `b.file`), and `File.RemoveBoard` removes every link into the removed board. Both emit nothing extra: the cleanup is a consequence replay reproduces (the deleting event replays the same cleanup).
- Mentions: `mentionRE` becomes `(?:^|[^\w&])(?:([A-Za-z0-9_-]+))?#(\d+)\b`; `Mentions(text)` keeps returning `[]int` for the bare ones and a new `MentionRefs(text, cur, f) []Ref` returns all; `linkMentions` uses the refs.
- Query filters `blocks:`, `blockedby:`, `parent:` accept refs; store `Ref` in the query struct; `Matches` uses `Resolve`.
- ParseLinkSpec unchanged (words), but callers now pass refs.

Tests (write first): `TestCrossBoardLinkRoundTrip` (goal on board A, tickets on board B; `A.AddLinkTo(1, LinkSubtaskOf reversed via ParseLinkSpec("parent-of"), ...)` from either side; `SubtaskProgress` on the goal counts B's tickets and their done state on B's last column; `Relations` on the ticket shows `subtask of` with `Board == "a"`; deleting the ticket on B drops the link on A; `RemoveBoard("b")` drops links into B; cycle A#1 blocks B#2 blocks A#1 refused), `TestParseRef` (all spellings, unknown board error, nil file), `TestMentionsAcrossBoards`, `TestQueryRefsAcrossBoards`, and replay equality: emit through `AddLinkTo`, replay onto a fresh file, compare JSON. Add ops `xlink`/`xunlink` to the property test that pick a random other board.

Commit: `feat(board): links across boards with work#12 references`.

---

### Task 2: Board kind

**Files:** `internal/board/data.go` (`Board.Kind`, `File.SetBoardKind`), `internal/board/events.go` (`EvBoardKind`), `internal/board/sample.go` (the demo file gains a goal board "Roadmap" with two goals linked to demo tickets — see Task 4 goldens), tests.

**Interfaces (verbatim):**
```go
Kind string `json:"kind,omitempty"`  // "" or "tasks" = ticket board; "goals" = goal board
const BoardKindTasks, BoardKindGoals = "tasks", "goals"
func (b *Board) IsGoals() bool
func (b *Board) Noun() string          // "task" or "goal"
func (f *File) SetBoardKind(id, kind string) error   // validates, no-op emits nothing, emits board.kind (Text = kind)
EvBoardKind EventKind = "board.kind"
```
`Apply`/`Describe`/stats walker (nothing to do for stats). Tests: set/replay/no-op/invalid kind; `Noun`.

Commit: `feat(board): goal boards (board kind) with a board.kind event`.

---

### Task 3: CLI

**Files:** `internal/cli/root.go`, `internal/cli/cli.go`, `internal/cli/cli_test.go`.
- `link` / `unlink` accept refs on both sides (`kancli link 12 subtask-of roadmap#3`, `kancli link roadmap#3 parent-of work#12`); the first argument may itself be a ref (then the link is stored from that board). Output uses `Ref.String()` for foreign ends: `#12 subtask-of roadmap#3`.
- `boards new <name> --goals`; `boards kind <board> goals|tasks` with completion of `goals|tasks`; `boards` list shows `goals` after the name column for goal boards.
- `list` / `show`: relation lines print foreign refs with prefix; `add` on a goal board prints `Added goal #3 to To Do` (use `b.Noun()`); `--json` unchanged.
- Search filters with refs work through Task 1.
Tests: `TestCLIGoalBoards` end-to-end: create Roadmap as goals, add a goal, add tickets on main, link both ways, `show` on both sides shows the prefixed relation and progress `1/2` after `done` on one ticket, `boards` shows `goals`, `rm` of a ticket drops the link, `list -q parent:roadmap#1` on main lists the tickets.

Commit: `feat(cli): goal boards and cross-board links`.

---

### Task 4: UI

**Files:** `internal/ui/model.go` (link prompt parsing via `ParseRef`; `g` on a foreign relation → `switchBoard` then open the task; status wording via `Noun`; header tag), `internal/ui/detail.go` (relation lines with prefix; progress line counts foreign subtasks), `internal/ui/column.go` (compact cards on goal boards still show `↳ n/m`), `internal/ui/dialogs.go` (picker row `goals ·`, picker key `k` toggles kind), `internal/ui/keys.go` (`Kind: bind("k", "toggle goals", "k")` in the picker map, and the `n` help text reads "new goal" on goal boards — compute from the board when rendering help if bindings are static), `internal/ui/golden_test.go` (new cases `goals-board` (open the Roadmap board via `b`, select it, enter) and `goals-detail` (enter on the first goal)), goldens: only `boards.golden` (demo gains the Roadmap board), `help.golden` if the picker help line changed, and the two new files.
Tests: `TestGoalBoardLooksTheSameButSaysSo` (header contains "goals", card shows progress, status after `n` says "goal"), `TestGoToForeignLink` (on a ticket, `g` on the "subtask of roadmap#1" relation lands on Roadmap with the goal open).

Commit: `feat(ui): goal boards, cross-board relations and jumps`.

---

### Task 5: Docs

`README.md` (a "Goals" section: what a goal board is, `work#12` syntax, keys and commands), `docs/compatibility.md` (new fields and kind; the older-kancli caveat from the spec), `CHANGELOG.md`.

Commit: `docs: goal boards and cross-board links`.
