// Package embed adds semantic search on top of the lexical index: every
// note, task and topic gets a vector from an OpenAI-compatible /embeddings
// endpoint, kept in a small on-disk cache under the data directory. Vectors
// are derived data, never part of the event log, and are rebuilt from the
// state when the model changes. Search fuses lexical and vector ranks.
package embed

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"crypto/sha256"
	"encoding/base64"
)

// Embedder turns texts into vectors.
type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// Client speaks the OpenAI-compatible /embeddings endpoint.
type Client struct {
	Name    string
	BaseURL string
	APIKey  string
	Headers map[string]string
	HTTP    *http.Client
}

// Embed implements Embedder. Inputs are sent in one request; callers batch.
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{"model": model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/embeddings", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	res, err := hc.Do(req)
	if err != nil {
		var nerr net.Error
		return nil, &Error{Message: err.Error(), Retryable: errors.As(err, &nerr)}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, &Error{Message: err.Error(), Retryable: true}
	}
	if res.StatusCode >= 400 {
		var er struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &er) == nil && er.Error.Message != "" {
			msg = er.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, &Error{Status: res.StatusCode, Message: msg, Retryable: res.StatusCode == 429 || res.StatusCode >= 500}
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &Error{Message: "decode embeddings: " + err.Error()}
	}
	if len(out.Data) != len(texts) {
		return nil, &Error{Message: fmt.Sprintf("embeddings: got %d vectors for %d texts", len(out.Data), len(texts))}
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, &Error{Message: "embeddings: index out of range"}
		}
		vecs[d.Index] = normalize(d.Embedding)
	}
	return vecs, nil
}

// Error is an embedding failure.
type Error struct {
	Status    int
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("embeddings: HTTP %d: %s", e.Status, e.Message)
	}
	return "embeddings: " + e.Message
}

func normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		return v
	}
	s := float32(1 / math.Sqrt(n))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * s
	}
	return out
}

// Hash identifies the text a vector was made from.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// entry is one cached vector.
type entry struct {
	ID   string
	Hash string
	Vec  []float32
}

// Index holds the vectors of the current state and answers nearest-neighbour
// queries by brute force, which is fine for the tens of thousands of objects
// a personal store holds.
type Index struct {
	mu      sync.RWMutex
	model   string
	entries map[string]*entry
	path    string
	dirty   int
}

// Open loads the cache for model from dir (one file per model); a cache
// written for another model is ignored.
func Open(dir, model string) (*Index, error) {
	ix := &Index{model: model, entries: map[string]*entry{}}
	if dir == "" || model == "" {
		return ix, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "embeddings"), 0o700); err != nil {
		return nil, err
	}
	ix.path = filepath.Join(dir, "embeddings", safeName(model)+".vec")
	f, err := os.Open(ix.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ix, nil
		}
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		e, err := readEntry(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			// A damaged tail loses at most the last entries; they are
			// recomputed on the next backfill.
			break
		}
		ix.entries[e.ID] = e
	}
	return ix, nil
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Model returns the embedding model the index was built with.
func (ix *Index) Model() string { return ix.model }

// Len returns the number of vectors held.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.entries)
}

// Has reports whether id has a vector for the given text hash.
func (ix *Index) Has(id, hash string) bool {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	e, ok := ix.entries[id]
	return ok && e.Hash == hash
}

// Put stores a vector and appends it to the cache file.
func (ix *Index) Put(id, hash string, vec []float32) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	e := &entry{ID: id, Hash: hash, Vec: vec}
	ix.entries[id] = e
	ix.dirty++
	if ix.path == "" {
		return nil
	}
	f, err := os.OpenFile(ix.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeEntry(f, e)
}

// Remove drops a vector; the file is compacted on the next Compact.
func (ix *Index) Remove(id string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.entries[id]; ok {
		delete(ix.entries, id)
		ix.dirty++
	}
}

// Compact rewrites the cache file with the live entries only.
func (ix *Index) Compact() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.path == "" {
		return nil
	}
	tmp := ix.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for _, e := range ix.entries {
		if err := writeEntry(w, e); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	ix.dirty = 0
	return os.Rename(tmp, ix.path)
}

// Hit is a nearest-neighbour result.
type Hit struct {
	ID    string
	Score float64 // cosine similarity, 1 = identical
}

// Nearest returns the ids closest to vec, best first, skipping those the
// filter rejects.
func (ix *Index) Nearest(vec []float32, limit int, filter func(id string) bool) []Hit {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	hits := make([]Hit, 0, len(ix.entries))
	for id, e := range ix.entries {
		if filter != nil && !filter(id) {
			continue
		}
		hits = append(hits, Hit{ID: id, Score: dot(vec, e.Vec)})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// Similar returns the ids closest to the stored vector of id.
func (ix *Index) Similar(id string, limit int, filter func(id string) bool) []Hit {
	ix.mu.RLock()
	e, ok := ix.entries[id]
	ix.mu.RUnlock()
	if !ok {
		return nil
	}
	return ix.Nearest(e.Vec, limit, func(other string) bool { return other != id && (filter == nil || filter(other)) })
}

func dot(a, b []float32) float64 {
	n := min(len(a), len(b))
	var s float64
	for i := 0; i < n; i++ {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// File format: per entry a length-prefixed id, hash and float32 vector.
func writeEntry(w io.Writer, e *entry) error {
	if err := writeString(w, e.ID); err != nil {
		return err
	}
	if err := writeString(w, e.Hash); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(e.Vec))); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, e.Vec)
}

func writeString(w io.Writer, s string) error {
	if err := binary.Write(w, binary.LittleEndian, uint16(len(s))); err != nil {
		return err
	}
	_, err := io.WriteString(w, s)
	return err
}

func readEntry(r io.Reader) (*entry, error) {
	id, err := readString(r)
	if err != nil {
		return nil, err
	}
	hash, err := readString(r)
	if err != nil {
		return nil, err
	}
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, err
	}
	if n > 1<<16 {
		return nil, errors.New("vector too long")
	}
	vec := make([]float32, n)
	if err := binary.Read(r, binary.LittleEndian, vec); err != nil {
		return nil, err
	}
	return &entry{ID: id, Hash: hash, Vec: vec}, nil
}

func readString(r io.Reader) (string, error) {
	var n uint16
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

// Fuse merges a lexical ranking and a vector ranking with reciprocal rank
// fusion: an id that appears in both lists rises above one that appears in
// only one. k=60 is the usual constant.
func Fuse(lexical []string, vector []string, limit int) []string {
	const k = 60.0
	score := map[string]float64{}
	order := []string{}
	add := func(ids []string) {
		for i, id := range ids {
			if _, ok := score[id]; !ok {
				order = append(order, id)
			}
			score[id] += 1 / (k + float64(i+1))
		}
	}
	add(lexical)
	add(vector)
	sort.SliceStable(order, func(i, j int) bool { return score[order[i]] > score[order[j]] })
	if limit > 0 && len(order) > limit {
		order = order[:limit]
	}
	return order
}
