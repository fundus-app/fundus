package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fundus-app/fundus/internal/model"
)

// ---------------------------------------------------------------------------
// helpers

// mkTxn builds a transaction with enough variety to exercise encoding: a
// pointer field, HTML-sensitive characters, a nil raw message and nested ops.
func mkTxn(i int) *model.Txn {
	title := fmt.Sprintf("Note <%d> & “friends”", i)
	id := fmt.Sprintf("note-%d", i)
	return &model.Txn{
		ID:    fmt.Sprintf("txn-%03d", i),
		At:    time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second),
		Actor: "user:cli",
		Cause: &model.Cause{Kind: "user"},
		Ops: []model.Op{{
			Op:       "note.create",
			ID:       id,
			Title:    &title,
			Markdown: strings.Repeat("body text ", 5),
			Topics:   []string{"t1", "t2"},
		}},
		Before:  map[string]json.RawMessage{id: nil},
		Touched: []string{id},
		Summary: fmt.Sprintf("summary %d", i),
	}
}

type wantRec struct {
	seq  uint64
	json []byte
	hash string
}

func openOK(t *testing.T, dir string) *Log {
	t.Helper()
	l, rec, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if rec != nil {
		t.Fatalf("Open: unexpected recovery %+v", rec)
	}
	return l
}

// fill appends txns numbered from..to (inclusive) and records what was
// written: the marshalled txn after Append assigned its seq, and the chain
// hash after that append.
func fill(t *testing.T, l *Log, from, to int) []wantRec {
	t.Helper()
	var out []wantRec
	for i := from; i <= to; i++ {
		txn := mkTxn(i)
		txn.Seq = 999999 // must be ignored
		if err := l.Append(txn); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		b, err := json.Marshal(txn)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, wantRec{seq: txn.Seq, json: b, hash: l.LastHash()})
	}
	return out
}

func replayAll(t *testing.T, l *Log, from uint64) []*model.Txn {
	t.Helper()
	var got []*model.Txn
	err := l.Replay(from, func(txn *model.Txn) error {
		got = append(got, txn)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay(%d): %v", from, err)
	}
	return got
}

func assertReplayMatches(t *testing.T, l *Log, want []wantRec) {
	t.Helper()
	got := replayAll(t, l, 1)
	if len(got) != len(want) {
		t.Fatalf("replayed %d txns, want %d", len(got), len(want))
	}
	for i, txn := range got {
		if txn.Seq != want[i].seq {
			t.Fatalf("txn %d: seq %d, want %d", i, txn.Seq, want[i].seq)
		}
		b, err := json.Marshal(txn)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, want[i].json) {
			t.Fatalf("txn %d differs after replay:\n got %s\nwant %s", i, b, want[i].json)
		}
	}
}

func segPath(dir string, firstSeq uint64) string {
	return filepath.Join(dir, "events", fmt.Sprintf("%012d.jsonl", firstSeq))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// splitLines returns the terminated lines of data, each keeping its newline.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			lines = append(lines, data)
			break
		}
		lines = append(lines, data[:i+1])
		data = data[i+1:]
	}
	return lines
}

// newLog creates a log with n records and closes it, returning the dir.
func newLog(t *testing.T, n int) (string, []wantRec) {
	t.Helper()
	dir := t.TempDir()
	l := openOK(t, dir)
	want := fill(t, l, 1, n)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, want
}

func expectCorrupt(t *testing.T, dir string, wantInMsg string) {
	t.Helper()
	l, rec, err := Open(dir)
	if err == nil {
		l.Close()
		t.Fatalf("Open succeeded (recovery %+v), want ErrCorrupt", rec)
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open: %v, want ErrCorrupt", err)
	}
	if wantInMsg != "" && !strings.Contains(err.Error(), wantInMsg) {
		t.Fatalf("Open error %q does not mention %q", err, wantInMsg)
	}
}

func setSegmentMax(t *testing.T, n int64) {
	t.Helper()
	old := segmentMaxBytes
	segmentMaxBytes = n
	t.Cleanup(func() { segmentMaxBytes = old })
}

// ---------------------------------------------------------------------------
// basic behaviour

