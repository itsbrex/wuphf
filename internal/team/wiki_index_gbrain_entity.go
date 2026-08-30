package team

// wiki_index_gbrain_entity.go — FactStore + TextIndex on gbrain's RECOMMENDED
// one-page-per-entity shape.
//
// See wiki_gbrain_page_render.go for the page format and why it replaced the
// one-page-per-fact backend. The short version: gbrain's own schema doc argues
// that pre-computed per-entity synthesis is the point, and the atom backend
// measured exactly the way that doc predicts flat RAG would.
//
// What this buys over the atom backend
// ====================================
//   - Retrieval: one hit on a person's page yields EVERY fact about them,
//     because the whole timeline is on that page.
//   - Round-trips: ListFactsForEntity is a single get_page instead of a graph
//     traversal plus one get_page per fact.
//   - Enumeration: the corpus is ~38 entity pages instead of 475 fact pages, so
//     it fits under gbrain's 100-row list_pages cap that the atom backend could
//     not paginate past.
//
// What it costs
// =============
//   - UpsertFact is read-modify-write on the entity page. Facts arriving one at
//     a time re-render and re-embed the whole page each time. Bulk reconcile
//     therefore wants UpsertFacts (batched), which is why that exists.
//   - GetFact(id) has no direct index: a fact ID does not name its entity. The
//     store keeps a lazily-built factID -> entity map and falls back to a full
//     scan on a miss.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/nex-crm/wuphf/internal/gbrain"
)

// gbrainEntityStore implements FactStore against one page per entity.
type gbrainEntityStore struct {
	client *gbrain.Client

	mu sync.Mutex
	// pages caches the decoded state of each entity page, keyed by entity slug.
	// It is a write-through cache: every mutation updates it and the brain in
	// the same critical section, so a reader never sees one without the other.
	pages map[string]*entityPageState
	// factOwner maps a fact ID to its entity slug, for GetFact.
	factOwner map[string]string
	// scanned records that a full corpus scan has populated the maps, so a
	// GetFact miss does not re-scan on every call.
	scanned bool
}

// entityPageState is the decoded content of one entity page.
type entityPageState struct {
	entity IndexEntity
	facts  map[string]TypedFact
}

// NewGBrainEntityStore constructs the recommended-shape store.
func NewGBrainEntityStore(ctx context.Context, opts ...gbrain.Option) (FactStore, error) {
	client := gbrain.NewClient(opts...)
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("wiki_index_gbrain_entity: connect: %w", err)
	}
	return &gbrainEntityStore{
		client:    client,
		pages:     map[string]*entityPageState{},
		factOwner: map[string]string{},
	}, nil
}

// loadPage returns the cached state for an entity, reading through to the brain
// on a miss. Callers must hold mu.
func (s *gbrainEntityStore) loadPage(ctx context.Context, kind, slug string) (*entityPageState, error) {
	if st, ok := s.pages[slug]; ok {
		return st, nil
	}
	// The kind decides the directory, so a lookup with the wrong kind would
	// miss a page that exists. Try the caller's kind first, then the others.
	dirs := []string{entityDirForKind(kind)}
	for _, d := range allEntityDirs() {
		if d != dirs[0] {
			dirs = append(dirs, d)
		}
	}
	for _, dir := range dirs {
		page, err := s.client.GetPage(ctx, dir+slug)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entity, facts, err := decodeEntityPage(page.Frontmatter, page.Slug)
		if err != nil {
			// A page in an entity directory without our blobs belongs to
			// someone else (a human's own notes in a shared brain). Treat the
			// slug as unoccupied rather than failing the write.
			continue
		}
		st := &entityPageState{entity: entity, facts: facts}
		s.pages[slug] = st
		for id := range facts {
			s.factOwner[id] = slug
		}
		return st, nil
	}
	st := &entityPageState{
		entity: IndexEntity{Slug: slug, CanonicalSlug: slug, Kind: kind},
		facts:  map[string]TypedFact{},
	}
	s.pages[slug] = st
	return st, nil
}

