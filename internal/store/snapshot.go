package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	snapshotFileName = "state.json"
	snapshotTempName = "state.json.tmp"
)

// Snapshot is the materialised state after applying every transaction up to
// and including Seq. Hash is the chain hash of that record, which ties the
// snapshot to one exact log history. Objects are the encoded objects.
type Snapshot struct {
	Seq     uint64            `json:"seq"`
	Hash    string            `json:"hash"`
	At      time.Time         `json:"at"`
	Objects []json.RawMessage `json:"objects"`
}

// WriteSnapshot replaces snapshots/state.json atomically: the data is written
// to a temporary file, synced, renamed over the old snapshot, and the
// directory is synced. A snapshot ahead of the log, or one whose hash does
// not match the log's last record when it claims the last seq, is rejected.
func (l *Log) WriteSnapshot(s *Snapshot) error {
	if s == nil {
		return errors.New("store: nil snapshot")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	if s.Seq > l.lastSeq {
		return fmt.Errorf("store: snapshot seq %d is ahead of the log (last seq %d)", s.Seq, l.lastSeq)
	}
	if s.Seq == l.lastSeq && s.Hash != l.lastHash {
		return fmt.Errorf("store: snapshot hash does not match record %d", s.Seq)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("store: encode snapshot: %w", err)
	}
	final := filepath.Join(l.snapDir, snapshotFileName)
	tmp := filepath.Join(l.snapDir, snapshotTempName)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return fmt.Errorf("store: create %s: %w", tmp, err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store: rename snapshot: %w", err)
	}
	return syncDir(l.snapDir)
}

// ReadSnapshot loads snapshots/state.json. It returns (nil, nil) when no
// snapshot exists and an error when the file cannot be read or decoded.
// A snapshot that is ahead of the log, or whose Hash is not the hash of the
// log record with its Seq, returns ErrSnapshotStale; the caller should then
// rebuild by replaying the log from the start.
func (l *Log) ReadSnapshot() (*Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	path := filepath.Join(l.snapDir, snapshotFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read snapshot: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("store: decode %s: %w", path, err)
	}
	if s.Seq > l.lastSeq {
		return nil, fmt.Errorf("%w: snapshot seq %d is ahead of the log (last seq %d)", ErrSnapshotStale, s.Seq, l.lastSeq)
	}
	if s.Seq == 0 {
		if s.Hash != "" {
			return nil, fmt.Errorf("%w: snapshot at seq 0 carries a hash", ErrSnapshotStale)
		}
		return &s, nil
	}
	hash, err := l.hashAt(s.Seq)
	if err != nil {
		return nil, err
	}
	if hash != s.Hash {
		return nil, fmt.Errorf("%w: snapshot hash does not match record %d", ErrSnapshotStale, s.Seq)
	}
	return &s, nil
}

// hashAt returns the chain hash of the record with the given seq by scanning
// the segment that holds it. seq must be between 1 and l.lastSeq.
func (l *Log) hashAt(seq uint64) (string, error) {
	seg := &l.segments[l.segmentIndex(seq)]
	expectSeq, expectPrev := seg.firstSeq, seg.prevHash
	var found string
	err := l.walkSegment(seg, &expectSeq, &expectPrev, func(rec *record, _ int) error {
		if rec.Seq == seq {
			found = rec.Hash
			return errStopWalk
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", corruptf(l.segPath(seg.name), 0, "record %d not found", seq)
	}
	return found, nil
}
