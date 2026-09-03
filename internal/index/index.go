// Package index implements a small in-memory full-text search index with
// BM25 ranking, intended for a personal knowledge base of up to roughly 100k
// short documents written in a mix of German and English.
//
// The index has no persistence: it is rebuilt from snapshots on startup and
// updated incrementally through Put and Remove. All methods are safe for
// concurrent use.
package index

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// BM25 parameters.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// Tokenizer parameters.
const (
	// minTokenRunes is the minimum length of a token after folding.
	minTokenRunes = 2
	// minStemRunes is the minimum token length at which suffix stripping is
	// attempted.
	minStemRunes = 6
	// minStemRemainder is the minimum length a token must keep after a suffix
	// has been stripped. It prevents e.g. "zungen" from collapsing to "z".
	minStemRemainder = 3
)

// Hit is a single search result.
type Hit struct {
	ID    string
	Score float64
}

// fieldCount pairs a field name with a count (a term frequency or a field
// length in tokens).
type fieldCount struct {
	field string
	n     int
}

// fieldTF holds the per-field term frequencies of one token in one
// document. Documents have very few fields, so a small slice with linear
// lookup is much cheaper than a map per posting.
type fieldTF []fieldCount

// document records what the index needs to know about one indexed document
// in order to score and to remove it again.
type document struct {
	// lengths holds the token count of every field with at least one token.
	lengths []fieldCount
	// tokens lists the distinct tokens of the document across all fields.
	tokens []string
}

// length returns the token count of a field, or 0 if the field is empty or
// absent.
func (d *document) length(field string) int {
	for _, fc := range d.lengths {
		if fc.field == field {
			return fc.n
		}
	}
	return 0
}

// Index is an in-memory inverted index with BM25 scoring.
// The zero value is not usable; use New.
type Index struct {
	mu sync.RWMutex

	// postings maps token -> document id -> per-field term frequencies.
	postings map[string]map[string]fieldTF
	// docs maps document id -> per-document bookkeeping.
	docs map[string]*document
	// fieldTotal maps field name -> sum of token counts over all documents
	// that have a non-empty value for the field.
	fieldTotal map[string]int
	// fieldDocs maps field name -> number of documents with a non-empty
	// value for the field. Average field lengths are derived from
	// fieldTotal and fieldDocs on demand.
	fieldDocs map[string]int
}

// New returns an empty index.
func New() *Index {
	return &Index{
		postings:   make(map[string]map[string]fieldTF),
		docs:       make(map[string]*document),
		fieldTotal: make(map[string]int),
		fieldDocs:  make(map[string]int),
	}
}

// fieldWeight returns the scoring multiplier of a field.
func fieldWeight(field string) float64 {
	switch field {
	case "title", "aliases":
		return 3.0
	default:
		return 1.0
	}
}

// Put indexes or re-indexes one document. fields maps a field name to its
// text (e.g. "title", "body", "aliases"). Replacing an existing document
// removes its old postings first. A document without any tokens is still
// counted by Len but can never be found by Search.
func (ix *Index) Put(id string, fields map[string]string) {
	// Tokenize outside the lock; this is the expensive part.
	type fieldTerms struct {
		name   string
		tf     map[string]int
		length int
	}
	terms := make([]fieldTerms, 0, len(fields))
	distinct := make(map[string]struct{})
	for name, text := range fields {
		toks := Tokenize(text)
		if len(toks) == 0 {
			continue
		}
		tf := make(map[string]int, len(toks))
		for _, t := range toks {
			tf[t]++
			distinct[t] = struct{}{}
		}
		terms = append(terms, fieldTerms{name: name, tf: tf, length: len(toks)})
	}
	doc := &document{
		lengths: make([]fieldCount, 0, len(terms)),
		tokens:  make([]string, 0, len(distinct)),
	}
	for t := range distinct {
		doc.tokens = append(doc.tokens, t)
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()

	if _, exists := ix.docs[id]; exists {
		ix.removeLocked(id)
	}
	for _, ft := range terms {
		doc.lengths = append(doc.lengths, fieldCount{field: ft.name, n: ft.length})
		ix.fieldTotal[ft.name] += ft.length
		ix.fieldDocs[ft.name]++
		for t, n := range ft.tf {
			byDoc := ix.postings[t]
			if byDoc == nil {
				byDoc = make(map[string]fieldTF)
				ix.postings[t] = byDoc
			}
			byDoc[id] = append(byDoc[id], fieldCount{field: ft.name, n: n})
		}
	}
	ix.docs[id] = doc
}

// Remove deletes a document from the index. Removing an unknown id is a
// no-op.
func (ix *Index) Remove(id string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.removeLocked(id)
}

// removeLocked deletes a document and all its postings. The caller must
// hold the write lock.
func (ix *Index) removeLocked(id string) {
	doc, ok := ix.docs[id]
	if !ok {
		return
	}
	for _, t := range doc.tokens {
		byDoc := ix.postings[t]
		delete(byDoc, id)
		if len(byDoc) == 0 {
			delete(ix.postings, t)
		}
	}
	for _, fc := range doc.lengths {
		ix.fieldTotal[fc.field] -= fc.n
		ix.fieldDocs[fc.field]--
		if ix.fieldDocs[fc.field] <= 0 {
			delete(ix.fieldDocs, fc.field)
			delete(ix.fieldTotal, fc.field)
		}
	}
	delete(ix.docs, id)
}

// Len returns the number of indexed documents.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.docs)
}

