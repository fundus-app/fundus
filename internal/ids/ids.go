// Package ids generates prefixed, time-sortable identifiers for all Fundus objects.
//
// IDs look like "note_01J8ZK3V2Q9F1H6C4X0M5B7N8P". The prefix names the object
// type so that an ID is self-describing in logs, receipts and exports; the body
// is a ULID, which sorts by creation time and is safe to generate without
// coordination.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Known prefixes. Keep this list in sync with model.Type.
const (
	PrefixCapture = "cap"
	PrefixNote    = "note"
	PrefixTask    = "task"
	PrefixTopic   = "topic"
	PrefixSource  = "src"
	PrefixTxn     = "txn"
	PrefixConv    = "conv"
	PrefixMessage = "msg"
	PrefixBlock   = "b"
)

// New returns a fresh ID with the given prefix.
func New(prefix string) string {
	return prefix + "_" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// Derived returns a deterministic ID for the given prefix and seed, in the
// same shape as New. Used when an ID must be reproducible on replay.
func Derived(prefix, seed string) string {
	sum := sha256.Sum256([]byte(seed))
	var entropy [10]byte
	copy(entropy[:], sum[:10])
	ms := binary.BigEndian.Uint64(sum[10:18]) % (1 << 47)
	id := ulid.ULID{}
	_ = id.SetTime(ms)
	_ = id.SetEntropy(entropy[:])
	return prefix + "_" + id.String()
}

// Prefix returns the prefix part of an ID, or "" if the ID is malformed.
func Prefix(id string) string {
	i := strings.IndexByte(id, '_')
	if i <= 0 {
		return ""
	}
	return id[:i]
}

// Valid reports whether id has the shape "<prefix>_<ULID>".
func Valid(id string) bool {
	i := strings.IndexByte(id, '_')
	if i <= 0 || i == len(id)-1 {
		return false
	}
	_, err := ulid.ParseStrict(id[i+1:])
	return err == nil
}

// MustHavePrefix validates that id carries the expected prefix.
func MustHavePrefix(id, prefix string) error {
	if !Valid(id) {
		return fmt.Errorf("malformed id %q", id)
	}
	if Prefix(id) != prefix {
		return fmt.Errorf("id %q is not a %s id", id, prefix)
	}
	return nil
}
