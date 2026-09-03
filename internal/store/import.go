package store

// The one-way import from the file-store layout kancli used before
// board.db. It runs once, when the database is missing and the old
// board.json is still there; afterwards the old files live in a backup
// directory and nothing here runs again.

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"
)

// DatabaseFormat is the user-facing format number of board.db; the file
// store counted as 1 or 2, the version written in its board.json. It is
// what the upgrade notice prints; the schema number inside the database is
// StoreFormat.
const DatabaseFormat = 3

// legacyBase strips the extension from the database path, giving the stem
// the file store named its files after (base+".json", base+".events.jsonl").
func legacyBase(dbPath string) string {
	return strings.TrimSuffix(dbPath, filepath.Ext(dbPath))
}

// legacyExists reports whether a file-store board sits at base.
func legacyExists(base string) bool { return exists(base + ".json") }

// maybeImportLegacy runs the importer when this store's database does not
// exist yet but the file store's board.json does. It is called from open,
// before the database is used for anything else.
func (s *Store) maybeImportLegacy() error {
	if s.path == "" || exists(s.path) {
		return nil
	}
	base := legacyBase(s.path)
	if !legacyExists(base) {
		return nil
	}
	up, err := s.importLegacy(base)
	if err != nil {
		return err
	}
	s.upgrade = &up
	return nil
}

// importLegacy builds a new database at s.path from the file-store layout
// rooted at base (base+".json" etc.), then moves the old files to backup.
func (s *Store) importLegacy(base string) (Upgrade, error) {
	snaps, from, err := readLegacySnapshots(base)
	if err != nil {
		return Upgrade{}, err
	}
	events, err := readLegacyEvents(base)
	if err != nil {
		return Upgrade{}, err
	}
	if err := s.writeImported(snaps, events); err != nil {
		removeDatabase(s.path)
		return Upgrade{}, err
	}
	backup, err := backupLegacy(base, from)
	if err != nil {
		return Upgrade{}, err
	}
	return Upgrade{From: from, To: DatabaseFormat, Backup: backup}, nil
}

// importedSnapshot is one folded state on its way into the database.
type importedSnapshot struct {
	seq  int64
	file *board.File
}

// readLegacySnapshots collects the snapshot history: every file in
// base+".snapshots" (named by sequence number) plus the live board.json,
// which is normally a copy of the newest one but may be ahead of it. The
// second result is the file version of board.json, which is what the
// upgrade notice reports.
func readLegacySnapshots(base string) ([]importedSnapshot, int, error) {
	var out []importedSnapshot
	newest := int64(-1)

	entries, err := os.ReadDir(base + ".snapshots")
	if err != nil && !os.IsNotExist(err) {
		return nil, 0, fmt.Errorf("read %s.snapshots: %w", base, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(base+".snapshots", name)
		seq, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
		if err != nil {
			continue // not a snapshot of ours; leave it for the backup
		}
		f, err := decodeLegacyFile(path)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, importedSnapshot{seq: seq, file: f})
		if seq > newest {
			newest = seq
		}
	}

	live, err := decodeLegacyFile(base + ".json")
	if err != nil {
		return nil, 0, err
	}
	from, err := legacyFileVersion(base + ".json")
	if err != nil {
		return nil, 0, err
	}
	// The live file is the newest state; import it whenever the snapshot
	// directory does not already hold something at least as new.
	if live.LastSeq >= newest {
		out = append(out, importedSnapshot{seq: live.LastSeq, file: live})
	}
	return out, from, nil
}

// decodeLegacyFile reads one board file of any released version. A version
// 1 file is migrated, exactly as the file store did.
func decodeLegacyFile(path string) (*board.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, _, err := board.DecodeVersion(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// legacyFileVersion is the version number written in the file: 1 for the
// original single-board format (or a file with no version at all), 2 for
// the multi-board one.
func legacyFileVersion(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	_, ver, err := board.DecodeVersion(data)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if ver < 1 {
		ver = 1
	}
	return ver, nil
}

// readLegacyEvents reads the archived segments in name order and then the
// live tail, with the byte-accurate reader the file store used.
func readLegacyEvents(base string) ([]board.Event, error) {
	var out []board.Event
	entries, err := os.ReadDir(base + ".events")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s.events: %w", base, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		events, _, err := readEventFile(filepath.Join(base+".events", name))
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}
	events, _, err := readEventFile(base + ".events.jsonl")
	if err != nil {
		return nil, err
	}
	return append(out, events...), nil
}

// writeImported creates the database and fills it in one transaction.
//
// The store opens its own connection the moment this returns, so this one
// folds the write-ahead log back into board.db and closes first: a
// checkpoint cannot truncate the log while another connection is holding
// it, and leaving that to the driver's own close is what used to strand
// board.db-wal and board.db-shm beside a freshly imported board.
func (s *Store) writeImported(snaps []importedSnapshot, events []board.Event) (err error) {
	db, err := sql.Open("sqlite", s.dsn())
	if err != nil {
		return fmt.Errorf("create %s: %w", s.Path(), err)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if err == nil && s.path != "" {
			if _, cerr := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); cerr != nil {
				err = fmt.Errorf("checkpoint %s: %w", s.Path(), cerr)
			}
		}
		if cerr := db.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if err := s.ensureSchema(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := importRows(tx, snaps, events); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

func importRows(tx *sql.Tx, snaps []importedSnapshot, events []board.Event) error {
	for _, s := range snaps {
		if err := insertSnapshot(tx, s.file, s.seq); err != nil {
			return fmt.Errorf("import snapshot %d: %w", s.seq, err)
		}
	}
	return insertLegacyEvents(tx, events)
}

// insertLegacyEvents writes the events with the sequence numbers they
// already carry, so the snapshots' last_seq keeps pointing at the right
// place. An event without a version predates the field and is version 1.
func insertLegacyEvents(tx *sql.Tx, events []board.Event) error {
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO events (` + eventColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	var next int64
	for i := range events {
		e := &events[i]
		if e.Seq <= next {
			e.Seq = next + 1 // an unnumbered or repeated event keeps the order
		}
		next = e.Seq
		if e.V == 0 {
			e.V = 1
		}
		var data any
		if len(e.Data) > 0 {
			data = []byte(e.Data)
		}
		if _, err := stmt.Exec(e.Seq, e.V, formatTime(e.At), e.Board, string(e.Kind),
			e.Task, e.From, e.To, e.Index, e.Text, data, e.Actor); err != nil {
			return fmt.Errorf("import event %d: %w", e.Seq, err)
		}
	}
	return nil
}

// removeDatabase deletes a half-written database and its journals, so a
// failed import leaves the legacy files as the only copy of the data.
func removeDatabase(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
}

// backupLegacy moves the file store's files into base+".backups/v<from>",
// or a timestamped sibling when that directory is already taken. Moving
// rather than copying means an older kancli cannot go on writing to files
// this build no longer reads.
func backupLegacy(base string, from int) (string, error) {
	dir := filepath.Join(base+".backups", fmt.Sprintf("v%d", from))
	if exists(dir) {
		dir = filepath.Join(base+".backups", fmt.Sprintf("v%d-%d", from, time.Now().Unix()))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	for _, suffix := range []string{".json", ".events.jsonl", ".events", ".snapshots"} {
		src := base + suffix
		if !exists(src) {
			continue
		}
		if err := os.Rename(src, filepath.Join(dir, filepath.Base(src))); err != nil {
			return "", fmt.Errorf("move %s: %w", src, err)
		}
	}
	_ = os.Remove(base + ".lock")
	return dir, nil
}
