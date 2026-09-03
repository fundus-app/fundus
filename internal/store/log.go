// Package store implements Fundus's on-disk persistence: an append-only,
// hash-chained event log of transactions and a JSON snapshot for fast start.
//
// Layout under the data directory passed to Open:
//
//	events/<first seq, 12 digits>.jsonl   log segments, one JSON record per line
//	snapshots/state.json                  latest snapshot, written atomically
//
// Each log record is one line of the form
//
//	{"seq":N,"prev":"<hex>","hash":"<hex>","txn":{...}}
//
// where hash is the SHA-256 of prev + "\n" + the exact bytes of the txn
// member, and prev is the hash of the previous record ("" for seq 1). The
// chain makes every record depend on all records before it, so tampering,
// reordering and gaps are detected on Open and on Replay.
//
// The log is canonical. Snapshots are derived and always reconstructible by
// replaying the log; ReadSnapshot rejects snapshots that no longer match it.
// The log is never rewritten: Open only ever removes a damaged tail of the
// last segment (after copying it aside) and refuses to start when corruption
// sits anywhere else.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/model"
)

var (
	// ErrCorrupt is wrapped by errors that report damage which Open cannot
	// repair safely. The message names the file and line.
	ErrCorrupt = errors.New("event log corrupt")
	// ErrSnapshotStale is returned by ReadSnapshot when the snapshot on disk
	// does not match the log; the caller should replay from scratch.
	ErrSnapshotStale = errors.New("snapshot stale")
	// ErrClosed is returned by every method after Close.
	ErrClosed = errors.New("event log closed")
)

// segmentMaxBytes is the size beyond which Append starts a new segment. It is
// a variable so tests can force rotation with small inputs.
var segmentMaxBytes int64 = 16 << 20

const (
	eventsDirName    = "events"
	snapshotsDirName = "snapshots"
	dirPerm          = 0o700
	filePerm         = 0o600
)

var segmentNameRe = regexp.MustCompile(`^[0-9]{12}\.jsonl$`)

// segment is one file of the log. prevHash is the chain value the first
// record of the segment must carry as "prev"; it lets Replay start at any
// segment with full verification.
type segment struct {
	name     string
	firstSeq uint64
	prevHash string
	size     int64
}

// Log is an open event log. All methods are safe for concurrent use.
type Log struct {
	eventsDir string
	snapDir   string

	mu       sync.Mutex
	segments []segment
	lock     *os.File // exclusive lock on <dir>/LOCK
	f        *os.File // last segment open for append; nil until the first append on an empty log
	lastSeq  uint64
	lastHash string
	closed   bool
	failed   error // sticky: set when the on-disk state may no longer match memory
}

// Recovery describes what Open had to do to make the log consistent.
type Recovery struct {
	TruncatedFile string // segment whose damaged tail was cut
	DroppedBytes  int64  // number of bytes removed from its end
	CorruptCopy   string // file holding the removed bytes (<segment>.corrupt-<unix ts>)
	Line          int    // line number of the first damaged line
	Reason        string // what was wrong with it
}

// Open opens or creates the log under dir and validates every record.
//
// Damage confined to the tail of the last segment (a truncated, unparseable
// or hash-mismatched final line, or trailing garbage) is repaired: the bad
// bytes are copied to a .corrupt-<ts> file, the segment is truncated to the
// last good line, and a non-nil Recovery is returned. Any other problem (a
// bad record followed by good ones, a seq gap, a chain break, a missing
// segment) returns an error wrapping ErrCorrupt and nothing is changed.
func Open(dir string) (*Log, *Recovery, error) {
	l := &Log{
		eventsDir: filepath.Join(dir, eventsDirName),
		snapDir:   filepath.Join(dir, snapshotsDirName),
	}
	for _, d := range []string{dir, l.eventsDir, l.snapDir} {
		if err := os.MkdirAll(d, dirPerm); err != nil {
			return nil, nil, fmt.Errorf("store: create %s: %w", d, err)
		}
	}
	lock, err := lockDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}
	l.lock = lock
	ok := false
	defer func() {
		if !ok {
			_ = unlockDir(lock)
		}
	}()
	names, err := listSegments(l.eventsDir)
	if err != nil {
		return nil, nil, err
	}
	var rec *Recovery
	for i, name := range names {
		first, _ := strconv.ParseUint(name[:12], 10, 64)
		if first != l.lastSeq+1 {
			return nil, nil, corruptf(l.segPath(name), 0, "segment starts at seq %d, expected %d", first, l.lastSeq+1)
		}
		seg := segment{name: name, firstSeq: first, prevHash: l.lastHash}
		r, err := l.validateSegment(&seg, i == len(names)-1)
		if err != nil {
			return nil, nil, err
		}
		if r != nil {
			rec = r
		}
		l.segments = append(l.segments, seg)
	}
	if n := len(l.segments); n > 0 {
		f, err := os.OpenFile(l.segPath(l.segments[n-1].name), os.O_WRONLY|os.O_APPEND, filePerm)
		if err != nil {
			return nil, nil, fmt.Errorf("store: open segment for append: %w", err)
		}
		l.f = f
	}
	ok = true
	return l, rec, nil
}

