package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SabienNguyen/kancli/internal/board"

	"github.com/gofrs/flock"
)

// compactAfter is how many events may accumulate in the tail log before
// the store folds them into a fresh snapshot.
const CompactAfter = 500

// errStale is returned by compact when another process changed the log or
// snapshot since this store last read them. Callers reload and retry.
var ErrStale = errors.New("the board changed on disk; reload before writing a snapshot")

// snapshotSeq reads only the last_seq of a snapshot file.
func snapshotSeq(path string) (int64, time.Time) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, time.Time{}
	}
	var probe struct {
		LastSeq int64 `json:"last_seq"`
	}
	_ = json.Unmarshal(data, &probe)
	mod := time.Time{}
	if info, err := os.Stat(path); err == nil {
		mod = info.ModTime()
	}
	return probe.LastSeq, mod
}

// maxEventLine bounds one event line; undo events carry a whole board.
const maxEventLine = 32 << 20

// store persists the data as a snapshot plus an append-only event log:
//
//	board.json            snapshot: full state as of LastSeq
//	board.events.jsonl    tail: events appended since the snapshot
//	board.events/         archived tail segments (immutable, for analytics)
//	board.snapshots/      every snapshot ever written (for "as of" views)
//	board.lock            cross-process lock
//
// A store with an empty path keeps everything in memory only.
type Store struct {
	path       string
	logPath    string
	archiveDir string
	snapDir    string
	lockPath   string
	backupDir  string
	actor      string

	logSize    int64     // bytes of the tail log this process has consumed
	tailCount  int       // events in the tail log
	snapMod    time.Time // snapshot modification time at the last load/save
	nextSeq    int64     // next event sequence number
	needReload bool      // another process appended events since our load

	mem []board.Event // event log for in-memory stores

	segments map[string]cachedSegment      // archived segments already parsed
	walkers  map[string]*board.StatsWalker // incremental stats per board

	upgrade *Upgrade // set by Load when it opened older-format data
}

// Upgrade describes a data directory that an older kancli wrote and this
// one has just opened.
type Upgrade struct {
	From, To int
	Backup   string // directory holding a copy of the old files
}

// Upgraded reports the upgrade Load performed, if any.
func (s *Store) Upgraded() (Upgrade, bool) {
	if s.upgrade == nil {
		return Upgrade{}, false
	}
	return *s.upgrade, true
}

// cachedSegment is a parsed archive file; archived segments never change,
// so the size check only guards against a file being replaced.
type cachedSegment struct {
	size   int64
	events []board.Event
}

func New(path string) *Store {
	s := &Store{path: path, actor: "ui"}
	if path != "" {
		base := strings.TrimSuffix(path, filepath.Ext(path))
		s.logPath = base + ".events.jsonl"
		s.archiveDir = base + ".events"
		s.snapDir = base + ".snapshots"
		s.lockPath = base + ".lock"
		s.backupDir = base + ".backups"
	}
	return s
}

// enabled reports whether the store writes to disk.
// Path is the snapshot file; LogPath, ArchiveDir and SnapDir are its
// siblings. They are empty for in-memory stores.
func (s *Store) Path() string       { return s.path }
func (s *Store) LogPath() string    { return s.logPath }
func (s *Store) ArchiveDir() string { return s.archiveDir }
func (s *Store) SnapDir() string    { return s.snapDir }

// SetActor names the process in the events this store writes.
func (s *Store) SetActor(actor string) { s.actor = actor }

// NeedReload reports that another process wrote to the log or snapshot
// since this store last read them; callers should Load again.
func (s *Store) NeedReload() bool { return s.needReload }

func (s *Store) Enabled() bool { return s != nil && s.path != "" }

// describe is shown in the header.
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

// defaultStorePath returns the data file location: $KANCLI_FILE if set,
// otherwise kancli/board.json under $XDG_DATA_HOME or the OS config dir.
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
	return filepath.Join(base, "kancli", "board.json"), nil
}

