# Goal boards and cross-board links

Date: 2026-09-03. Status: approved in conversation. Plan:
`docs/superpowers/plans/2026-09-03-goal-boards.md`.

## What the user asked for

A board filled with goals, where each goal links to tickets that live on
other boards. Goal boards must not look much different from ticket boards,
but it must be clear to the user which is which.

## Decisions

- **Links can cross boards.** `Link` gains `Board string` (json
  `board,omitempty`): the id of the other task's board, empty for the same
  board. Every existing file stays valid. The `link.added` / `link.removed`
  events carry the target board in the otherwise unused `To` field.
- **A task on another board is written `work#12`**: board id or name (case
  insensitive, the same lookup `File.Board` uses), then `#`, then the
  number. Plain `#12` keeps meaning the current board. The syntax is accepted
  everywhere a task is referenced by number: the link prompt in the task
  view, `kancli link` / `kancli unlink`, `#` mentions in descriptions and
  comments (which auto-link, as today), and the search filters `blocks:`,
  `blockedby:` and `parent:`.
- **Boards know their file.** `File.Attach` (already called on every load)
  sets a back-reference `b.file` on each board. Board methods that resolve a
  link (`Relations`, `Blockers`, `Subtasks`, `SubtaskProgress`, `Parent`,
  `reaches`, `dropLinksTo`, `AddLink` validation) follow `Link.Board` through
  it. Call sites do not change. A board without a file (tests that build a
  bare `Board`) treats every foreign link as unresolvable and skips it.
- **A goal is any task on a goal board.** No new task type. A board gains
  `Kind string` (json `kind,omitempty`): `""`/`"tasks"` or `"goals"`. It is
  set with `kancli boards new Roadmap --goals`, `kancli boards kind <board>
  goals|tasks`, and `k` in the board picker (toggles). Changing it emits
  `board.kind` (Text = kind).
- **How a goal board is visibly different, and nothing else:**
  - Header: `Kancli · Roadmap · goals` (the word "goals" in the muted style
    after the board name).
  - Board picker row and `kancli boards` list: `goals ·` before the counts.
  - Cards on a goal board show the subtask progress (`↳ 3/7`) even when the
    card is compact, and the progress counts tickets on every board.
  - Wording: "Added goal #3", "goal" instead of "task" in status messages and
    CLI output when the board is a goal board. The `n` key help says
    "new goal".
  - Columns, keys, sorting, search and everything else are identical.
- **Semantics across boards** are the ones that exist today: a task is
  finished when it is in the last column of its own board or archived; a
  goal blocked by a foreign ticket shows the blocked marker; finishing a goal
  with open foreign subtasks gets the existing warning; cycles in
  `blocks`/`subtask_of` are refused across boards; deleting a task removes
  links to it on every board; deleting a board removes links into it.
- **Relations display** the other task with its board prefix when it is
  foreign: `blocked by work#12 Fix login (In Progress)`. `g` in the task
  view on a foreign relation switches to that board and opens the task.
- **Undo** stays per board. Linking a goal to a ticket on another board is
  stored on the source task only, so a single undo entry covers it.

## Out of scope

Auto-finishing a goal when its last ticket finishes; the all-boards view;
links to tasks in other files.

## Compatibility

Additive fields (`Link.Board`, `Board.Kind`) and one new event kind
(`board.kind`). Old files load unchanged. An older kancli ignores the fields:
it would show foreign links as dangling (`no task #12` on the wrong board)
and drop `Link.Board` on its next write of that task, which the release
notes must say.
