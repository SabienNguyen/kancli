package ui

import (
	"strings"
	"testing"
)

func TestMoveAnimatesCard(t *testing.T) {
	m, _ := newTestApp(t)
	first, _ := m.col().selected()
	mm, cmd := m.Update(keyMsg("L"))
	m = mm.(App)
	if cmd == nil || m.anim == nil || m.anim.taskID != first.ID {
		t.Fatalf("moving a card should start an animation (anim=%v)", m.anim)
	}
	if m.anim.x == m.anim.tx {
		t.Error("the ghost should start in the source column")
	}
	frames := 0
	for m.anim != nil {
		view := m.View()
		assertFits(t, m, "animation frame")
		if !strings.Contains(view, first.Title) {
			t.Fatal("the ghost card must show the moved task")
		}
		mm, _ = m.Update(animMsg{gen: m.anim.gen})
		m = mm.(App)
		if frames++; frames > 3*animFPS {
			t.Fatal("animation never settled")
		}
	}
	if frames < 5 {
		t.Errorf("only %d frames", frames)
	}
	if got, _ := m.cols[1].selected(); got.ID != first.ID {
		t.Errorf("moved task should be selected in its new column")
	}
	if strings.Count(m.View(), first.Title) != 1 {
		t.Error("the card must be drawn exactly once after the animation")
	}
}

func TestStaleAnimationTickIsIgnored(t *testing.T) {
	m, _ := newTestApp(t)
	mm, _ := m.Update(keyMsg("L"))
	m = mm.(App)
	before := m.anim.frames
	mm, cmd := m.Update(animMsg{gen: m.anim.gen - 1})
	if m = mm.(App); m.anim.frames != before || cmd != nil {
		t.Error("a tick from an earlier animation must not advance the current one")
	}
}

func TestNoAnimationsFlag(t *testing.T) {
	m, _ := newTestApp(t)
	m.cfg.NoAnimations = true
	mm, _ := m.Update(keyMsg("L"))
	if mm.(App).anim != nil {
		t.Error("animations should be off")
	}
}

func TestOverlay(t *testing.T) {
	view := "abcdefgh\nijklmnop\nqrstuvwx"
	got := overlay(view, []string{"XX", "YY"}, 3, 1)
	want := "abcdefgh\nijk\x1b[0mXXnop\nqrs\x1b[0mYYvwx"
	if got != want {
		t.Errorf("overlay = %q, want %q", got, want)
	}
	if out := overlay("ab", []string{"ZZ"}, 5, 0); out != "ab   \x1b[0mZZ" {
		t.Errorf("overlay past the end = %q", out)
	}
}
