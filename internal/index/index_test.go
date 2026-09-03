package index

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func joined(toks []string) string { return strings.Join(toks, " ") }

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // space-joined tokens
	}{
		{"empty", "", ""},
		{"only separators", " --- !!! ... ", ""},
		{"lowercase", "GRAFANA Grafana", "grafana grafana"},
		{"umlauts folded", "Über Äpfel Öl", "uber apfel ol"},
		{"umlaut equals ascii", "Muller", "mull"},
		{"umlaut compound", "Müller-Lüdenscheidt", "mull ludenscheidt"},
		{"sharp s", "Straße", "strass"},
		{"accents", "Café résumé naïve", "cafe resum naive"},
		{"hyphen splits and short dropped", "e-mail", "mail"},
		{"underscore and slash split", "foo_bar/baz", "foo bar baz"},
		{"digits kept", "2024-09 Q3 v2", "2024 09 q3 v2"},
		{"german stopwords", "der Hund und die Katze im Garten", "hund katze gart"},
		{"english stopwords", "the quick and the dead on a hill", "quick dead hill"},
		{"folded stopword fuer", "Daten für Grafana", "daten grafana"},
		{"short tokens dropped", "a b cd ef g", "cd ef"},
		{"compound word", "Heizungsdaten", "heizungsdat"},
		{"stem ungen", "Heizungen", "heiz"},
		{"stem ung", "Heizung", "heiz"},
		{"stem once only", "Wanderungen", "wander"},
		{"stem e", "Solaranlage", "solaranlag"},
		{"stem en", "Solaranlagen", "solaranlag"},
		{"no stem below six runes", "Katze Daten Häuser", "katze daten haus"},
		{"stem keeps remainder", "Zungen Lungen", "zung lung"},
		{"no matching suffix", "liefert", "liefert"},
		{"cyrillic letters kept", "Привет мир", "привет мир"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := joined(Tokenize(c.in))
			if got != c.want {
				t.Fatalf("Tokenize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTokenizeConsistency(t *testing.T) {
	pairs := [][2]string{
		{"Straße", "Strasse"},
		{"Heizungen", "Heizung"},
		{"Solaranlage", "Solaranlagen"},
		{"Müller", "Muller"},
		{"GRAFANA", "grafana"},
		{"e-mail", "mail"},
	}
	for _, p := range pairs {
		a, b := joined(Tokenize(p[0])), joined(Tokenize(p[1]))
		if a != b {
			t.Errorf("Tokenize(%q)=%q but Tokenize(%q)=%q", p[0], a, p[1], b)
		}
	}
}

func TestPutRemoveReplace(t *testing.T) {
	ix := New()
	if ix.Len() != 0 {
		t.Fatalf("Len of empty index = %d", ix.Len())
	}

	ix.Put("a", map[string]string{"title": "Grafana Dashboard", "body": "Heizungsdaten anzeigen"})
	ix.Put("b", map[string]string{"title": "Einkaufsliste", "body": "Milch und Brot"})
	if ix.Len() != 2 {
		t.Fatalf("Len = %d, want 2", ix.Len())
	}
	if hits := ix.Search("grafana", 0, nil); len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("Search(grafana) = %v, want [a]", hits)
	}
	if hits := ix.Search("Brot", 0, nil); len(hits) != 1 || hits[0].ID != "b" {
		t.Fatalf("Search(Brot) = %v, want [b]", hits)
	}

	// Replace a: old tokens must vanish, new tokens must be found, Len unchanged.
	ix.Put("a", map[string]string{"title": "Wetterstation", "body": "Temperatur und Luftfeuchte"})
	if ix.Len() != 2 {
		t.Fatalf("Len after replace = %d, want 2", ix.Len())
	}
	if hits := ix.Search("grafana", 0, nil); len(hits) != 0 {
		t.Fatalf("Search(grafana) after replace = %v, want none", hits)
	}
	if hits := ix.Search("Heizungsdaten", 0, nil); len(hits) != 0 {
		t.Fatalf("Search(Heizungsdaten) after replace = %v, want none", hits)
	}
	if hits := ix.Search("Temperatur", 0, nil); len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("Search(Temperatur) = %v, want [a]", hits)
	}

	// Internal bookkeeping after replace: no stale postings or field stats.
	ix.mu.RLock()
	if _, ok := ix.postings["grafana"]; ok {
		t.Errorf("stale posting for grafana")
	}
	if ix.fieldDocs["title"] != 2 || ix.fieldDocs["body"] != 2 {
		t.Errorf("fieldDocs = %v, want title=2 body=2", ix.fieldDocs)
	}
	ix.mu.RUnlock()

	// Remove.
	ix.Remove("b")
	if ix.Len() != 1 {
		t.Fatalf("Len after remove = %d, want 1", ix.Len())
	}
	if hits := ix.Search("Brot", 0, nil); len(hits) != 0 {
		t.Fatalf("Search(Brot) after remove = %v, want none", hits)
	}
	// Removing an unknown id is a no-op.
	ix.Remove("does-not-exist")
	ix.Remove("b")
	if ix.Len() != 1 {
		t.Fatalf("Len after removing unknown ids = %d, want 1", ix.Len())
	}

	ix.Remove("a")
	ix.mu.RLock()
	if len(ix.postings) != 0 || len(ix.docs) != 0 || len(ix.fieldDocs) != 0 || len(ix.fieldTotal) != 0 {
		t.Errorf("index not empty after removing everything: postings=%d docs=%d fieldDocs=%v fieldTotal=%v",
			len(ix.postings), len(ix.docs), ix.fieldDocs, ix.fieldTotal)
	}
	ix.mu.RUnlock()
}

func TestPutDocumentWithoutTokens(t *testing.T) {
	ix := New()
	ix.Put("empty", map[string]string{"title": "", "body": "the and of"})
	if ix.Len() != 1 {
		t.Fatalf("Len = %d, want 1", ix.Len())
	}
	if hits := ix.Search("the", 0, nil); hits != nil {
		t.Fatalf("Search(the) = %v, want nil", hits)
	}
	ix.Remove("empty")
	if ix.Len() != 0 {
		t.Fatalf("Len after remove = %d, want 0", ix.Len())
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	ix := New()
	ix.Put("a", map[string]string{"title": "Grafana"})
	for _, q := range []string{"", "   ", "the", "der die das", "a b c"} {
		if hits := ix.Search(q, 0, nil); hits != nil {
			t.Errorf("Search(%q) = %v, want nil", q, hits)
		}
	}
	if hits := ix.Search("nothing-here", 0, nil); hits != nil {
		t.Errorf("Search with no matches = %v, want nil", hits)
	}
}

func TestRankingTitleBeatsBody(t *testing.T) {
	ix := New()
	ix.Put("body-only", map[string]string{
		"title": "Monitoring Notizen",
		"body":  "Grafana Dashboard fuer die Heizung einrichten",
	})
	ix.Put("title-hit", map[string]string{
		"title": "Grafana Dashboard",
		"body":  "Monitoring Notizen fuer die Heizung einrichten",
	})
	hits := ix.Search("grafana", 0, nil)
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %v", len(hits), hits)
	}
	if hits[0].ID != "title-hit" {
		t.Fatalf("title match should rank first, got %v", hits)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("title hit score %v should exceed body hit score %v", hits[0].Score, hits[1].Score)
	}
}

func TestRankingAliasBeatsBody(t *testing.T) {
	ix := New()
	ix.Put("body-only", map[string]string{
		"title":   "Heizung",
		"aliases": "Waermepumpe",
		"body":    "Daten in Grafana anzeigen",
	})
	ix.Put("alias-hit", map[string]string{
		"title":   "Monitoring",
		"aliases": "Grafana",
		"body":    "Daten der Heizung anzeigen",
	})
	hits := ix.Search("grafana", 0, nil)
	if len(hits) != 2 || hits[0].ID != "alias-hit" {
		t.Fatalf("alias match should rank first, got %v", hits)
	}
}

func TestRankingMoreTokensBeatsFewer(t *testing.T) {
	ix := New()
	// Heavy repetition of one query token in a short body.
	ix.Put("one-token", map[string]string{
		"title": "Solar",
		"body":  "solar solar solar solar solar solar solar solar",
	})
	// Both query tokens, each only once, in a longer body.
	ix.Put("two-tokens", map[string]string{
		"title": "Notiz",
		"body":  "der wechselrichter vom solar dach meldet heute morgen einen fehler beim start",
	})
	// Filler so that IDF is not degenerate.
	for i := 0; i < 5; i++ {
		ix.Put(fmt.Sprintf("filler-%d", i), map[string]string{
			"title": fmt.Sprintf("Notiz %d", i),
			"body":  "einkaufen kochen aufraeumen schlafen",
		})
	}
	hits := ix.Search("Solar Wechselrichter", 0, nil)
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %v", len(hits), hits)
	}
	if hits[0].ID != "two-tokens" {
		t.Fatalf("document matching both tokens should rank first, got %v", hits)
	}
}