func TestEmptyLog(t *testing.T) {
	dir := t.TempDir()
	l := openOK(t, dir)
	defer l.Close()
	if l.LastSeq() != 0 || l.LastHash() != "" {
		t.Fatalf("empty log: seq %d hash %q", l.LastSeq(), l.LastHash())
	}
	if n, b := l.Stats(); n != 0 || b != 0 {
		t.Fatalf("Stats = %d, %d; want 0, 0", n, b)
	}
	if got := replayAll(t, l, 1); len(got) != 0 {
		t.Fatalf("replayed %d txns from empty log", len(got))
	}
	if _, err := os.Stat(segPath(dir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("segment created before first append: %v", err)
	}
	if err := l.Append(mkTxn(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(segPath(dir, 1)); err != nil {
		t.Fatalf("first segment missing after append: %v", err)
	}
	if l.LastSeq() != 1 {
		t.Fatalf("LastSeq = %d, want 1", l.LastSeq())
	}
}

func TestAppendReopenReplay(t *testing.T) {
	const n = 25
	dir, want := newLog(t, n)

	l, rec, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatalf("unexpected recovery: %+v", rec)
	}
	defer l.Close()
	if l.LastSeq() != n {
		t.Fatalf("LastSeq = %d, want %d", l.LastSeq(), n)
	}
	if l.LastHash() != want[n-1].hash {
		t.Fatalf("LastHash changed across reopen")
	}
	assertReplayMatches(t, l, want)

	segs, size := l.Stats()
	st, err := os.Stat(segPath(dir, 1))
	if err != nil {
		t.Fatal(err)
	}
	if segs != 1 || size != st.Size() {
		t.Fatalf("Stats = %d, %d; want 1, %d", segs, size, st.Size())
	}

	// Appending after reopen continues the chain.
	more := fill(t, l, n+1, n+5)
	assertReplayMatches(t, l, append(want, more...))
}

func TestRecordFormat(t *testing.T) {
	dir, want := newLog(t, 3)
	lines := splitLines(readFile(t, segPath(dir, 1)))
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3", len(lines))
	}
	prev := ""
	for i, ln := range lines {
		var rec struct {
			Seq  uint64          `json:"seq"`
			Prev string          `json:"prev"`
			Hash string          `json:"hash"`
			Txn  json.RawMessage `json:"txn"`
		}
		if err := json.Unmarshal(ln, &rec); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if rec.Seq != uint64(i+1) {
			t.Fatalf("line %d: seq %d", i+1, rec.Seq)
		}
		if rec.Prev != prev {
			t.Fatalf("line %d: prev %q, want %q", i+1, rec.Prev, prev)
		}
		if !bytes.Equal(rec.Txn, want[i].json) {
			t.Fatalf("line %d: txn bytes differ from json.Marshal output", i+1)
		}
		sum := sha256.Sum256(append(append([]byte(prev), '\n'), rec.Txn...))
		if h := hex.EncodeToString(sum[:]); h != rec.Hash || h != want[i].hash {
			t.Fatalf("line %d: hash %q, recomputed %q, LastHash %q", i+1, rec.Hash, h, want[i].hash)
		}
		prev = rec.Hash
	}
}

func TestReplayFromSeq(t *testing.T) {
	dir, want := newLog(t, 10)
	l := openOK(t, dir)
	defer l.Close()

	got := replayAll(t, l, 7)
	if len(got) != 4 {
		t.Fatalf("Replay(7) yielded %d, want 4", len(got))
	}
	for i, txn := range got {
		if txn.Seq != uint64(7+i) || txn.ID != mkTxn(7+i).ID {
			t.Fatalf("Replay(7)[%d] = seq %d id %s", i, txn.Seq, txn.ID)
		}
	}
	if got := replayAll(t, l, 10); len(got) != 1 || got[0].Seq != 10 {
		t.Fatalf("Replay(10) yielded %d", len(got))
	}
	if got := replayAll(t, l, 11); len(got) != 0 {
		t.Fatalf("Replay(11) yielded %d, want 0", len(got))
	}
	if got := replayAll(t, l, 0); len(got) != len(want) {
		t.Fatalf("Replay(0) yielded %d, want %d", len(got), len(want))
	}

	// fn errors stop the replay and come back unchanged.
	sentinel := errors.New("stop here")
	calls := 0
	err := l.Replay(1, func(*model.Txn) error {
		calls++
		if calls == 3 {
			return sentinel
		}
		return nil
	})
	if err != sentinel {
		t.Fatalf("Replay returned %v, want sentinel", err)
	}
	if calls != 3 {
		t.Fatalf("fn called %d times after error, want 3", calls)
	}
}

func TestClosed(t *testing.T) {
	dir, _ := newLog(t, 2)
	l := openOK(t, dir)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := l.Append(mkTxn(3)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append after Close: %v", err)
	}
	if err := l.Replay(1, func(*model.Txn) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("Replay after Close: %v", err)
	}
	if _, err := l.ReadSnapshot(); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadSnapshot after Close: %v", err)
	}
}

