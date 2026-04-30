// Package index implements a BM25 full-text index over PARA notes.
//
// Design: a single writer goroutine owns all mutable state and publishes
// immutable snapshots via an atomic pointer so readers need no locks.
package index

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/kljensen/snowball/english"
	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

const (
	bm25K1            = 1.2
	bm25B             = 0.75
	defaultTitleBoost = 2.0
)

// StemmerKind selects the stemming algorithm.
type StemmerKind string

const (
	StemmerPorter StemmerKind = "porter"
	StemmerNone   StemmerKind = "none"
)

// Option configures an Index.
type Option func(*config)

type config struct {
	stemmer    StemmerKind
	titleBoost float64
	stopWords  []string
}

// WithStemmer sets the stemming algorithm (default: Porter).
func WithStemmer(k StemmerKind) Option {
	return func(c *config) { c.stemmer = k }
}

// WithTitleBoost sets the BM25 title field multiplier (default: 2.0).
func WithTitleBoost(v float64) Option {
	return func(c *config) { c.titleBoost = v }
}

// WithStopWords adds extra stop words on top of the built-in set.
func WithStopWords(words []string) Option {
	return func(c *config) { c.stopWords = words }
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

type posting struct {
	bodyTF  float32
	titleTF float32
}

type docMeta struct {
	ref       domain.NoteRef
	bodyLen   int
	titleLen  int
	updatedAt time.Time
}

type snapshot struct {
	postings map[string]map[string]posting // term -> docKey -> posting
	docs     map[string]docMeta            // docKey -> meta
	avgBody  float64
	avgTitle float64
}

type writeOp struct {
	doc      *Doc
	removeID *domain.NoteRef
	syncDone chan<- struct{}
}

// Index is a BM25 full-text index.
type Index struct {
	cfg       config
	ch        chan writeOp
	snap      atomic.Pointer[snapshot]
	stopWords map[string]bool
	done      chan struct{}
}

// New creates and starts a new Index with the given options.
func New(opts ...Option) *Index {
	cfg := config{stemmer: StemmerPorter, titleBoost: defaultTitleBoost}
	for _, o := range opts {
		o(&cfg)
	}
	sw := buildStopWords(cfg.stopWords)
	idx := &Index{
		cfg:       cfg,
		ch:        make(chan writeOp, 512),
		stopWords: sw,
		done:      make(chan struct{}),
	}
	empty := &snapshot{
		postings: make(map[string]map[string]posting),
		docs:     make(map[string]docMeta),
	}
	idx.snap.Store(empty)
	go idx.writer()
	return idx
}

// Close shuts down the index writer goroutine.
func (idx *Index) Close() {
	close(idx.done)
}

// Add indexes or re-indexes a document.
func (idx *Index) Add(doc Doc) {
	d := doc
	idx.ch <- writeOp{doc: &d}
}

// Remove removes a document from the index.
func (idx *Index) Remove(ref domain.NoteRef) {
	r := ref
	idx.ch <- writeOp{removeID: &r}
}

// Search returns results matching the query, ordered by BM25 score descending.
func (idx *Index) Search(query string, limit int) []Result {
	if query == "" {
		return nil
	}
	snap := idx.snap.Load()
	terms := idx.tokenize(query)
	if len(terms) == 0 {
		return nil
	}

	n := float64(len(snap.docs))
	scores := make(map[string]float64, len(snap.docs))

	for _, term := range terms {
		postMap, ok := snap.postings[term]
		if !ok {
			continue
		}
		df := float64(len(postMap))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))

		for docKey, p := range postMap {
			meta := snap.docs[docKey]

			var bodyScore float64
			if snap.avgBody > 0 {
				norm := 1 - bm25B + bm25B*float64(meta.bodyLen)/snap.avgBody
				tf := float64(p.bodyTF)
				bodyScore = idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*norm)
			}

			var titleScore float64
			if snap.avgTitle > 0 && p.titleTF > 0 {
				norm := 1 - bm25B + bm25B*float64(meta.titleLen)/snap.avgTitle
				tf := float64(p.titleTF)
				titleScore = idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*norm) * idx.cfg.titleBoost
			}

			scores[docKey] += bodyScore + titleScore
		}
	}

	type kv struct {
		key   string
		score float64
	}
	ranked := make([]kv, 0, len(scores))
	for k, s := range scores {
		ranked = append(ranked, kv{k, s})
	}
	slices.SortFunc(ranked, func(a, b kv) int {
		return cmp.Compare(b.score, a.score)
	})

	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}

	results := make([]Result, 0, len(ranked))
	for _, kv := range ranked {
		meta := snap.docs[kv.key]
		results = append(results, Result{
			Ref:       meta.ref,
			Score:     kv.score,
			UpdatedAt: meta.updatedAt,
		})
	}
	return results
}

