package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func fakeEmbeddings(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type d struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []d
		// Returned out of order on purpose; index says which text a vector is for.
		for i := len(req.Input) - 1; i >= 0; i-- {
			// A toy embedding: counts of a few letters, so similar texts are close.
			v := make([]float32, 4)
			for _, c := range req.Input[i] {
				switch c {
				case 'a':
					v[0]++
				case 'e':
					v[1]++
				case 'o':
					v[2]++
				default:
					v[3] += 0.1
				}
			}
			data = append(data, d{Index: i, Embedding: v})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func TestClientAndIndexRoundTrip(t *testing.T) {
	srv := fakeEmbeddings(t)
	defer srv.Close()
	c := &Client{Name: "t", BaseURL: srv.URL, APIKey: "k"}
	vecs, err := c.Embed(context.Background(), "m", []string{"banana bread", "apple pie", "zzz"})
	if err != nil || len(vecs) != 3 {
		t.Fatalf("embed: %v %d", err, len(vecs))
	}
	dir := t.TempDir()
	ix, err := Open(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"note_1", "note_2", "note_3"} {
		if err := ix.Put(id, Hash("text"+id), vecs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if !ix.Has("note_1", Hash("textnote_1")) || ix.Has("note_1", "other") {
		t.Fatal("Has")
	}
	q, _ := c.Embed(context.Background(), "m", []string{"banana"})
	hits := ix.Nearest(q[0], 2, nil)
	if len(hits) != 2 || hits[0].ID != "note_1" {
		t.Fatalf("nearest %+v", hits)
	}
	if sim := ix.Similar("note_1", 1, nil); len(sim) != 1 || sim[0].ID == "note_1" {
		t.Fatalf("similar %+v", sim)
	}
	ix.Remove("note_3")
	if err := ix.Compact(); err != nil {
		t.Fatal(err)
	}
	// Reopen: two entries survive, from the compacted file.
	ix2, err := Open(dir, "m")
	if err != nil || ix2.Len() != 2 || !ix2.Has("note_2", Hash("textnote_2")) {
		t.Fatalf("reopen: %v len=%d", err, ix2.Len())
	}
	if other, _ := Open(dir, "other-model"); other.Len() != 0 {
		t.Fatal("a different model must start empty")
	}
	if _, err := (&Client{BaseURL: srv.URL, APIKey: "bad"}).Embed(context.Background(), "m", []string{"x"}); err == nil {
		t.Fatal("bad key accepted")
	}
	if fi, err := filepath.Glob(filepath.Join(dir, "embeddings", "*.vec")); err != nil || len(fi) != 1 {
		t.Fatalf("cache files %v %v", fi, err)
	}
}

func TestFuse(t *testing.T) {
	got := Fuse([]string{"a", "b", "c"}, []string{"c", "d"}, 3)
	if len(got) != 3 || got[0] != "c" {
		t.Fatalf("fuse %v (c is in both lists and must lead)", got)
	}
	_ = io.EOF
}
