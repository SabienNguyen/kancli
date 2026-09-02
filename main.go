package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func usage() {
	fmt.Fprintf(flag.CommandLine.Output(), `kancli — a kanban board for your terminal

Usage:
  kancli [flags]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprintf(flag.CommandLine.Output(), `
Environment:
  KANCLI_FILE   board file to use when -file is not given
  KANCLI_DEBUG  when set, write debug logs to ./debug.log

The board is stored under $XDG_DATA_HOME/kancli/board.json, falling back to
the user config directory when XDG_DATA_HOME is unset.
`)
}

func main() {
	var (
		path string
		demo bool
	)
	flag.StringVar(&path, "file", "", "path to the board file")
	flag.StringVar(&path, "f", "", "shorthand for -file")
	flag.BoolVar(&demo, "demo", false, "start with sample tasks and don't save anything")
	flag.Usage = usage
	flag.Parse()

	if os.Getenv("KANCLI_DEBUG") != "" {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			fmt.Fprintf(os.Stderr, "kancli: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
	}

	var (
		st    store
		tasks []Task
	)
	if demo {
		tasks = sampleTasks()
	} else {
		if path == "" {
			var err error
			path, err = defaultStorePath()
			if err != nil {
				fmt.Fprintf(os.Stderr, "kancli: %v\n", err)
				os.Exit(1)
			}
		}
		st = newStore(path)
		var err error
		tasks, err = st.load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "kancli: %v\n", err)
			os.Exit(1)
		}
	}

	p := tea.NewProgram(newBoard(st, tasks), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "kancli: %v\n", err)
		os.Exit(1)
	}
}
