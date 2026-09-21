// Package memory is a Go port of OptMem's `memo` tool: a permanent,
// append-only memory for AI agents, byte-compatible with its on-disk
// format (github.com/VictorTaelin/OptMem).
//
// Records are FIXED WIDTH, so a memory or a block is found by seeking to its
// offset -- no scanning, no index file to keep in sync. Position IS identity:
// memory i lives at i*LogRec of LOG.txt, and block [k*s,(k+1)*s) lives at
// k*TreeRec of TREE/<s>.
package memory

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andrewmautone/claudewhats/internal/config"
)

// ToolName is how the tool names itself in every command it prints, so that
// the printed command runs as written.
const ToolName = "claudewhats memory"

const (
	LogRec     = 320   // bytes per LOG.txt record
	TreeRec    = 288   // bytes per TREE/<size> record
	EntryChars = 280   // the longest one memory may be, in bytes
	WakeLines  = 96    // the memory context: how many lines wake prints
	RawMax     = 16    // blocks up to this many memories compress from the raw log
	PartChars  = 20000 // output paging: largest part, in bytes
	PartLines  = 500   // output paging: largest part, in lines
)

// Entry is one memory: its position in the log, the day it was noted and
// its text.
type Entry struct {
	ID   int
	Date string
	Text string
}

// String renders the entry as wake prints it.
func (e Entry) String() string {
	return fmt.Sprintf("#%d %s %s", e.ID, e.Date, e.Text)
}

// Store is one memory directory.
type Store struct {
	Dir string
}

// DefaultDir is where the memory lives: $CLAUDEWHATS_MEMORY_DIR, or
// <home>/memory.
func DefaultDir() string {
	if d := os.Getenv("CLAUDEWHATS_MEMORY_DIR"); d != "" {
		return d
	}
	return filepath.Join(config.Home(), "memory")
}

// Open opens an existing memory. The directory is only ever created by
// Create: creating it IS creating the identity, and that is a deliberate
// act. If Open created it, a typo in the dir would silently open an empty
// store, and the agent would wake with no past and write a second identity.
func Open(dir string) (*Store, error) {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("No memory at %s.\nTo create one, run: %s init\nTo use an existing one, point CLAUDEWHATS_MEMORY_DIR at it.", dir, ToolName)
	}
	return open(dir)
}

// Create makes the memory directory if it is missing and opens it. Safe to
// re-run: it only ever creates what is missing. The bool says whether the
// directory was fresh.
func Create(dir string) (*Store, bool, error) {
	_, err := os.Stat(dir)
	fresh := err != nil
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	s, err := open(dir)
	return s, fresh, err
}

func open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "TREE"), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "LOG.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	return &Store{Dir: dir}, nil
}

func (s *Store) logPath() string { return filepath.Join(s.Dir, "LOG.txt") }

func (s *Store) treePath(size int) string {
	return filepath.Join(s.Dir, "TREE", strconv.Itoa(size))
}

// count is how many whole records a file holds; 0 if it does not exist.
func count(path string, rec int) (int, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err // any other failure is real and must surface
	}
	return int(fi.Size() / int64(rec)), nil
}

// LogLen is how many memories the log holds: one stat.
func (s *Store) LogLen() (int, error) {
	return count(s.logPath(), LogRec)
}

// repair drops a partial trailing record left by a crash. It was never
// acknowledged. Without this the next append lands at a wrong offset and
// every later record is misaligned. Callers hold the lock.
func repair(path string, rec int) error {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	n := fi.Size()
	if n%int64(rec) != 0 {
		return os.Truncate(path, n-n%int64(rec))
	}
	return nil
}

// Parse decodes one log record. ok is false when the record is not of the
// form "#<id> <date> <text>".
func Parse(rec []byte) (Entry, bool) {
	line := string(bytes.TrimRight(rec, " \n"))
	head, rest, _ := strings.Cut(line, " ")
	date, text, _ := strings.Cut(rest, " ")
	if len(head) < 2 || head[0] != '#' {
		return Entry{}, false
	}
	id, err := strconv.Atoi(head[1:])
	if err != nil || id < 0 {
		return Entry{}, false
	}
	return Entry{ID: id, Date: date, Text: text}, true
}

