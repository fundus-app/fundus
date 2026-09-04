package embed

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/doc"
	"github.com/fundus-app/fundus/internal/model"
)

// letterEmbedder embeds by letter counts, deterministic and local.
type letterEmbedder struct{ calls int }

func (l *letterEmbedder) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	l.calls++
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 26)
		for _, c := range strings.ToLower(t) {
			if c >= 'a' && c <= 'z' {
				v[c-'a']++
			}
		}
		out[i] = normalize(v)
	}
	return out, nil
}

func str(s string) *string { return &s }

func TestSyncerBackfillFlushAndHybridSearch(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := core.Open(t.TempDir(), core.Options{Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "note.create", Title: str("Heizung Wartung"), Markdown: "Brenner reinigen, Filter tauschen."},
		{Op: "note.create", Title: str("Grafana dashboards"), Markdown: "Panels for the heating data."},
		{Op: "task.create", Text: "Buy pellets for the winter"},
	})
	n1, n2 := rec.Lines[0].ObjectID, rec.Lines[1].ObjectID
	ix, _ := Open(t.TempDir(), "letters")
	emb := &letterEmbedder{}
	s := NewSyncer(c, ix, emb, "letters", quiet)
	written, err := s.Backfill(context.Background())
	if err != nil || written != 3 || ix.Len() != 3 {
		t.Fatalf("backfill: %v written=%d len=%d", err, written, ix.Len())
	}
	if w, _ := s.Backfill(context.Background()); w != 0 {
		t.Fatalf("second backfill wrote %d", w)
	}
	// A changed note is re-embedded through the event stream.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	obj, _ := c.Get(n1)
	_, err = c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "note.revise", ID: n1, ExpectedRev: obj.GetMeta().Rev,
		Edits: []doc.Edit{{Action: "append", Markdown: "Zusätzlich die Pumpe prüfen."}}}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		obj, _ = c.Get(n1)
		if ix.Has(n1, Hash(Text(obj))) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("changed note was not re-embedded")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Hybrid search: the lexical miss "heating filter" still ranks the
	// heating note through the vectors and Grafana through the words.
	hits := Search(context.Background(), c, ix, emb, "letters", "heating filter", 5, []model.Type{model.TypeNote}, false)
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.ID] = true
	}
	if !ids[n1] || !ids[n2] {
		t.Fatalf("hybrid hits %+v", hits)
	}
	// Without an embedder the lexical result stands.
	if lex := Search(context.Background(), c, nil, nil, "", "Grafana", 5, nil, false); len(lex) != 1 || lex[0].ID != n2 {
		t.Fatalf("lexical only %+v", lex)
	}
}