func (idx *Index) writer() {
	current := idx.snap.Load()
	for {
		select {
		case <-idx.done:
			return
		case op := <-idx.ch:
			if op.syncDone != nil {
				op.syncDone <- struct{}{}
				continue
			}
			current = idx.apply(current, op)
			idx.snap.Store(current)
		}
	}
}

func (idx *Index) apply(old *snapshot, op writeOp) *snapshot {
	next := &snapshot{
		postings: maps.Clone(old.postings),
		docs:     maps.Clone(old.docs),
	}
	for k, v := range old.postings {
		next.postings[k] = maps.Clone(v)
	}

	if op.removeID != nil {
		docKey := docKey(*op.removeID)
		idx.removeDoc(next, docKey)
	} else if op.doc != nil {
		docKey := docKey(op.doc.Ref)
		idx.removeDoc(next, docKey)
		idx.addDoc(next, docKey, op.doc)
	}

	idx.recomputeAvg(next)
	return next
}

func (idx *Index) removeDoc(snap *snapshot, key string) {
	if _, ok := snap.docs[key]; !ok {
		return
	}
	delete(snap.docs, key)
	for term, postMap := range snap.postings {
		delete(postMap, key)
		if len(postMap) == 0 {
			delete(snap.postings, term)
		}
	}
}

func (idx *Index) addDoc(snap *snapshot, key string, doc *Doc) {
	bodyTerms := idx.tokenize(doc.Body)
	titleTerms := idx.tokenize(doc.Title)

	bodyFreq := termFreq(bodyTerms)
	titleFreq := termFreq(titleTerms)

	allTerms := make(map[string]struct{}, len(bodyFreq)+len(titleFreq))
	for t := range bodyFreq {
		allTerms[t] = struct{}{}
	}
	for t := range titleFreq {
		allTerms[t] = struct{}{}
	}

	for term := range allTerms {
		postMap, ok := snap.postings[term]
		if !ok {
			postMap = make(map[string]posting)
			snap.postings[term] = postMap
		}
		postMap[key] = posting{
			bodyTF:  float32(bodyFreq[term]),
			titleTF: float32(titleFreq[term]),
		}
	}

	snap.docs[key] = docMeta{
		ref:       doc.Ref,
		bodyLen:   len(bodyTerms),
		titleLen:  len(titleTerms),
		updatedAt: doc.UpdatedAt,
	}
}

func (idx *Index) recomputeAvg(snap *snapshot) {
	if len(snap.docs) == 0 {
		snap.avgBody = 0
		snap.avgTitle = 0
		return
	}
	var sumBody, sumTitle float64
	for _, m := range snap.docs {
		sumBody += float64(m.bodyLen)
		sumTitle += float64(m.titleLen)
	}
	n := float64(len(snap.docs))
	snap.avgBody = sumBody / n
	snap.avgTitle = sumTitle / n
}

func (idx *Index) tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if idx.stopWords[f] {
			continue
		}
		if idx.cfg.stemmer == StemmerPorter {
			f = english.Stem(f, false)
		}
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func termFreq(terms []string) map[string]int {
	freq := make(map[string]int, len(terms))
	for _, t := range terms {
		freq[t]++
	}
	return freq
}

func docKey(ref domain.NoteRef) string {
	return ref.Scope + ":" + ref.Path
}

func buildStopWords(extra []string) map[string]bool {
	builtin := []string{
		"a", "an", "the", "and", "or", "but", "in", "on", "at", "to", "for",
		"of", "with", "by", "from", "is", "it", "its", "be", "as", "was",
		"are", "were", "been", "has", "have", "had", "do", "does", "did",
		"will", "would", "could", "should", "may", "might", "this", "that",
		"these", "those", "i", "we", "you", "he", "she", "they", "not",
	}
	sw := make(map[string]bool, len(builtin)+len(extra))
	for _, w := range builtin {
		sw[w] = true
	}
	for _, w := range extra {
		sw[strings.ToLower(w)] = true
	}
	return sw
}