// ---------------------------------------------------------------------------
// integrity: unrecoverable damage

func TestTamperMiddleRecordIsCorrupt(t *testing.T) {
	dir, _ := newLog(t, 10)
	path := segPath(dir, 1)
	lines := splitLines(readFile(t, path))
	// Flip one byte inside the txn of record 5 without breaking the JSON.
	old, repl := []byte(`"summary":"summary 5"`), []byte(`"summary":"summary X"`)
	if !bytes.Contains(lines[4], old) {
		t.Fatal("test setup: marker not found in line 5")
	}
	lines[4] = bytes.Replace(lines[4], old, repl, 1)
	writeFile(t, path, bytes.Join(lines, nil))
	expectCorrupt(t, dir, "line 5")
}

func TestBadLineInMiddleIsCorrupt(t *testing.T) {
	t.Run("garbage line inserted", func(t *testing.T) {
		dir, _ := newLog(t, 6)
		path := segPath(dir, 1)
		lines := splitLines(readFile(t, path))
		out := bytes.Join([][]byte{lines[0], lines[1], lines[2], []byte("garbage\n"), lines[3], lines[4], lines[5]}, nil)
		writeFile(t, path, out)
		expectCorrupt(t, dir, "line 4")
	})
	t.Run("record truncated then good records", func(t *testing.T) {
		dir, _ := newLog(t, 6)
		path := segPath(dir, 1)
		lines := splitLines(readFile(t, path))
		cut := append(append([]byte{}, lines[2][:len(lines[2])/2]...), '\n')
		out := bytes.Join([][]byte{lines[0], lines[1], cut, lines[3], lines[4], lines[5]}, nil)
		writeFile(t, path, out)
		expectCorrupt(t, dir, "line 3")
	})
	t.Run("tampered record followed by good records", func(t *testing.T) {
		dir, _ := newLog(t, 4)
		path := segPath(dir, 1)
		lines := splitLines(readFile(t, path))
		lines[2] = bytes.Replace(lines[2], []byte("summary 3"), []byte("summary Q"), 1)
		writeFile(t, path, bytes.Join(lines, nil))
		expectCorrupt(t, dir, "line 3")
	})
}

func TestSeqGapIsCorrupt(t *testing.T) {
	t.Run("record removed from the middle", func(t *testing.T) {
		dir, _ := newLog(t, 6)
		path := segPath(dir, 1)
		lines := splitLines(readFile(t, path))
		writeFile(t, path, bytes.Join(append(lines[:3], lines[4:]...), nil))
		expectCorrupt(t, dir, "line 4")
	})
	t.Run("well-formed final record with wrong seq", func(t *testing.T) {
		// A complete, self-consistent record in the wrong place is not tail
		// damage from an interrupted write; it must not be dropped silently.
		dir, _ := newLog(t, 6)
		path := segPath(dir, 1)
		lines := splitLines(readFile(t, path))
		writeFile(t, path, bytes.Join(append(lines[:4], lines[5]), nil))
		expectCorrupt(t, dir, "line 5")
	})
	t.Run("first segment misnamed", func(t *testing.T) {
		dir, _ := newLog(t, 3)
		if err := os.Rename(segPath(dir, 1), segPath(dir, 2)); err != nil {
			t.Fatal(err)
		}
		expectCorrupt(t, dir, "expected 1")
	})
}

func TestMissingSegmentIsCorrupt(t *testing.T) {
	setSegmentMax(t, 1)
	dir, _ := newLog(t, 5) // one record per segment
	if err := os.Remove(segPath(dir, 3)); err != nil {
		t.Fatal(err)
	}
	expectCorrupt(t, dir, "expected 3")
}

