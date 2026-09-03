package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
)

// CompactAfter is how many events may accumulate after the newest snapshot
// before the store folds them into a fresh one.
const CompactAfter = 500

// ErrStale is returned by Compact when another process wrote to the
// database since this store last read it. Callers reload and retry.
var ErrStale = errors.New("the board changed on disk; reload before writing a snapshot")

// Store persists the data in one SQLite database, board.db:
//
//	events      the append-only log; the source of truth
//	snapshots   folded state, pruned by the retention policy
//	meta        the store format and creation time
//
// A store with an empty path keeps everything in a private in-memory
// database, which is how demo mode discards its changes.
type Store struct {
	path  string // the .db file, empty for demo mode
	actor string

	db      *sql.DB
	openErr error

	nextSeq   int64 // next event sequence number to hand out
	seen      int64 // highest sequence number this store has read or written
	tailCount int   // events after the newest snapshot
	dataVer   int64 // PRAGMA data_version at the last load or save

	needReload bool // another process wrote since our load

	walkers map[string]*board.StatsWalker // incremental stats per board

	upgrade *Upgrade // set when opening data an older kancli wrote
}

// Upgrade describes a data file that an older kancli wrote and this one has
// just opened.
type Upgrade struct {
	From, To int
	Backup   string // directory holding the old files
}

// Upgraded reports the upgrade performed while opening, if any.
func (s *Store) Upgraded() (Upgrade, bool) {
	if s.upgrade == nil {
		return Upgrade{}, false
	}
	return *s.upgrade, true
}

// New returns a store for path. Nothing is opened until the first read or
// write, because the CLI builds stores for completion and for demo mode. An
// empty path is demo mode; a configured .json path (the file store's name)
// maps to the board.db beside it.
func New(path string) *Store {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		path = strings.TrimSuffix(path, filepath.Ext(path)) + ".db"
	}
	return &Store{path: path, actor: "ui"}
}

// Path is the database file. It is empty for in-memory stores.
func (s *Store) Path() string { return s.path }

// SetActor names the process in the events this store writes.
func (s *Store) SetActor(actor string) { s.actor = actor }

// NeedReload reports that another process wrote to the database since this
// store last read it; callers should Load again.
func (s *Store) NeedReload() bool { return s.needReload }

// Enabled reports whether the store writes to disk.
func (s *Store) Enabled() bool { return s != nil && s.path != "" }

// Describe is shown in the header.
func (s *Store) Describe() string {
	if !s.Enabled() {
		return "demo mode · changes are not saved"
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, s.path); err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "~" + string(filepath.Separator) + rel
		}
	}
	return s.path
}

// DefaultPath returns the data file location: $KANCLI_FILE if set,
// otherwise kancli/board.db under $XDG_DATA_HOME or the OS config dir.
func DefaultPath() (string, error) {
	if p := os.Getenv("KANCLI_FILE"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate data directory: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "kancli", "board.db"), nil
}

// Close checkpoints the write-ahead log and releases the database, so a
// clean exit leaves board.db alone on disk and copying it is a valid
// backup. It is safe to call more than once.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db := s.db
	s.db, s.openErr = nil, nil
	var errs []error
	if s.path != "" {
		if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			errs = append(errs, fmt.Errorf("checkpoint %s: %w", s.path, err))
		}
	}
	if err := db.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// --- loading ----------------------------------------------------------------