// lock takes the cross-process lock, waiting a little for a busy peer.
func (s *Store) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.lockPath), 0o755); err != nil {
		return nil, err
	}
	fl := flock.New(s.lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := fl.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", s.lockPath, err)
	}
	if !ok {
		return nil, fmt.Errorf("another kancli is holding %s", s.lockPath)
	}
	return func() { fl.Unlock() }, nil //nolint:errcheck // best effort
}

// --- loading ----------------------------------------------------------------

// load reads the snapshot, replays the tail log and returns the live state.
func (s *Store) Load() (*board.File, error) {
	if !s.Enabled() {
		f := board.NewFile()
		f.SetActor(s.actor)
		return f, nil
	}
	f, fresh, version, err := s.readSnapshot(s.path)
	if err != nil {
		return nil, err
	}
	if version < board.FileVersion && exists(s.path) {
		if err := s.backupOld(version); err != nil {
			return nil, err
		}
	}
	f.SetActor(s.actor)

	events, size, err := readEventFile(s.logPath)
	if err != nil {
		return nil, err
	}
	if err := f.Replay(events); err != nil {
		return nil, replayError(s.logPath, err)
	}
	s.logSize = size
	s.tailCount = len(events)
	s.nextSeq = f.LastSeq + 1
	for _, e := range events {
		if e.Seq >= s.nextSeq {
			s.nextSeq = e.Seq + 1
		}
	}
	s.needReload = false

	// A board that predates the event log gets a history seeded from its
	// task timestamps so "as of" views and stats have something to work on.
	if fresh && len(events) == 0 && !exists(s.snapDir) && hasTasks(f) {
		if err := s.bootstrap(f); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func hasTasks(f *board.File) bool {
	for _, b := range f.Boards {
		if len(b.Tasks) > 0 {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readSnapshot decodes a snapshot file. fresh reports that it existed but
// carries no event sequence yet (a pre-event-log board).
func (s *Store) readSnapshot(path string) (f *board.File, fresh bool, version int, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.snapMod = time.Time{}
		return board.NewFile(), false, board.FileVersion, nil
	}
	if err != nil {
		return nil, false, 0, fmt.Errorf("read %s: %w", path, err)
	}
	if info, err := os.Stat(path); err == nil && path == s.path {
		s.snapMod = info.ModTime()
	}
	f, version, err = board.DecodeVersion(data)
	if err != nil {
		return nil, false, version, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, f.LastSeq == 0 && f.SnapshotAt.IsZero(), version, nil
}

// backupOld copies the snapshot and the live log as they were before this
// kancli touches them, once per source version. Nothing is overwritten:
// a directory that already exists is left alone.
func (s *Store) backupOld(from int) error {
	dir := filepath.Join(s.backupDir, fmt.Sprintf("v%d", from))
	s.upgrade = &Upgrade{From: from, To: board.FileVersion, Backup: dir}
	if exists(dir) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup %s: %w", dir, err)
	}
	for _, src := range []string{s.path, s.logPath} {
		data, err := os.ReadFile(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("back up %s: %w", src, err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(src)), data, 0o644); err != nil {
			return fmt.Errorf("back up %s: %w", src, err)
		}
	}
	return nil
}

// bootstrap seeds the log for a board that predates it: an empty initial
// snapshot, one created event per task at its creation time, and moves or
// archives at the task's last update. It then compacts so the live
// snapshot carries the real state.
func (s *Store) bootstrap(f *board.File) error {
	if err := s.writeSnapshotCopy(f.EmptyBase(), 0); err != nil {
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
	if err := s.append(events); err != nil {
		return err
	}
	return s.Compact(f)
}

// loadAsOf reconstructs the state at a point in time from the newest
// snapshot before it plus the events up to it.
func (s *Store) LoadAsOf(t time.Time) (*board.File, error) {
	if !s.Enabled() {
		f := board.NewFile()
		return f, nil
	}
	snapshots, _ := filepath.Glob(filepath.Join(s.snapDir, "*.json"))
	sort.Strings(snapshots)
	var base *board.File
	for i := len(snapshots) - 1; i >= 0; i-- {
		f, _, _, err := s.readSnapshot(snapshots[i])
		if err != nil {
			continue
		}
		if f.SnapshotAt.IsZero() || !f.SnapshotAt.After(t) {
			base = f
			break
		}
	}
	if base == nil {
		if len(snapshots) == 0 {
			return nil, fmt.Errorf("no history: the board has not been saved since the event log was introduced")
		}
		return nil, fmt.Errorf("no snapshot from before %s", t.Format(board.DateLayout))
	}
	events, err := s.Events()
	if err != nil {
		return nil, err
	}
	var upTo []board.Event
	for _, e := range events {
		if !e.At.After(t) {
			upTo = append(upTo, e)
		}
	}
	if err := base.Replay(upTo); err != nil {
		return nil, replayError(s.logPath, err)
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

// readEventFile parses a JSONL file. A torn last line (from a crash mid
// write) is dropped; any other damage is an error. Line lengths are counted
// in real bytes, so a log with CRLF endings is measured accurately and the
// caller never truncates at the wrong offset.
func readEventFile(path string) ([]board.Event, int64, error) {
	fh, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer fh.Close()
	rd := bufio.NewReaderSize(fh, 64<<10)
	var events []board.Event
	var consumed int64
	for {
		raw, err := readEventLine(rd)
		if errors.Is(err, errEventLineTooLong) {
			return nil, 0, fmt.Errorf("%s: bad event line after seq %d: %w", path, lastSeq(events), err)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
		atEOF := errors.Is(err, io.EOF)
		if len(raw) == 0 {
			break // nothing left to read
		}
		// The terminator counts towards the line, but not towards the JSON.
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			consumed += int64(len(raw)) // a blank line is skipped but counted
			if atEOF {
				break
			}
			continue
		}
		var e board.Event
		if jerr := json.Unmarshal(line, &e); jerr != nil {
			if atEOF || isLastLine(rd) {
				// A torn tail: leave it unconsumed so the caller truncates.
				break
			}
			return nil, 0, fmt.Errorf("%s: bad event line after seq %d: %w", path, lastSeq(events), jerr)
		}
		events = append(events, e)
		consumed += int64(len(raw))
		if atEOF {
			break
		}
	}
	return events, consumed, nil
}

// isLastLine reports whether the reader is exhausted, so the line just read
// was the file's last one.
func isLastLine(rd *bufio.Reader) bool {
	_, err := rd.Peek(1)
	return errors.Is(err, io.EOF)
}

// errEventLineTooLong reports a line past maxEventLine, which means the log
// is damaged rather than merely large.
var errEventLineTooLong = fmt.Errorf("event line exceeds %d bytes", maxEventLine)

// readEventLine returns one line including its terminator, or io.EOF along
// with the final unterminated line. The returned slice is only valid until
// the next read.
func readEventLine(rd *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		frag, err := rd.ReadSlice('\n')
		if len(buf) == 0 && !errors.Is(err, bufio.ErrBufferFull) {
			if len(frag) > maxEventLine {
				return frag, errEventLineTooLong
			}
			return frag, err
		}
		buf = append(buf, frag...)
		if len(buf) > maxEventLine {
			return buf, errEventLineTooLong
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

func lastSeq(events []board.Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

// events returns the complete history: archived segments then the tail.
func (s *Store) Events() ([]board.Event, error) {
	if !s.Enabled() {
		return append([]board.Event(nil), s.mem...), nil
	}
	segments, _ := filepath.Glob(filepath.Join(s.archiveDir, "*.jsonl"))
	sort.Strings(segments)
	if s.segments == nil {
		s.segments = map[string]cachedSegment{}
	}
	var all []board.Event
	for _, seg := range segments {
		info, err := os.Stat(seg)
		if err != nil {
			return nil, err
		}
		cached, ok := s.segments[seg]
		if !ok || cached.size != info.Size() {
			evs, _, err := readEventFile(seg)
			if err != nil {
				return nil, err
			}
			cached = cachedSegment{size: info.Size(), events: evs}
			s.segments[seg] = cached
		}
		all = append(all, cached.events...)
	}
	tail, _, err := readEventFile(s.logPath)
	if err != nil {
		return nil, err
	}
	all = append(all, tail...)
	ordered := true
	for i := 1; i < len(all); i++ {
		if all[i].Seq < all[i-1].Seq {
			ordered = false
			break
		}
	}
	if !ordered {
		sort.SliceStable(all, func(i, j int) bool { return all[i].Seq < all[j].Seq })
	}
	return all, nil
}

// boardStats returns statistics for a board, folding only the events that
// arrived since the last call into a cached walker.
func (s *Store) BoardStats(b *board.Board, now time.Time, days int) (board.Stats, error) {
	events, err := s.Events()
	if err != nil {
		return board.Stats{}, err
	}
	if s.walkers == nil {
		s.walkers = map[string]*board.StatsWalker{}
	}
	w := s.walkers[b.ID]
	if w == nil || !w.Compatible(b) || (len(events) > 0 && events[len(events)-1].Seq < w.Seq()) {
		w = board.NewStatsWalker(b)
		s.walkers[b.ID] = w
	}
	w.Feed(events)
	return w.Finish(b, now, days), nil
}

// append writes events to the tail log under the lock and numbers them.
func (s *Store) append(events []board.Event) error {
	if len(events) == 0 {
		return nil
	}
	if !s.Enabled() {
		for i := range events {
			s.nextSeq++
			events[i].Seq = s.nextSeq
			s.mem = append(s.mem, events[i])
		}
		return nil
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	// Another process may have appended, or compacted the log away, since
	// we last read it. Re-derive the next sequence number from whatever is
	// on disk so ours can never collide with or fall behind a snapshot.
	if info, err := os.Stat(s.logPath); err == nil {
		if info.Size() != s.logSize {
			s.needReload = true
			all, consumed, err := readEventFile(s.logPath)
			if err != nil {
				return err
			}
			if last := lastSeq(all); last >= s.nextSeq {
				s.nextSeq = last + 1
			}
			// A torn last line (from a crash) is garbage; cut it off so the
			// next event starts on a clean line.
			if consumed < info.Size() {
				if err := os.Truncate(s.logPath, consumed); err != nil {
					return fmt.Errorf("repair %s: %w", s.logPath, err)
				}
			}
			s.logSize = consumed
			s.tailCount = len(all)
		}
	} else if s.logSize != 0 {
		// The tail we knew about was compacted by someone else.
		s.needReload = true
		s.logSize, s.tailCount = 0, 0
	}
	if seq, mod := snapshotSeq(s.path); !mod.IsZero() && !mod.Equal(s.snapMod) {
		s.needReload = true
		if seq >= s.nextSeq {
			s.nextSeq = seq + 1
		}
	}

	fh, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", s.logPath, err)
	}
	w := bufio.NewWriter(fh)
	var written int64
	for i := range events {
		events[i].Seq = s.nextSeq
		s.nextSeq++
		if events[i].V == 0 {
			events[i].V = board.EventVersion
		}
		line, err := json.Marshal(events[i])
		if err != nil {
			fh.Close()
			return err
		}
		n, err := w.Write(append(line, '\n'))
		if err != nil {
			fh.Close()
			return fmt.Errorf("append %s: %w", s.logPath, err)
		}
		written += int64(n)
	}
	if err := w.Flush(); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		return err
	}
	if err := fh.Close(); err != nil {
		return err
	}
	s.logSize += written
	s.tailCount += len(events)
	return nil
}

// save appends the file's pending events and compacts when the tail has
// grown large. It never rewrites state another process may have changed.
func (s *Store) Save(f *board.File) error {
	if err := s.append(f.Pending()); err != nil {
		return err
	}
	if !s.Enabled() {
		return nil
	}
	// The first save writes the base snapshot the log replays onto; later
	// saves compact once the tail has grown.
	if !exists(s.path) || (s.tailCount >= CompactAfter && !s.needReload) {
		return s.Compact(f)
	}
	return nil
}

// compact writes a fresh snapshot and moves the tail log into the archive.
func (s *Store) Compact(f *board.File) error {
	if !s.Enabled() {
		return nil
	}
	if err := s.append(f.Pending()); err != nil {
		return err
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	// Never fold events this process has not replayed: if the log or the
	// snapshot moved under us, the caller must reload first.
	if s.needReload {
		return ErrStale
	}
	if info, err := os.Stat(s.logPath); err == nil {
		if info.Size() != s.logSize {
			s.needReload = true
			return ErrStale
		}
	} else if s.logSize != 0 {
		s.needReload = true
		return ErrStale
	}
	if _, mod := snapshotSeq(s.path); !mod.IsZero() && !s.snapMod.IsZero() && !mod.Equal(s.snapMod) {
		s.needReload = true
		return ErrStale
	}

	// The very first snapshot is preceded by an empty base so that "as of"
	// views can replay the whole history from the beginning.
	if snaps, _ := filepath.Glob(filepath.Join(s.snapDir, "*.json")); len(snaps) == 0 {
		if err := s.writeSnapshotCopy(f.EmptyBase(), 0); err != nil {
			return err
		}
	}
	f.LastSeq = s.nextSeq - 1
	f.SnapshotAt = board.Now()
	if err := writeAtomic(s.path, f); err != nil {
		return err
	}
	if info, err := os.Stat(s.path); err == nil {
		s.snapMod = info.ModTime()
	}
	if err := s.writeSnapshotCopy(f, f.LastSeq); err != nil {
		return err
	}
	if s.tailCount > 0 || exists(s.logPath) {
		tail, _, err := readEventFile(s.logPath)
		if err != nil {
			return err
		}
		if len(tail) > 0 {
			if err := os.MkdirAll(s.archiveDir, 0o755); err != nil {
				return err
			}
			name := fmt.Sprintf("%012d-%012d.jsonl", tail[0].Seq, tail[len(tail)-1].Seq)
			if err := os.Rename(s.logPath, filepath.Join(s.archiveDir, name)); err != nil {
				return fmt.Errorf("archive %s: %w", s.logPath, err)
			}
		} else {
			os.Remove(s.logPath)
		}
	}
	s.logSize = 0
	s.tailCount = 0
	return nil
}

// tailEvents reports how many events are waiting to be compacted.
func (s *Store) TailEvents() int { return s.tailCount }

func (s *Store) writeSnapshotCopy(f *board.File, seq int64) error {
	if err := os.MkdirAll(s.snapDir, 0o755); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.snapDir, fmt.Sprintf("%012d.json", seq)), f)
}

// writeAtomic marshals v to path via a temp file and rename.
func writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".kancli-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// changedOnDisk reports whether another process has written the log or
// snapshot since this store last read them.
func (s *Store) ChangedOnDisk() bool {
	if !s.Enabled() {
		return false
	}
	if s.needReload {
		return true
	}
	if info, err := os.Stat(s.logPath); err == nil {
		if info.Size() != s.logSize {
			return true
		}
	} else if s.logSize != 0 {
		return true
	}
	if info, err := os.Stat(s.path); err == nil {
		return !info.ModTime().Equal(s.snapMod)
	}
	return !s.snapMod.IsZero()
}
