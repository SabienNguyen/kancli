package board

import (
	"time"
)

// sampleFile returns the board used in demo mode and tests. The first eight
// tasks are created through the normal mutation path with a clock that
// walks through the last three weeks, so the event history is realistic.
func SampleFile() *File {
	f := NewFile()
	b := f.Boards[0]
	b.Name = "Demo"
	b.Description = "Sample board with three weeks of history"
	b.ID = "demo"
	f.ActiveBoard = "demo"
	b.Columns[1].WIPLimit = 2
	f.Attach()

	now := Now()
	cursor := now.Add(-21 * 24 * time.Hour)
	b.clock = func() time.Time { return cursor }
	step := func(hours float64) { cursor = cursor.Add(time.Duration(hours * float64(time.Hour))) }
	day := func(n int) string { return Today().AddDate(0, 0, n).Format(DateLayout) }
	add := func(col, title, desc string, p Priority, due string, labels []string, who string) *Task {
		t, _ := b.AddTask(Task{Column: col, Title: title, Description: desc, Priority: p, Due: due, Labels: labels, Assignee: who})
		return t
	}

	// Day 0: the board is set up.
	t1 := add("todo", "Write the release notes", "Cover the **new search syntax**, undo and the CLI.\n\n- link to the changelog\n- mention `kancli review`\n- screenshots of the *stats* view", PriorityHigh, day(1), []string{"docs"}, "sam")
	step(2)
	add("todo", "Fix the flaky terminal resize test", "", PriorityMedium, day(-2), []string{"bug", "tests"}, "")
	step(1)
	add("todo", "Buy milk", "strawberry milk", PriorityNone, "", []string{"home"}, "")
	step(3)
	add("todo", "Plan the team offsite", "Venue, agenda and travel.", PriorityLow, day(20), []string{"planning"}, "alex")
	step(20)
	// Day 1: work starts.
	t5 := add("todo", "Ship the CLI subcommands", "add, list, move, done, export and import.", PriorityUrgent, day(0), []string{"feature"}, "sam")
	step(4)
	b.MoveTask(t5.ID, "in_progress") //nolint:errcheck // demo data
	step(30)
	t6 := add("todo", "Review the mouse support PR", "", PriorityMedium, day(3), []string{"review"}, "alex")
	step(50)
	b.MoveTask(t6.ID, "in_progress") //nolint:errcheck // demo data
	step(6)
	// Day 5 onwards: two tasks go all the way to Done.
	t7 := add("todo", "Upgrade to Bubble Tea v1", "", PriorityNone, "", []string{"chore"}, "")
	step(20)
	b.MoveTask(t7.ID, "in_progress") //nolint:errcheck // demo data
	step(52)
	b.MoveTask(t7.ID, "done") //nolint:errcheck // demo data
	step(10)
	t8 := add("todo", "Stay cool", "as a cucumber", PriorityLow, "", nil, "")
	step(3)
	b.MoveTask(t8.ID, "done") //nolint:errcheck // demo data
	step(24)
	b.AddChecklistItem(t1.ID, "Draft")   //nolint:errcheck // demo data
	b.AddChecklistItem(t1.ID, "Review")  //nolint:errcheck // demo data
	b.AddChecklistItem(t1.ID, "Publish") //nolint:errcheck // demo data
	b.ToggleChecklistItem(t1.ID, 0)
	b.AddAttachment(t1.ID, "https://github.com/charmbracelet/kancli") //nolint:errcheck // demo data
	step(40)
	b.AddComment(t5.ID, "Export works, import is next.") //nolint:errcheck // demo data

	b.clock = Now
	return f
}

// demoFile is sampleFile plus a few weeks of finished, archived work so
// the stats screen has throughput and cycle-time history to draw.
func DemoFile() *File {
	f := SampleFile()
	b := f.Boards[0]
	now := Now()
	cursor := now.Add(-20 * 24 * time.Hour)
	b.clock = func() time.Time { return cursor }
	step := func(hours float64) { cursor = cursor.Add(time.Duration(hours * float64(time.Hour))) }
	finished := []struct {
		title  string
		labels []string
		p      Priority
		work   float64 // hours in progress
	}{
		{"Set up CI", []string{"chore"}, PriorityMedium, 6},
		{"Write the store tests", []string{"tests"}, PriorityHigh, 30},
		{"Fix due-date parsing across DST", []string{"bug"}, PriorityUrgent, 5},
		{"Add the board picker", []string{"feature"}, PriorityMedium, 40},
		{"Document the search syntax", []string{"docs"}, PriorityLow, 9},
		{"Mouse support", []string{"feature"}, PriorityHigh, 55},
		{"Archive view", []string{"feature"}, PriorityMedium, 20},
		{"Release v0.2", []string{"chore"}, PriorityHigh, 3},
	}
	for _, w := range finished {
		t, _ := b.AddTask(Task{Column: "todo", Title: w.title, Labels: w.labels, Priority: w.p})
		step(10)
		b.MoveTask(t.ID, "in_progress") //nolint:errcheck // demo data
		step(w.work)
		b.MoveTask(t.ID, "done") //nolint:errcheck // demo data
		step(14)
		b.ArchiveTask(t.ID)
		step(20)
	}
	b.clock = Now
	return f
}
