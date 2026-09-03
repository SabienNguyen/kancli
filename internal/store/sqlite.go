package store

// The SQLite backing for Store: connection handling, schema, and the row
// level read and write primitives. store.go holds the public API built on
// top of them.

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

// StoreFormat is the schema number written into meta. A database carrying a
// higher number was written by a newer kancli and is refused.
const StoreFormat = 1

// timeLayout is RFC3339Nano with a fixed nine-digit fraction so that the
// text sorts and compares lexically (plain RFC3339Nano drops trailing
// zeros, which breaks byte order). It parses as RFC3339Nano.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS events (
  seq   INTEGER PRIMARY KEY,
  v     INTEGER NOT NULL,
  at    TEXT    NOT NULL,
  board TEXT    NOT NULL,
  kind  TEXT    NOT NULL,
  task  INTEGER, from_col TEXT, to_col TEXT, idx INTEGER, text TEXT,
  data  BLOB,
  actor TEXT
);
CREATE INDEX IF NOT EXISTS events_at ON events(at);
CREATE INDEX IF NOT EXISTS events_board_seq ON events(board, seq);
CREATE TABLE IF NOT EXISTS snapshots (
  seq   INTEGER PRIMARY KEY,
  at    TEXT    NOT NULL,
  state BLOB    NOT NULL
);
`

const eventColumns = `seq, v, at, board, kind, task, from_col, to_col, idx, text, data, actor`

// memCounter names the private in-memory database of each demo store.
var memCounter atomic.Int64

// formatTime renders a timestamp for storage.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime reads a stored timestamp back in the local zone, which is how
// events reach the rest of the program.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.In(time.Local), nil
}

// dsn builds the connection string for this store.
func (s *Store) dsn() string {
	if s.path == "" {
		return fmt.Sprintf("file:kancli-demo-%d?mode=memory&cache=shared&_txlock=immediate", memCounter.Add(1))
	}
	return "file:" + s.path +
		"?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
}

// conn opens the database on first use and creates or checks the schema.
// New never touches disk, because the CLI builds stores for completion and
// for paths that may not exist.
func (s *Store) conn() (*sql.DB, error) {
	if s.db != nil || s.openErr != nil {
		return s.db, s.openErr
	}
	s.db, s.openErr = s.open()
	return s.db, s.openErr
}

func (s *Store) open() (*sql.DB, error) {
	if s.path != "" {
		if dir := filepath.Dir(s.path); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create %s: %w", dir, err)
			}
		}
	}
	// A board still in the old file-store layout is imported first, so
	// everything below opens the database the importer just built.
	if err := s.maybeImportLegacy(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", s.dsn())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", s.Path(), err)
	}
	// One connection: PRAGMA data_version is per connection, and it is how
	// this store notices another process writing.
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)
	if err := s.ensureSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.initState(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// initState primes the sequence counter and the change counter from what
// the database already holds, so a store that writes without loading first
// still numbers its events correctly and does not mistake its own database
// for a foreign write. Load refines all of it.
func (s *Store) initState(db *sql.DB) error {
	var events, snapshots sql.NullInt64
	if err := db.QueryRow(`SELECT (SELECT max(seq) FROM events), (SELECT max(seq) FROM snapshots)`).Scan(&events, &snapshots); err != nil {
		return fmt.Errorf("read %s: %w", s.Path(), err)
	}
	s.seen = events.Int64
	if snapshots.Int64 > s.seen {
		s.seen = snapshots.Int64
	}
	s.nextSeq = s.seen + 1
	return db.QueryRow(`PRAGMA data_version`).Scan(&s.dataVer)
}

// ensureSchema creates the tables when they are missing and refuses a
// database written by a newer kancli.
func (s *Store) ensureSchema(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("prepare %s: %w", s.Path(), err)
	}
	var value string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = 'format'`).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = db.Exec(`INSERT INTO meta (key, value) VALUES ('format', ?), ('created', ?)`,
			fmt.Sprint(StoreFormat), formatTime(board.Now()))
		if err != nil {
			return fmt.Errorf("prepare %s: %w", s.Path(), err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("read %s: %w", s.Path(), err)
	}
	n := 0
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return fmt.Errorf("%s has an unreadable store format %q", s.Path(), value)
	}
	if n > StoreFormat {
		return fmt.Errorf("%s was written by a newer kancli (store format %d, this build reads %d); upgrade kancli",
			s.Path(), n, StoreFormat)
	}
	return nil
}