// flushPage renders and writes an entity page. Callers must hold mu.
func (s *gbrainEntityStore) flushPage(ctx context.Context, slug string, st *entityPageState) error {
	content, err := renderEntityPage(st.entity, st.facts)
	if err != nil {
		return err
	}
	pageSlug := entityPageSlug(st.entity.Kind, slug)
	if _, err := s.client.PutPage(ctx, content, gbrain.PutOptions{
		Slug:        pageSlug,
		IngestedVia: gbrainLinkSource,
	}); err != nil {
		return err
	}
	// put_page does not clear deleted_at; see the note on the atom backend's
	// putPage. Without this a re-created entity stays invisible.
	if err := s.client.RestorePage(ctx, pageSlug); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// --- writes ---------------------------------------------------------------

// UpsertFact files a fact onto its entity's page.
func (s *gbrainEntityStore) UpsertFact(ctx context.Context, f TypedFact) error {
	return s.UpsertFacts(ctx, []TypedFact{f})
}

// UpsertFacts files a batch of facts, writing each touched entity page ONCE.
//
// This is the bulk path: a reconcile that calls UpsertFact per fact would
// re-render and re-embed a person's page once per fact, which is O(facts) page
// writes instead of O(entities).
func (s *gbrainEntityStore) UpsertFacts(ctx context.Context, facts []TypedFact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	touched := map[string]*entityPageState{}
	for _, f := range facts {
		if strings.TrimSpace(f.ID) == "" {
			return fmt.Errorf("gbrain UpsertFact: fact ID is required")
		}
		owner := strings.TrimSpace(f.EntitySlug)
		if owner == "" && f.Triplet != nil {
			owner = tripletRefToEntitySlug(f.Triplet.Subject)
		}
		if owner == "" {
			return fmt.Errorf("gbrain UpsertFact %s: no entity to file under", f.ID)
		}
		st, err := s.loadPage(ctx, f.Kind, owner)
		if err != nil {
			return err
		}
		if st.entity.Kind == "" && f.Kind != "" {
			st.entity.Kind = f.Kind
		}
		st.facts[f.ID] = f
		s.factOwner[f.ID] = owner
		touched[owner] = st
	}

	for _, slug := range sortedPageKeys(touched) {
		if err := s.flushPage(ctx, slug, touched[slug]); err != nil {
			return err
		}
	}
	return s.linkFacts(ctx, facts)
}

// linkFacts maintains the relationship graph: typed entity->entity edges, the
// fourth database primitive the schema doc calls for. Unlike the atom backend
// there are no fact pages, so edges connect entities directly and the typed
// walks read the subject's page to recover the facts behind an edge.
func (s *gbrainEntityStore) linkFacts(ctx context.Context, facts []TypedFact) error {
	type edge struct{ from, to, pred string }
	seen := map[edge]bool{}
	for _, f := range facts {
		if f.Triplet == nil {
			continue
		}
		pred := strings.TrimSpace(f.Triplet.Predicate)
		subj := tripletRefToEntitySlug(f.Triplet.Subject)
		obj := tripletRefToEntitySlug(f.Triplet.Object)
		if pred == "" || subj == "" || obj == "" || subj == obj {
			continue
		}
		e := edge{subj, obj, pred}
		if seen[e] {
			continue
		}
		seen[e] = true

		// Both endpoints must exist as pages or add_link is rejected.
		if err := s.ensureEntityStub(ctx, subj); err != nil {
			return err
		}
		if err := s.ensureEntityStub(ctx, obj); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx,
			entityPageSlug(s.kindOf(subj), subj),
			entityPageSlug(s.kindOf(obj), obj),
			pred, gbrainLinkSource, ""); err != nil {
			return err
		}
	}
	return nil
}

// kindOf returns the cached kind for an entity slug, defaulting to concept.
// Callers must hold mu.
func (s *gbrainEntityStore) kindOf(slug string) string {
	if st, ok := s.pages[slug]; ok {
		return st.entity.Kind
	}
	return ""
}

