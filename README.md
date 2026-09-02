# Kancli

A kanban board for the command line, built with
[Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles) and
[Lip Gloss](https://github.com/charmbracelet/lipgloss).

```
 Kancli · Demo                                  1 overdue · 1 due today · ~/.config/kancli/board.json
╭───────────────────────────────╮╭───────────────────────────────╮╭──────────────────────────────╮
│    To Do 4                    ││    In Progress 2/2            ││    Done 2                    │
│                               ││                               ││                              │
│ │ #1 ↑ Write the release not… ││ │ #5 ‼ Ship the CLI subcomma… ││ │ #7 Upgrade to Bubble Tea…  │
│ │ Cover the new search synta… ││ │ add, list, move, done, exp… ││ │                            │
│ │ tomorrow · @sam · +docs · … ││ │ today · @sam · +feature · … ││ │ +chore                     │
│                               ││                               ││                              │
│   #2 • Fix the flaky termina… ││   #6 • Review the mouse supp… ││   #8 ↓ Stay cool             │
│                               ││                               ││   as a cucumber              │
│   2d overdue · +bug · +tests  ││   in 3d · @alex · +review     ││                              │
╰───────────────────────────────╯╰───────────────────────────────╯╰──────────────────────────────╯
 n new task • enter/v view • e edit • L/⇧→ move right • / search • u undo • ? help • q quit
```

This started as the demo repo for the kanban tutorial on the Charm
[YouTube channel](https://youtube.com/c/charmcli) and has grown into a
complete app.

## Features

- **Tasks** with a title, description, priority, due date, labels, assignee,
  checklist, attachments, comments and an activity log. Every task has a
  number you can refer to (`#12`).
- **Columns** you can add, rename, recolour, reorder and delete, with optional
  WIP limits.
- **Multiple boards** in one file.
- **Search** across every column and field, **sort** modes, **multi-select**
  for bulk moves, **undo/redo**, an **archive**, and **mouse** support.
- **Automatic saving** to a JSON file, with detection of edits made by other
  programs.
- A **CLI** for scripts and shell prompts: add, list, move, export, import,
  due-date reminders and more.
- **Themes** (default, high-contrast, mono), ASCII borders, compact cards and
  configurable keys.

## Install

```sh
go install github.com/charmbracelet/kancli@latest
```

Prebuilt binaries for Linux, macOS, Windows and FreeBSD are attached to each
[release](https://github.com/SabienNguyen/kancli/releases). Or build from
source:

```sh
git clone https://github.com/SabienNguyen/kancli.git
cd kancli
go build
./kancli -demo
```

## The board

```
kancli                 open your board
kancli -demo           try it with sample data, nothing is saved
kancli -b work         open the board named "work"
kancli -theme mono     no colours; also: high-contrast
kancli -ascii          +-| borders for terminals without box drawing
kancli -compact        two-line cards
```

### Keys

| Key                | Action                                        |
| ------------------ | --------------------------------------------- |
| `↑`/`k`, `↓`/`j`   | Move the cursor                               |
| `←`/`h`, `→`/`l`   | Focus the previous/next column                |
| `1`…`9`            | Jump to a column                              |
| `n`                | New task in the focused column                |
| `enter` / `v`      | Open the task                                 |
| `e`                | Edit the task                                 |
| `d`                | Delete (asks first)                           |
| `a`                | Archive                                       |
| `space`            | Mark the task for a bulk action               |
| `L`, `⇧→`, `]`     | Move right (all marked tasks if any)          |
| `H`, `⇧←`, `[`     | Move left                                     |
| `J`/`⇧↓`, `K`/`⇧↑` | Move down/up within the column               |
| `/`                | Search (see below); `esc` clears              |
| `s`                | Cycle sort: manual, priority, due, created, updated, title |
| `u` / `U`          | Undo / redo                                   |
| `b`                | Boards: open, create, rename, delete          |
| `z`                | Archived tasks: restore or delete             |
| `Z`                | Archive every task in the last column         |
| `C` / `E` / `D`    | Add / edit / delete the focused column        |
| `<` / `>`          | Move the focused column                       |
| `R`                | Reload the file from disk                     |
| `ctrl+s`           | Force a save after an external edit           |
| `?`                | Toggle the full help                          |
| `q` / `ctrl+c`     | Quit                                          |

Mouse: click a column to focus it, click a card to select it, click it again
to open it, and scroll with the wheel.

In the **task form**, `tab` and `shift+tab` move between fields, `enter`
moves to the next field (and saves on the last one), `ctrl+s` saves and `esc`
cancels. The priority field cycles with `←`/`→`. The due date accepts
`2026-09-10`, `today`, `tomorrow`, `fri`, `+3d`, `+2w` or `+1m`.

In the **task view**, `tab` steps through checklist items and attachments,
`space` toggles an item, `X` removes it, `t` adds a checklist item, `c` adds a
comment, `A` attaches a link or path, `o` opens the attachment, `e` edits,
`H`/`L` move the task, `a` archives, `d` deletes and `esc` goes back.

### Search

`/` filters every column at once. Plain words match the title, description,
labels, assignee, checklist and comments. These terms narrow by field:

```
#12          task number
@sam         assignee
+bug         label (also label:bug)
p:high       priority: none, low, medium, high, urgent
due:today    also tomorrow, overdue, week, none, or a date
col:done     column name or id
```

Terms combine: `+bug due:week @sam` finds Sam's bugs due this week.

### Multiple boards

Press `b` to switch boards, or pass `-board NAME`. Each board has its own
columns and tasks. The last column of a board counts as "done" for the
`done` command, the `Z` shortcut and the due-date badge.

### Working with other programs

Every change is written to the data file immediately. If another program
(or a second kancli) changes the file while the board is open, kancli reloads
it within a couple of seconds as long as you have nothing unsaved. If you
made a change in the meantime, the save is refused and the header tells you:
`R` reloads the file (dropping your change) and `ctrl+s` overwrites it.

## The CLI

Everything can be done without the UI, which makes kancli easy to script:

```sh
kancli add "Write the release notes" -p high -due fri -l docs -a sam
kancli add -c "in progress" "Ship the CLI"
kancli list                       # table of live tasks
kancli list -q "+bug due:week"    # same search syntax as the UI
kancli list -json                 # for jq and friends
kancli show 12
kancli move 12 done
kancli done 12 13                 # move to the last column
kancli archive 12 && kancli restore 12
kancli rm 12
kancli due                        # overdue and due-today tasks
kancli due -days 7 -notify        # desktop notification when something is due
kancli export -f md               # or csv, json; -o file
kancli import tasks.csv           # or .md, .json
kancli boards new Work            # boards use|rename|rm
kancli columns
kancli config                     # where the config file lives
kancli keys                       # configurable actions
kancli -version
```

`kancli due -q` prints nothing and exits 1 when something is due, so it works
in a shell prompt or a cron job:

```sh
0 9 * * 1-5  kancli due -notify
```

### Export and import formats

- **JSON**: the board as stored on disk. `-all` exports every board.
- **CSV**: one row per task with `id, column, title, description, priority,
  due, labels, assignee, created_at, updated_at, archived_at`. Import needs
  only a `title` column.
- **Markdown**: a heading per column and a task list item per task, with
  metadata in backticks and indented lines for the description and checklist:

  ```markdown
  ## To Do (2)

  - [ ] #1 Write the release notes `!high due:2026-09-05 +docs @sam`
    Cover the new search syntax.
    - [x] Draft
    - [ ] Review
  ```

Imported tasks get new numbers. Columns are matched by name; unmatched
tasks go to `-c COLUMN` or the first column.

## Files

| What        | Where                                                             |
| ----------- | ----------------------------------------------------------------- |
| Data file   | `$KANCLI_FILE`, else `$XDG_DATA_HOME/kancli/board.json`, else the OS config dir (`~/.config/kancli/board.json` on Linux, `~/Library/Application Support/kancli/board.json` on macOS) |
| Config file | `$KANCLI_CONFIG`, else `$XDG_CONFIG_HOME/kancli/config.json`, else the OS config dir |

Override the data file for one run with `-file PATH`.

### Config file

All keys are optional:

```json
{
  "file": "~/notes/board.json",
  "board": "work",
  "theme": "default",
  "ascii": false,
  "compact": false,
  "sort": "manual",
  "keys": {
    "quit": ["q", "ctrl+c"],
    "archive": ["x"],
    "archive_done": []
  }
}
```

`theme` is `default`, `high-contrast` or `mono`. `NO_COLOR` in the environment
disables colour too. `keys` maps an action (see `kancli keys`) to a list of
keys; an empty list disables the action.

### Data file

The file is plain JSON, so it is easy to back up, diff or edit by hand.
Version 1 files from the original tutorial are migrated automatically.

```json
{
  "version": 2,
  "active_board": "main",
  "boards": [
    {
      "id": "main",
      "name": "Main",
      "columns": [
        { "id": "todo", "name": "To Do", "color": "62" },
        { "id": "in_progress", "name": "In Progress", "color": "214", "wip_limit": 3 },
        { "id": "done", "name": "Done", "color": "35" }
      ],
      "tasks": [
        {
          "id": 1,
          "column": "todo",
          "title": "Write the release notes",
          "description": "Cover the new search syntax.",
          "priority": "high",
          "due": "2026-09-05",
          "labels": ["docs"],
          "assignee": "sam",
          "checklist": [{ "text": "Draft", "done": true }],
          "attachments": ["https://example.com/notes"],
          "comments": [{ "at": "2026-09-02T09:00:00Z", "text": "Started." }],
          "history": [{ "at": "2026-09-02T08:00:00Z", "text": "Created in To Do" }],
          "created_at": "2026-09-02T08:00:00Z",
          "updated_at": "2026-09-02T09:00:00Z"
        }
      ],
      "next_id": 2
    }
  ]
}
```

Tasks are kept in board order, so the order in the file is the manual order
within each column. An archived task carries an `archived_at` timestamp.

## Development

```sh
go test -race ./...
```

CI runs `gofmt`, `go vet`, the tests on Linux, macOS and Windows, and
golangci-lint. Pushing a tag like `v1.0.0` builds release binaries with
GoReleaser (see `.goreleaser.yaml`, which also has a commented Homebrew tap
section). Set `KANCLI_DEBUG=1` to write Bubble Tea debug logs to
`./debug.log`.

## Feedback

We'd love to hear your thoughts on this tutorial. Feel free to drop us a note!

* [Twitter](https://twitter.com/charmcli)
* [The Fediverse](https://mastodon.social/@charmcli)
* [Discord](https://charm.sh/chat)

## License

[MIT](https://github.com/charmbracelet/bubbletea/raw/master/LICENSE)

***

Part of [Charm](https://charm.sh).

<a href="https://charm.sh/"><img alt="The Charm logo" src="https://stuff.charm.sh/charm-badge.jpg" width="400"></a>

Charm热爱开源 • Charm loves open source
