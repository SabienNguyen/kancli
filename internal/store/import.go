package store

// The one-way import from the file-store layout kancli used before
// board.db. It runs once, when the database is missing and the old
// board.json is still there; afterwards the old files live in a backup
// directory and nothing here runs again.

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
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

// sqliteMagic opens every SQLite database file.
const sqliteMagic = "SQLite format 3\x00"

// staleImportLock is how old the import marker has to be before it counts
// as left behind by a process that died, rather than held by a live one.
const staleImportLock = 10 * time.Minute

// importWait bounds how long an import waits for another process — for its
// marker to go away, or for its backup to finish. importPoll is how often
// it looks. Both are variables so the tests do not have to sit them out.
var (
	importWait = 5 * time.Second
	importPoll = 20 * time.Millisecond
)

// beforeImportRename runs between building the database and moving it into
// place. It is the seam the test for a lost race writes through; it is nil
// in every real run.
var beforeImportRename func()

// legacyBase strips the extension from a path, giving the stem the file
// store named its sidecars after (base+".events.jsonl", base+".events",
// base+".snapshots"). It is what the old store did to whatever path it was
// configured with, so "~/tasks" keeps its whole name and "board.data"
// becomes "board".
func legacyBase(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path))
}

// legacyLayout names the file-store files an open may have to deal with:
// the state file, the stem of its sidecars, and the database that replaces
// them.
type legacyLayout struct {
	json string // the state file the file store wrote
	base string // the stem the sidecars are named after
	db   string // where the database belongs
}

// legacyLayoutFor works out whether a file-store board sits at or beside
// path, and where its database belongs.
//
// Two shapes reach here. The configured path may be the database (New maps
// a .json path to the .db beside it), in which case the old state file is
// base+".json". Or the configured path may be the old state file itself
// under a name that is not .json at all — KANCLI_FILE=~/tasks, -file
// board.data — which is only detectable by looking at the bytes.
func legacyLayoutFor(path string) (legacyLayout, bool) {
	if path == "" {
		return legacyLayout{}, false
	}
	if isJSONFile(path) {
		base := legacyBase(path)
		db := base + ".db"
		if db == path {
			db = path + ".db" // a .db path holding JSON; do not overwrite it
		}
		return legacyLayout{json: path, base: base, db: db}, true
	}
	base := legacyBase(path)
	if json := base + ".json"; json != path && exists(json) {
		return legacyLayout{json: json, base: base, db: path}, true
	}
	return legacyLayout{}, false
}

// isJSONFile reports whether path exists and begins with a JSON object
// rather than with SQLite's file magic.
func isJSONFile(path string) bool {
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = fh.Close() }()
	var head [64]byte
	n, _ := fh.Read(head[:]) // a file we cannot read is not one we import
	if n == 0 {
		return false
	}
	if bytes.HasPrefix(head[:n], []byte(sqliteMagic)) {
		return false
	}
	trimmed := bytes.TrimLeft(head[:n], " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// maybeImportLegacy runs the importer when a file-store board is still
// there and no database has replaced it yet. It is called from open,
// before the database is used for anything else, and it is what points the
// store at the database when the configured path is the old state file.
func (s *Store) maybeImportLegacy() error {
	lay, ok := legacyLayoutFor(s.path)
	if !ok {
		return nil
	}
	s.path = lay.db // everything below this point works on the database
	if exists(lay.db) {
		// An upgrade that was interrupted between writing the database and
		// moving the old files away. Either file could be the real board,
		// so neither is opened.
		return fmt.Errorf("%s: both %s and %s exist; move one of them away (a previous upgrade may have been interrupted)",
			filepath.Dir(lay.db), filepath.Base(lay.db), filepath.Base(lay.json))
	}
	up, imported, err := s.importLegacy(lay)
	if err != nil {
		return err
	}
	if imported {
		s.upgrade = &up
	}
	return nil
}

// importLegacy builds a new database from the file-store layout and moves
// the old files to a backup. The second result is false when there was
// nothing left to do because another process got there first.
//
// The whole run is serialised on a marker file, and the database is built
// under a temporary name and only then renamed into place, so two
// processes starting on the same legacy directory cannot destroy each
// other's work. Nothing here ever deletes a file this process did not
// create.
func (s *Store) importLegacy(lay legacyLayout) (Upgrade, bool, error) {
	unlock, err := lockImport(lay.base)
	if err != nil {
		return Upgrade{}, false, err
	}
	defer unlock()
	if exists(lay.db) {
		// Another process imported while we waited for the marker; its
		// backup has already run, because it held the marker until then.
		return Upgrade{}, false, nil
	}

	snaps, from, err := readLegacySnapshots(lay)
	if err != nil {
		return Upgrade{}, false, err
	}
	events, err := readLegacyEvents(lay.base)
	if err != nil {
		return Upgrade{}, false, err
	}
	tmp, err := tempDatabase(lay.db)
	if err != nil {
		return Upgrade{}, false, err
	}
	if err := s.writeImported(tmp, snaps, events); err != nil {
		removeDatabase(tmp)
		return Upgrade{}, false, err
	}
	if beforeImportRename != nil {
		beforeImportRename()
	}
	if exists(lay.db) {
		// Someone without the marker (an older build) won the race. Their
		// database is the live one; ours goes away untouched by them.
		removeDatabase(tmp)
		awaitBackup(lay)
		return Upgrade{}, false, nil
	}
	if err := os.Rename(tmp, lay.db); err != nil {
		removeDatabase(tmp)
		return Upgrade{}, false, fmt.Errorf("install %s: %w", lay.db, err)
	}
	backup, err := backupLegacy(lay, from)
	if err != nil {
		// The legacy files are still live, so the database this process
		// just created must not stay: it would make the next run skip the
		// import and quietly run on half the data.
		removeDatabase(lay.db)
		return Upgrade{}, false, err
	}
	return Upgrade{From: from, To: DatabaseFormat, Backup: backup}, true, nil
}

// lockImport takes the exclusive marker that serialises importers, waiting
// for a live one and taking over a marker left behind by a dead one. The
// returned function removes it.
func lockImport(base string) (func(), error) {
	path := base + ".import.lock"
	deadline := time.Now().Add(importWait)
	for {
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := fmt.Fprintf(fh, "%d\n", os.Getpid())
			cerr := fh.Close()
			return func() { _ = os.Remove(path) }, errors.Join(werr, cerr)
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create %s: %w", path, err)
		}
		if info, serr := os.Stat(path); serr == nil && time.Since(info.ModTime()) > staleImportLock {
			_ = os.Remove(path) // a marker from a process that died mid-import
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s: another kancli is importing this board; if none is running, remove %s and try again", base, path)
		}
		time.Sleep(importPoll)
	}
}