func TestRankingExactPhrase(t *testing.T) {
	ix := New()
	ix.Put("phrase", map[string]string{"body": "die Solaranlage liefert heute wenig Strom"})
	ix.Put("first-only", map[string]string{"body": "Solaranlage auf dem Dach"})
	ix.Put("second-only", map[string]string{"body": "der Bäcker liefert Brot"})
	ix.Put("unrelated", map[string]string{"body": "Termin beim Zahnarzt"})

	hits := ix.Search("Solaranlage liefert", 0, nil)
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want 3: %v", len(hits), hits)
	}
	if hits[0].ID != "phrase" {
		t.Fatalf("document with the exact phrase should rank first, got %v", hits)
	}
	for _, h := range hits {
		if h.ID == "unrelated" {
			t.Fatalf("unrelated document returned: %v", hits)
		}
		if h.Score <= 0 {
			t.Fatalf("non-positive score: %v", h)
		}
	}
}

func TestSearchTieBreakByID(t *testing.T) {
	ix := New()
	for _, id := range []string{"c", "a", "b"} {
		ix.Put(id, map[string]string{"title": "Grafana", "body": "gleicher Inhalt"})
	}
	hits := ix.Search("grafana", 0, nil)
	if got := fmt.Sprint(hitIDs(hits)); got != "[a b c]" {
		t.Fatalf("tie-break order = %s, want [a b c]", got)
	}
	if hits[0].Score != hits[1].Score || hits[1].Score != hits[2].Score {
		t.Fatalf("identical documents should score identically: %v", hits)
	}
}