// listSegments returns the segment file names in dir in ascending order.
// Names are zero padded, so lexical order is numeric order.
func listSegments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("store: list %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && segmentNameRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// validateSegment reads one segment, verifying every record against the
// chain, and advances l.lastSeq/l.lastHash past its last good record. For the
// last segment it repairs tail damage as documented on Open.
func (l *Log) validateSegment(seg *segment, last bool) (*Recovery, error) {
	path := l.segPath(seg.name)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	defer f.Close()

	expectSeq, expectPrev := seg.firstSeq, seg.prevHash
	var goodEnd int64
	var damage *Recovery
	lr := newLineReader(f)
	for {
		ln, ok, err := lr.next()
		if err != nil {
			return nil, fmt.Errorf("store: read %s: %w", path, err)
		}
		if !ok {
			break
		}
		if damage != nil {
			// Past the first damaged line. A self-consistent record here means
			// the damage is not confined to the tail; refuse to drop it.
			if _, perr := parseRecord(ln.data); perr == nil {
				return nil, corruptf(path, damage.Line, "%s, followed by a valid record at line %d", damage.Reason, ln.no)
			}
			continue
		}
		var reason string
		if !ln.terminated {
			reason = "truncated line (missing newline)"
		} else if rec, perr := parseRecord(ln.data); perr != nil {
			reason = perr.Error()
		} else if creason := chainCheck(&rec, expectSeq, expectPrev); creason != "" {
			// A well-formed record in the wrong place is never tail damage
			// from an interrupted write; it must not be dropped.
			return nil, corruptf(path, ln.no, "%s", creason)
		} else {
			expectSeq, expectPrev, goodEnd = rec.Seq+1, rec.Hash, ln.end
			continue
		}
		if !last {
			return nil, corruptf(path, ln.no, "%s", reason)
		}
		damage = &Recovery{TruncatedFile: path, Line: ln.no, Reason: reason}
	}
	l.lastSeq, l.lastHash = expectSeq-1, expectPrev
	seg.size = goodEnd
	if damage == nil {
		return nil, nil
	}
	if err := l.cutTail(f, path, goodEnd, damage); err != nil {
		return nil, err
	}
	return damage, nil
}

// cutTail copies everything after goodEnd to a .corrupt file, then truncates
// the segment to goodEnd. The copy is durable before the segment is touched.
func (l *Log) cutTail(f *os.File, path string, goodEnd int64, r *Recovery) error {
	copyPath := corruptCopyPath(path)
	cf, err := os.OpenFile(copyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return fmt.Errorf("store: create %s: %w", copyPath, err)
	}
	if _, err := f.Seek(goodEnd, io.SeekStart); err != nil {
		cf.Close()
		return fmt.Errorf("store: seek %s: %w", path, err)
	}
	n, err := io.Copy(cf, f)
	if err == nil {
		err = cf.Sync()
	}
	if cerr := cf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("store: write %s: %w", copyPath, err)
	}
	wf, err := os.OpenFile(path, os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("store: open %s: %w", path, err)
	}
	err = wf.Truncate(goodEnd)
	if err == nil {
		err = wf.Sync()
	}
	if cerr := wf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("store: truncate %s: %w", path, err)
	}
	if err := syncDir(l.eventsDir); err != nil {
		return err
	}
	r.DroppedBytes = n
	r.CorruptCopy = copyPath
	return nil
}

func corruptCopyPath(segPath string) string {
	base := fmt.Sprintf("%s.corrupt-%d", segPath, time.Now().Unix())
	p := base
	for i := 1; ; i++ {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
		p = fmt.Sprintf("%s-%d", base, i)
	}
}

