package main

import "github.com/charmbracelet/lipgloss"

var (
	colorTodo       = lipgloss.Color("62")
	colorInProgress = lipgloss.Color("214")
	colorDone       = lipgloss.Color("35")
	colorAccent     = lipgloss.Color("205")
	colorMuted      = lipgloss.Color("240")
	colorError      = lipgloss.Color("196")
	colorText       = lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"}
	colorOnColor    = lipgloss.Color("230")
	colorOnBright   = lipgloss.Color("235")
)

// statusColor is the accent colour used for a column.
func statusColor(s status) lipgloss.Color {
	switch s {
	case inProgress:
		return colorInProgress
	case done:
		return colorDone
	default:
		return colorTodo
	}
}

// statusTitleForeground is the text colour used on top of statusColor.
func statusTitleForeground(s status) lipgloss.Color {
	if s == inProgress {
		return colorOnBright
	}
	return colorOnColor
}

var (
	appTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	statusMsgStyle = lipgloss.NewStyle().
			Foreground(colorDone)

	columnStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)

	dialogStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(1, 2)

	dialogTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent)

	labelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText)

	focusedLabelStyle = labelStyle.
				Foreground(colorAccent)

	highlightStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText)

	helpStyle = lipgloss.NewStyle().
			Padding(0, 1)
)
