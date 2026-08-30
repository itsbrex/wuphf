package team

// wiki_index_gbrain_text.go — TextIndex backed by gbrain's hybrid search.
//
// This replaces the bleve BM25 index. gbrain's `query` tool runs its own
// pipeline (keyword + vector, fused with RRF at k=60, then normalize, boost,
// cosine re-score, and dedup), so WUPHF's retrieval no longer owns ranking for
// the default path — it owns routing and the typed graph walks only.
//
// Index() is deliberately a no-op
// ===============================
// In the SQLite + bleve pairing, the fact store and the text index are two
// separate stores and each write has to be applied twice. In gbrain they are
// one store: gbrainFactStore.UpsertFact writes the atom page, and gbrain chunks
// and indexes it as part of that same put_page call. Indexing again here would
// double-write every fact.
//
// The consequence is that gbrainTextIndex is NOT independently usable: it must
// be paired with the gbrainFactStore that owns the writes. NewGBrainIndex
// constructs the pair together so the two cannot drift apart.

import (
	"context"
	"strings"
)

// gbrainTextIndex implements TextIndex against gbrain's hybrid search.
type gbrainTextIndex struct {
	store *gbrainFactStore
}

// Index is a no-op: the paired fact store already wrote and indexed the page.
// See the file header.
func (t *gbrainTextIndex) Index(context.Context, TypedFact) error { return nil }

// Delete removes a fact's page from the brain. Unlike Index this is NOT a
// no-op: the FactStore interface has no DeleteFact, so this is the only path
// that retires a fact, and it must actually remove it from the brain.
func (t *gbrainTextIndex) Delete(ctx context.Context, factID string) error {
	factID = strings.TrimSpace(factID)
	if factID == "" {
		return nil
	}
	if t.store == nil {
		return nil
	}
	t.store.writeMu.Lock()
	defer t.store.writeMu.Unlock()
	return t.store.client.DeletePage(ctx, factSlug(factID))
}

// Search runs gbrain's hybrid query and maps the hits onto SearchHit.
//
// Two adaptations are required:
//
//  1. gbrain searches the whole brain. Entity, category, and article pages all
//     match, but SearchHit.FactID must name a fact, so non-atom hits are
//     dropped. The topK is over-fetched to compensate, otherwise a query that
//     matches several entity pages would return fewer than topK facts.
//  2. gbrain returns chunk rows, not facts. Several chunks can belong to one
//     fact page, so hits are deduped on fact ID keeping the best-scoring chunk.
func (t *gbrainTextIndex) Search(ctx context.Context, query string, topK int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = 10
	}
	// Over-fetch so non-fact pages filtered out below do not starve the result.
	results, err := t.store.client.Query(ctx, query, topK*3)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(results))
	hits := make([]SearchHit, 0, topK)
	for _, r := range results {
		factID := factIDFromSlug(r.Slug)
		if factID == "" || seen[factID] {
			continue // not a fact page, or a second chunk of one already taken
		}
		seen[factID] = true
		hits = append(hits, SearchHit{
			FactID:  factID,
			Score:   r.Score,
			Snippet: strings.TrimSpace(r.ChunkText),
		})
		if len(hits) >= topK {
			break
		}
	}

	// Populate Entity: it surfaces as the citation title in QueryAnswer, so an
	// empty value degrades the user-visible answer. gbrain's query result does
	// not carry frontmatter, so each hit costs one page read. That is the main
	// latency cost of this backend versus bleve, and it is bounded by topK.
	if t.store != nil {
		for i := range hits {
			f, ok, err := t.store.GetFact(ctx, hits[i].FactID)
			if err != nil {
				return nil, err
			}
			if ok {
				hits[i].Entity = f.EntitySlug
				if hits[i].Snippet == "" {
					hits[i].Snippet = f.Text
				}
			}
		}
	}
	return hits, nil
}

// Close releases the index. gbrain owns its connection lifecycle per
// invocation, so there is nothing to release.
func (t *gbrainTextIndex) Close() error { return nil }

// NewGBrainIndex constructs a WikiIndex whose fact store AND text index are
// both backed by gbrain, replacing the SQLite + bleve pairing entirely.
//
// The two halves are constructed together and share one store instance: the
// text index reads through the store to hydrate hits, and relies on the store
// having performed the write. They must not be mixed with other backends.
func NewGBrainIndex(ctx context.Context, root string, opts ...IndexOption) (*WikiIndex, error) {
	store, err := NewGBrainFactStore(ctx)
	if err != nil {
		return nil, err
	}
	gs, ok := store.(*gbrainFactStore)
	if !ok {
		return nil, errGBrainStoreType
	}
	text := &gbrainTextIndex{store: gs}
	all := append([]IndexOption{WithFactStore(store), WithTextIndex(text)}, opts...)
	return NewWikiIndex(root, all...), nil
}