func TestDamagedNonLastSegmentIsCorrupt(t *testing.T) {
	setSegmentMax(t, 1)
	dir, _ := newLog(t, 4)
	path := segPath(dir, 2)
	data := readFile(t, path)
	writeFile(t, path, data[:len(data)/2])
	expectCorrupt(t, dir, "line 1")
}

// ---------------------------------------------------------------------------
// integrity: recoverable tail damage

func TestTruncatedTailRecovers(t *testing.T) {
	const n = 10
	dir, want := newLog(t, n)
	path := segPath(dir, 1)
	data := readFile(t, path)
	lines := splitLines(data)
	last := lines[n-1]
	cut := data[:len(data)-len(last)+len(last)/2]
	writeFile(t, path, cut)

	l, rec, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	if rec == nil {
		t.Fatal("Open returned no Recovery")
	}
	if rec.TruncatedFile != path {
		t.Fatalf("TruncatedFile = %q, want %q", rec.TruncatedFile, path)
	}
	if wantDropped := int64(len(last) / 2); rec.DroppedBytes != wantDropped {
		t.Fatalf("DroppedBytes = %d, want %d", rec.DroppedBytes, wantDropped)
	}
	if rec.Line != n {
		t.Fatalf("Recovery.Line = %d, want %d", rec.Line, n)
	}
	if !strings.HasPrefix(rec.CorruptCopy, path+".corrupt-") {
		t.Fatalf("CorruptCopy = %q", rec.CorruptCopy)
	}
	if got := readFile(t, rec.CorruptCopy); !bytes.Equal(got, cut[len(cut)-len(last)/2:]) {
		t.Fatalf("corrupt copy holds %q, want the cut bytes", got)
	}
	if got := readFile(t, path); !bytes.Equal(got, data[:len(data)-len(last)]) {
		t.Fatal("segment was not truncated to the last good line")
	}
	if l.LastSeq() != n-1 || l.LastHash() != want[n-2].hash {
		t.Fatalf("after recovery: seq %d hash %q", l.LastSeq(), l.LastHash())
	}

	// The log is usable: the next append takes the seq of the dropped record.
	more := fill(t, l, 11, 11)
	if more[0].seq != n {
		t.Fatalf("append after recovery got seq %d, want %d", more[0].seq, n)
	}
	assertReplayMatches(t, l, append(want[:n-1], more...))
	l.Close()

	// A clean reopen sees a consistent log and no further recovery.
	l2 := openOK(t, dir)
	defer l2.Close()
	assertReplayMatches(t, l2, append(want[:n-1], more...))
	// The corrupt copy is not mistaken for a segment.
	if segs, _ := l2.Stats(); segs != 1 {
		t.Fatalf("Stats segments = %d, want 1", segs)
	}
}

func TestTrailingGarbageRecovers(t *testing.T) {
	cases := []struct {
		name    string
		garbage string
	}{
		{"nul padding", "\x00\x00\x00\x00\x00\x00"},
		{"text without newline", "not json"},
		{"text line", "not json\n"},
		{"empty line", "\n"},
		{"partial record then junk lines", `{"seq":11,"prev":"ab` + "\n\n\x00garbage\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const n = 10
			dir, want := newLog(t, n)
			path := segPath(dir, 1)
			data := readFile(t, path)
			writeFile(t, path, append(append([]byte{}, data...), tc.garbage...))

			l, rec, err := Open(dir)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer l.Close()
			if rec == nil {
				t.Fatal("no Recovery")
			}
			if rec.DroppedBytes != int64(len(tc.garbage)) {
				t.Fatalf("DroppedBytes = %d, want %d", rec.DroppedBytes, len(tc.garbage))
			}
			if got := readFile(t, rec.CorruptCopy); string(got) != tc.garbage {
				t.Fatalf("corrupt copy = %q, want %q", got, tc.garbage)
			}
			if got := readFile(t, path); !bytes.Equal(got, data) {
				t.Fatal("segment not restored to its last good line")
			}
			if l.LastSeq() != n {
				t.Fatalf("LastSeq = %d, want %d", l.LastSeq(), n)
			}
			assertReplayMatches(t, l, want)
		})
	}
}

