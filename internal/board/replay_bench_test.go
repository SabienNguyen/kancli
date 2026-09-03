package board

import (
	"fmt"
	"testing"
)

// buildCreateEvents makes n task.created events by driving a real board, so
// the payloads are exactly what a live session would have written.
func buildCreateEvents(tb testing.TB, n int) []Event {
	tb.Helper()
	f := NewFile()
	b := f.Boards[0]
	for i := 0; i < n; i++ {
		if _, err := b.AddTask(Task{Title: fmt.Sprintf("task %d", i), Column: b.Columns[0].ID}); err != nil {
			tb.Fatalf("add task %d: %v", i, err)
		}
	}
	events := f.Pending()
	if len(events) < n {
		tb.Fatalf("got %d events, want at least %d", len(events), n)
	}
	for i := range events {
		events[i].Seq = int64(i + 1)
	}
	return events
}

// BenchmarkReplayCreates replays a long tail of task.created events onto an
// empty file, the shape of a cold Load on a busy board.
func BenchmarkReplayCreates(b *testing.B) {
	events := buildCreateEvents(b, 20000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := NewFile()
		if err := f.Replay(events); err != nil {
			b.Fatalf("replay: %v", err)
		}
		if got := len(f.Boards[0].Tasks); got != len(events) {
			b.Fatalf("replayed %d tasks, want %d", got, len(events))
		}
	}
}

// BenchmarkReplayMovesOnLargeBoard replays task.moved events onto a board
// that already holds a lot of tasks, the shape of LoadAsOf and stats.
func BenchmarkReplayMovesOnLargeBoard(b *testing.B) {
	const tasks, moves = 20000, 5000
	creates := buildCreateEvents(b, tasks)

	base := NewFile()
	if err := base.Replay(creates); err != nil {
		b.Fatalf("seed replay: %v", err)
	}
	bd := base.Boards[0]
	from, to := bd.Columns[0].ID, bd.Columns[len(bd.Columns)-1].ID
	moveEvents := make([]Event, 0, moves)
	for i := 0; i < moves; i++ {
		moveEvents = append(moveEvents, Event{
			Seq: int64(len(creates) + i + 1), Board: bd.ID, Kind: EvTaskMoved,
			Task: bd.Tasks[i].ID, From: from, To: to, At: bd.Tasks[i].CreatedAt,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		f := NewFile()
		if err := f.Replay(creates); err != nil {
			b.Fatalf("seed: %v", err)
		}
		b.StartTimer()
		if err := f.Replay(moveEvents); err != nil {
			b.Fatalf("replay moves: %v", err)
		}
	}
}
