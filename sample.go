package main

import "time"

// sampleFile returns the data used in demo mode.
func sampleFile() *File {
	f := newFile()
	b := f.Boards[0]
	b.Name = "Demo"
	b.ID = "demo"
	f.ActiveBoard = "demo"
	b.Columns[1].WIPLimit = 2
	now := timeNow()
	day := func(n int) string { return today().AddDate(0, 0, n).Format(dateLayout) }

	add := func(col, title, desc string, p Priority, due string, labels []string, who string) *Task {
		t, _ := b.AddTask(Task{Column: col, Title: title, Description: desc, Priority: p, Due: due, Labels: labels, Assignee: who})
		return t
	}
	t := add("todo", "Write the release notes", "Cover the new search syntax, undo and the CLI.\nLink to the changelog.", priorityHigh, day(1), []string{"docs"}, "sam")
	t.Checklist = []ChecklistItem{{Text: "Draft", Done: true}, {Text: "Review", Done: false}, {Text: "Publish", Done: false}}
	t.Attachments = []string{"https://github.com/charmbracelet/kancli"}
	add("todo", "Fix the flaky terminal resize test", "", priorityMedium, day(-2), []string{"bug", "tests"}, "")
	add("todo", "Buy milk", "strawberry milk", priorityNone, "", []string{"home"}, "")
	add("todo", "Plan the team offsite", "Venue, agenda and travel.", priorityLow, day(20), []string{"planning"}, "alex")
	t = add("in_progress", "Ship the CLI subcommands", "add, list, move, done, export and import.", priorityUrgent, day(0), []string{"feature"}, "sam")
	t.Comments = []Comment{{At: now.Add(-3 * time.Hour), Text: "Export works, import is next."}}
	add("in_progress", "Review the mouse support PR", "", priorityMedium, day(3), []string{"review"}, "alex")
	t = add("done", "Upgrade to Bubble Tea v1", "", priorityNone, "", []string{"chore"}, "")
	t.CreatedAt = now.Add(-72 * time.Hour)
	add("done", "Stay cool", "as a cucumber", priorityLow, "", nil, "")
	return f
}