// ensureEntityStub materialises an entity page so links can attach to it.
// Callers must hold mu.
func (s *gbrainEntityStore) ensureEntityStub(ctx context.Context, slug string) error {
	st, err := s.loadPage(ctx, "", slug)
	if err != nil {
		return err
	}
	if len(st.facts) > 0 {
		return nil // already a real page
	}
	return s.flushPage(ctx, slug, st)
}

// UpsertEntity writes the entity's header, preserving its existing facts.
func (s *gbrainEntityStore) UpsertEntity(ctx context.Context, e IndexEntity) error {
	if strings.TrimSpace(e.Slug) == "" {
		return fmt.Errorf("gbrain UpsertEntity: slug is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadPage(ctx, e.Kind, e.Slug)
	if err != nil {
		return err
	}
	st.entity = e
	return s.flushPage(ctx, e.Slug, st)
}

// UpsertEdge records a typed entity-to-entity edge.
func (s *gbrainEntityStore) UpsertEdge(ctx context.Context, e IndexEdge) error {
	subj := tripletRefToEntitySlug(e.Subject)
	obj := tripletRefToEntitySlug(e.Object)
	pred := strings.TrimSpace(e.Predicate)
	if subj == "" || obj == "" || pred == "" {
		return fmt.Errorf("gbrain UpsertEdge: subject, predicate, and object are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureEntityStub(ctx, subj); err != nil {
		return err
	}
	if err := s.ensureEntityStub(ctx, obj); err != nil {
		return err
	}
	return s.client.AddLink(ctx,
		entityPageSlug(s.kindOf(subj), subj),
		entityPageSlug(s.kindOf(obj), obj),
		pred, gbrainLinkSource, edgeContext(e))
}

// UpsertRedirect records a slug merge as a typed link.
func (s *gbrainEntityStore) UpsertRedirect(ctx context.Context, r Redirect) error {
	from, to := strings.TrimSpace(r.From), strings.TrimSpace(r.To)
	if from == "" || to == "" {
		return fmt.Errorf("gbrain UpsertRedirect: from and to are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureEntityStub(ctx, from); err != nil {
		return err
	}
	if err := s.ensureEntityStub(ctx, to); err != nil {
		return err
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.client.AddLink(ctx,
		entityPageSlug(s.kindOf(from), from),
		entityPageSlug(s.kindOf(to), to),
		gbrainLinkRedirect, gbrainLinkSource, string(payload))
}

// --- reads ----------------------------------------------------------------

// GetFact returns one fact by ID, scanning the corpus once if the owner of that
// ID is not yet known.
func (s *gbrainEntityStore) GetFact(ctx context.Context, id string) (TypedFact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner, ok := s.factOwner[id]; ok {
		if st, ok := s.pages[owner]; ok {
			if f, ok := st.facts[id]; ok {
				return f, true, nil
			}
		}
	}
	if !s.scanned {
		if err := s.scanAllLocked(ctx); err != nil {
			return TypedFact{}, false, err
		}
		if owner, ok := s.factOwner[id]; ok {
			if st, ok := s.pages[owner]; ok {
				if f, ok := st.facts[id]; ok {
					return f, true, nil
				}
			}
		}
	}
	return TypedFact{}, false, nil
}

// scanAllLocked loads every entity page into the cache. Callers must hold mu.
func (s *gbrainEntityStore) scanAllLocked(ctx context.Context) error {
	for _, dir := range allEntityDirs() {
		kept, raw, err := s.client.ListPageBatch(ctx, gbrain.ListPageOptions{
			SlugPrefix: dir,
			Limit:      gbrainListPageSize,
		})
		if err != nil {
			return err
		}
		if raw >= gbrainListPageCap {
			return fmt.Errorf("%w (dir %q hit the %d-row cap)", errCorpusExceedsListCap, dir, gbrainListPageCap)
		}
		for _, meta := range kept {
			slug := entitySlugFromPageSlug(meta.Slug)
			if slug == "" {
				continue
			}
			if _, ok := s.pages[slug]; ok {
				continue
			}
			page, err := s.client.GetPage(ctx, meta.Slug)
			if isNotFound(err) {
				continue
			}
			if err != nil {
				return err
			}
			entity, facts, err := decodeEntityPage(page.Frontmatter, page.Slug)
			if err != nil {
				continue
			}
			s.pages[slug] = &entityPageState{entity: entity, facts: facts}
			for id := range facts {
				s.factOwner[id] = slug
			}
		}
	}
	s.scanned = true
	return nil
}

// ListFactsForEntity returns an entity's facts, ordered by ID.
func (s *gbrainEntityStore) ListFactsForEntity(ctx context.Context, slug string) ([]TypedFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadPage(ctx, "", slug)
	if err != nil {
		return nil, err
	}
	return sortedFacts(st.facts), nil
}

// ListFactsByPredicateObject walks in-edges of the object under the predicate,
// then filters each subject's facts to exact triplet matches.
func (s *gbrainEntityStore) ListFactsByPredicateObject(ctx context.Context, predicate, object string) ([]TypedFact, error) {
	obj := tripletRefToEntitySlug(object)
	if strings.TrimSpace(predicate) == "" || obj == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	edges, err := s.client.Traverse(ctx, entityPageSlug(s.kindOf(obj), obj), predicate, "in", 1)
	if err != nil {
		return nil, err
	}
	var out []TypedFact
	for _, e := range edges {
		subj := entitySlugFromPageSlug(e.FromSlug)
		if subj == "" {
			continue
		}
		st, err := s.loadPage(ctx, "", subj)
		if err != nil {
			return nil, err
		}
		for _, f := range st.facts {
			if f.Triplet != nil && f.Triplet.Predicate == predicate && f.Triplet.Object == object {
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListFactsByTriplet returns facts matching subject+predicate with an object
// prefix filter.
func (s *gbrainEntityStore) ListFactsByTriplet(ctx context.Context, subject, predicate, objectPrefix string) ([]TypedFact, error) {
	subj := tripletRefToEntitySlug(subject)
	if subj == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadPage(ctx, "", subj)
	if err != nil {
		return nil, err
	}
	var out []TypedFact
	for _, f := range st.facts {
		if f.Triplet == nil || f.Triplet.Subject != subject || f.Triplet.Predicate != predicate {
			continue
		}
		if objectPrefix != "" && !strings.HasPrefix(f.Triplet.Object, objectPrefix) {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListEdgesForEntity reconstructs typed edges incident to an entity.
func (s *gbrainEntityStore) ListEdgesForEntity(ctx context.Context, slug string) ([]IndexEdge, error) {
	s.mu.Lock()
	st, err := s.loadPage(ctx, "", slug)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	preds := map[string]bool{}
	for _, f := range st.facts {
		if f.Triplet != nil && strings.TrimSpace(f.Triplet.Predicate) != "" {
			preds[f.Triplet.Predicate] = true
		}
	}
	pageSlug := entityPageSlug(st.entity.Kind, slug)
	s.mu.Unlock()

	var out []IndexEdge
	for _, pred := range sortedStrings(preds) {
		edges, err := s.client.Traverse(ctx, pageSlug, pred, "both", 1)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			from := entitySlugFromPageSlug(e.FromSlug)
			to := entitySlugFromPageSlug(e.ToSlug)
			if from == "" || to == "" {
				continue
			}
			edge := IndexEdge{Subject: from, Predicate: e.LinkType, Object: to}
			applyEdgeContext(&edge, e.Context)
			out = append(out, edge)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		if out[i].Predicate != out[j].Predicate {
			return out[i].Predicate < out[j].Predicate
		}
		return out[i].Object < out[j].Object
	})
	return out, nil
}

// ListReinforcedFactsByPredicate returns reinforced facts, optionally filtered.
func (s *gbrainEntityStore) ListReinforcedFactsByPredicate(ctx context.Context, predicate string) ([]TypedFact, error) {
	all, err := s.ListAllFacts(ctx)
	if err != nil {
		return nil, err
	}
	var out []TypedFact
	for _, f := range all {
		if f.ReinforcedAt == nil {
			continue
		}
		if predicate != "" && (f.Triplet == nil || f.Triplet.Predicate != predicate) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// ListAllFacts returns every fact across every entity page, ordered by ID.
func (s *gbrainEntityStore) ListAllFacts(ctx context.Context) ([]TypedFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.scanAllLocked(ctx); err != nil {
		return nil, err
	}
	merged := map[string]TypedFact{}
	for _, st := range s.pages {
		for id, f := range st.facts {
			merged[id] = f
		}
	}
	return sortedFacts(merged), nil
}

// ListAllFactsPaged returns up to limit facts with ID strictly after afterID.
func (s *gbrainEntityStore) ListAllFactsPaged(ctx context.Context, afterID string, limit int) ([]TypedFact, error) {
	if limit <= 0 {
		limit = 1000
	}
	all, err := s.ListAllFacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TypedFact, 0, limit)
	for _, f := range all {
		if afterID != "" && f.ID <= afterID {
			continue
		}
		out = append(out, f)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// CountFacts returns the number of indexed facts.
func (s *gbrainEntityStore) CountFacts(ctx context.Context) (int, error) {
	all, err := s.ListAllFacts(ctx)
	if err != nil {
		return 0, err
	}
	return len(all), nil
}

// ResolveRedirect follows a redirect link, if one exists.
func (s *gbrainEntityStore) ResolveRedirect(ctx context.Context, slug string) (string, bool, error) {
	s.mu.Lock()
	pageSlug := entityPageSlug(s.kindOf(slug), slug)
	s.mu.Unlock()
	edges, err := s.client.Traverse(ctx, pageSlug, gbrainLinkRedirect, "out", 1)
	if err != nil {
		return "", false, err
	}
	for _, e := range edges {
		if to := entitySlugFromPageSlug(e.ToSlug); to != "" {
			return to, true, nil
		}
	}
	return "", false, nil
}

// IterateEntities invokes fn for every entity, in slug order.
func (s *gbrainEntityStore) IterateEntities(ctx context.Context, fn func(IndexEntity) error) error {
	s.mu.Lock()
	if err := s.scanAllLocked(ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	entities := make([]IndexEntity, 0, len(s.pages))
	for _, st := range s.pages {
		entities = append(entities, st.entity)
	}
	s.mu.Unlock()

	sort.Slice(entities, func(i, j int) bool { return entities[i].Slug < entities[j].Slug })
	for _, e := range entities {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// --- canonical hashes -----------------------------------------------------

// CanonicalHashFacts hashes every fact with ReinforcedAt cleared, matching the
// SQLite and in-memory stores byte for byte.
func (s *gbrainEntityStore) CanonicalHashFacts(ctx context.Context) (string, error) {
	facts, err := s.ListAllFacts(ctx)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range facts {
		f.ReinforcedAt = nil
		b, err := json.Marshal(f)
		if err != nil {
			return "", err
		}
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CanonicalHashAll extends the facts hash over entities and edges.
func (s *gbrainEntityStore) CanonicalHashAll(ctx context.Context) (string, error) {
	h := sha256.New()
	facts, err := s.ListAllFacts(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range facts {
		b, err := json.Marshal(f)
		if err != nil {
			return "", err
		}
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	var entities []IndexEntity
	if err := s.IterateEntities(ctx, func(e IndexEntity) error {
		entities = append(entities, e)
		return nil
	}); err != nil {
		return "", err
	}
	for _, e := range entities {
		b, err := json.Marshal(e)
		if err != nil {
			return "", err
		}
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	for _, e := range entities {
		edges, err := s.ListEdgesForEntity(ctx, e.Slug)
		if err != nil {
			return "", err
		}
		for _, edge := range edges {
			b, err := json.Marshal(edge)
			if err != nil {
				return "", err
			}
			h.Write(b)
			h.Write([]byte{'\n'})
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Close releases the underlying gbrain session.
func (s *gbrainEntityStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// sortedFacts returns a fact map's values ordered by ID.
func sortedFacts(m map[string]TypedFact) []TypedFact {
	out := make([]TypedFact, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// sortedPageKeys returns map keys in ascending order, for deterministic writes.
func sortedPageKeys(m map[string]*entityPageState) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
