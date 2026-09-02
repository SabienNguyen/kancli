// Command kancli is a kanban board for the terminal.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/SabienNguyen/kancli/internal/cli"
	"github.com/SabienNguyen/kancli/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	os.Exit(cli.Main(version, os.Args[1:], os.Stdout, os.Stderr, launch))
}

// launch opens the interactive board for a prepared environment.
func launch(e *cli.Env) error {
	if os.Getenv("KANCLI_DEBUG") != "" {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			return err
		}
		defer f.Close()
	}
	a := ui.New(e.Cfg, e.Styles, e.Glyphs, e.Store, e.File)
	if e.ReadOnly {
		a.SetReadOnly(e.AsOf)
	}
	p := tea.NewProgram(a, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}
