package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// compactAfter is how many events may accumulate in the tail log before
// the store folds them into a fresh snapshot.
const compactAfter = 500

// errStale is returned by compact when another process changed the log or
// snapshot since this store last read them. Callers reload and retry.
var errStale = errors.New("the board changed on disk; reload before writing a snapshot")

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
type store struct {
	path       string
	logPath    string
	archiveDir string
	snapDir    string
	lockPath   string
	actor      string

	logSize    int64     // bytes of the tail log this process has consumed
	tailCount  int       // events in the tail log
	snapMod    time.Time // snapshot modification time at the last load/save
	nextSeq    int64     // next event sequence number
	needReload bool      // another process appended events since our load

	mem []Event // event log for in-memory stores

	segments map[string]cachedSegment // archived segments already parsed
	walkers  map[string]*statsWalker  // incremental stats per board
}

// cachedSegment is a parsed archive file; archived segments never change,
// so the size check only guards against a file being replaced.
type cachedSegment struct {
	size   int64
	events []Event
}

func newStore(path string) *store {
	s := &store{path: path, actor: "ui"}
	if path != "" {
		base := strings.TrimSuffix(path, filepath.Ext(path))
		s.logPath = base + ".events.jsonl"
		s.archiveDir = base + ".events"
		s.snapDir = base + ".snapshots"
		s.lockPath = base + ".lock"
	}
	return s
}

// enabled reports whether the store writes to disk.
func (s *store) enabled() bool { return s != nil && s.path != "" }

