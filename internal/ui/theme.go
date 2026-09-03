package ui

import (
	"fmt"
	"strings"

	"github.com/SabienNguyen/kancli/internal/board"

	"github.com/charmbracelet/lipgloss"
)

// theme is a named colour scheme.
type Theme struct {
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

var ThemeNames = []string{"default", "high-contrast", "mono"}

// themeByName builds a theme. ascii swaps box-drawing borders for +-|.
func ThemeByName(name string, ascii bool) (Theme, error) {
	var th Theme
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "default":
		th = Theme{
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
		th = Theme{
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
		th = Theme{
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
		return th, fmt.Errorf("unknown theme %q (use %s)", name, strings.Join(ThemeNames, ", "))
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
func (th Theme) columnColor(c string) lipgloss.TerminalColor {
	if th.mono || c == "" {
		return th.muted
	}
	return lipgloss.Color(c)
}

// priorityColor picks a colour for a priority marker.
func (th Theme) priorityColor(p board.Priority) lipgloss.TerminalColor {
	switch p {
	case board.PriorityUrgent:
		return th.err
	case board.PriorityHigh:
		return th.warning
	case board.PriorityMedium:
		return th.info
	case board.PriorityLow:
		return th.muted
	}
	return th.text
}

// dueColor picks a colour for a due-date label.
func (th Theme) dueColor(s board.DueState) lipgloss.TerminalColor {
	switch s {
	case board.DueOverdue:
		return th.err
	case board.DueToday:
		return th.warning
	case board.DueSoon:
		return th.info
	}
	return th.muted
}

// styles are the lipgloss styles derived from a theme.
type Styles struct {
	th           Theme
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

// ThemeName returns the name of the theme the styles were built from.
func (s Styles) ThemeName() string { return s.th.name }

func NewStyles(th Theme, ascii bool) Styles {
	s := Styles{th: th, ascii: ascii}
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

// dialogBorder returns the dialog style with its border drawn in c, so a
// form can preview the colour being picked. The accent border is kept for
// the mono theme and when no colour has been chosen.
func (s Styles) dialogBorder(c string) lipgloss.Style {
	if s.th.mono || c == "" {
		return s.dialog
	}
	return s.dialog.BorderForeground(lipgloss.Color(c))
}

// glyphs are the symbols used on cards, with ASCII fallbacks.
type Glyphs struct {
	mark, unmarked, checked, unchecked, urgent, high, medium, low, ellipsis, dot, blocked, subtask string
}

func NewGlyphs(ascii bool) Glyphs {
	if ascii {
		return Glyphs{mark: "*", unmarked: " ", checked: "[x]", unchecked: "[ ]",
			urgent: "!!", high: "!", medium: "-", low: "v", ellipsis: "...", dot: "*", blocked: "[blocked]", subtask: "->"}
	}
	return Glyphs{mark: "✓", unmarked: " ", checked: "☑", unchecked: "☐",
		urgent: "‼", high: "↑", medium: "•", low: "↓", ellipsis: "…", dot: "·", blocked: "⛔", subtask: "↳"}
}

func (g Glyphs) priority(p board.Priority) string {
	switch p {
	case board.PriorityUrgent:
		return g.urgent
	case board.PriorityHigh:
		return g.high
	case board.PriorityMedium:
		return g.medium
	case board.PriorityLow:
		return g.low
	}
	return ""
}