// dataVersion returns SQLite's change counter for this connection. It moves
// when another connection commits, and never for our own writes.
func (s *Store) dataVersion() (int64, error) {
	db, err := s.conn()
	if err != nil {
		return 0, err
	}
	var v int64
	if err := db.QueryRow(`PRAGMA data_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// --- snapshots ---------------------------------------------------------------

type snapshotRow struct {
	seq int64
	at  time.Time
}

// cutSeq is the highest event sequence stamped at or before t, or zero
// when no event is that old. It is what a point in time means to the log.
//
// Snapshots are chosen against it rather than against their own at, which
// records when the fold happened and not what it covers: history that was
// imported or written in bulk carries today's snapshots over events dated
// years back, and picking by wall clock then falls all the way to the
// empty base and replays everything.
func (s *Store) cutSeq(t time.Time) (int64, error) {
	db, err := s.conn()
	if err != nil {
		return 0, err
	}
	var seq sql.NullInt64
	if err := db.QueryRow(`SELECT max(seq) FROM events WHERE at <= ?`, formatTime(t)).Scan(&seq); err != nil {
		return 0, fmt.Errorf("read %s: %w", s.Path(), err)
	}
	return seq.Int64, nil
}

// readSnapshot returns the newest snapshot covering no more than the events
// up to upTo (a zero time means "the newest"), or nil when the database
// holds none.
func (s *Store) readSnapshot(upTo time.Time) (*board.File, int64, bool, error) {
	cut := int64(-1) // negative: no upper bound
	if !upTo.IsZero() {
		var err error
		if cut, err = s.cutSeq(upTo); err != nil {
			return nil, 0, false, err
		}
	}
	return s.readSnapshotAt(cut)
}

// readSnapshotAt returns the newest snapshot at or before sequence cut. A
// negative cut asks for the newest snapshot of all.
func (s *Store) readSnapshotAt(cut int64) (*board.File, int64, bool, error) {
	db, err := s.conn()
	if err != nil {
		return nil, 0, false, err
	}
	query := `SELECT seq, state FROM snapshots ORDER BY seq DESC LIMIT 1`
	args := []any{}
	if cut >= 0 {
		query = `SELECT seq, state FROM snapshots WHERE seq <= ? ORDER BY seq DESC LIMIT 1`
		args = append(args, cut)
	}
	var seq int64
	var blob []byte
	err = db.QueryRow(query, args...).Scan(&seq, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("read %s: %w", s.Path(), err)
	}
	state, err := gunzip(blob)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read snapshot %d of %s: %w", seq, s.Path(), err)
	}
	f, err := board.Decode(state)
	if err != nil {
		return nil, 0, false, fmt.Errorf("parse snapshot %d of %s: %w", seq, s.Path(), err)
	}
	return f, seq, true, nil
}

// snapshotRows lists every snapshot, oldest first.
func (s *Store) snapshotRows() ([]snapshotRow, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT seq, at FROM snapshots ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	return scanSnapshotRows(rows)
}

// snapshotRowsTx is snapshotRows inside an open transaction.
func snapshotRowsTx(tx *sql.Tx) ([]snapshotRow, error) {
	rows, err := tx.Query(`SELECT seq, at FROM snapshots ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	return scanSnapshotRows(rows)
}

func scanSnapshotRows(rows *sql.Rows) ([]snapshotRow, error) {
	defer func() { _ = rows.Close() }()
	var out []snapshotRow
	for rows.Next() {
		var r snapshotRow
		var at string
		if err := rows.Scan(&r.seq, &at); err != nil {
			return nil, err
		}
		t, err := parseTime(at)
		if err != nil {
			return nil, err
		}
		r.at = t
		out = append(out, r)
	}
	return out, rows.Err()
}

// snapshotSeqs is the sequence numbers of the stored snapshots; tests and
// pruning use it.
func (s *Store) snapshotSeqs() ([]int64, error) {
	rows, err := s.snapshotRows()
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.seq
	}
	return out, nil
}

