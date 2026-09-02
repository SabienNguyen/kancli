package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

const helpGutter = 4

// fullHelp lays out key groups in as many rows as width needs. Each
// group is one aligned block (keys left, descriptions right); blocks sit
// side by side separated by a gutter and wrap to a new row when the next
// block would not fit. If the result is taller than maxHeight the blank
// lines between rows go first, then the tail is cut and an ellipsis line
// is appended.
func fullHelp(groups [][]key.Binding, width, maxHeight int, keyStyle, descStyle, ellipsis lipgloss.Style) string {
	var blocks []string
	for _, g := range groups {
		if b := helpBlock(g, keyStyle, descStyle); b != "" {
			blocks = append(blocks, b)
		}
	}
	var rows []string
	var row []string
	used := 0
	for _, b := range blocks {
		w := lipgloss.Width(b)
		if len(row) > 0 && used+helpGutter+w > width {
			rows = append(rows, joinBlocks(row))
			row, used = nil, 0
		}
		if len(row) > 0 {
			used += helpGutter
		}
		row = append(row, b)
		used += w
	}
	if len(row) > 0 {
		rows = append(rows, joinBlocks(row))
	}

	out := strings.Join(rows, "\n\n")
	if maxHeight > 0 && lipgloss.Height(out) > maxHeight {
		out = strings.Join(rows, "\n")
	}
	if maxHeight > 0 && lipgloss.Height(out) > maxHeight {
		lines := strings.Split(out, "\n")
		keep := max(0, maxHeight-1)
		out = strings.Join(append(lines[:keep], ellipsis.Render("…")), "\n")
	}
	return out
}

// helpBlock renders one group as aligned "key  desc" lines, skipping
// disabled or empty bindings.
func helpBlock(g []key.Binding, keyStyle, descStyle lipgloss.Style) string {
	type item struct{ k, d string }
	var items []item
	keyW := 0
	for _, b := range g {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		if h.Key == "" && h.Desc == "" {
			continue
		}
		items = append(items, item{h.Key, h.Desc})
		keyW = max(keyW, lipgloss.Width(h.Key))
	}
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		pad := strings.Repeat(" ", keyW-lipgloss.Width(it.k))
		lines = append(lines, keyStyle.Render(it.k)+pad+" "+descStyle.Render(it.d))
	}
	return strings.Join(lines, "\n")
}

// joinBlocks puts blocks side by side, top-aligned, with the gutter.
func joinBlocks(blocks []string) string {
	gutter := strings.Repeat(" ", helpGutter)
	parts := make([]string, 0, len(blocks)*2-1)
	for i, b := range blocks {
		if i > 0 {
			parts = append(parts, gutter)
		}
		parts = append(parts, b)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}
