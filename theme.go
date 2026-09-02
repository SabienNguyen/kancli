package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// theme is a named colour scheme.
type theme struct {
	name    string
	mono    bool
	accent  lipgloss.TerminalColor
	muted   lipgloss.TerminalColor
	text    lipgloss.TerminalColor
	strong  lipgloss.TerminalColor
	err     lipgloss.TerminalColor
	success lipgloss.TerminalColor
	warning lipgloss.TerminalColor
	info    lipgloss.TerminalColor
	onColor lipgloss.TerminalColor
	border  lipgloss.Border
}

var themeNames = []string{"default", "high-contrast", "mono"}

// themeByName builds a theme. ascii swaps box-drawing borders for +-|.
func themeByName(name string, ascii bool) (theme, error) {
	var th theme
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "default":
		th = theme{
			name:    "default",
			accent:  lipgloss.Color("205"),
			muted:   lipgloss.Color("240"),
			text:    lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"},
			strong:  lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
			err:     lipgloss.Color("196"),
			success: lipgloss.Color("35"),
			warning: lipgloss.Color("214"),
			info:    lipgloss.Color("39"),
			onColor: lipgloss.Color("230"),
		}
	case "high-contrast", "contrast", "hc":
		th = theme{
			name:    "high-contrast",
			accent:  lipgloss.Color("13"),
			muted:   lipgloss.AdaptiveColor{Light: "#444444", Dark: "#bbbbbb"},
			text:    lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
			strong:  lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
			err:     lipgloss.Color("9"),
			success: lipgloss.Color("10"),
			warning: lipgloss.Color("11"),
			info:    lipgloss.Color("14"),
			onColor: lipgloss.Color("0"),
		}
	case "mono", "monochrome", "none":
		th = theme{
			name:    "mono",
			mono:    true,
			accent:  lipgloss.NoColor{},
			muted:   lipgloss.NoColor{},
			text:    lipgloss.NoColor{},
			strong:  lipgloss.NoColor{},
			err:     lipgloss.NoColor{},
			success: lipgloss.NoColor{},
			warning: lipgloss.NoColor{},
			info:    lipgloss.NoColor{},
			onColor: lipgloss.NoColor{},
		}
	default:
		return th, fmt.Errorf("unknown theme %q (use %s)", name, strings.Join(themeNames, ", "))
	}
	if ascii {
		th.border = lipgloss.Border{
			Top: "-", Bottom: "-", Left: "|", Right: "|",
			TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
		}
	} else {
		th.border = lipgloss.RoundedBorder()
	}
	return th, nil
}

// columnColor resolves a column's configured colour under the theme.
func (th theme) columnColor(c string) lipgloss.TerminalColor {
	if th.mono || c == "" {
		return th.muted
	}
	return lipgloss.Color(c)
}

// priorityColor picks a colour for a priority marker.
func (th theme) priorityColor(p Priority) lipgloss.TerminalColor {
	switch p {
	case priorityUrgent:
		return th.err
	case priorityHigh:
		return th.warning
	case priorityMedium:
		return th.info
	case priorityLow:
		return th.muted
	}
	return th.text
}

// dueColor picks a colour for a due-date label.
func (th theme) dueColor(s dueState) lipgloss.TerminalColor {
	switch s {
	case dueOverdue:
		return th.err
	case dueToday:
		return th.warning
	case dueSoon:
		return th.info
	}
	return th.muted
}

// styles are the lipgloss styles derived from a theme.
type styles struct {
	th           theme
	ascii        bool
	appTitle     lipgloss.Style
	muted        lipgloss.Style
	err          lipgloss.Style
	success      lipgloss.Style
	warning      lipgloss.Style
	column       lipgloss.Style
	dialog       lipgloss.Style
	dialogTitle  lipgloss.Style
	label        lipgloss.Style
	focusedLabel lipgloss.Style
	strong       lipgloss.Style
	help         lipgloss.Style
	chip         lipgloss.Style
	mark         lipgloss.Style
	searchPrompt lipgloss.Style
}

func newStyles(th theme, ascii bool) styles {
	s := styles{th: th, ascii: ascii}
	s.appTitle = lipgloss.NewStyle().Bold(true).Foreground(th.accent)
	s.muted = lipgloss.NewStyle().Foreground(th.muted)
	s.err = lipgloss.NewStyle().Foreground(th.err).Bold(th.mono)
	s.success = lipgloss.NewStyle().Foreground(th.success)
	s.warning = lipgloss.NewStyle().Foreground(th.warning)
	s.column = lipgloss.NewStyle().Border(th.border).BorderForeground(th.muted).Padding(0, 1)
	s.dialog = lipgloss.NewStyle().Border(th.border).BorderForeground(th.accent).Padding(1, 2)
	s.dialogTitle = lipgloss.NewStyle().Bold(true).Foreground(th.accent)
	s.label = lipgloss.NewStyle().Bold(true).Foreground(th.text)
	s.focusedLabel = lipgloss.NewStyle().Bold(true).Foreground(th.accent).Underline(th.mono)
	s.strong = lipgloss.NewStyle().Bold(true).Foreground(th.strong)
	s.help = lipgloss.NewStyle().Padding(0, 1)
	s.chip = lipgloss.NewStyle().Foreground(th.info)
	s.mark = lipgloss.NewStyle().Foreground(th.success).Bold(true)
	s.searchPrompt = lipgloss.NewStyle().Foreground(th.accent).Bold(true)
	return s
}

// glyphs are the symbols used on cards, with ASCII fallbacks.
type glyphs struct {
	mark, unmarked, checked, unchecked, urgent, high, medium, low, ellipsis, dot string
}

func newGlyphs(ascii bool) glyphs {
	if ascii {
		return glyphs{mark: "*", unmarked: " ", checked: "[x]", unchecked: "[ ]",
			urgent: "!!", high: "!", medium: "-", low: "v", ellipsis: "...", dot: "*"}
	}
	return glyphs{mark: "✓", unmarked: " ", checked: "☑", unchecked: "☐",
		urgent: "‼", high: "↑", medium: "•", low: "↓", ellipsis: "…", dot: "·"}
}

func (g glyphs) priority(p Priority) string {
	switch p {
	case priorityUrgent:
		return g.urgent
	case priorityHigh:
		return g.high
	case priorityMedium:
		return g.medium
	case priorityLow:
		return g.low
	}
	return ""
}