// describe is shown in the header.
func (s *store) describe() string {
	if !s.enabled() {
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
func defaultStorePath() (string, error) {
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
func (s *store) lock() (func(), error) {
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
func (s *store) load() (*File, error) {
	if !s.enabled() {
		f := newFile()
		f.rec.actor = s.actor
		return f, nil
	}
	f, fresh, err := s.readSnapshot(s.path)
	if err != nil {
		return nil, err
	}
	f.rec.actor = s.actor

	events, size, err := readEventFile(s.logPath)
	if err != nil {
		return nil, err
	}
	if err := f.replay(events); err != nil {
		return nil, fmt.Errorf("replay %s: %w", s.logPath, err)
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

func hasTasks(f *File) bool {
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
func (s *store) readSnapshot(path string) (f *File, fresh bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.snapMod = time.Time{}
		return newFile(), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if info, err := os.Stat(path); err == nil && path == s.path {
		s.snapMod = info.ModTime()
	}
	f, err = decodeFile(data)
	if err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, f.LastSeq == 0 && f.SnapshotAt.IsZero(), nil
}

// emptyBase returns a copy of f with the same boards and columns but no
// tasks, sequence zero and no snapshot time.
func emptyBase(f *File) *File {
	empty := *f
	empty.Boards = nil
	for _, b := range f.Boards {
		nb := *b
		nb.Tasks = nil
		nb.NextID = 1
		empty.Boards = append(empty.Boards, &nb)
	}
	empty.LastSeq, empty.SnapshotAt = 0, time.Time{}
	empty.rec = nil
	return &empty
}

// bootstrap seeds the log for a board that predates it: an empty initial
// snapshot, one created event per task at its creation time, and moves or
// archives at the task's last update. It then compacts so the live
// snapshot carries the real state.
func (s *store) bootstrap(f *File) error {
	if err := s.writeSnapshotCopy(emptyBase(f), 0); err != nil {
		return err
	}
	var events []Event
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
			events = append(events, Event{At: t.CreatedAt, Board: b.ID, Kind: evTaskCreated, Task: t.ID, To: created.Column, Data: mustJSON(created), Actor: "bootstrap"})
			if t.Column != created.Column {
				events = append(events, Event{At: t.UpdatedAt, Board: b.ID, Kind: evTaskMoved, Task: t.ID, From: created.Column, To: t.Column, Actor: "bootstrap"})
			}
			if t.Archived() {
				events = append(events, Event{At: *t.ArchivedAt, Board: b.ID, Kind: evTaskArchived, Task: t.ID, From: t.Column, Actor: "bootstrap"})
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	if err := s.append(events); err != nil {
		return err
	}
	return s.compact(f)
}

// loadAsOf reconstructs the state at a point in time from the newest
// snapshot before it plus the events up to it.
func (s *store) loadAsOf(t time.Time) (*File, error) {
	if !s.enabled() {
		f := newFile()
		return f, nil
	}
	snapshots, _ := filepath.Glob(filepath.Join(s.snapDir, "*.json"))
	sort.Strings(snapshots)
	var base *File
	for i := len(snapshots) - 1; i >= 0; i-- {
		f, _, err := s.readSnapshot(snapshots[i])
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
		return nil, fmt.Errorf("no snapshot from before %s", t.Format(dateLayout))
	}
	events, err := s.events()
	if err != nil {
		return nil, err
	}
	var upTo []Event
	for _, e := range events {
		if !e.At.After(t) {
			upTo = append(upTo, e)
		}
	}
	if err := base.replay(upTo); err != nil {
		return nil, err
	}
	base.rec.muted = true
	return base, nil
}

// --- events ------------------------------------------------------------------

// readEventFile parses a JSONL file. A torn last line (from a crash mid
// write) is dropped; any other damage is an error.
func readEventFile(path string) ([]Event, int64, error) {
	fh, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return nil, 0, err
	}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	var events []Event
	var consumed int64
	var pendingErr error
	for sc.Scan() {
		line := sc.Bytes()
		lineLen := int64(len(line)) + 1
		if len(strings.TrimSpace(string(line))) == 0 {
			consumed += lineLen
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			pendingErr = fmt.Errorf("%s: bad event line after seq %d: %w", path, lastSeq(events), err)
			break
		}
		events = append(events, e)
		consumed += lineLen
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}
	if pendingErr != nil && consumed+int64(len(sc.Bytes()))+1 < info.Size() {
		// Damage in the middle of the file, not just a torn tail.
		return nil, 0, pendingErr
	}
	if consumed > info.Size() {
		consumed = info.Size()
	}
	return events, consumed, nil
}

func lastSeq(events []Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

// events returns the complete history: archived segments then the tail.
func (s *store) events() ([]Event, error) {
	if !s.enabled() {
		return append([]Event(nil), s.mem...), nil
	}
	segments, _ := filepath.Glob(filepath.Join(s.archiveDir, "*.jsonl"))
	sort.Strings(segments)
	if s.segments == nil {
		s.segments = map[string]cachedSegment{}
	}
	var all []Event
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
func (s *store) boardStats(b *Board, now time.Time, days int) (boardStats, error) {
	events, err := s.events()
	if err != nil {
		return boardStats{}, err
	}
	if s.walkers == nil {
		s.walkers = map[string]*statsWalker{}
	}
	w := s.walkers[b.ID]
	if w == nil || !w.compatible(b) || (len(events) > 0 && events[len(events)-1].Seq < w.seq) {
		w = newStatsWalker(b)
		s.walkers[b.ID] = w
	}
	w.feed(events)
	return w.finish(b, now, days), nil
}

// append writes events to the tail log under the lock and numbers them.
func (s *store) append(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	if !s.enabled() {
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
func (s *store) save(f *File) error {
	if err := s.append(f.pending()); err != nil {
		return err
	}
	if !s.enabled() {
		return nil
	}
	// The first save writes the base snapshot the log replays onto; later
	// saves compact once the tail has grown.
	if !exists(s.path) || (s.tailCount >= compactAfter && !s.needReload) {
		return s.compact(f)
	}
	return nil
}

// compact writes a fresh snapshot and moves the tail log into the archive.
func (s *store) compact(f *File) error {
	if !s.enabled() {
		return nil
	}
	if err := s.append(f.pending()); err != nil {
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
		return errStale
	}
	if info, err := os.Stat(s.logPath); err == nil {
		if info.Size() != s.logSize {
			s.needReload = true
			return errStale
		}
	} else if s.logSize != 0 {
		s.needReload = true
		return errStale
	}
	if _, mod := snapshotSeq(s.path); !mod.IsZero() && !s.snapMod.IsZero() && !mod.Equal(s.snapMod) {
		s.needReload = true
		return errStale
	}

	// The very first snapshot is preceded by an empty base so that "as of"
	// views can replay the whole history from the beginning.
	if snaps, _ := filepath.Glob(filepath.Join(s.snapDir, "*.json")); len(snaps) == 0 {
		if err := s.writeSnapshotCopy(emptyBase(f), 0); err != nil {
			return err
		}
	}
	f.LastSeq = s.nextSeq - 1
	f.SnapshotAt = timeNow()
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
func (s *store) tailEvents() int { return s.tailCount }

func (s *store) writeSnapshotCopy(f *File, seq int64) error {
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
func (s *store) changedOnDisk() bool {
	if !s.enabled() {
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

// --- decoding ----------------------------------------------------------------

// decodeFile parses any supported file version.
func decodeFile(data []byte) (*File, error) {
	var probe struct {
		Version int             `json:"version"`
		Boards  json.RawMessage `json:"boards"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch {
	case probe.Version > fileVersion:
		return nil, fmt.Errorf("file was written by a newer kancli (version %d)", probe.Version)
	case probe.Version <= 1 && len(probe.Boards) == 0:
		// The original single-board format (or a file with no boards at all).
		return migrateV1(data)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	normalizeFile(&f)
	return &f, nil
}

// normalizeFile repairs anything a hand-edited file may be missing.
func normalizeFile(f *File) {
	f.Version = fileVersion
	boards := f.Boards[:0]
	for _, b := range f.Boards {
		if b != nil {
			boards = append(boards, b)
		}
	}
	f.Boards = boards
	if len(f.Boards) == 0 {
		f.Boards = append(f.Boards, newBoard("Main"))
	}
	seenBoards := map[string]bool{}
	for _, b := range f.Boards {
		if b.Name == "" {
			b.Name = "Board"
		}
		if b.ID == "" {
			b.ID = slug(b.Name)
		}
		for seenBoards[b.ID] {
			b.ID += "_2"
		}
		seenBoards[b.ID] = true
		normalizeBoard(b)
	}
	if f.Board(f.ActiveBoard) == nil {
		f.ActiveBoard = f.Boards[0].ID
	}
	f.attach()
}

func normalizeBoard(b *Board) {
	if len(b.Columns) == 0 {
		b.Columns = defaultColumns()
	}
	seenCols := map[string]bool{}
	for i := range b.Columns {
		c := &b.Columns[i]
		if c.Name == "" {
			c.Name = "Column"
		}
		if c.ID == "" {
			c.ID = slug(c.Name)
		}
		for seenCols[c.ID] {
			c.ID += "_2"
		}
		seenCols[c.ID] = true
		if c.Color == "" {
			c.Color = columnPalette[i%len(columnPalette)]
		}
	}
	seenTasks := map[int]bool{}
	maxID := 0
	now := timeNow()
	for i := range b.Tasks {
		t := &b.Tasks[i]
		if t.ID <= 0 || seenTasks[t.ID] {
			t.ID = 0 // assigned below once we know the max
		} else {
			seenTasks[t.ID] = true
			maxID = max(maxID, t.ID)
		}
		if b.Column(t.Column) == nil {
			t.Column = b.Columns[0].ID
		} else {
			t.Column = b.Column(t.Column).ID
		}
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now
		}
		if t.UpdatedAt.IsZero() {
			t.UpdatedAt = t.CreatedAt
		}
		t.Labels = normalizeLabels(t.Labels)
		if t.Due != "" {
			if d, err := parseDue(t.Due, now); err == nil {
				t.Due = d
			} else {
				t.Due = ""
			}
		}
	}
	for i := range b.Tasks {
		if b.Tasks[i].ID == 0 {
			maxID++
			b.Tasks[i].ID = maxID
		}
	}
	if b.NextID <= maxID {
		b.NextID = maxID + 1
	}
	b.touch()
}

// migrateV1 converts the original single-board format, where each task had
// a string id and a status of todo/in_progress/done.
func migrateV1(data []byte) (*File, error) {
	var old struct {
		Tasks []struct {
			Status      string    `json:"status"`
			Title       string    `json:"title"`
			Description string    `json:"description"`
			CreatedAt   time.Time `json:"created_at"`
			UpdatedAt   time.Time `json:"updated_at"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}
	f := newFile()
	b := f.Boards[0]
	for _, ot := range old.Tasks {
		col := ot.Status
		if b.Column(col) == nil {
			col = b.Columns[0].ID
		}
		t := Task{
			ID:          b.NextID,
			Column:      col,
			Title:       ot.Title,
			Description: ot.Description,
			CreatedAt:   ot.CreatedAt,
			UpdatedAt:   ot.UpdatedAt,
		}
		b.NextID++
		b.Tasks = append(b.Tasks, t)
	}
	normalizeFile(f)
	return f, nil
}