// records decodes a run of log records. They are sliced as BYTES and decoded
// one by one -- slicing decoded text would shift every boundary after the
// first multi-byte character.
func records(buf []byte) []Entry {
	out := make([]Entry, 0, len(buf)/LogRec)
	for i := 0; i+LogRec <= len(buf); i += LogRec {
		e, _ := Parse(buf[i : i+LogRec])
		out = append(out, e)
	}
	return out
}

// reader reads the log and the tree levels, opening each file at most once
// for as long as it lives: a wake reads ~WakeLines blocks, and an open per
// block costs more than the read.
type reader struct {
	s    *Store
	log  *os.File
	tree map[int]*os.File // level size -> file; nil once known missing
}

func (s *Store) reader() *reader {
	return &reader{s: s, tree: map[int]*os.File{}}
}

func (r *reader) close() {
	if r.log != nil {
		r.log.Close()
	}
	for _, f := range r.tree {
		if f != nil {
			f.Close()
		}
	}
}

// logSlice returns memories [lo,hi) in one read.
func (r *reader) logSlice(lo, hi int) ([]Entry, error) {
	if hi <= lo {
		return nil, nil
	}
	if r.log == nil {
		f, err := os.Open(r.s.logPath())
		if err != nil {
			return nil, err
		}
		r.log = f
	}
	buf := make([]byte, (hi-lo)*LogRec)
	n, err := r.log.ReadAt(buf, int64(lo)*LogRec)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return records(buf[:n]), nil
}

// treeGet returns the summary of block [lo,hi), in one seek. ok is false if
// it is not built yet, or blank.
func (r *reader) treeGet(lo, hi int) (string, bool, error) {
	size := hi - lo
	f, seen := r.tree[size]
	if !seen {
		var err error
		f, err = os.Open(r.s.treePath(size))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) { // any other failure is real
				return "", false, err
			}
			f = nil // not built yet
		}
		r.tree[size] = f
	}
	if f == nil {
		return "", false, nil
	}
	rec := make([]byte, TreeRec)
	n, err := f.ReadAt(rec, int64(lo/size)*TreeRec)
	if err != nil && err != io.EOF {
		return "", false, err
	}
	rec = rec[:n]
	if !utf8.Valid(rec) {
		return "", false, fmt.Errorf("The summary of #%d-%d is corrupt. Run: %s forget %d-%d", lo, hi-1, ToolName, lo, hi-1)
	}
	text := string(bytes.TrimRight(rec, " \n"))
	return text, text != "", nil
}

// LogSlice returns memories [lo,hi) in one read.
func (s *Store) LogSlice(lo, hi int) ([]Entry, error) {
	r := s.reader()
	defer r.close()
	return r.logSlice(lo, hi)
}

// LogGet returns memory i, in one seek.
func (s *Store) LogGet(i int) (Entry, error) {
	es, err := s.LogSlice(i, i+1)
	if err != nil {
		return Entry{}, err
	}
	if len(es) == 0 {
		return Entry{}, fmt.Errorf("no memory #%d", i)
	}
	return es[0], nil
}

// TreeGet returns the summary of block [lo,hi), in one seek. ok is false if
// it is not built yet, or blank.
func (s *Store) TreeGet(lo, hi int) (string, bool, error) {
	r := s.reader()
	defer r.close()
	return r.treeGet(lo, hi)
}

// pad lays text into one fixed-width record: the text, spaces, a newline.
func pad(text string, rec int) ([]byte, error) {
	if len(text) > rec-1 {
		return nil, fmt.Errorf("Too long: %d bytes. The record holds %d.", len(text), rec-1)
	}
	out := make([]byte, rec)
	copy(out, text)
	for i := len(text); i < rec-1; i++ {
		out[i] = ' '
	}
	out[rec-1] = '\n'
	return out, nil
}