// awaitBackup waits out the window in which the process that won the race
// has installed its database but not yet moved the legacy files away, so
// the loser does not report an interrupted upgrade for a healthy one. It
// never touches a file: the winner owns them all.
func awaitBackup(lay legacyLayout) {
	deadline := time.Now().Add(importWait)
	for exists(lay.json) && time.Now().Before(deadline) {
		time.Sleep(importPoll)
	}
}

// tempDatabase creates the file the import writes into, beside the
// database it will become so the rename is on one filesystem.
func tempDatabase(dbPath string) (string, error) {
	dir, name := filepath.Dir(dbPath), filepath.Base(dbPath)
	fh, err := os.CreateTemp(dir, fmt.Sprintf("%s.importing-%d-*", name, os.Getpid()))
	if err != nil {
		return "", fmt.Errorf("create a temporary database in %s: %w", dir, err)
	}
	path := fh.Name()
	if err := fh.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// importedSnapshot is one folded state on its way into the database.
type importedSnapshot struct {
	seq  int64
	file *board.File
}

// readLegacySnapshots collects the snapshot history: every file in
// base+".snapshots" (named by sequence number) plus the live state file,
// which is normally a copy of the newest one but may be ahead of it. The
// second result is the file version of the live file, which is what the
// upgrade notice reports.
func readLegacySnapshots(lay legacyLayout) ([]importedSnapshot, int, error) {
	var out []importedSnapshot
	newest := int64(-1)

	entries, err := os.ReadDir(lay.base + ".snapshots")
	if err != nil && !os.IsNotExist(err) {
		return nil, 0, fmt.Errorf("read %s.snapshots: %w", lay.base, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(lay.base+".snapshots", name)
		seq, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
		if err != nil {
			continue // not a snapshot of ours; leave it for the backup
		}
		f, _, err := readLegacyFile(path)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, importedSnapshot{seq: seq, file: f})
		if seq > newest {
			newest = seq
		}
	}

	live, from, err := readLegacyFile(lay.json)
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

// readLegacyFile reads one board file of any released version, returning
// the migrated file and the version it was written as: 1 for the original
// single-board format (or a file with no version at all), 2 for the
// multi-board one. A version 1 file is migrated exactly as the file store
// did.
func readLegacyFile(path string) (*board.File, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	f, ver, err := board.DecodeVersion(data)
	if err != nil {
		return nil, 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if ver < 1 {
		ver = 1
	}
	return f, ver, nil
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

// writeImported creates the database at path and fills it in one
// transaction.
//
// The store opens its own connection the moment this returns, so this one
// folds the write-ahead log back into the file and closes first: a
// checkpoint cannot truncate the log while another connection is holding
// it, and leaving that to the driver's own close is what used to strand
// board.db-wal and board.db-shm beside a freshly imported board.
func (s *Store) writeImported(path string, snaps []importedSnapshot, events []board.Event) (err error) {
	db, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return fmt.Errorf("create %s: %w", s.Path(), err)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if err == nil {
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
		if err := execEvent(stmt, *e, e.Seq); err != nil {
			return fmt.Errorf("import event %d: %w", e.Seq, err)
		}
	}
	return nil
}

// removeDatabase deletes a database this process created, and its
// journals: a failed import leaves the legacy files as the only copy of
// the data.
func removeDatabase(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
}

// backupLegacy moves the file store's files into base+".backups/v<from>",
// or a timestamped sibling when that directory is already taken. Moving
// rather than copying means an older kancli cannot go on writing to files
// this build no longer reads.
//
// The state file moves last, and the directories first, because the state
// file is what every later run keys its detection on: a move that fails
// half way through then still looks like a board waiting to be imported
// rather than like a database that is missing its history.
func backupLegacy(lay legacyLayout, from int) (string, error) {
	dir := filepath.Join(lay.base+".backups", fmt.Sprintf("v%d", from))
	if exists(dir) {
		dir = filepath.Join(lay.base+".backups", fmt.Sprintf("v%d-%d", from, time.Now().Unix()))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	sources := []string{lay.base + ".events", lay.base + ".snapshots", lay.base + ".events.jsonl", lay.json}
	for _, src := range sources {
		if !exists(src) {
			continue
		}
		if err := os.Rename(src, filepath.Join(dir, filepath.Base(src))); err != nil {
			return "", fmt.Errorf("move %s: %w", src, err)
		}
	}
	_ = os.Remove(lay.base + ".lock")
	return dir, nil
}
