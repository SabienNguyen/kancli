package ui

import (
	"bytes"
	"math"
	"strings"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// animFPS is the frame rate of card animations.
const animFPS = 60

// animMsg advances the running card animation by one frame. gen names
// the animation the tick belongs to, so a ticker left over from an
// animation that was replaced mid-flight does not double the frame rate.
type animMsg struct{ gen int }

func animTick(gen int) tea.Cmd {
	return tea.Tick(time.Second/animFPS, func(time.Time) tea.Msg { return animMsg{gen: gen} })
}

// cardAnim slides a "lifted" copy of a card from where it was to where it
// landed, driven by a damped spring so the motion eases out and settles.
// While it runs the real card is hidden in its column.
type cardAnim struct {
	gen    int
	taskID int
	ghost  []string
	x, y   float64
	vx, vy float64
	tx, ty float64
	spring harmonica.Spring
	frames int
}

// newCardAnim prepares an animation from (fx, fy) to (tx, ty).
func newCardAnim(id int, ghost []string, fx, fy, tx, ty int) *cardAnim {
	return &cardAnim{
		taskID: id, ghost: ghost,
		x: float64(fx), y: float64(fy), tx: float64(tx), ty: float64(ty),
		spring: harmonica.NewSpring(harmonica.FPS(animFPS), 7.0, 0.7),
	}
}

// step advances one frame and reports whether the card has settled.
func (a *cardAnim) step() bool {
	a.frames++
	a.x, a.vx = a.spring.Update(a.x, a.vx, a.tx)
	a.y, a.vy = a.spring.Update(a.y, a.vy, a.ty)
	settled := math.Abs(a.x-a.tx) < 0.4 && math.Abs(a.y-a.ty) < 0.4 &&
		math.Abs(a.vx) < 0.4 && math.Abs(a.vy) < 0.4
	return settled || a.frames > 2*animFPS
}

// pos is the current top-left cell of the ghost.
func (a *cardAnim) pos() (int, int) {
	return int(math.Round(a.x)), int(math.Round(a.y))
}

// cardPos returns the screen cell of the top-left corner of the card for
// task id in column col, or false when the card is not on screen.
func (m *App) cardPos(col int, id int) (x, y int, ok bool) {
	if col < 0 || col >= len(m.cols) {
		return 0, 0, false
	}
	c := &m.cols[col]
	idx := -1
	for i, it := range c.list.Items() {
		if cd, ok := it.(card); ok && cd.t.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, 0, false
	}
	row, ok := c.rowOf(idx)
	if !ok {
		return 0, 0, false
	}
	for i := 0; i < col; i++ {
		x += m.cols[i].width
	}
	return x, lipgloss.Height(m.headerView()) + row, true
}

// startAnim begins a slide for task id from (fx, fy) to its new place in
// column col. It returns nil when nothing can be animated.
func (m *App) startAnim(id int, col int, fx, fy int) tea.Cmd {
	tx, ty, ok := m.cardPos(col, id)
	t := m.board.Task(id)
	if !ok || t == nil || m.cfg.NoAnimations {
		return nil
	}
	m.animGen++
	m.anim = newCardAnim(id, m.ghostCard(*t, col), fx, fy, tx, ty)
	m.anim.gen = m.animGen
	m.refresh() // hide the real card
	return animTick(m.animGen)
}

// ghostCard renders a framed copy of t at the width of column col.
func (m *App) ghostCard(t board.Task, col int) []string {
	c := m.cols[col]
	frame := m.st.column.BorderForeground(c.color).Padding(0)
	inner := max(1, c.width-frame.GetHorizontalFrameSize())
	d := cardDelegate{st: m.st, g: m.g, compact: m.compact, focused: true, color: c.color, now: board.Now(), board: m.board}
	l := list.New([]list.Item{card{t}}, d, inner, 10)
	var buf bytes.Buffer
	d.Render(&buf, l, 0, card{t})
	body := lipgloss.NewStyle().Width(inner).MaxWidth(inner).Render(buf.String())
	return strings.Split(frame.Render(body), "\n")
}

// overlay paints lines onto view at cell (x, y), keeping whatever is to
// the left and right of them.
func overlay(view string, lines []string, x, y int) string {
	rows := strings.Split(view, "\n")
	for i, gl := range lines {
		r := y + i
		if r < 0 || r >= len(rows) {
			continue
		}
		line := rows[r]
		w := ansi.StringWidth(line)
		gw := ansi.StringWidth(gl)
		left := ansi.Truncate(line, max(0, x), "")
		if lw := ansi.StringWidth(left); lw < x {
			left += strings.Repeat(" ", x-lw)
		}
		right := ""
		if x+gw < w {
			right = ansi.Cut(line, x+gw, w)
		}
		rows[r] = left + "\x1b[0m" + gl + right
	}
	return strings.Join(rows, "\n")
}