// Search ranks documents by BM25 (k1=1.2, b=0.75) over the distinct tokens
// of query, summing per-field weighted scores (title and aliases ×3, other
// fields ×1). The BM25 sum is multiplied by a coverage factor
// (1 + matched/len(queryTokens)), and results are ordered so that a document
// matching more distinct query tokens always ranks above one matching fewer;
// within the same coverage they are sorted by score descending, then id
// ascending. If filter is non-nil, only ids for which filter returns true
// are returned. limit <= 0 means no limit. An empty query (or one consisting
// only of stopwords and short tokens) returns nil, as does a query with no
// matching documents.
func (ix *Index) Search(query string, limit int, filter func(id string) bool) []Hit {
	qtokens := distinctTokens(Tokenize(query))
	if len(qtokens) == 0 {
		return nil
	}

	type candidate struct {
		score   float64
		matched int
	}
	cands := make(map[string]candidate)

	ix.mu.RLock()
	n := float64(len(ix.docs))
	// Average field lengths are computed lazily, once per field per query.
	avgCache := make(map[string]float64, len(ix.fieldDocs))
	avgLen := func(field string) float64 {
		if v, ok := avgCache[field]; ok {
			return v
		}
		var v float64
		if c := ix.fieldDocs[field]; c > 0 {
			v = float64(ix.fieldTotal[field]) / float64(c)
		}
		avgCache[field] = v
		return v
	}
	for _, t := range qtokens {
		byDoc := ix.postings[t]
		if len(byDoc) == 0 {
			continue
		}
		df := float64(len(byDoc))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		for id, tfs := range byDoc {
			doc := ix.docs[id]
			var s float64
			for _, fc := range tfs {
				norm := 1.0
				if avg := avgLen(fc.field); avg > 0 {
					norm = 1 - bm25B + bm25B*float64(doc.length(fc.field))/avg
				}
				tf := float64(fc.n)
				s += fieldWeight(fc.field) * idf * tf * (bm25K1 + 1) / (tf + bm25K1*norm)
			}
			c := cands[id]
			c.score += s
			c.matched++
			cands[id] = c
		}
	}
	ix.mu.RUnlock()

	// Filtering and sorting happen outside the lock so that a filter may
	// safely call back into the index.
	type scored struct {
		Hit
		matched int
	}
	results := make([]scored, 0, len(cands))
	q := float64(len(qtokens))
	for id, c := range cands {
		if filter != nil && !filter(id) {
			continue
		}
		coverage := 1 + float64(c.matched)/q
		results = append(results, scored{
			Hit:     Hit{ID: id, Score: c.score * coverage},
			matched: c.matched,
		})
	}
	if len(results) == 0 {
		return nil
	}
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.matched != b.matched {
			return a.matched > b.matched
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.ID < b.ID
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	hits := make([]Hit, len(results))
	for i, r := range results {
		hits[i] = r.Hit
	}
	return hits
}

// distinctTokens returns the tokens with duplicates removed, preserving the
// order of first occurrence.
func distinctTokens(toks []string) []string {
	if len(toks) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(toks))
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// stopwords holds the (folded, lowercased) words dropped by Tokenize.
var stopwords = func() map[string]struct{} {
	words := []string{
		// German
		"der", "die", "das", "und", "oder", "ein", "eine", "ist", "im", "in",
		"am", "an", "auf", "mit", "für", "von", "zu", "den", "dem", "des",
		"nicht", "ich", "wir", "es", "sich", "noch", "mal", "auch",
		// English
		"the", "a", "an", "and", "or", "of", "to", "in", "on", "for", "is",
		"it", "this", "that", "with", "as", "at", "be", "by",
	}
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[string(foldString(w))] = struct{}{}
	}
	return m
}()

// suffixes are stripped by stem, longest first, at most one per token.
var suffixes = []string{"ungen", "ung", "en", "er", "es", "e", "s", "n"}

