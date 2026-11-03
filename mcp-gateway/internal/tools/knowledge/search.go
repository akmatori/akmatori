// Package knowledge implements the built-in runbooks.* and memory.* gateway
// tools: lexical search (BM25) and memory reads and writes against the
// database, with no LLM in the loop.
package knowledge

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// titleWeight weights name + description (memories) or the title
	// (runbooks) against the body.
	titleWeight = 3.0
	// maxQueryRunes caps the query (an alert payload can be long).
	maxQueryRunes = 4000
	// maxQueryTerms keeps the most selective distinct terms of a long query.
	maxQueryTerms = 40
	// excerptRadius is the excerpt half-width in runes.
	excerptRadius = 200
)

// stopwords are common English words that carry no search signal.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "but": true,
	"by": true, "for": true, "from": true, "has": true, "have": true, "if": true, "in": true,
	"into": true, "is": true, "it": true, "its": true, "no": true, "not": true, "of": true, "on": true,
	"or": true, "so": true, "than": true, "that": true, "the": true, "then": true, "there": true,
	"these": true, "this": true, "to": true, "was": true, "were": true, "will": true, "with": true,
	"we": true, "you": true, "your": true, "our": true, "can": true, "do": true, "does": true,
	"when": true, "what": true, "which": true, "who": true, "how": true, "all": true, "any": true,
}

// compoundSeparators are kept inside a token (hostnames, IPs, paths) and
// split for the extra part tokens.
const compoundSeparators = ".-_/:"

func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(compoundSeparators, r)
}

// tokenize lowercases text and returns its tokens: whole compound tokens
// (nginx-edge-03, 10.0.0.1, /var/lib) plus their parts, without stopwords
// and tokens shorter than 2 runes.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	var out []string
	for _, raw := range strings.FieldsFunc(text, func(r rune) bool { return !isTokenRune(r) }) {
		tok := strings.Trim(raw, compoundSeparators)
		if tok == "" {
			continue
		}
		parts := strings.FieldsFunc(tok, func(r rune) bool { return strings.ContainsRune(compoundSeparators, r) })
		if len(parts) > 1 && keepToken(tok) {
			out = append(out, tok)
		}
		for _, p := range parts {
			if keepToken(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func keepToken(t string) bool {
	return utf8.RuneCountInString(t) >= 2 && !stopwords[t]
}

// document is one searchable row: Title holds the weighted fields.
type document struct {
	ID        uint
	Title     string
	Body      string
	UpdatedAt time.Time
}

type indexedDoc struct {
	doc      document
	titleTF  map[string]int
	bodyTF   map[string]int
	titleLen int
	bodyLen  int
}

// scored is one search hit.
type scored struct {
	Doc   document
	Score float64
	Terms []string // matched query terms
}

func termFreq(tokens []string) map[string]int {
	tf := make(map[string]int, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}
	return tf
}

// search scores docs against query with BM25 over two weighted fields and
// returns at most limit hits with a positive score, best first; ties go to
// the newest updated_at, then the lowest id.
func search(docs []document, query string, limit int) []scored {
	if r := []rune(query); len(r) > maxQueryRunes {
		query = string(r[:maxQueryRunes])
	}
	idx := make([]indexedDoc, len(docs))
	df := map[string]int{}
	var sumLen float64
	for i, d := range docs {
		tt := tokenize(d.Title)
		bt := tokenize(d.Body)
		idx[i] = indexedDoc{doc: d, titleTF: termFreq(tt), bodyTF: termFreq(bt), titleLen: len(tt), bodyLen: len(bt)}
		seen := map[string]bool{}
		for _, t := range tt {
			seen[t] = true
		}
		for _, t := range bt {
			seen[t] = true
		}
		for t := range seen {
			df[t]++
		}
		sumLen += titleWeight*float64(len(tt)) + float64(len(bt))
	}
	if len(docs) == 0 {
		return nil
	}
	avgLen := sumLen / float64(len(docs))
	if avgLen == 0 {
		avgLen = 1
	}

	terms := queryTerms(query, df)
	if len(terms) == 0 {
		return nil
	}
	n := float64(len(docs))
	var hits []scored
	for _, d := range idx {
		docLen := titleWeight*float64(d.titleLen) + float64(d.bodyLen)
		var score float64
		var matched []string
		for _, t := range terms {
			tf := titleWeight*float64(d.titleTF[t]) + float64(d.bodyTF[t])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			score += idf * tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*docLen/avgLen))
			matched = append(matched, t)
		}
		if score > 0 {
			hits = append(hits, scored{Doc: d.doc, Score: score, Terms: matched})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Doc.UpdatedAt.Equal(hits[j].Doc.UpdatedAt) {
			return hits[i].Doc.UpdatedAt.After(hits[j].Doc.UpdatedAt)
		}
		return hits[i].Doc.ID < hits[j].Doc.ID
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// queryTerms returns the distinct query terms that occur in the corpus,
// keeping the maxQueryTerms most selective (lowest document frequency).
func queryTerms(query string, df map[string]int) []string {
	seen := map[string]bool{}
	var terms []string
	for _, t := range tokenize(query) {
		if seen[t] || df[t] == 0 {
			continue
		}
		seen[t] = true
		terms = append(terms, t)
	}
	if len(terms) > maxQueryTerms {
		sort.SliceStable(terms, func(i, j int) bool { return df[terms[i]] < df[terms[j]] })
		terms = terms[:maxQueryTerms]
	}
	return terms
}

// excerpt returns about 2*excerptRadius runes of body around the densest
// cluster of matched terms, cut at line ends where possible, with … at a cut.
func excerpt(body string, terms []string) string {
	runes := []rune(body)
	if len(runes) <= 2*excerptRadius {
		return strings.TrimSpace(body)
	}
	lower := []rune(strings.ToLower(body))
	var positions []int
	for _, t := range terms {
		tr := []rune(t)
		for i := 0; i+len(tr) <= len(lower); i++ {
			if string(lower[i:i+len(tr)]) == t {
				positions = append(positions, i)
			}
		}
	}
	center := 0
	if len(positions) > 0 {
		sort.Ints(positions)
		best, bestCount := positions[0], 0
		for i, p := range positions {
			count := 0
			for _, q := range positions[i:] {
				if q-p > 2*excerptRadius {
					break
				}
				count++
			}
			if count > bestCount {
				best, bestCount = p, count
			}
		}
		center = best + excerptRadius/2
	}
	start := center - excerptRadius
	if start < 0 {
		start = 0
	}
	end := start + 2*excerptRadius
	if end > len(runes) {
		end = len(runes)
		start = end - 2*excerptRadius
		if start < 0 {
			start = 0
		}
	}
	// Prefer line boundaries within 60 runes of the cut.
	for i := start; i > 0 && start-i < 60; i-- {
		if runes[i-1] == '\n' {
			start = i
			break
		}
	}
	for i := end; i < len(runes) && i-end < 60; i++ {
		if runes[i] == '\n' {
			end = i
			break
		}
	}
	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

// memoryTitle is the weighted field of a memory: name (as words) plus
// description.
func memoryTitle(name, description string) string {
	return strings.ReplaceAll(name, "-", " ") + " " + name + " " + description
}
