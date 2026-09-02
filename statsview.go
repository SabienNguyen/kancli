package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/barchart"
	"github.com/NimbleMarkets/ntcharts/sparkline"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

// statsKeyMap holds the stats screen bindings.
type statsKeyMap struct {
	Scroll, Window, Refresh, Back key.Binding
}

func (k statsKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Scroll, k.Window, k.Refresh, k.Back}
}
func (k statsKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Scroll, k.Window}, {k.Refresh, k.Back}}
}

var statsKeys = statsKeyMap{
	Scroll:  bind("j/k", "scroll", "j", "k", "down", "up", "pgdown", "pgup"),
	Window:  bind("w", "window: 30/90/365 days", "w"),
	Refresh: bind("r", "refresh", "r"),
	Back:    bind("esc", "back", "esc", "q"),
}

// statsView renders boardStats with charts.
type statsView struct {
	stats  boardStats
	days   int
	err    error
	vp     viewport.Model
	st     styles
	g      glyphs
	keys   statsKeyMap
	help   help.Model
	width  int
	height int
}

func newStatsView(st styles, g glyphs) statsView {
	return statsView{days: 90, vp: viewport.New(0, 0), st: st, g: g, keys: statsKeys, help: help.New()}
}

func (v *statsView) setSize(width, height int) {
	v.width, v.height = width, height
	frame := v.st.dialog.GetHorizontalFrameSize()
	v.vp.Width = max(20, min(width, 110)-frame)
	v.vp.Height = max(3, height-v.st.dialog.GetVerticalFrameSize()-2)
	v.help.Width = v.vp.Width
}

func (v *statsView) cycleWindow() {
	switch v.days {
	case 30:
		v.days = 90
	case 90:
		v.days = 365
	default:
		v.days = 30
	}
}

// load recomputes the statistics from the store.
func (v *statsView) load(b *Board, s *store, now time.Time) {
	st, err := s.boardStats(b, now, v.days)
	if err != nil {
		v.err = err
		return
	}
	v.err = nil
	v.stats = st
	v.render()
}

