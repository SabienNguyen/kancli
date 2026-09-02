package board

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const DateLayout = "2006-01-02"

// timeNow is the clock used for timestamps; tests replace it.
var Now = time.Now

// plural returns "s" unless n is 1.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// colName returns a column's display name, or the id when unknown.
func ColName(b *Board, id string) string {
	if c := b.Column(id); c != nil {
		return c.Name
	}
	return id
}

// Today returns the current local date at midnight, from the same clock
// as Now so tests and demo data can freeze it.
func Today() time.Time {
	y, m, d := Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// parseDue turns user input into a YYYY-MM-DD due date. It accepts ISO
// dates, "today", "tomorrow", "+3d", "+2w", "+1m", weekday names (the next
// such day) and an empty string or "none" to clear the date.
func ParseDue(input string, now time.Time) (string, error) {
	s := strings.ToLower(strings.TrimSpace(input))
	y, m, d := now.Date()
	base := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch s {
	case "", "none", "-":
		return "", nil
	case "today", "tod":
		return base.Format(DateLayout), nil
	case "tomorrow", "tom", "tmr":
		return base.AddDate(0, 0, 1).Format(DateLayout), nil
	case "yesterday":
		return base.AddDate(0, 0, -1).Format(DateLayout), nil
	}
	if t, err := time.ParseInLocation(DateLayout, s, now.Location()); err == nil {
		return t.Format(DateLayout), nil
	}
	if t, err := time.ParseInLocation("2006/01/02", s, now.Location()); err == nil {
		return t.Format(DateLayout), nil
	}
	if t, err := time.ParseInLocation("01-02", s, now.Location()); err == nil {
		t = time.Date(y, t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
		if t.Before(base) {
			t = t.AddDate(1, 0, 0)
		}
		return t.Format(DateLayout), nil
	}
	if len(s) >= 3 {
		if wd, ok := weekdays[s[:3]]; ok {
			days := (int(wd) - int(base.Weekday()) + 7) % 7
			if days == 0 {
				days = 7
			}
			return base.AddDate(0, 0, days).Format(DateLayout), nil
		}
	}
	if strings.HasPrefix(s, "+") && len(s) >= 2 {
		unit := s[len(s)-1]
		num := s[1 : len(s)-1]
		if num == "" {
			num = "1"
		}
		n, err := strconv.Atoi(num)
		if err == nil && n >= 0 {
			switch unit {
			case 'd':
				return base.AddDate(0, 0, n).Format(DateLayout), nil
			case 'w':
				return base.AddDate(0, 0, 7*n).Format(DateLayout), nil
			case 'm':
				return base.AddDate(0, n, 0).Format(DateLayout), nil
			}
		}
	}
	return "", fmt.Errorf("cannot parse date %q (try 2026-09-10, today, tomorrow, fri, +3d)", input)
}

// daysBetween returns the number of calendar days from a to b, ignoring
// the time of day and daylight-saving shifts.
func DaysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua).Hours() / 24)
}

// dueState classifies a due date relative to now.
type DueState int

const (
	DueNone DueState = iota
	DueLater
	DueSoon // within a week
	DueToday
	DueOverdue
)

// dueInfo returns a short human label and the state of a task's due date.
func DueInfo(t Task, now time.Time) (string, DueState) {
	due, ok := t.DueDate()
	if !ok {
		return "", DueNone
	}
	days := DaysBetween(now, due)
	switch {
	case days < 0:
		return fmt.Sprintf("%dd overdue", -days), DueOverdue
	case days == 0:
		return "today", DueToday
	case days == 1:
		return "tomorrow", DueSoon
	case days < 7:
		return fmt.Sprintf("in %dd", days), DueSoon
	case due.Year() == now.Year():
		return due.Format("Jan 2"), DueLater
	default:
		return due.Format("Jan 2 2006"), DueLater
	}
}

// relTime renders a timestamp as "3m ago" style text.
func RelTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2 2006")
	}
}
