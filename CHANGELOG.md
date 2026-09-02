# Changelog

## Unreleased

### Added

- Task priority, due dates (with natural input like `fri` or `+3d`), labels,
  assignee, checklists, attachments, comments and a per-task activity log.
- User-visible task numbers (`#12`) on every card.
- Configurable columns: add, rename, recolour, reorder and delete columns,
  with optional WIP limits shown in the column header.
- Multiple boards in one file, with a board picker (`b`) and `-board` flag.
- Archive: archive tasks (`a`), archive every done task (`Z`), browse,
  restore or purge archived tasks (`z`).
- Search across all columns and fields with `/`, including `#id`, `@who`,
  `+label`, `p:high`, `due:today` and `col:name` filters.
- Sort modes (`s`): manual, priority, due, created, updated, title.
- Undo and redo (`u` / `U`) for every change.
- Task detail view (`enter`) with checklist toggling, comments, attachment
  opening and history.
- Multi-select with `space` for bulk move, archive and delete.
- Mouse support: click to focus and select, click again to open, wheel to
  scroll.
- Detection of external edits to the data file: automatic reload when there
  is nothing unsaved, and a refusal to overwrite otherwise (`R` reloads,
  `ctrl+s` overwrites).
- Non-interactive CLI: `add`, `list`, `show`, `move`, `done`, `archive`,
  `restore`, `rm`, `due` (with `-notify` desktop notifications), `export`
  and `import` (JSON, CSV, Markdown), `boards`, `columns`, `config`, `keys`.
- Overdue / due-today badge in the header.
- Config file with theme, ASCII borders, compact cards, default sort and key
  binding overrides. Themes: default, high-contrast, mono. `NO_COLOR` is
  respected.
- Automatic migration of version 1 board files.
- CI on Linux, macOS and Windows, golangci-lint, and GoReleaser releases with
  a `-version` flag.

### Changed

- The data file format is now version 2 (multiple boards, numeric task ids).
- `enter` opens the task; `e` edits it.
