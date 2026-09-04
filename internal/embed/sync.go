package embed

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/model"
)

// Text is what gets embedded for an object: title and body for notes, the
// text for tasks, name, aliases and summary for topics. Long bodies are cut
// so one object stays one request.
func Text(o model.Object) string {
	const maxChars = 6000
	var s string
	switch v := o.(type) {
	case *model.Note:
		s = v.NoteTitle + "\n" + v.Body.PlainText()
	case *model.Task:
		s = v.Text
	case *model.Topic:
		s = v.Name + "\n" + strings.Join(v.Aliases, ", ") + "\n" + v.Summary.PlainText()
	default:
		return ""
	}
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxChars {
		s = string(r[:maxChars])
	}
	return s
}

// Types lists the object types that get vectors.
var Types = []model.Type{model.TypeNote, model.TypeTask, model.TypeTopic}

// Syncer keeps the index in step with the store: a backfill on start and on
// model change, then one re-embedding per changed object.
type Syncer struct {
	Core     *core.Core
	Index    *Index
	Embedder Embedder
	Model    string
	Log      *slog.Logger
	Batch    int

	mu      sync.Mutex
	pending map[string]bool
	wake    chan struct{}
}

// NewSyncer wires a syncer; Run starts it.
func NewSyncer(c *core.Core, ix *Index, e Embedder, modelName string, lg *slog.Logger) *Syncer {
	if lg == nil {
		lg = slog.Default()
	}
	return &Syncer{Core: c, Index: ix, Embedder: e, Model: modelName, Log: lg, Batch: 32, pending: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// Backfill embeds every object whose vector is missing or stale. It returns
// how many vectors were written.
func (s *Syncer) Backfill(ctx context.Context) (int, error) {
	var ids []string
	var texts []string
	s.Core.Each(Types, func(o model.Object) bool {
		m := o.GetMeta()
		if m.Archived {
			return true
		}
		text := Text(o)
		if text == "" {
			return true
		}
		if !s.Index.Has(m.ID, Hash(text)) {
			ids = append(ids, m.ID)
			texts = append(texts, text)
		}
		return true
	})
	// Drop vectors of objects that no longer exist.
	s.Index.mu.RLock()
	var stale []string
	for id := range s.Index.entries {
		if _, err := s.Core.Get(id); err != nil {
			stale = append(stale, id)
		}
	}
	s.Index.mu.RUnlock()
	for _, id := range stale {
		s.Index.Remove(id)
	}
	written := 0
	for i := 0; i < len(ids); i += s.batch() {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		j := min(i+s.batch(), len(ids))
		vecs, err := s.Embedder.Embed(ctx, s.Model, texts[i:j])
		if err != nil {
			return written, err
		}
		for k, v := range vecs {
			if err := s.Index.Put(ids[i+k], Hash(texts[i+k]), v); err != nil {
				return written, err
			}
			written++
		}
	}
	if len(stale) > 0 || written > 0 {
		if err := s.Index.Compact(); err != nil {
			s.Log.Warn("embeddings compact", "err", err)
		}
	}
	return written, nil
}

func (s *Syncer) batch() int {
	if s.Batch <= 0 {
		return 32
	}
	return s.Batch
}

// Run backfills, then follows object changes until ctx ends. Failures are
// logged and retried on the next change or backfill; search degrades to
// lexical meanwhile.
func (s *Syncer) Run(ctx context.Context) {
	if n, err := s.Backfill(ctx); err != nil {
		s.Log.Warn("embeddings backfill", "err", err, "written", n)
	} else if n > 0 {
		s.Log.Info("embeddings backfilled", "vectors", n, "model", s.Model)
	}
	events, unsub := s.Core.Subscribe()
	defer unsub()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			if ev.Type != "object.changed" {
				continue
			}
			payload, ok := ev.Payload.(map[string]any)
			if !ok {
				continue
			}
			id, _ := payload["id"].(string)
			if id == "" {
				continue
			}
			s.mu.Lock()
			s.pending[id] = true
			s.mu.Unlock()
			debounce.Reset(500 * time.Millisecond)
		case <-debounce.C:
			s.flush(ctx)
		}
	}
}

// flush re-embeds the pending objects.
func (s *Syncer) flush(ctx context.Context) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	s.pending = map[string]bool{}
	s.mu.Unlock()
	var todo, texts []string
	for _, id := range ids {
		o, err := s.Core.Get(id)
		if err != nil || o.GetMeta().Archived {
			s.Index.Remove(id)
			continue
		}
		text := Text(o)
		if text == "" || s.Index.Has(id, Hash(text)) {
			continue
		}
		todo = append(todo, id)
		texts = append(texts, text)
	}
	for i := 0; i < len(todo); i += s.batch() {
		j := min(i+s.batch(), len(todo))
		vecs, err := s.Embedder.Embed(ctx, s.Model, texts[i:j])
		if err != nil {
			s.Log.Warn("embeddings update", "err", err)
			return
		}
		for k, v := range vecs {
			_ = s.Index.Put(todo[i+k], Hash(texts[i+k]), v)
		}
	}
}

// Search fuses the lexical search with the nearest vectors for the query.
// When embedding the query fails, the lexical result stands.
func Search(ctx context.Context, c *core.Core, ix *Index, e Embedder, modelName, q string, limit int, types []model.Type, includeAll bool) []core.Hit {
	lexical := c.Search(q, limit*2, types, includeAll)
	if ix == nil || e == nil || modelName == "" || ix.Len() == 0 {
		if len(lexical) > limit && limit > 0 {
			lexical = lexical[:limit]
		}
		return lexical
	}
	vecs, err := e.Embed(ctx, modelName, []string{q})
	if err != nil || len(vecs) != 1 {
		if len(lexical) > limit && limit > 0 {
			lexical = lexical[:limit]
		}
		return lexical
	}
	want := map[model.Type]bool{}
	for _, t := range types {
		want[t] = true
	}
	byID := map[string]core.Hit{}
	var lexIDs []string
	for _, h := range lexical {
		byID[h.ID] = h
		lexIDs = append(lexIDs, h.ID)
	}
	var vecIDs []string
	for _, h := range ix.Nearest(vecs[0], limit*2, func(id string) bool {
		if h, ok := byID[id]; ok {
			return len(want) == 0 || want[h.Type]
		}
		o, err := c.Get(id)
		if err != nil {
			return false
		}
		m := o.GetMeta()
		if m.Archived && !includeAll {
			return false
		}
		return len(want) == 0 || want[m.Type]
	}) {
		if h.Score < 0.25 {
			break
		}
		vecIDs = append(vecIDs, h.ID)
		if _, ok := byID[h.ID]; !ok {
			o, err := c.Get(h.ID)
			if err != nil {
				continue
			}
			byID[h.ID] = core.Hit{ID: h.ID, Type: o.GetMeta().Type, Title: o.Title(), Score: h.Score, Object: o}
		}
	}
	out := make([]core.Hit, 0, limit)
	for _, id := range Fuse(lexIDs, vecIDs, limit) {
		out = append(out, byID[id])
	}
	return out
}
