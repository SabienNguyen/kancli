package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestFullHelpWrapsToWidth(t *testing.T) {
	groups := DefaultKeyMap().FullHelp()
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
