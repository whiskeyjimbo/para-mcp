// Package index implements a BM25 full-text index over PARA notes.
//
// Design: a single writer goroutine owns all mutable state and publishes
// immutable snapshots via an atomic pointer so readers need no locks.
package index

import (
	"maps"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/kljensen/snowball/english"
	"github.com/whiskeyjimbo/paras/domain"
)

const (
	bm25K1            = 1.2
	bm25B             = 0.75
	defaultTitleBoost = 2.0
)

// StemmerKind selects the stemming algorithm.
type StemmerKind string

const (
	StemmerPorter StemmerKind = "porter" // English Porter stemmer (default)
	StemmerNone   StemmerKind = "none"   // no stemming; use for non-English vaults
)

// Config holds per-vault index configuration.
type Config struct {
	Stemmer    StemmerKind // default: StemmerPorter
	TitleBoost float64     // default: 2.0
	StopWords  []string    // merged with built-in English stop words
}

// Doc is the document representation fed into the index.
type Doc struct {
	Ref       domain.NoteRef
	Title     string
	Body      string
	UpdatedAt time.Time
}

// Result is a single search hit with its BM25 score.
type Result struct {
	Ref       domain.NoteRef
	Score     float64
	UpdatedAt time.Time
}

// posting records per-doc term frequencies for one term.
type posting struct {
	bodyTF  float32
	titleTF float32
}

// snapshot is the immutable index state exposed to readers.
type snapshot struct {
	// inv: term -> (ref.String() -> posting)
	inv        map[string]map[string]posting
	docs       map[string]snapDoc
	avgBodyLen float64
}

type snapDoc struct {
	ref       domain.NoteRef
	updatedAt time.Time
	bodyLen   int
}

// mutableDoc is private to the writer goroutine; holds terms for fast removal.
type mutableDoc struct {
	ref       domain.NoteRef
	bodyLen   int
	updatedAt time.Time
	terms     []string // all terms this doc contributes postings for
}

type writeOp struct {
	add      *Doc
	del      *domain.NoteRef
	syncDone chan<- struct{} // non-nil: signal after this batch's snapshot is published
}

// Index is a BM25 full-text index. All writes are serialized through a
// single goroutine; reads use an atomic snapshot pointer with no locking.
type Index struct {
	cfg     Config
	stopSet map[string]bool

	snap atomic.Pointer[snapshot]

	// mutable state — touched only by the writer goroutine
	inv  map[string]map[string]posting // term -> ref -> posting
	docs map[string]mutableDoc         // ref.String() -> doc

	ch   chan writeOp
	done chan struct{}
}

// New creates and starts an Index with the given config.
func New(cfg Config) *Index {
	if cfg.TitleBoost == 0 {
		cfg.TitleBoost = defaultTitleBoost
	}
	if cfg.Stemmer == "" {
		cfg.Stemmer = StemmerPorter
	}
	idx := &Index{
		cfg:     cfg,
		stopSet: buildStopSet(cfg.StopWords),
		inv:     make(map[string]map[string]posting),
		docs:    make(map[string]mutableDoc),
		ch:      make(chan writeOp, 256),
		done:    make(chan struct{}),
	}
	idx.publishSnapshot()
	go idx.writer()
	return idx
}

// Add indexes (or re-indexes) a document. Non-blocking; the write is
// applied asynchronously by the writer goroutine.
func (idx *Index) Add(doc Doc) {
	idx.ch <- writeOp{add: &doc}
}

// Remove deletes a document from the index. Non-blocking.
func (idx *Index) Remove(ref domain.NoteRef) {
	idx.ch <- writeOp{del: &ref}
}

// Close stops the writer goroutine and waits for it to drain.
func (idx *Index) Close() {
	close(idx.ch)
	<-idx.done
}

// Search returns up to limit results for text, ranked by BM25 score.
// Returns nil when text is empty or no docs are indexed.
func (idx *Index) Search(text string, limit int) []Result {
	terms := idx.tokenize(text)
	if len(terms) == 0 {
		return nil
	}
	snap := idx.snap.Load()
	if snap == nil || len(snap.docs) == 0 {
		return nil
	}

	N := float64(len(snap.docs))
	avgDL := snap.avgBodyLen
	if avgDL == 0 {
		avgDL = 1
	}

	scores := make(map[string]float64, 32)
	for _, term := range terms {
		postings, ok := snap.inv[term]
		if !ok {
			continue
		}
		nt := float64(len(postings))
		idf := math.Log((N-nt+0.5)/(nt+0.5) + 1)

		for refStr, p := range postings {
			dl := float64(snap.docs[refStr].bodyLen)
			tf := float64(p.bodyTF)
			body := idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*dl/avgDL))
			title := idf * float64(p.titleTF) * idx.cfg.TitleBoost
			scores[refStr] += body + title
		}
	}

	type hit struct {
		refStr string
		score  float64
	}
	hits := make([]hit, 0, len(scores))
	for r, s := range scores {
		hits = append(hits, hit{r, s})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}

	results := make([]Result, len(hits))
	for i, h := range hits {
		d := snap.docs[h.refStr]
		results[i] = Result{Ref: d.ref, Score: h.score, UpdatedAt: d.updatedAt}
	}
	return results
}

