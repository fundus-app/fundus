package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

// record is the on-disk envelope of one transaction. Txn holds the exact
// bytes of the txn member as they appear in the file so that verification
// never depends on re-marshalling.
type record struct {
	Seq  uint64          `json:"seq"`
	Prev string          `json:"prev"`
	Hash string          `json:"hash"`
	Txn  json.RawMessage `json:"txn"`
}

// recordHash computes the chain hash: sha256(prev + "\n" + txn), hex encoded.
func recordHash(prev string, txn []byte) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte{'\n'})
	h.Write(txn)
	return hex.EncodeToString(h.Sum(nil))
}

// encodeRecord builds one log line. It is assembled by hand rather than via
// json.Marshal so the txn bytes are written verbatim, exactly as hashed.
func encodeRecord(seq uint64, prev, hash string, txn []byte) []byte {
	buf := make([]byte, 0, len(txn)+len(prev)+len(hash)+40)
	buf = append(buf, `{"seq":`...)
	buf = strconv.AppendUint(buf, seq, 10)
	buf = append(buf, `,"prev":"`...)
	buf = append(buf, prev...)
	buf = append(buf, `","hash":"`...)
	buf = append(buf, hash...)
	buf = append(buf, `","txn":`...)
	buf = append(buf, txn...)
	buf = append(buf, "}\n"...)
	return buf
}

// parseRecord decodes one line and checks that it is self-consistent: it
// must be a complete record whose hash matches its own prev and txn bytes.
// Chain position (seq, prev) is checked separately by chainCheck.
func parseRecord(line []byte) (record, error) {
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		return rec, fmt.Errorf("unparseable record: %v", err)
	}
	if rec.Seq == 0 || rec.Hash == "" || len(rec.Txn) == 0 {
		return rec, errors.New("incomplete record")
	}
	if got := recordHash(rec.Prev, rec.Txn); got != rec.Hash {
		return rec, errors.New("hash mismatch")
	}
	return rec, nil
}

// chainCheck reports why rec does not belong at the expected chain position,
// or "" when it does.
func chainCheck(rec *record, expectSeq uint64, expectPrev string) string {
	if rec.Seq != expectSeq {
		return fmt.Sprintf("seq %d, expected %d", rec.Seq, expectSeq)
	}
	if rec.Prev != expectPrev {
		return "prev does not match the hash of the previous record"
	}
	return ""
}

// line is one physical line of a segment.
type line struct {
	no         int    // 1-based line number
	data       []byte // contents without the trailing newline
	terminated bool   // whether a trailing newline was present
	end        int64  // byte offset just past the line, newline included
}

// lineReader yields lines of unbounded length with their byte offsets.
type lineReader struct {
	br     *bufio.Reader
	offset int64
	lineNo int
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{br: bufio.NewReaderSize(r, 256<<10)}
}

// next returns the next line. ok is false at a clean end of file.
func (lr *lineReader) next() (ln line, ok bool, err error) {
	data, err := lr.br.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return line{}, false, err
	}
	if len(data) == 0 {
		return line{}, false, nil
	}
	lr.lineNo++
	ln = line{no: lr.lineNo, data: data, end: lr.offset + int64(len(data))}
	lr.offset = ln.end
	if data[len(data)-1] == '\n' {
		ln.terminated = true
		ln.data = data[:len(data)-1]
	}
	return ln, true, nil
}

// errStopWalk lets a walkSegment visitor end the walk early without error.
var errStopWalk = errors.New("stop")

// walkSegment reads seg from disk, verifying every record against the chain
// state in expectSeq/expectPrev and advancing both. Any problem at all is an
// ErrCorrupt error; unlike Open it never repairs. visit may return
// errStopWalk to end early, which walkSegment swallows.
func (l *Log) walkSegment(seg *segment, expectSeq *uint64, expectPrev *string, visit func(rec *record, lineNo int) error) error {
	path := l.segPath(seg.name)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("store: open %s: %w", path, err)
	}
	defer f.Close()
	lr := newLineReader(f)
	for {
		ln, ok, err := lr.next()
		if err != nil {
			return fmt.Errorf("store: read %s: %w", path, err)
		}
		if !ok {
			return nil
		}
		if !ln.terminated {
			return corruptf(path, ln.no, "truncated line (missing newline)")
		}
		rec, perr := parseRecord(ln.data)
		if perr != nil {
			return corruptf(path, ln.no, "%s", perr.Error())
		}
		if reason := chainCheck(&rec, *expectSeq, *expectPrev); reason != "" {
			return corruptf(path, ln.no, "%s", reason)
		}
		*expectSeq, *expectPrev = rec.Seq+1, rec.Hash
		if err := visit(&rec, ln.no); err != nil {
			if err == errStopWalk {
				return nil
			}
			return err
		}
	}
}