// insertSnapshot stores f as the snapshot for seq, replacing any snapshot
// already recorded there.
func insertSnapshot(tx *sql.Tx, f *board.File, seq int64) error {
	state, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	blob, err := gzipBytes(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT OR REPLACE INTO snapshots (seq, at, state) VALUES (?, ?, ?)`,
		seq, formatTime(f.SnapshotAt), blob)
	return err
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzip(blob []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

// --- events ------------------------------------------------------------------

// scanEvents reads event rows into board events.
func scanEvents(rows *sql.Rows) ([]board.Event, error) {
	defer func() { _ = rows.Close() }()
	var out []board.Event
	for rows.Next() {
		var e board.Event
		var at string
		var brd, kind sql.NullString
		var from, to, text, actor sql.NullString
		var task, idx sql.NullInt64
		var data []byte
		if err := rows.Scan(&e.Seq, &e.V, &at, &brd, &kind, &task, &from, &to, &idx, &text, &data, &actor); err != nil {
			return nil, err
		}
		t, err := parseTime(at)
		if err != nil {
			return nil, fmt.Errorf("event %d has an unreadable time %q: %w", e.Seq, at, err)
		}
		e.At = t
		e.Board, e.Kind = brd.String, board.EventKind(kind.String)
		e.Task, e.Index = int(task.Int64), int(idx.Int64)
		e.From, e.To, e.Text, e.Actor = from.String, to.String, text.String, actor.String
		if len(data) > 0 {
			e.Data = append(json.RawMessage(nil), data...)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// readEvents returns the events after seq, optionally stopping at a time.
func (s *Store) readEvents(after int64, upTo time.Time) ([]board.Event, error) {
	return s.readEventsThrough(after, -1, upTo)
}

// readEventsThrough returns the events after seq and at or before sequence
// through (negative: no bound), optionally stopping at a time as well. The
// two bounds agree for well-formed history; keeping both means an event
// whose timestamp runs ahead of its sequence still cannot leak into a view
// of the past.
func (s *Store) readEventsThrough(after, through int64, upTo time.Time) ([]board.Event, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + eventColumns + ` FROM events WHERE seq > ?`
	args := []any{after}
	if through >= 0 {
		query += ` AND seq <= ?`
		args = append(args, through)
	}
	if !upTo.IsZero() {
		query += ` AND at <= ?`
		args = append(args, formatTime(upTo))
	}
	query += ` ORDER BY seq`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.Path(), err)
	}
	return scanEvents(rows)
}

// insertEvents numbers and writes the events, returning the next free
// sequence number.
func insertEvents(tx *sql.Tx, events []board.Event, next int64) (int64, error) {
	stmt, err := tx.Prepare(`INSERT INTO events (` + eventColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return next, err
	}
	defer func() { _ = stmt.Close() }()
	for i := range events {
		e := &events[i]
		e.Seq = next
		next++
		if e.V == 0 {
			e.V = board.EventVersion
		}
		var data any
		if len(e.Data) > 0 {
			data = []byte(e.Data)
		}
		if _, err := stmt.Exec(e.Seq, e.V, formatTime(e.At), e.Board, string(e.Kind),
			e.Task, e.From, e.To, e.Index, e.Text, data, e.Actor); err != nil {
			return next, fmt.Errorf("append event %d: %w", e.Seq, err)
		}
	}
	return next, nil
}

// maxEventSeq is the highest sequence number in the database.
func (s *Store) maxEventSeq() (int64, error) {
	db, err := s.conn()
	if err != nil {
		return 0, err
	}
	var seq sql.NullInt64
	if err := db.QueryRow(`SELECT max(seq) FROM events`).Scan(&seq); err != nil {
		return 0, err
	}
	return seq.Int64, nil
}

// countTail is the number of events written since the newest snapshot.
func (s *Store) countTail() (int, error) {
	db, err := s.conn()
	if err != nil {
		return 0, err
	}
	var n int
	err = db.QueryRow(`SELECT count(*) FROM events WHERE seq > coalesce((SELECT max(seq) FROM snapshots), 0)`).Scan(&n)
	return n, err
}

// --- retention ---------------------------------------------------------------

// prune deletes snapshots the retention policy no longer needs: sequence
// zero and the newest five always stay, then one per calendar day for the
// last 30 days and one per ISO week before that.
func prune(tx *sql.Tx, rows []snapshotRow, now time.Time) error {
	keep := map[int64]bool{0: true}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].seq < rows[j].seq })
	// The caller hands us the snapshot it is writing alongside the rows
	// already in the table, and writing one is INSERT OR REPLACE: a
	// compaction that adds no events rewrites the newest snapshot in place
	// and the same sequence arrives twice. Collapsing repeats keeps that
	// rewrite from using up two of the five newest slots (which is how the
	// fifth-newest snapshot used to be deleted). The later row wins, since
	// it carries the replacement's timestamp.
	uniq := make([]snapshotRow, 0, len(rows))
	for _, r := range rows {
		if n := len(uniq); n > 0 && uniq[n-1].seq == r.seq {
			uniq[n-1] = r
			continue
		}
		uniq = append(uniq, r)
	}
	rows = uniq
	for i := len(rows) - 1; i >= 0 && len(rows)-i <= 5; i-- {
		keep[rows[i].seq] = true
	}
	cutoff := now.AddDate(0, 0, -30)
	newest := map[string]int64{} // bucket -> newest seq in it
	for _, r := range rows {
		at := r.at.In(time.Local)
		var bucket string
		if at.After(cutoff) {
			bucket = at.Format("2006-01-02")
		} else {
			y, w := at.ISOWeek()
			bucket = fmt.Sprintf("%04d-W%02d", y, w)
		}
		if r.seq > newest[bucket] {
			newest[bucket] = r.seq
		}
	}
	for _, seq := range newest {
		keep[seq] = true
	}
	for _, r := range rows {
		if keep[r.seq] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM snapshots WHERE seq = ?`, r.seq); err != nil {
			return err
		}
	}
	return nil
}