// Pad is pad for callers that already checked the length: a text longer
// than the record is a programming error and panics.
func Pad(text string, rec int) []byte {
	b, err := pad(text, rec)
	if err != nil {
		panic(err)
	}
	return b
}

// Lock takes the store's exclusive write lock; unlock releases it.
func (s *Store) Lock() (unlock func(), err error) {
	// Open for append, NOT truncating: reopening with O_TRUNC would break
	// advisory locks held by other processes on Windows.
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unlockFile(f)
		f.Close()
	}, nil
}

func today() string { return time.Now().Format("2006-01-02") }

// LogAppend appends memories dated today. The only way LOG.txt ever changes.
// Ids are assigned INSIDE the lock: two sessions noting at the same moment
// must not be handed the same id. Returns the first id used.
func (s *Store) LogAppend(texts []string) (int, error) {
	items := make([]Entry, len(texts))
	for i, t := range texts {
		items[i] = Entry{Date: today(), Text: t}
	}
	return s.appendDated(items)
}

func (s *Store) appendDated(items []Entry) (int, error) {
	unlock, err := s.Lock()
	if err != nil {
		return 0, err
	}
	defer unlock()
	if err := repair(s.logPath(), LogRec); err != nil {
		return 0, err
	}
	base, err := s.LogLen()
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 0, len(items)*LogRec)
	for k, e := range items {
		rec, err := pad(fmt.Sprintf("#%d %s %s", base+k, e.Date, e.Text), LogRec)
		if err != nil {
			return 0, err
		}
		buf = append(buf, rec...)
	}
	if err := appendSync(s.logPath(), buf); err != nil {
		return 0, err
	}
	return base, nil
}

func appendSync(path string, buf []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// TreePut writes block [lo,hi). Blocks are built in order, so this only ever
// appends one record to one level file. False if the block's slot is not the
// next one at its level.
func (s *Store) TreePut(lo, hi int, text string) (bool, error) {
	size := hi - lo
	unlock, err := s.Lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	p := s.treePath(size)
	if err := repair(p, TreeRec); err != nil {
		return false, err
	}
	n, err := count(p, TreeRec)
	if err != nil {
		return false, err
	}
	if n != lo/size {
		return false, nil
	}
	rec, err := pad(text, TreeRec)
	if err != nil {
		return false, err
	}
	if err := appendSync(p, rec); err != nil {
		return false, err
	}
	return true, nil
}

// TreeDrop forgets block [lo,hi) and every block built from it, by
// truncating each level back to that point. Later blocks at those levels go
// too and are rebuilt; the log is never touched, so nothing is lost. Returns
// how many blocks went; the first of them is always [lo,hi).
func (s *Store) TreeDrop(lo, hi int) (int, error) {
	gone, size := 0, hi-lo
	unlock, err := s.Lock()
	if err != nil {
		return 0, err
	}
	defer unlock()
	T, err := s.LogLen()
	if err != nil {
		return 0, err
	}
	for size <= T {
		p, k := s.treePath(size), lo/size
		n, err := count(p, TreeRec)
		if err != nil {
			return gone, err
		}
		if n > k {
			gone += n - k
			if err := os.Truncate(p, int64(k)*TreeRec); err != nil {
				return gone, err
			}
		}
		size *= 2
	}
	return gone, nil
}

// clean validates one memory and returns it stripped, as the python `check`
// does.
func clean(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("Empty. A memory is one line of text.")
	}
	if strings.ContainsAny(text, "\n\r") {
		return "", fmt.Errorf("%d lines. A memory is one line: merge them, or note them separately.", strings.Count(text, "\n")+1)
	}
	if n := len(text); n > EntryChars {
		return "", fmt.Errorf("Too long: %d bytes, limit %d. Accented characters cost 2 bytes. Compress it further.", n, EntryChars)
	}
	return text, nil
}

// Check reports why text cannot be one memory: empty, several lines, or over
// EntryChars bytes.
func Check(text string) error {
	_, err := clean(text)
	return err
}
