package main

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// Task is a single card on the board.
type Task struct {
	id          string
	status      status
	title       string
	description string
	createdAt   time.Time
	updatedAt   time.Time
}

// newTask creates a task with a fresh ID and timestamps.
func newTask(st status, title, description string) Task {
	now := time.Now()
	return Task{
		id:          newID(),
		status:      st,
		title:       strings.TrimSpace(title),
		description: strings.TrimSpace(description),
		createdAt:   now,
		updatedAt:   now,
	}
}

// newID returns a random 16 character hex identifier.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand only fails when the OS entropy source is broken;
		// fall back to a time based ID so the app keeps working.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000")))[:16]
	}
	return hex.EncodeToString(b[:])
}

// The following methods implement list.DefaultItem so tasks can be rendered
// by the default list delegate.

// FilterValue is the text the list filters on.
func (t Task) FilterValue() string { return t.title }

// Title is the first line of the card.
func (t Task) Title() string { return t.title }

// Description is the second line of the card. Only the first line of a
// multi-line description is shown; the full text is visible when editing.
func (t Task) Description() string {
	first, rest, multi := strings.Cut(t.description, "\n")
	first = strings.TrimSpace(first)
	if multi && strings.TrimSpace(rest) != "" {
		return first + " …"
	}
	return first
}
