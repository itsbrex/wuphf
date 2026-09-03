package team

// wiki_index_gbrain_entity_text.go — TextIndex over the recommended
// one-page-per-entity shape.
//
// The retrieval property this exists to exploit
// =============================================
// gbrain returns chunks. Under the atom backend a chunk was one fact, so a
// query had to surface each fact independently and a person's twenty facts had
// to win twenty separate ranking contests. Under this shape a person's whole
// timeline is one page, usually one or two chunks, so ONE hit yields every
// fact about them — recovered by parsing the `^factID` anchors out of the
// chunk text.
//
// That is the concrete mechanism behind the schema doc's claim that
// pre-computed synthesis beats flat RAG, and it is exactly what the atom
// backend's status-query failure was missing.

import (
	"context"
	"strings"

	"github.com/nex-crm/wuphf/internal/gbrain"
)

// gbrainEntityTextIndex implements TextIndex against gbrain's hybrid search.
type gbrainEntityTextIndex struct {
	store *gbrainEntityStore

	// queryFn is the retrieval call. It exists as a seam so the chunk-to-fact
	// mapping below can be tested WITHOUT a live brain.
	//
	// That mapping is where this backend's retrieval quality actually lives —
	// it is what took the bench's status class from 60% to 85% — and it was
	// previously reachable only from opt-in tests that CI never runs. A
	// regression there would have been invisible until someone re-ran the
	// bench by hand.
	//
	// Nil means "use the paired store's client", which is the production path.
	queryFn func(ctx context.Context, query string, limit int) ([]gbrain.Hit, error)
}

// query runs the retrieval call, honouring the test seam.
func (t *gbrainEntityTextIndex) query(ctx context.Context, q string, limit int) ([]gbrain.Hit, error) {
	if t.queryFn != nil {
		return t.queryFn(ctx, q, limit)
	}
	return t.store.client.QueryTypes(ctx, q, limit, entityPageTypes)
}

// Index is a no-op: the paired store writes the entity page, and gbrain chunks
// and embeds it as part of that same put_page.
func (t *gbrainEntityTextIndex) Index(context.Context, TypedFact) error { return nil }

// Delete removes a fact from its entity page and rewrites the page.
//
// Unlike the atom backend this cannot delete a page: the fact is one line among
// many on a shared entity page, so retiring it is a read-modify-write.
func (t *gbrainEntityTextIndex) Delete(ctx context.Context, factID string) error {
	factID = strings.TrimSpace(factID)
	if factID == "" || t.store == nil {
		return nil
	}
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()

	owner, ok := s.factOwner[factID]
	if !ok {
		if err := s.scanAllLocked(ctx); err != nil {
			return err
		}
		if owner, ok = s.factOwner[factID]; !ok {
			return nil // nothing to retire
		}
	}
	st, ok := s.pages[owner]
	if !ok {
		return nil
	}
	if _, present := st.facts[factID]; !present {
		return nil
	}
	delete(st.facts, factID)
	delete(s.factOwner, factID)
	return s.flushPage(ctx, owner, st)
}