// Append assigns the next sequence number to txn, writes it durably and
// advances the chain. txn.Seq is overwritten; any incoming value is ignored.
func (l *Log) Append(txn *model.Txn) error {
	if txn == nil {
		return errors.New("store: nil transaction")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	if l.failed != nil {
		return fmt.Errorf("store: log disabled after write failure: %w", l.failed)
	}
	seq := l.lastSeq + 1
	txn.Seq = seq
	body, err := json.Marshal(txn)
	if err != nil {
		return fmt.Errorf("store: encode txn: %w", err)
	}
	hash := recordHash(l.lastHash, body)
	line := encodeRecord(seq, l.lastHash, hash, body)

	if err := l.ensureSegment(seq); err != nil {
		return err
	}
	cur := l.current()
	n, err := l.f.Write(line)
	if err != nil || n != len(line) {
		if err == nil {
			err = io.ErrShortWrite
		}
		// Roll the partial write back so the next append does not land
		// behind garbage. If that fails the file no longer matches memory.
		if terr := l.f.Truncate(cur.size); terr != nil {
			l.failed = fmt.Errorf("%v; rollback failed: %v", err, terr)
		}
		return fmt.Errorf("store: append seq %d: %w", seq, err)
	}
	if err := l.f.Sync(); err != nil {
		// After a failed fsync the kernel may have discarded the dirty pages
		// without ever reporting it again, so nothing further can be trusted.
		l.failed = err
		return fmt.Errorf("store: sync seq %d: %w", seq, err)
	}
	cur.size += int64(len(line))
	l.lastSeq, l.lastHash = seq, hash
	return nil
}

// ensureSegment makes sure l.f points at a segment that may take the record
// with the given seq, creating the first segment or rotating as needed.
func (l *Log) ensureSegment(seq uint64) error {
	if l.f != nil && l.current().size <= segmentMaxBytes {
		return nil
	}
	name := segmentName(seq)
	path := l.segPath(name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("store: create segment: %w", err)
	}
	if err := syncDir(l.eventsDir); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if l.f != nil {
		_ = l.f.Close() // every write to it has already been synced
	}
	l.f = f
	l.segments = append(l.segments, segment{name: name, firstSeq: seq, prevHash: l.lastHash})
	return nil
}

func (l *Log) current() *segment { return &l.segments[len(l.segments)-1] }

// Replay streams every transaction with seq >= fromSeq to fn in order,
// re-verifying the hash chain from disk as it goes. It stops at the first
// error from fn and returns it unchanged. fn must not call back into the Log.
func (l *Log) Replay(fromSeq uint64, fn func(*model.Txn) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	if fromSeq > l.lastSeq {
		return nil
	}
	start := l.segmentIndex(fromSeq)
	expectSeq, expectPrev := l.segments[start].firstSeq, l.segments[start].prevHash
	for i := start; i < len(l.segments); i++ {
		seg := &l.segments[i]
		err := l.walkSegment(seg, &expectSeq, &expectPrev, func(rec *record, lineNo int) error {
			if rec.Seq < fromSeq {
				return nil
			}
			var txn model.Txn
			if err := json.Unmarshal(rec.Txn, &txn); err != nil {
				return corruptf(l.segPath(seg.name), lineNo, "decode txn: %v", err)
			}
			if txn.Seq != rec.Seq {
				return corruptf(l.segPath(seg.name), lineNo, "txn seq %d does not match record seq %d", txn.Seq, rec.Seq)
			}
			return fn(&txn)
		})
		if err != nil {
			return err
		}
		want := l.lastSeq + 1
		if i+1 < len(l.segments) {
			want = l.segments[i+1].firstSeq
		}
		if expectSeq != want {
			return corruptf(l.segPath(seg.name), 0, "segment ends at seq %d, expected %d", expectSeq-1, want-1)
		}
	}
	return nil
}

// segmentIndex returns the index of the segment holding seq. seq must be at
// least 1 and the log must not be empty.
func (l *Log) segmentIndex(seq uint64) int {
	i := sort.Search(len(l.segments), func(i int) bool { return l.segments[i].firstSeq > seq })
	if i == 0 {
		return 0
	}
	return i - 1
}

// LastSeq returns the sequence number of the last durable transaction, 0 for
// an empty log.
func (l *Log) LastSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq
}

// LastHash returns the chain hash of the last durable transaction, "" for an
// empty log.
func (l *Log) LastHash() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastHash
}

// Stats reports the number of segment files and their total size in bytes.
func (l *Log) Stats() (segments int, bytes int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.segments {
		bytes += s.size
	}
	return len(l.segments), bytes
}

// Close releases the segment file. Further calls on l return ErrClosed.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	var err error
	if l.f != nil {
		err = l.f.Close()
		l.f = nil
	}
	if uerr := unlockDir(l.lock); err == nil {
		err = uerr
	}
	l.lock = nil
	return err
}

func (l *Log) segPath(name string) string { return filepath.Join(l.eventsDir, name) }

func segmentName(firstSeq uint64) string { return fmt.Sprintf("%012d.jsonl", firstSeq) }

// corruptf builds an error wrapping ErrCorrupt that names the file and, when
// line is positive, the line number.
func corruptf(path string, line int, format string, args ...any) error {
	where := path
	if line > 0 {
		where = fmt.Sprintf("%s line %d", path, line)
	}
	return fmt.Errorf("%w: %s: %s", ErrCorrupt, where, fmt.Sprintf(format, args...))
}

// syncDir flushes a directory so that entries created or removed in it are
// durable.
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("store: open dir %s: %w", path, err)
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("store: sync dir %s: %w", path, err)
	}
	return nil
}

// Files lists the on-disk files of the log and snapshot directories (absolute
// paths), for backups taken while the writer lock is held.
func (l *Log) Files() ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, dir := range []string{l.eventsDir, l.snapDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Type().IsRegular() {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	return out, nil
}

// errStop ends a Replay early.
var errStop = errors.New("stop")

// ReadTxn returns the transaction with the given seq from disk.
func (l *Log) ReadTxn(seq uint64) (*model.Txn, error) {
	var found *model.Txn
	err := l.Replay(seq, func(t *model.Txn) error {
		found = t
		return errStop
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}
	if found == nil || found.Seq != seq {
		return nil, fmt.Errorf("store: txn %d not found", seq)
	}
	return found, nil
}