func (v *statsView) render() {
	st := v.st
	th := st.th
	s := v.stats
	w := v.vp.Width
	var out []string
	head := fmt.Sprintf("%s %s last %d days %s %d events", st.dialogTitle.Render("Stats"), st.muted.Render(v.g.dot), s.Days, st.muted.Render(v.g.dot), s.Events)
	out = append(out, head, "")
	if v.err != nil {
		out = append(out, st.err.Render(v.err.Error()))
		v.vp.SetContent(strings.Join(out, "\n"))
		return
	}

	// Headline numbers.
	num := func(label string, n int, style lipgloss.Style) string {
		return style.Render(fmt.Sprintf("%d", n)) + " " + st.muted.Render(label)
	}
	line := []string{num("open", s.Live, st.strong), num("in progress", s.InProgress, st.strong), num("finished", len(s.Finished), st.success)}
	if s.Overdue > 0 {
		line = append(line, num("overdue", s.Overdue, st.err))
	}
	if s.DueToday > 0 {
		line = append(line, num("due today", s.DueToday, st.warning))
	}
	out = append(out, strings.Join(line, st.muted.Render("  "+v.g.dot+"  ")))
	if len(s.Finished) > 0 {
		out = append(out, st.muted.Render("cycle time ")+st.strong.Render(humanDuration(s.CycleMedian))+
			st.muted.Render(" median "+v.g.dot+" "+humanDuration(s.CycleMean)+" mean "+v.g.dot+" "+humanDuration(s.CycleP90)+" p90"))
	} else {
		out = append(out, st.muted.Render("no finished tasks in this window yet"))
	}
	out = append(out, "")

	// Throughput sparkline.
	out = append(out, st.label.Render("Finished per week"))
	// Each week is drawn three columns wide so the chart fills its width.
	spark := sparkline.New(min(w-2, max(8, len(s.Weeks)*3)), 5, sparkline.WithStyle(lipgloss.NewStyle().Foreground(th.success)))
	vals := make([]float64, 0, len(s.Weeks)*3)
	total := 0
	for _, wk := range s.Weeks {
		vals = append(vals, float64(wk.Done), float64(wk.Done), float64(wk.Done))
		total += wk.Done
	}
	spark.PushAll(vals)
	spark.Draw()
	out = append(out, indentBlock(spark.View(), 2)...)
	if len(s.Weeks) > 0 {
		out = append(out, st.muted.Render(fmt.Sprintf("  %s → %s, %d finished, %d added",
			s.Weeks[0].Week.Format("Jan 2"), s.Weeks[len(s.Weeks)-1].Week.Format("Jan 2"), total, sumCreated(s.Weeks))))
	}
	out = append(out, "")

	// WIP sparkline.
	out = append(out, st.label.Render("Work in progress per day"))
	wip := sparkline.New(min(w-2, max(8, len(s.WIP))), 4, sparkline.WithStyle(lipgloss.NewStyle().Foreground(th.warning)))
	peak := 0
	for _, d := range s.WIP {
		wip.Push(float64(d.Count))
		peak = max(peak, d.Count)
	}
	wip.Draw()
	out = append(out, indentBlock(wip.View(), 2)...)
	if n := len(s.WIP); n > 0 {
		out = append(out, st.muted.Render(fmt.Sprintf("  %d now, peak %d over %d days", s.WIP[n-1].Count, peak, n)))
	}
	out = append(out, "")

	// Time in column bar chart.
	out = append(out, st.label.Render("Mean time in column"))
	var bars []barchart.BarData
	maxHours := 1.0
	for _, cs := range s.Stays {
		hours := cs.Mean.Hours()
		maxHours = max(maxHours, hours)
		color := th.columnColor(colColorByID(v, cs.Column))
		bars = append(bars, barchart.BarData{Label: truncateLabel(cs.Name, barWidth), Values: []barchart.BarValue{{Name: cs.Name, Value: hours, Style: lipgloss.NewStyle().Foreground(color)}}})
	}
	if len(bars) > 0 {
		bc := barchart.New(min(w-2, max(20, len(bars)*(barWidth+2))), 7, barchart.WithDataSet(bars), barchart.WithMaxValue(maxHours), barchart.WithBarWidth(barWidth), barchart.WithBarGap(2))
		bc.Draw()
		out = append(out, indentBlock(bc.View(), 2)...)
		var legend []string
		for _, cs := range s.Stays {
			if cs.Samples > 0 {
				legend = append(legend, fmt.Sprintf("%s %s (%d)", cs.Name, humanDuration(cs.Mean), cs.Samples))
			}
		}
		if len(legend) > 0 {
			out = append(out, lipgloss.NewStyle().Width(w).Render(st.muted.Render("  "+strings.Join(legend, "  "+v.g.dot+"  "))))
		}
	}
	out = append(out, "")

	// Aging.
	if len(s.Aging) > 0 {
		out = append(out, st.label.Render("Oldest open tasks"))
		for _, a := range s.Aging {
			out = append(out, fmt.Sprintf("  %s  %s %s %s", st.warning.Render(fmt.Sprintf("%8s", humanDuration(a.Age))), st.muted.Render(fmt.Sprintf("#%d", a.ID)), a.Title, st.muted.Render("in "+a.Column)))
		}
		out = append(out, "")
	}

	// Labels.
	if len(s.Labels) > 0 {
		out = append(out, st.label.Render("Labels")+st.muted.Render("  open / finished, median cycle"))
		for _, l := range s.Labels {
			cycle := ""
			if l.Done > 0 {
				cycle = humanDuration(l.CycleMedian)
			}
			out = append(out, fmt.Sprintf("  %-18s %3d / %-3d %s", st.chip.Render("+"+l.Label), l.Open, l.Done, st.muted.Render(cycle)))
		}
		out = append(out, "")
	}

	// Recently finished.
	if len(s.Finished) > 0 {
		out = append(out, st.label.Render("Recently finished"))
		for i, ft := range s.Finished {
			if i == 8 {
				out = append(out, st.muted.Render(fmt.Sprintf("  … and %d more", len(s.Finished)-8)))
				break
			}
			out = append(out, fmt.Sprintf("  %s  %s %s %s", st.muted.Render(ft.DoneAt.Format("Jan 02")), st.muted.Render(fmt.Sprintf("#%d", ft.ID)), ft.Title, st.success.Render(humanDuration(ft.Cycle))))
		}
	}
	v.vp.SetContent(strings.Join(out, "\n"))
}

func colColorByID(v *statsView, id string) string {
	// The stats view only knows column ids; colours come from the board
	// palette by position so charts stay distinguishable.
	for i, cs := range v.stats.Stays {
		if cs.Column == id {
			return columnPalette[i%len(columnPalette)]
		}
	}
	return ""
}

func sumCreated(weeks []weekCount) int {
	n := 0
	for _, w := range weeks {
		n += w.Created
	}
	return n
}

// barWidth is the bar chart column width; labels are cut to fit it because
// the chart truncates by byte otherwise.
const barWidth = 10

func truncateLabel(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func indentBlock(block string, n int) []string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

func (v statsView) view() string {
	footer := v.help.View(v.keys)
	body := lipgloss.JoinVertical(lipgloss.Left, v.vp.View(), "", footer)
	return v.st.dialog.Render(body)
}
