# Kancli

A kanban board for the command line, built with
[Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles) and
[Lip Gloss](https://github.com/charmbracelet/lipgloss).

```
 Kancli                                            ~/.local/share/kancli/board.json
╭────────────────────────────╮╭────────────────────────────╮╭────────────────────────────╮
│    To Do                   ││    In Progress             ││    Done                    │
│                            ││                            ││                            │
│   3 tasks                  ││   1 task                   ││   1 task                   │
│                            ││                            ││                            │
│ │ buy milk                 ││ │ write code               ││ │ stay cool                │
│ │ strawberry milk          ││ │ don't worry, it's Go     ││ │ as a cucumber            │
│                            ││                            ││                            │
│   eat sushi                ││                            ││                            │
│   negitoro roll, miso so…  ││                            ││                            │
╰────────────────────────────╯╰────────────────────────────╯╰────────────────────────────╯
 n new • e/enter edit • d delete • L/⇧→ move right • / filter • ? help • q quit
```

This started as the demo repo for the kanban tutorial on the Charm
[YouTube channel](https://youtube.com/c/charmcli) and has grown into a
complete little app.

## Features

* Three columns: **To Do**, **In Progress** and **Done**.
* Create, edit and delete tasks with a title and a multi-line description.
* Move tasks between columns and reorder them within a column.
* Fuzzy filter the focused column.
* Everything is saved automatically to a JSON file, so your board is there
  the next time you open it.
* Responsive layout that adapts to the terminal size.

## Install

```sh
go install github.com/charmbracelet/kancli@latest
```

Or build from source:

```sh
git clone https://github.com/charmbracelet/kancli.git
cd kancli
go build
./kancli
```

## Usage

```
kancli [flags]

Flags:
  -f, -file string   path to the board file
  -demo              start with sample tasks and don't save anything
```

The board is stored at `$XDG_DATA_HOME/kancli/board.json`, falling back to
the user config directory (for example `~/.config/kancli/board.json` on Linux
or `~/Library/Application Support/kancli/board.json` on macOS). Set
`KANCLI_FILE` to use a different file by default, or pass `-file` for a single
run. Every change is written to disk immediately.

Try it without touching your real board:

```sh
kancli -demo
```

### Keys

| Key                | Action                       |
| ------------------ | ---------------------------- |
| `↑`/`k`, `↓`/`j`   | Move the cursor              |
| `←`/`h`, `→`/`l`   | Focus the previous/next column |
| `1`, `2`, `3`      | Jump to a column             |
| `n`                | New task in the focused column |
| `e` or `enter`     | Edit the selected task       |
| `d`                | Delete the selected task (asks first) |
| `L`, `⇧→` or `]`   | Move the task one column right |
| `H`, `⇧←` or `[`   | Move the task one column left |
| `J`/`⇧↓`, `K`/`⇧↑` | Move the task down/up within its column |
| `/`                | Filter the focused column (`esc` clears) |
| `?`                | Toggle the full help         |
| `q` or `ctrl+c`    | Quit                         |

In the task form, `tab` and `shift+tab` switch between the title and the
description, `enter` in the title jumps to the description, `ctrl+s` saves and
`esc` cancels.

### Board file

The board is plain JSON, so it is easy to back up or edit by hand:

```json
{
  "version": 1,
  "tasks": [
    {
      "id": "3f2c9a1e7b5d4c60",
      "status": "todo",
      "title": "buy milk",
      "description": "strawberry milk",
      "created_at": "2026-09-02T09:00:00Z",
      "updated_at": "2026-09-02T09:00:00Z"
    }
  ]
}
```

`status` is one of `todo`, `in_progress` or `done`. Tasks are kept in the
order they appear in the file.

## Development

```sh
go test ./...
```

Set `KANCLI_DEBUG=1` to write Bubble Tea debug logs to `./debug.log`.

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
