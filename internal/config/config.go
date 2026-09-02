package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

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
	// NoAnimations turns off the card animation when a task moves.
	NoAnimations bool `json:"no_animations,omitempty"`
	// Images controls inline previews of image attachments in terminals
	// that support the kitty graphics protocol: "auto" (default), "on" to
	// force them, or "off".
	Images string `json:"images,omitempty"`
	// Sort is the initial sort mode: manual, priority, due, created,
	// updated or title.
	Sort string `json:"sort,omitempty"`
	// Keys overrides key bindings, keyed by action name (see `kancli keys`).
	Keys map[string][]string `json:"keys,omitempty"`
	// Warnings is advice about the file, for stderr.
	Warnings []string `json:"-"` // advice about the file, for stderr
}

// renamedKeys maps a key from an older release to its current name. A
// file that still uses the old key is read as if it used the new one and
// gets a warning. Add a row here whenever a key is renamed; never delete
// rows.
var renamedKeys = map[string]string{}

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
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	for old, now := range renamedKeys {
		v, ok := raw[old]
		if !ok {
			continue
		}
		if _, exists := raw[now]; !exists {
			raw[now] = v
		}
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s: %q is now called %q; rename it", path, old, now))
	}
	sort.Strings(c.Warnings)
	fixed, err := json.Marshal(raw)
	if err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := json.Unmarshal(fixed, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	switch c.Images {
	case "", "auto", "on", "off":
	default:
		return c, fmt.Errorf("%s: images must be \"auto\", \"on\" or \"off\", not %q", path, c.Images)
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
  "no_animations": false,
  "images": "auto",
  "keys": {
    "quit": ["q", "ctrl+c"],
    "new": ["n"]
  }
}
`