func TestSearchFilter(t *testing.T) {
	ix := New()
	for i := 0; i < 6; i++ {
		ix.Put(fmt.Sprintf("doc-%d", i), map[string]string{"body": "Grafana Dashboard"})
	}
	even := func(id string) bool {
		return strings.HasSuffix(id, "0") || strings.HasSuffix(id, "2") || strings.HasSuffix(id, "4")
	}
	hits := ix.Search("grafana", 0, even)
	if got := fmt.Sprint(hitIDs(hits)); got != "[doc-0 doc-2 doc-4]" {
		t.Fatalf("filtered ids = %s, want [doc-0 doc-2 doc-4]", got)
	}
	if hits := ix.Search("grafana", 0, func(string) bool { return false }); hits != nil {
		t.Fatalf("filter rejecting everything should yield nil, got %v", hits)
	}
	// Filter may call back into the index without deadlocking.
	hits = ix.Search("grafana", 0, func(id string) bool { return ix.Len() > 0 })
	if len(hits) != 6 {
		t.Fatalf("re-entrant filter: got %d hits, want 6", len(hits))
	}
}

func TestSearchLimit(t *testing.T) {
	ix := New()
	for i := 0; i < 10; i++ {
		ix.Put(fmt.Sprintf("doc-%02d", i), map[string]string{"body": "Grafana"})
	}
	cases := []struct{ limit, want int }{
		{1, 1}, {3, 3}, {10, 10}, {50, 10}, {0, 10}, {-1, 10},
	}
	for _, c := range cases {
		if got := len(ix.Search("grafana", c.limit, nil)); got != c.want {
			t.Errorf("limit %d: got %d hits, want %d", c.limit, got, c.want)
		}
	}
	// Limit is applied after sorting, so the best hits survive.
	ix.Put("best", map[string]string{"title": "Grafana"})
	if hits := ix.Search("grafana", 1, nil); len(hits) != 1 || hits[0].ID != "best" {
		t.Fatalf("limit 1 should keep the top hit, got %v", hits)
	}
}