// Search maps gbrain chunk hits onto fact-level SearchHits via the anchors.
//
// topK is the number of FACTS to return, but gbrain's limit is a number of
// CHUNKS, and one chunk can carry many facts. Asking gbrain for topK chunks
// would over-fetch wildly; asking for too few would cap recall. The fetch is
// therefore a fraction of topK with a floor, and the loop stops once enough
// facts have accumulated.
func (t *gbrainEntityTextIndex) Search(ctx context.Context, query string, topK int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" || t.store == nil {
		return nil, nil
	}
	if topK <= 0 {
		topK = 10
	}

	// Non-entity pages — category pages, article stubs, anything a human put in
	// the same brain — are excluded server-side by the `types` filter in query.
	// No overfetch is needed: they no longer consume slots in the ranked result.
	//
	// An earlier version of this filtered client-side because it believed the
	// server-side filter was ignored. It was really sending `type` instead of
	// `types`; see Client.QueryTypes.
	results, err := t.query(ctx, query, gbrainChunkFetch(topK))
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, topK)
	hits := make([]SearchHit, 0, topK)

	for _, r := range results {
		entity := entitySlugFromPageSlug(r.Slug)
		if entity == "" {
			// Backstop, not the primary filter — query already restricted the
			// types server-side. This catches category pages written BEFORE
			// they had a type of their own, which are still typed "concept"
			// and so pass the entity-type filter until they are rewritten.
			// It is free: the slug has to be mapped to an entity regardless.
			continue
		}

		// Anchored facts first: these are the exact evidence lines the chunk
		// matched on, so they carry a precise snippet.
		chunk := r.ChunkText
		for _, factID := range parseFactAnchors(chunk) {
			if seen[factID] {
				continue
			}
			seen[factID] = true
			snippet := snippetForFact(chunk, factID)
			if snippet == "" {
				snippet = strings.TrimSpace(chunk)
			}
			hits = append(hits, SearchHit{
				FactID: factID,
				// Facts from one chunk share that chunk's score; gbrain does
				// not expose a per-line score. Order within a chunk is the
				// timeline's reverse-chronological order.
				Score:   r.Score,
				Snippet: snippet,
				Entity:  entity,
			})
			if len(hits) >= topK {
				return hits, nil
			}
		}

		// Then the rest of that entity's facts.
		//
		// A hit can land on the compiled_truth chunk, which carries NO anchors
		// (it is the synthesis, not the evidence log) and is boosted 2x by
		// gbrain's ranking — so relying on anchors alone returns zero facts for
		// exactly the queries the summary answers best. More fundamentally,
		// this is the property the one-page-per-entity shape exists to buy:
		// matching a person's page means the question is about that person, so
		// what we know about them IS the result set. Under the atom backend
		// each of those facts had to win its own ranking contest.
		// Best-effort: a page that cannot be loaded (deleted mid-query, or a
		// slug in an entity directory that is not ours) must not fail the whole
		// search. The anchored hits already gathered are still valid answers.
		rest, err := t.store.ListFactsForEntity(ctx, entity)
		if err != nil {
			continue
		}
		for _, f := range factsForRender(factMap(rest)) {
			if seen[f.ID] {
				continue
			}
			seen[f.ID] = true
			hits = append(hits, SearchHit{
				FactID: f.ID,
				// Ranked below the anchored hits from the same page: the chunk
				// matched those lines, not these.
				Score:   r.Score,
				Snippet: strings.Join(strings.Fields(f.Text), " "),
				Entity:  entity,
			})
			if len(hits) >= topK {
				return hits, nil
			}
		}
	}
	return hits, nil
}

// factMap indexes facts by ID so they can be run through factsForRender, which
// orders newest-first the same way the page itself does.
func factMap(facts []TypedFact) map[string]TypedFact {
	out := make(map[string]TypedFact, len(facts))
	for _, f := range facts {
		out[f.ID] = f
	}
	return out
}

// gbrainChunkFetch converts a desired fact count into a chunk fetch size.
//
// A timeline chunk typically carries several facts, so fetching topK chunks
// would retrieve far more facts than asked for and pay latency for results the
// caller discards. A quarter of topK, floored at 5 and capped at 40, keeps the
// fetch bounded while leaving headroom for chunks that carry only one anchor
// (a compiled-truth chunk, or a page with a single fact).
func gbrainChunkFetch(topK int) int {
	n := topK / 4
	if n < 5 {
		n = 5
	}
	if n > 40 {
		n = 40
	}
	return n
}

// Close releases the index. The paired store owns the gbrain session.
func (t *gbrainEntityTextIndex) Close() error { return nil }

// NewGBrainEntityIndex constructs a WikiIndex whose fact store and text index
// both use gbrain's recommended one-page-per-entity shape.
func NewGBrainEntityIndex(ctx context.Context, root string, opts ...IndexOption) (*WikiIndex, error) {
	store, err := NewGBrainEntityStore(ctx)
	if err != nil {
		return nil, err
	}
	es, ok := store.(*gbrainEntityStore)
	if !ok {
		return nil, errGBrainStoreType
	}
	text := &gbrainEntityTextIndex{store: es}
	all := append([]IndexOption{WithFactStore(store), WithTextIndex(text)}, opts...)
	return NewWikiIndex(root, all...), nil
}

// compile-time assertion that the entity store satisfies the full contract.
var _ FactStore = (*gbrainEntityStore)(nil)

// gbrainEntityClient exposes the underlying client for tests.
func (s *gbrainEntityStore) gbrainEntityClient() *gbrain.Client { return s.client }
