package store

// Readers for the file-store layout kancli used before board.db. Only the
// importer (and the compatibility tests) use them; nothing here writes.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/SabienNguyen/kancli/internal/board"
)

// maxEventLine bounds one event line; undo events carry a whole board.
const maxEventLine = 32 << 20

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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
