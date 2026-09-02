package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const dateLayout = "2006-01-02"

// today returns the current local date at midnight.
func today() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// parseDue turns user input into a YYYY-MM-DD due date. It accepts ISO
// dates, "today", "tomorrow", "+3d", "+2w", "+1m", weekday names (the next
// such day) and an empty string or "none" to clear the date.
func parseDue(input string, now time.Time) (string, error) {
	s := strings.ToLower(strings.TrimSpace(input))
	y, m, d := now.Date()
	base := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch s {
	case "", "none", "-":
		return "", nil
	case "today", "tod":
		return base.Format(dateLayout), nil
	case "tomorrow", "tom", "tmr":
		return base.AddDate(0, 0, 1).Format(dateLayout), nil
	case "yesterday":
		return base.AddDate(0, 0, -1).Format(dateLayout), nil
	}
	if t, err := time.ParseInLocation(dateLayout, s, now.Location()); err == nil {
		return t.Format(dateLayout), nil
	}
	if t, err := time.ParseInLocation("2006/01/02", s, now.Location()); err == nil {
		return t.Format(dateLayout), nil
	}
	if t, err := time.ParseInLocation("01-02", s, now.Location()); err == nil {
		t = time.Date(y, t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
		if t.Before(base) {
			t = t.AddDate(1, 0, 0)
		}
		return t.Format(dateLayout), nil
	}
	if len(s) >= 3 {
		if wd, ok := weekdays[s[:3]]; ok {
			days := (int(wd) - int(base.Weekday()) + 7) % 7
			if days == 0 {
				days = 7
			}
			return base.AddDate(0, 0, days).Format(dateLayout), nil
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
				return base.AddDate(0, 0, n).Format(dateLayout), nil
			case 'w':
				return base.AddDate(0, 0, 7*n).Format(dateLayout), nil
			case 'm':
				return base.AddDate(0, n, 0).Format(dateLayout), nil
			}
		}
	}
	return "", fmt.Errorf("cannot parse date %q (try 2026-09-10, today, tomorrow, fri, +3d)", input)
}

// daysBetween returns the number of calendar days from a to b, ignoring
// the time of day and daylight-saving shifts.
func daysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua).Hours() / 24)
}

// dueState classifies a due date relative to now.
type dueState int

const (
	dueNone dueState = iota
	dueLater
	dueSoon // within a week
	dueToday
	dueOverdue
)

// dueInfo returns a short human label and the state of a task's due date.
func dueInfo(t Task, now time.Time) (string, dueState) {
	due, ok := t.DueDate()
	if !ok {
		return "", dueNone
	}
	days := daysBetween(now, due)
	switch {
	case days < 0:
		return fmt.Sprintf("%dd overdue", -days), dueOverdue
	case days == 0:
		return "today", dueToday
	case days == 1:
		return "tomorrow", dueSoon
	case days < 7:
		return fmt.Sprintf("in %dd", days), dueSoon
	case due.Year() == now.Year():
		return due.Format("Jan 2"), dueLater
	default:
		return due.Format("Jan 2 2006"), dueLater
	}
}

// relTime renders a timestamp as "3m ago" style text.
func relTime(t, now time.Time) string {
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
