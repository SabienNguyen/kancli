package cli

import (
	"testing"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
)

func TestParseAsOf(t *testing.T) {
	// Pin the real clock to a date far from now: relative offsets must be
	// measured from the given time, never from the wall clock.
	board.Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.Local) }
	defer func() { board.Now = time.Now }()

	now := time.Date(2026, 9, 2, 15, 0, 0, 0, time.Local)
	if got, err := parseAsOf("2026-08-25", now); err != nil || got.Format("2006-01-02 15:04") != "2026-08-25 23:59" {
		t.Errorf("date = %v, %v", got, err)
	}
	if got, err := parseAsOf("2026-08-25 14:00", now); err != nil || got.Hour() != 14 {
		t.Errorf("datetime = %v, %v", got, err)
	}
	if got, err := parseAsOf("-7d", now); err != nil || got.Day() != 26 {
		t.Errorf("relative = %v, %v", got, err)
	}
	if got, err := parseAsOf("-1d", now); err != nil || got.Day() != 1 || got.Hour() != 15 {
		t.Errorf("-1d = %v, %v", got, err)
	}
	if _, err := parseAsOf("whenever", now); err == nil {
		t.Error("garbage should fail")
	}
}