// Tokenize splits s into normalized tokens. It is exported so that callers
// can match aliases and phrases the same way the index does.
//
// Rules, in order:
//   - split on any rune that is not a letter or digit (Unicode aware);
//   - lowercase and fold diacritics (ä→a, ö→o, ü→u, é→e, …) and ß→ss;
//   - drop tokens shorter than 2 runes;
//   - drop a small German/English stopword list;
//   - for tokens of at least 6 runes, strip one trailing suffix from
//     "ungen", "ung", "en", "er", "es", "e", "s", "n" (longest first, only
//     once), provided at least 3 runes remain.
//
// The stemmer is deliberately crude; it only needs to be consistent between
// documents and queries, not linguistically correct.
func Tokenize(s string) []string {
	var out []string
	buf := make([]rune, 0, 16)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		tok := string(buf)
		n := len(buf)
		buf = buf[:0]
		if n < minTokenRunes {
			return
		}
		if _, stop := stopwords[tok]; stop {
			return
		}
		out = append(out, stem(tok))
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf = appendFolded(buf, unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return out
}

// stem strips at most one known suffix from tokens of at least minStemRunes
// runes, longest suffix first, skipping suffixes that would leave fewer
// than minStemRemainder runes.
func stem(tok string) string {
	if utf8.RuneCountInString(tok) < minStemRunes {
		return tok
	}
	for _, suf := range suffixes {
		if !strings.HasSuffix(tok, suf) {
			continue
		}
		rest := tok[:len(tok)-len(suf)]
		if utf8.RuneCountInString(rest) >= minStemRemainder {
			return rest
		}
	}
	return tok
}

// foldString lowercases and folds every rune of s.
func foldString(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		out = append(out, appendFolded(nil, unicode.ToLower(r))...)
	}
	return out
}

// appendFolded appends the diacritic-folded form of the lowercase rune r to
// dst. Runes without a folding are appended unchanged.
func appendFolded(dst []rune, r rune) []rune {
	switch r {
	case 'à', 'á', 'â', 'ã', 'ä', 'å', 'ā', 'ă', 'ą':
		return append(dst, 'a')
	case 'æ':
		return append(dst, 'a', 'e')
	case 'ç', 'ć', 'č', 'ĉ', 'ċ':
		return append(dst, 'c')
	case 'ď', 'đ', 'ð':
		return append(dst, 'd')
	case 'è', 'é', 'ê', 'ë', 'ē', 'ė', 'ę', 'ě', 'ĕ':
		return append(dst, 'e')
	case 'ĝ', 'ğ', 'ġ', 'ģ':
		return append(dst, 'g')
	case 'ĥ', 'ħ':
		return append(dst, 'h')
	case 'ì', 'í', 'î', 'ï', 'ī', 'į', 'ı', 'ĭ':
		return append(dst, 'i')
	case 'ĵ':
		return append(dst, 'j')
	case 'ķ':
		return append(dst, 'k')
	case 'ł', 'ľ', 'ĺ', 'ļ':
		return append(dst, 'l')
	case 'ñ', 'ń', 'ň', 'ņ':
		return append(dst, 'n')
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'ø', 'ō', 'ő', 'ŏ':
		return append(dst, 'o')
	case 'œ':
		return append(dst, 'o', 'e')
	case 'ř', 'ŕ', 'ŗ':
		return append(dst, 'r')
	case 'ś', 'š', 'ş', 'ŝ', 'ș':
		return append(dst, 's')
	case 'ß':
		return append(dst, 's', 's')
	case 'ť', 'ţ', 'ț', 'ŧ':
		return append(dst, 't')
	case 'þ':
		return append(dst, 't', 'h')
	case 'ù', 'ú', 'û', 'ü', 'ū', 'ů', 'ű', 'ų', 'ŭ':
		return append(dst, 'u')
	case 'ŵ':
		return append(dst, 'w')
	case 'ý', 'ÿ', 'ŷ':
		return append(dst, 'y')
	case 'ź', 'ż', 'ž':
		return append(dst, 'z')
	default:
		return append(dst, r)
	}
}

// ContainsPhrase reports whether the tokenized haystack contains the
// tokenized needle as a contiguous token sequence. Both sides go through
// Tokenize, so case, diacritics, stopwords and stemming are normalized
// consistently: "Solaranlage" is found in "die solaranlage liefert", and
// "Heizungsdaten Grafana" is found in "Heizungsdaten in Grafana" because the
// stopword "in" is dropped on both sides. A needle that tokenizes to nothing
// (empty, or only stopwords/short tokens) is never contained.
func ContainsPhrase(haystack, needle string) bool {
	n := Tokenize(needle)
	if len(n) == 0 {
		return false
	}
	h := Tokenize(haystack)
	if len(n) > len(h) {
		return false
	}
outer:
	for i := 0; i+len(n) <= len(h); i++ {
		for j := range n {
			if h[i+j] != n[j] {
				continue outer
			}
		}
		return true
	}
	return false
}