// writer is the single goroutine that processes all write ops.
func (idx *Index) writer() {
	defer close(idx.done)
	for op := range idx.ch {
		var signals []chan<- struct{}
		idx.applyOp(op, &signals)
		// Drain any queued ops before publishing one snapshot.
		for {
			select {
			case op2, ok := <-idx.ch:
				if !ok {
					idx.publishSnapshot()
					for _, s := range signals {
						close(s)
					}
					return
				}
				idx.applyOp(op2, &signals)
			default:
				goto publish
			}
		}
	publish:
		idx.publishSnapshot()
		for _, s := range signals {
			close(s)
		}
	}
	idx.publishSnapshot()
}

func (idx *Index) applyOp(op writeOp, signals *[]chan<- struct{}) {
	if op.add != nil {
		idx.applyAdd(*op.add)
	} else if op.del != nil {
		idx.applyRemove(*op.del)
	}
	if op.syncDone != nil {
		*signals = append(*signals, op.syncDone)
	}
}

func (idx *Index) applyAdd(doc Doc) {
	refStr := doc.Ref.String()
	// Remove stale postings if the doc already exists.
	if _, exists := idx.docs[refStr]; exists {
		idx.removePostings(refStr)
	}

	bodyTerms := idx.tokenize(doc.Body)
	titleTerms := idx.tokenize(doc.Title)
	bodyTF := countTerms(bodyTerms)
	titleTF := countTerms(titleTerms)

	termSet := make(map[string]struct{}, len(bodyTF)+len(titleTF))
	for t := range bodyTF {
		termSet[t] = struct{}{}
	}
	for t := range titleTF {
		termSet[t] = struct{}{}
	}

	terms := make([]string, 0, len(termSet))
	for term := range termSet {
		if idx.inv[term] == nil {
			idx.inv[term] = make(map[string]posting)
		}
		idx.inv[term][refStr] = posting{
			bodyTF:  float32(bodyTF[term]),
			titleTF: float32(titleTF[term]),
		}
		terms = append(terms, term)
	}

	idx.docs[refStr] = mutableDoc{
		ref:       doc.Ref,
		bodyLen:   len(bodyTerms),
		updatedAt: doc.UpdatedAt,
		terms:     terms,
	}
}

func (idx *Index) applyRemove(ref domain.NoteRef) {
	refStr := ref.String()
	idx.removePostings(refStr)
	delete(idx.docs, refStr)
}

func (idx *Index) removePostings(refStr string) {
	md, ok := idx.docs[refStr]
	if !ok {
		return
	}
	for _, term := range md.terms {
		docs := idx.inv[term]
		delete(docs, refStr)
		if len(docs) == 0 {
			delete(idx.inv, term)
		}
	}
}

// publishSnapshot deep-copies mutable state into an immutable snapshot.
func (idx *Index) publishSnapshot() {
	inv := make(map[string]map[string]posting, len(idx.inv))
	for term, docs := range idx.inv {
		dc := make(map[string]posting, len(docs))
		maps.Copy(dc, docs)
		inv[term] = dc
	}

	docs := make(map[string]snapDoc, len(idx.docs))
	var totalBodyLen int
	for r, md := range idx.docs {
		docs[r] = snapDoc{ref: md.ref, updatedAt: md.updatedAt, bodyLen: md.bodyLen}
		totalBodyLen += md.bodyLen
	}

	var avgBodyLen float64
	if len(docs) > 0 {
		avgBodyLen = float64(totalBodyLen) / float64(len(docs))
	}

	idx.snap.Store(&snapshot{inv: inv, docs: docs, avgBodyLen: avgBodyLen})
}

func (idx *Index) tokenize(text string) []string {
	text = strings.ToLower(text)
	raw := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := raw[:0]
	for _, tok := range raw {
		if len(tok) < 2 || idx.stopSet[tok] {
			continue
		}
		if idx.cfg.Stemmer == StemmerPorter {
			tok = english.Stem(tok, false)
		}
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func countTerms(terms []string) map[string]int {
	tf := make(map[string]int, len(terms))
	for _, t := range terms {
		tf[t]++
	}
	return tf
}

func buildStopSet(extra []string) map[string]bool {
	stops := map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"is": true, "it": true, "as": true, "be": true, "was": true,
		"are": true, "were": true, "that": true, "this": true, "not": true,
		"have": true, "has": true, "had": true, "do": true, "did": true,
		"will": true, "would": true, "can": true, "could": true, "may": true,
		"might": true, "shall": true, "should": true,
	}
	for _, w := range extra {
		stops[strings.ToLower(w)] = true
	}
	return stops
}