func TestTamperedFinalRecordRecovers(t *testing.T) {
	const n = 5
	dir, want := newLog(t, n)
	path := segPath(dir, 1)
	lines := splitLines(readFile(t, path))
	lines[n-1] = bytes.Replace(lines[n-1], []byte("summary 5"), []byte("summary X"), 1)
	writeFile(t, path, bytes.Join(lines, nil))

	l, rec, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()
	if rec == nil || rec.Line != n || !strings.Contains(rec.Reason, "hash mismatch") {
		t.Fatalf("Recovery = %+v", rec)
	}
	if l.LastSeq() != n-1 {
		t.Fatalf("LastSeq = %d, want %d", l.LastSeq(), n-1)
	}
	assertReplayMatches(t, l, want[:n-1])
}

func TestRecoveryTwiceKeepsBothCopies(t *testing.T) {
	dir, _ := newLog(t, 3)
	path := segPath(dir, 1)
	for i := 0; i < 2; i++ {
		writeFile(t, path, append(readFile(t, path), "junk"...))
		l, rec, err := Open(dir)
		if err != nil || rec == nil {
			t.Fatalf("round %d: err %v rec %+v", i, err, rec)
		}
		l.Close()
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 2 {
		t.Fatalf("found %d corrupt copies, want 2: %v", len(matches), matches)
	}
}

// ---------------------------------------------------------------------------
// rotation

func TestRotation(t *testing.T) {
	setSegmentMax(t, 700) // a record is a few hundred bytes: two or three per segment
	const n = 20
	dir := t.TempDir()
	l := openOK(t, dir)
	want := fill(t, l, 1, n)

	files, err := filepath.Glob(filepath.Join(dir, "events", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("only %d segments after %d appends", len(files), n)
	}
	sort.Strings(files)
	if filepath.Base(files[0]) != "000000000001.jsonl" {
		t.Fatalf("first segment is %s", files[0])
	}
	// Each segment is named by the seq of its first record and holds the
	// consecutive records up to the next segment.
	var total int64
	nextSeq := uint64(1)
	for _, f := range files {
		var first uint64
		fmt.Sscanf(filepath.Base(f), "%012d.jsonl", &first)
		if first != nextSeq {
			t.Fatalf("segment %s starts at %d, expected %d", f, first, nextSeq)
		}
		data := readFile(t, f)
		total += int64(len(data))
		for i, ln := range splitLines(data) {
			var rec struct {
				Seq uint64 `json:"seq"`
			}
			if err := json.Unmarshal(ln, &rec); err != nil {
				t.Fatal(err)
			}
			if rec.Seq != first+uint64(i) {
				t.Fatalf("%s line %d: seq %d", f, i+1, rec.Seq)
			}
			nextSeq = rec.Seq + 1
		}
	}
	if nextSeq != n+1 {
		t.Fatalf("segments cover seqs up to %d, want %d", nextSeq-1, n)
	}
	if segs, size := l.Stats(); segs != len(files) || size != total {
		t.Fatalf("Stats = %d, %d; want %d, %d", segs, size, len(files), total)
	}
	assertReplayMatches(t, l, want)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen across segments; replay from the middle of a later segment.
	l2 := openOK(t, dir)
	defer l2.Close()
	if l2.LastSeq() != n || l2.LastHash() != want[n-1].hash {
		t.Fatalf("after reopen: seq %d hash %q", l2.LastSeq(), l2.LastHash())
	}
	assertReplayMatches(t, l2, want)
	got := replayAll(t, l2, 13)
	if len(got) != n-12 || got[0].Seq != 13 {
		t.Fatalf("Replay(13) yielded %d starting at %d", len(got), got[0].Seq)
	}

	// Snapshot hash lookup works for a seq in a middle segment.
	s := &Snapshot{Seq: 7, Hash: want[6].hash, At: time.Now()}
	if err := l2.WriteSnapshot(s); err != nil {
		t.Fatal(err)
	}
	if rs, err := l2.ReadSnapshot(); err != nil || rs == nil || rs.Seq != 7 {
		t.Fatalf("ReadSnapshot = %+v, %v", rs, err)
	}

	// Appending continues to rotate correctly.
	more := fill(t, l2, n+1, n+10)
	assertReplayMatches(t, l2, append(want, more...))
	segs, _ := l2.Stats()
	files, _ = filepath.Glob(filepath.Join(dir, "events", "*.jsonl"))
	if segs != len(files) || segs <= len(files)-len(more) {
		t.Fatalf("Stats segments %d, files %d", segs, len(files))
	}
}

func TestEmptyTrailingSegmentIsFine(t *testing.T) {
	// A crash between creating a new segment and writing its first record
	// leaves an empty file named lastSeq+1. That is a valid state.
	dir, want := newLog(t, 4)
	writeFile(t, segPath(dir, 5), nil)
	l := openOK(t, dir)
	defer l.Close()
	if l.LastSeq() != 4 {
		t.Fatalf("LastSeq = %d", l.LastSeq())
	}
	more := fill(t, l, 5, 6)
	assertReplayMatches(t, l, append(want, more...))
	if data := readFile(t, segPath(dir, 5)); len(splitLines(data)) != 2 {
		t.Fatal("appends did not go to the empty trailing segment")
	}
}

// ---------------------------------------------------------------------------
// snapshots

func snapshotPath(dir string) string { return filepath.Join(dir, "snapshots", "state.json") }

func editSnapshot(t *testing.T, dir string, edit func(m map[string]any)) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(readFile(t, snapshotPath(dir)), &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, snapshotPath(dir), b)
}

func TestSnapshotRoundtrip(t *testing.T) {
	dir := t.TempDir()
	l := openOK(t, dir)
	defer l.Close()

	if s, err := l.ReadSnapshot(); s != nil || err != nil {
		t.Fatalf("ReadSnapshot on fresh log = %+v, %v", s, err)
	}
	// An empty-state snapshot at seq 0 is valid.
	if err := l.WriteSnapshot(&Snapshot{At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if s, err := l.ReadSnapshot(); err != nil || s == nil || s.Seq != 0 {
		t.Fatalf("seq 0 snapshot = %+v, %v", s, err)
	}

	want := fill(t, l, 1, 5)
	in := &Snapshot{
		Seq:  5,
		Hash: l.LastHash(),
		At:   time.Date(2026, 9, 3, 1, 2, 3, 4000, time.UTC),
		Objects: []json.RawMessage{
			json.RawMessage(`{"id":"note-1","type":"note","title":"a"}`),
			json.RawMessage(`{"id":"task-2","type":"task","text":"b"}`),
		},
	}
	if err := l.WriteSnapshot(in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "snapshots", "state.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file left behind: %v", err)
	}
	out, err := l.ReadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if out.Seq != in.Seq || out.Hash != in.Hash || !out.At.Equal(in.At) {
		t.Fatalf("roundtrip: %+v", out)
	}
	if len(out.Objects) != 2 || !bytes.Equal(out.Objects[0], in.Objects[0]) || !bytes.Equal(out.Objects[1], in.Objects[1]) {
		t.Fatalf("objects differ: %s", out.Objects)
	}

	// A snapshot at an earlier seq than the log is fine when its hash is right.
	if err := l.WriteSnapshot(&Snapshot{Seq: 3, Hash: want[2].hash, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if s, err := l.ReadSnapshot(); err != nil || s.Seq != 3 {
		t.Fatalf("earlier snapshot = %+v, %v", s, err)
	}

	// Writing a snapshot the log cannot back is refused outright.
	if err := l.WriteSnapshot(&Snapshot{Seq: 6, Hash: "x"}); err == nil {
		t.Fatal("WriteSnapshot ahead of log succeeded")
	}
	if err := l.WriteSnapshot(&Snapshot{Seq: 5, Hash: "wrong"}); err == nil {
		t.Fatal("WriteSnapshot with wrong hash at last seq succeeded")
	}
	// The refused writes left the previous snapshot in place.
	if s, err := l.ReadSnapshot(); err != nil || s.Seq != 3 {
		t.Fatalf("snapshot after refused writes = %+v, %v", s, err)
	}

	// It survives a reopen.
	l.Close()
	l2 := openOK(t, dir)
	defer l2.Close()
	if s, err := l2.ReadSnapshot(); err != nil || s.Seq != 3 || s.Hash != want[2].hash {
		t.Fatalf("after reopen: %+v, %v", s, err)
	}
}

func TestSnapshotStale(t *testing.T) {
	dir := t.TempDir()
	l := openOK(t, dir)
	defer l.Close()
	fill(t, l, 1, 5)
	good := &Snapshot{Seq: 5, Hash: l.LastHash(), At: time.Now()}

	t.Run("seq ahead of log", func(t *testing.T) {
		if err := l.WriteSnapshot(good); err != nil {
			t.Fatal(err)
		}
		editSnapshot(t, dir, func(m map[string]any) { m["seq"] = 9 })
		if _, err := l.ReadSnapshot(); !errors.Is(err, ErrSnapshotStale) {
			t.Fatalf("got %v, want ErrSnapshotStale", err)
		}
	})
	t.Run("hash mismatch", func(t *testing.T) {
		if err := l.WriteSnapshot(good); err != nil {
			t.Fatal(err)
		}
		editSnapshot(t, dir, func(m map[string]any) { m["hash"] = strings.Repeat("ab", 32) })
		if _, err := l.ReadSnapshot(); !errors.Is(err, ErrSnapshotStale) {
			t.Fatalf("got %v, want ErrSnapshotStale", err)
		}
	})
	t.Run("hash of a different seq", func(t *testing.T) {
		if err := l.WriteSnapshot(good); err != nil {
			t.Fatal(err)
		}
		editSnapshot(t, dir, func(m map[string]any) { m["seq"] = 4 })
		if _, err := l.ReadSnapshot(); !errors.Is(err, ErrSnapshotStale) {
			t.Fatalf("got %v, want ErrSnapshotStale", err)
		}
	})
	t.Run("log rewound behind snapshot", func(t *testing.T) {
		// Simulate a log restored from an older backup than the snapshot.
		if err := l.WriteSnapshot(good); err != nil {
			t.Fatal(err)
		}
		path := segPath(dir, 1)
		lines := splitLines(readFile(t, path))
		writeFile(t, path, bytes.Join(lines[:3], nil))
		// The directory lock allows one open Log at a time.
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		l2 := openOK(t, dir)
		if _, err := l2.ReadSnapshot(); !errors.Is(err, ErrSnapshotStale) {
			t.Fatalf("got %v, want ErrSnapshotStale", err)
		}
		if err := l2.Close(); err != nil {
			t.Fatal(err)
		}
		l = openOK(t, dir)
	})
	t.Run("invalid json is an error, not stale", func(t *testing.T) {
		writeFile(t, snapshotPath(dir), []byte("{not json"))
		_, err := l.ReadSnapshot()
		if err == nil || errors.Is(err, ErrSnapshotStale) {
			t.Fatalf("got %v, want a decode error", err)
		}
	})
}

// ---------------------------------------------------------------------------
// concurrency

func TestConcurrentAppend(t *testing.T) {
	const workers, per = 8, 50
	dir := t.TempDir()
	l := openOK(t, dir)
	defer l.Close()

	var mu sync.Mutex
	seqs := make([]uint64, 0, workers*per)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				txn := mkTxn(w*per + i)
				if err := l.Append(txn); err != nil {
					t.Errorf("worker %d: %v", w, err)
					return
				}
				mu.Lock()
				seqs = append(seqs, txn.Seq)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	if len(seqs) != workers*per {
		t.Fatalf("%d seqs assigned, want %d", len(seqs), workers*per)
	}
	for i, s := range seqs {
		if s != uint64(i+1) {
			t.Fatalf("seq[%d] = %d, want %d (gap or duplicate)", i, s, i+1)
		}
	}
	if l.LastSeq() != workers*per {
		t.Fatalf("LastSeq = %d", l.LastSeq())
	}
	got := replayAll(t, l, 1)
	if len(got) != workers*per {
		t.Fatalf("replayed %d", len(got))
	}
	for i, txn := range got {
		if txn.Seq != uint64(i+1) {
			t.Fatalf("replayed seq %d at position %d", txn.Seq, i)
		}
	}
	// Reading and writing at the same time must not trip the race detector
	// or see an inconsistent chain.
	var wg2 sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg2.Add(2)
		go func() {
			defer wg2.Done()
			for i := 0; i < 10; i++ {
				if err := l.Append(mkTxn(i)); err != nil {
					t.Error(err)
				}
			}
		}()
		go func() {
			defer wg2.Done()
			for i := 0; i < 3; i++ {
				if err := l.Replay(1, func(*model.Txn) error { return nil }); err != nil {
					t.Error(err)
				}
				if _, err := l.ReadSnapshot(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg2.Wait()
}