func TestSearchDuplicateQueryTokens(t *testing.T) {
	ix := New()
	ix.Put("a", map[string]string{"body": "Grafana"})
	single := ix.Search("grafana", 0, nil)
	double := ix.Search("grafana grafana Grafana", 0, nil)
	if len(single) != 1 || len(double) != 1 || single[0].Score != double[0].Score {
		t.Fatalf("duplicate query tokens should not change the score: %v vs %v", single, double)
	}
}

func TestContainsPhrase(t *testing.T) {
	cases := []struct {
		haystack, needle string
		want             bool
	}{
		{"die solaranlage liefert", "Solaranlage", true},
		{"Die Solaranlage liefert Strom", "solaranlage liefert", true},
		{"Die Solaranlage liefert Strom", "Solaranlagen liefert", true}, // stemming
		{"Die Solaranlage heute liefert", "Solaranlage liefert", false}, // not contiguous
		{"liefert die Solaranlage", "Solaranlage liefert", false},       // wrong order
		{"Heizungsdaten in Grafana", "Heizungsdaten Grafana", true},     // stopword dropped on both sides
		{"Strasse", "Straße", true},
		{"Straße", "strasse", true},
		{"Müller", "Mueller", false}, // ue is not folded to u
		{"Müller", "Muller", true},
		{"E-Mail schreiben", "email", false},
		{"E-Mail schreiben", "Mail", true},
		{"Solaranlage", "Solaranlage liefert", false}, // needle longer than haystack
		{"", "Solaranlage", false},
		{"Solaranlage", "", false},
		{"the", "the", false}, // needle tokenizes to nothing
		{"Grafana Dashboard", "der", false},
		{"Kaffee Milch Zucker", "Milch", true},
		{"Kaffeemilch Zucker", "Milch", false}, // no substring matching inside a token
	}
	for _, c := range cases {
		if got := ContainsPhrase(c.haystack, c.needle); got != c.want {
			t.Errorf("ContainsPhrase(%q, %q) = %v, want %v", c.haystack, c.needle, got, c.want)
		}
	}
}

func TestConcurrentPutSearch(t *testing.T) {
	ix := New()
	const writers, readers, rounds = 4, 4, 200

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				id := fmt.Sprintf("w%d-%d", w, i%20)
				ix.Put(id, map[string]string{
					"title": fmt.Sprintf("Notiz %d Grafana", i),
					"body":  fmt.Sprintf("Heizungsdaten Solaranlage Eintrag %d von Writer %d", i, w),
				})
				if i%3 == 0 {
					ix.Remove(fmt.Sprintf("w%d-%d", w, (i+7)%20))
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				_ = ix.Search("Grafana Heizungsdaten", 5, nil)
				_ = ix.Search("Solaranlagen", 0, func(id string) bool { return ix.Len() >= 0 })
				_ = ix.Len()
				_ = Tokenize("Über die Straße")
			}
		}()
	}
	wg.Wait()

	if n := ix.Len(); n < 1 || n > writers*20 {
		t.Fatalf("unexpected Len after concurrent updates: %d", n)
	}
	hits := ix.Search("grafana", 0, nil)
	if len(hits) != ix.Len() {
		t.Fatalf("every remaining document contains grafana: got %d hits, Len %d", len(hits), ix.Len())
	}
}

func hitIDs(hits []Hit) []string {
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	return ids
}

func BenchmarkSearch(b *testing.B) {
	ix := New()
	words := strings.Fields("grafana heizung solaranlage wechselrichter termin einkaufen notiz projekt server backup netzwerk garten fahrrad rechnung urlaub buch")
	for i := 0; i < 20000; i++ {
		var sb strings.Builder
		for j := 0; j < 12; j++ {
			sb.WriteString(words[(i*7+j*3)%len(words)])
			sb.WriteByte(' ')
		}
		ix.Put(fmt.Sprintf("doc-%d", i), map[string]string{
			"title": words[i%len(words)] + " " + words[(i+5)%len(words)],
			"body":  sb.String(),
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ix.Search("Grafana Heizung Solaranlage", 20, nil)
	}
}