// Load reads the newest snapshot, replays the events after it and returns
// the live state.
func (s *Store) Load() (*board.File, error) {
	f, snapSeq, found, err := s.readSnapshot(time.Time{})
	if err != nil {
		return nil, err
	}
	if !found {
		f = board.NewFile()
	}
	f.SetActor(s.actor)

	events, err := s.readEvents(snapSeq, time.Time{})
	if err != nil {
		return nil, err
	}
	if err := f.Replay(events); err != nil {
		return nil, replayError(s.describePath(), err)
	}
	maxSeq, err := s.maxEventSeq()
	if err != nil {
		return nil, err
	}
	s.seen = max64(max64(f.LastSeq, snapSeq), maxSeq)
	s.nextSeq = s.seen + 1
	s.tailCount = len(events)
	s.needReload = false
	if s.dataVer, err = s.dataVersion(); err != nil {
		return nil, err
	}

	// A board that predates the event log (one the importer just brought
	// over) gets a history seeded from its task timestamps so "as of" views
	// and stats have something to work on.
	if found && len(events) == 0 && f.LastSeq == 0 && f.SnapshotAt.IsZero() && hasTasks(f) {
		if err := s.bootstrap(f); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// describePath names the database in error messages.
func (s *Store) describePath() string {
	if s.path == "" {
		return "the in-memory board"
	}
	return s.path
}

func hasTasks(f *board.File) bool {
	for _, b := range f.Boards {
		if len(b.Tasks) > 0 {
			return true
		}
	}
	return false
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// bootstrap seeds the log for a board that predates it: an empty base
// snapshot at sequence zero, one created event per task at its creation
// time, and moves or archives at the task's last update. It then compacts
// so the newest snapshot carries the real state.
func (s *Store) bootstrap(f *board.File) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := insertSnapshot(tx, f.EmptyBase(), 0); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	var events []board.Event
	for _, b := range f.Boards {
		first := ""
		if len(b.Columns) > 0 {
			first = b.Columns[0].ID
		}
		for _, t := range b.Tasks {
			created := t
			created.Column = first
			created.History = nil
			created.ArchivedAt = nil
			created.UpdatedAt = t.CreatedAt
			if created.Column == "" {
				created.Column = t.Column
			}
			events = append(events, board.Event{At: t.CreatedAt, Board: b.ID, Kind: board.EvTaskCreated, Task: t.ID, To: created.Column, Data: board.MustJSON(created), Actor: "bootstrap"})
			if t.Column != created.Column {
				events = append(events, board.Event{At: t.UpdatedAt, Board: b.ID, Kind: board.EvTaskMoved, Task: t.ID, From: created.Column, To: t.Column, Actor: "bootstrap"})
			}
			if t.Archived() {
				events = append(events, board.Event{At: *t.ArchivedAt, Board: b.ID, Kind: board.EvTaskArchived, Task: t.ID, From: t.Column, Actor: "bootstrap"})
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	if err := s.append(f, events); err != nil {
		return err
	}
	return s.Compact(f)
}

// LoadAsOf reconstructs the state at a point in time from the newest
// snapshot before it plus the events up to it. The result is frozen.
func (s *Store) LoadAsOf(t time.Time) (*board.File, error) {
	base, snapSeq, found, err := s.readSnapshot(t)
	if err != nil {
		return nil, err
	}
	if !found {
		seqs, err := s.snapshotSeqs()
		if err != nil {
			return nil, err
		}
		if len(seqs) == 0 {
			return nil, fmt.Errorf("no history: the board has not been saved since the event log was introduced")
		}
		return nil, fmt.Errorf("no snapshot from before %s", t.Format(board.DateLayout))
	}
	events, err := s.readEvents(snapSeq, t)
	if err != nil {
		return nil, err
	}
	if err := base.Replay(events); err != nil {
		return nil, replayError(s.describePath(), err)
	}
	base.Freeze()
	return base, nil
}

// replayError words a replay failure. Data from a newer build gets advice
// instead of a bare parse error.
func replayError(path string, err error) error {
	var newer *board.NewerEventError
	if errors.As(err, &newer) {
		return fmt.Errorf("%s was %w; upgrade kancli to the version that wrote it (or newer) and try again (event %d: %s)",
			path, board.ErrNewerEvents, newer.Seq, newer.Detail)
	}
	if errors.Is(err, board.ErrNewerEvents) {
		return fmt.Errorf("%s was %w; upgrade kancli to the version that wrote it (or newer) and try again",
			path, board.ErrNewerEvents)
	}
	return fmt.Errorf("replay %s: %w", path, err)
}

// --- events ------------------------------------------------------------------

// Events returns the complete history in sequence order.
func (s *Store) Events() ([]board.Event, error) {
	return s.readEvents(0, time.Time{})
}

// ExportEventsJSONL writes every event to path, one JSON object per line,
// in sequence order. The DuckDB bridge reads it.
func (s *Store) ExportEventsJSONL(path string) error {
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	events, err := s.Events()
	if err != nil {
		fh.Close()
		return err
	}
	enc := json.NewEncoder(fh)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			fh.Close()
			return err
		}
	}
	return fh.Close()
}

// BoardStats returns statistics for a board, folding only the events that
// arrived since the last call into a cached walker.
func (s *Store) BoardStats(b *board.Board, now time.Time, days int) (board.Stats, error) {
	if s.walkers == nil {
		s.walkers = map[string]*board.StatsWalker{}
	}
	w := s.walkers[b.ID]
	if w == nil || !w.Compatible(b) {
		w = board.NewStatsWalker(b)
		s.walkers[b.ID] = w
	}
	events, err := s.readEvents(w.Seq(), time.Time{})
	if err != nil {
		return board.Stats{}, err
	}
	w.Feed(events)
	return w.Finish(b, now, days), nil
}

// TailEvents reports how many events are waiting to be compacted.
func (s *Store) TailEvents() int { return s.tailCount }

// append writes the events in one immediate transaction. When another
// process has written since our last read, its events are replayed into f
// first so nothing is lost and the sequence numbers stay consecutive.
func (s *Store) append(f *board.File, events []board.Event) error {
	if len(events) == 0 {
		return nil
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := s.mergeAndInsert(tx, f, events); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.tailCount, err = s.countTail(); err != nil {
		return err
	}
	s.dataVer, err = s.dataVersion()
	return err
}

func (s *Store) mergeAndInsert(tx *sql.Tx, f *board.File, events []board.Event) error {
	var ver int64
	if err := tx.QueryRow(`PRAGMA data_version`).Scan(&ver); err != nil {
		return err
	}
	if ver != s.dataVer {
		s.needReload = true
		rows, err := tx.Query(`SELECT `+eventColumns+` FROM events WHERE seq > ? ORDER BY seq`, s.seen)
		if err != nil {
			return err
		}
		foreign, err := scanEvents(rows)
		if err != nil {
			return err
		}
		if err := f.Replay(foreign); err != nil {
			return replayError(s.describePath(), err)
		}
		f.Pending() // replay is silent, but never leak events back into the log
		if last := lastSeq(foreign); last >= s.nextSeq {
			s.nextSeq = last + 1
			s.seen = last
		}
	}
	next, err := insertEvents(tx, events, s.nextSeq)
	if err != nil {
		return err
	}
	s.nextSeq = next
	s.seen = next - 1
	return nil
}

// Save appends the file's pending events and compacts when the tail has
// grown large. It never rewrites state another process may have changed.
func (s *Store) Save(f *board.File) error {
	if err := s.append(f, f.Pending()); err != nil {
		return err
	}
	if s.needReload {
		return nil // the caller reloads; compaction would drop foreign events
	}
	seqs, err := s.snapshotSeqs()
	if err != nil {
		return err
	}
	// The first save writes the base snapshot the log replays onto; later
	// saves compact once the tail has grown.
	if len(seqs) == 0 || s.tailCount >= CompactAfter {
		return s.Compact(f)
	}
	return nil
}

// Compact folds the events into a fresh snapshot and prunes the snapshot
// history. It refuses (ErrStale) when another process has written since
// this store last read, because folding would drop those events.
func (s *Store) Compact(f *board.File) error {
	if err := s.append(f, f.Pending()); err != nil {
		return err
	}
	if s.needReload {
		return ErrStale
	}
	db, err := s.conn()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := s.writeSnapshot(tx, f); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.tailCount = 0
	s.dataVer, err = s.dataVersion()
	return err
}

func (s *Store) writeSnapshot(tx *sql.Tx, f *board.File) error {
	var ver int64
	if err := tx.QueryRow(`PRAGMA data_version`).Scan(&ver); err != nil {
		return err
	}
	if ver != s.dataVer {
		s.needReload = true
		return ErrStale
	}
	rows, err := snapshotRowsTx(tx)
	if err != nil {
		return err
	}
	// The very first snapshot is preceded by an empty base so that "as of"
	// views can replay the whole history from the beginning.
	if len(rows) == 0 {
		if err := insertSnapshot(tx, f.EmptyBase(), 0); err != nil {
			return err
		}
		rows = append(rows, snapshotRow{seq: 0})
	}
	f.LastSeq = s.nextSeq - 1
	f.SnapshotAt = board.Now()
	if err := insertSnapshot(tx, f, f.LastSeq); err != nil {
		return err
	}
	rows = append(rows, snapshotRow{seq: f.LastSeq, at: f.SnapshotAt})
	return prune(tx, rows, board.Now())
}

// ChangedOnDisk reports whether another process has written to the
// database since this store last read it.
func (s *Store) ChangedOnDisk() bool {
	if s.needReload {
		return true
	}
	if s.db == nil {
		return false // nothing has been read yet, so nothing can be stale
	}
	ver, err := s.dataVersion()
	if err != nil {
		return false
	}
	if ver != s.dataVer {
		s.needReload = true
		return true
	}
	return false
}
