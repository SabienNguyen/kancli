package main

import "fmt"

// status is the column a task lives in.
type status int

const (
	todo status = iota
	inProgress
	done
)

// numStatuses is the number of columns on the board.
const numStatuses = 3

// allStatuses lists the columns in board order.
var allStatuses = [numStatuses]status{todo, inProgress, done}

// String returns the human readable column name.
func (s status) String() string {
	switch s {
	case todo:
		return "To Do"
	case inProgress:
		return "In Progress"
	case done:
		return "Done"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// key is the stable identifier used when the board is written to disk.
func (s status) key() string {
	switch s {
	case todo:
		return "todo"
	case inProgress:
		return "in_progress"
	case done:
		return "done"
	}
	return ""
}

// parseStatus is the inverse of key.
func parseStatus(key string) (status, error) {
	for _, s := range allStatuses {
		if s.key() == key {
			return s, nil
		}
	}
	return todo, fmt.Errorf("unknown status %q", key)
}

// valid reports whether s is one of the board's columns.
func (s status) valid() bool {
	return s >= todo && s < numStatuses
}

// next returns the column to the right, wrapping around at the end.
func (s status) next() status {
	return (s + 1) % numStatuses
}

// prev returns the column to the left, wrapping around at the start.
func (s status) prev() status {
	return (s + numStatuses - 1) % numStatuses
}

// MarshalText implements encoding.TextMarshaler.
func (s status) MarshalText() ([]byte, error) {
	if !s.valid() {
		return nil, fmt.Errorf("cannot encode invalid status %d", int(s))
	}
	return []byte(s.key()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *status) UnmarshalText(text []byte) error {
	parsed, err := parseStatus(string(text))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
