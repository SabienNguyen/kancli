package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/SabienNguyen/kancli/internal/board"
)

// config is the optional user configuration file.
type Config struct {
	// File is the data file to open (same as -file).
	File string `json:"file,omitempty"`
	// Board is the board to open on start.
	Board string `json:"board,omitempty"`
	// Theme is one of default, high-contrast or mono.
	Theme string `json:"theme,omitempty"`
	// ASCII draws borders with +-| instead of box-drawing characters.
	ASCII bool `json:"ascii,omitempty"`
	// Compact shows two-line cards instead of three-line ones.
	Compact bool `json:"compact,omitempty"`
	// Sort is the initial sort mode: manual, priority, due, created,
	// updated or title.
	Sort string `json:"sort,omitempty"`
	// Keys overrides key bindings, keyed by action name (see `kancli keys`).
	Keys map[string][]string `json:"keys,omitempty"`
}

// defaultConfigPath is $KANCLI_CONFIG, else kancli/config.json under
// $XDG_CONFIG_HOME or the OS config dir.
func DefaultPath() (string, error) {
	if p := os.Getenv("KANCLI_CONFIG"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate config directory: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "kancli", "config.json"), nil
}

// Load reads the config file. A missing file yields the defaults.
func Load(path string) (Config, error) {
	var c Config
	if path == "" {
		return c, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Sort != "" {
		if _, ok := board.ParseSortMode(c.Sort); !ok {
			return c, fmt.Errorf("%s: unknown sort %q", path, c.Sort)
		}
	}
	for action := range c.Keys {
		if !ValidAction(action) {
			return c, fmt.Errorf("%s: unknown key action %q", path, action)
		}
	}
	return c, nil
}

// Actions are the key-binding action names accepted in the "keys" map.
var Actions = []string{
	"add_column", "archive", "archive_done", "archive_view", "back", "boards", "column_left", "column_right",
	"delete", "delete_column", "down", "edit", "edit_column", "help", "jump", "left", "mark", "move_down",
	"move_left", "move_right", "move_up", "new", "quit", "redo", "reload", "right", "save", "search", "sort",
	"stats", "undo", "up", "view",
}

// validAction reports whether name is a configurable action.
func ValidAction(name string) bool {
	for _, a := range Actions {
		if a == name {
			return true
		}
	}
	return false
}

// exampleConfig is printed by `kancli config`.
const Example = `{
  "theme": "default",
  "ascii": false,
  "compact": false,
  "sort": "manual",
  "keys": {
    "quit": ["q", "ctrl+c"],
    "new": ["n"]
  }
}
`
