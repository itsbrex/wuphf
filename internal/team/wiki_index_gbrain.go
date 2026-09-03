package team

// wiki_index_gbrain.go — FactStore backed by gbrain.
//
// This replaces the SQLite fact store: facts, entities, edges, redirects, and
// the category layer all live in the brain (PGLite or Postgres) rather than in
// wiki.sqlite. Everything above the FactStore interface — the retrieval
// routing, QueryHandler, the 19 /wiki/* HTTP routes, and the web article
// surface — is unchanged.
//
// Transport is the gbrain MCP client from internal/gbrain/mcp.go, which holds
// one session for the process. The alternative (shelling out to `gbrain call`)
// would pay a process spawn per operation, and this store issues many small
// operations per fact.
//
// Write amplification, and why it is accepted
// ============================================
// gbrain has no subject/predicate/object table. The triplet is reconstructed
// from typed links, which means one fact costs up to four writes:
//
//	1. the atom page itself                                    (the record)
//	2. entities/<entity> --wuphf_fact_of--> atoms/<id>         (ListFactsForEntity)
//	3. atoms/<id> --<predicate>--> entities/<object>           (ListFactsByPredicateObject)
//	4. entities/<subject> --<predicate>--> entities/<object>   (the entity graph / IndexEdge)
//
// Links 3 and 4 share a link type and are told apart by the from_slug prefix.
// Under PGLite this is a single-writer engine, so writes are serialised behind
// writeMu; a bulk reconcile is materially slower than the SQLite store it
// replaces. That is the honest cost of not having a native fact table.
//
// Read amplification
// ==================
// gbrain's list_pages returns metadata WITHOUT frontmatter, and frontmatter is
// where the authoritative record lives. Every full scan (ListAllFacts,
// IterateEntities, both canonical hashes) therefore costs one list plus one
// get_page per row. Callers that scan the whole corpus on a hot path will feel
// this; the SQLite store answered the same questions in one query.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nex-crm/wuphf/internal/gbrain"
)

// gbrainLinkFact is the ownership edge from an entity to each of its facts.
const gbrainLinkFact = "wuphf_fact_of"

// gbrainListPageSize bounds each list_pages round-trip during a full scan.
const gbrainListPageSize = 500

// errGBrainStoreType guards the NewGBrainIndex pairing invariant: the text
// index reads through the concrete store, so a substituted FactStore would
// silently lose citation titles rather than fail.
var errGBrainStoreType = errors.New("wiki_index_gbrain: fact store is not a *gbrainFactStore")

// gbrainFactStore implements FactStore against a gbrain brain.
//
// Writes are serialised: PGLite is single-writer, and concurrent writers
// surface as "Timed out waiting for PGLite lock" rather than as a queued write.
type gbrainFactStore struct {
	client  *gbrain.Client
	writeMu sync.Mutex

	// knownEntities memoises entity slugs already materialised as pages, so a
	// bulk reconcile does not pay a get_page per link endpoint per fact. Guarded
	// by writeMu, which every write path already holds.
	knownEntities map[string]bool
}

// NewGBrainFactStore constructs a FactStore over a gbrain MCP client.
//
// It fails fast when the brain is unreachable: a context layer that silently
// degraded to an empty brain would answer "no facts found" for every query,
// which reads as a product bug rather than as the outage it is.
func NewGBrainFactStore(ctx context.Context, opts ...gbrain.Option) (FactStore, error) {
	client := gbrain.NewClient(opts...)
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("wiki_index_gbrain: connect: %w", err)
	}
	return &gbrainFactStore{client: client, knownEntities: map[string]bool{}}, nil
}

// isNotFound reports whether an error is gbrain's "page does not exist" signal.
// gbrain returns this as a tool error rather than an empty result, so the
// FactStore's (value, false, nil) contract has to be recovered from the text.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

// getPage adapts Client.GetPage onto the (value, found, error) contract.
func (s *gbrainFactStore) getPage(ctx context.Context, slug string) (gbrain.Page, bool, error) {
	page, err := s.client.GetPage(ctx, slug)
	if isNotFound(err) {
		return gbrain.Page{}, false, nil
	}
	if err != nil {
		return gbrain.Page{}, false, err
	}
	return page, true, nil
}

// --- writes ---------------------------------------------------------------

// putPage writes a page and clears any soft-delete tombstone on it.
//
// gbrain's put_page does NOT reset deleted_at: writing to a previously deleted
// slug updates the row but leaves it invisible to get_page and to search. That
// makes a delete-then-re-extract cycle lose the fact permanently, which is
// silent data loss rather than a visible failure. Every write therefore goes
// through here. The restore is idempotent on a live page, and its not-found
// error on a brand-new slug is ignored.
func (s *gbrainFactStore) putPage(ctx context.Context, slug, content string) error {
	if _, err := s.client.PutPage(ctx, content, gbrain.PutOptions{
		Slug:        slug,
		IngestedVia: gbrainLinkSource,
	}); err != nil {
		return err
	}
	// Only needed before 0.48; see gbrain.NeedsPutPageRestore.
	if gbrain.NeedsPutPageRestore(ctx) {
		if err := s.client.RestorePage(ctx, slug); err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

// ensureEntityPage materialises a stub page for an entity slug if none exists.
//
// gbrain's add_link REJECTS an edge whose endpoint page is missing
// ("addLink failed: page ... not found"). Facts routinely reference entities
// the reconcile loop has not written yet — a triplet object naming a project,
// or a subject seen in an artifact before its own article exists — so without
// this every such link fails and the typed graph walks return nothing. That
// failure mode is what made multi_hop retrieval collapse: the walks were inert
// while BM25 quietly carried the whole result.
//
// The stub is deliberately minimal. A later UpsertEntity overwrites it with the
// real record; this only guarantees the link endpoint exists.
//
// Callers must hold writeMu.
func (s *gbrainFactStore) ensureEntityPage(ctx context.Context, slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil
	}
	if s.knownEntities[slug] {
		return nil
	}
	full := entitySlug(slug)
	if _, ok, err := s.getPage(ctx, full); err != nil {
		return err
	} else if ok {
		s.knownEntities[slug] = true
		return nil
	}
	fm := map[string]string{"type": "concept", "wuphf_slug": slug}
	if err := s.putPage(ctx, full, buildPageContent(fm, slug)); err != nil {
		return err
	}
	s.knownEntities[slug] = true
	return nil
}

// UpsertFact writes the fact page and reconciles its graph edges.
func (s *gbrainFactStore) UpsertFact(ctx context.Context, f TypedFact) error {
	if strings.TrimSpace(f.ID) == "" {
		return fmt.Errorf("gbrain UpsertFact: fact ID is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	blob, err := encodeBlob(f)
	if err != nil {
		return fmt.Errorf("gbrain UpsertFact %s: encode: %w", f.ID, err)
	}
	fm := map[string]string{
		"type":            gbrain.PageTypeAtom,
		gbrainFactBlobKey: blob,
		"wuphf_fact_id":   f.ID,
		"wuphf_entity":    f.EntitySlug,
	}
	// Advisory, human-readable keys so `gbrain get atoms/<id>` is legible to a
	// person browsing the brain. Never read back; the blob is authoritative.
	if f.Triplet != nil {
		fm["subject"] = f.Triplet.Subject
		fm["predicate"] = f.Triplet.Predicate
		fm["object"] = f.Triplet.Object
	}

	// The body is what gbrain chunks and indexes, so it must be the fact's
	// natural-language text — that is what the search leg matches against.
	if err := s.putPage(ctx, factSlug(f.ID), buildPageContent(fm, f.Text)); err != nil {
		return err
	}

	// Ownership edge: entity -> fact. Serves ListFactsForEntity.
	if slug := strings.TrimSpace(f.EntitySlug); slug != "" {
		if err := s.ensureEntityPage(ctx, slug); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx, entitySlug(slug), factSlug(f.ID), gbrainLinkFact, gbrainLinkSource, ""); err != nil {
			return err
		}
	}

	if f.Triplet == nil {
		return nil
	}
	pred := strings.TrimSpace(f.Triplet.Predicate)
	subj := tripletRefToEntitySlug(f.Triplet.Subject)
	obj := tripletRefToEntitySlug(f.Triplet.Object)
	if pred == "" || obj == "" {
		return nil
	}

	// A triplet whose subject differs from EntitySlug must still be reachable
	// from the subject entity.
	if subj != "" && subj != strings.TrimSpace(f.EntitySlug) {
		if err := s.ensureEntityPage(ctx, subj); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx, entitySlug(subj), factSlug(f.ID), gbrainLinkFact, gbrainLinkSource, ""); err != nil {
			return err
		}
	}
	// Fact -> object under the predicate. This is what makes
	// ListFactsByPredicateObject a single directed traversal.
	if err := s.ensureEntityPage(ctx, obj); err != nil {
		return err
	}
	if err := s.client.AddLink(ctx, factSlug(f.ID), entitySlug(obj), pred, gbrainLinkSource, ""); err != nil {
		return err
	}
	// Subject -> object under the predicate: the entity graph proper.
	if subj != "" {
		if err := s.ensureEntityPage(ctx, subj); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx, entitySlug(subj), entitySlug(obj), pred, gbrainLinkSource, ""); err != nil {
			return err
		}
	}
	return nil
}

// UpsertEntity writes the entity page.
func (s *gbrainFactStore) UpsertEntity(ctx context.Context, e IndexEntity) error {
	if strings.TrimSpace(e.Slug) == "" {
		return fmt.Errorf("gbrain UpsertEntity: slug is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	blob, err := encodeBlob(e)
	if err != nil {
		return fmt.Errorf("gbrain UpsertEntity %s: encode: %w", e.Slug, err)
	}
	fm := map[string]string{
		"type":              entityKindToPageType(e.Kind),
		gbrainEntityBlobKey: blob,
		"wuphf_slug":        e.Slug,
		"wuphf_kind":        e.Kind,
	}
	// The body seeds gbrain's text index with the entity's identifying signals,
	// so a query naming the person also retrieves the entity page itself.
	body := strings.TrimSpace(strings.Join([]string{
		e.Signals.PersonName, e.Signals.JobTitle, e.Signals.Email, e.Signals.Domain,
		strings.Join(e.Aliases, " "),
	}, " "))
	if body == "" {
		body = e.Slug
	}
	if err := s.putPage(ctx, entitySlug(e.Slug), buildPageContent(fm, body)); err != nil {
		return err
	}
	s.knownEntities[e.Slug] = true
	return nil
}

// UpsertEdge records a typed edge. Timestamp and source SHA ride in the link's
// context payload, which gbrain round-trips verbatim.
func (s *gbrainFactStore) UpsertEdge(ctx context.Context, e IndexEdge) error {
	subj := tripletRefToEntitySlug(e.Subject)
	obj := tripletRefToEntitySlug(e.Object)
	pred := strings.TrimSpace(e.Predicate)
	if subj == "" || obj == "" || pred == "" {
		return fmt.Errorf("gbrain UpsertEdge: subject, predicate, and object are required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.ensureEntityPage(ctx, subj); err != nil {
		return err
	}
	if err := s.ensureEntityPage(ctx, obj); err != nil {
		return err
	}
	return s.client.AddLink(ctx, entitySlug(subj), entitySlug(obj), pred, gbrainLinkSource, edgeContext(e))
}

// UpsertRedirect records a slug merge as a typed link.
func (s *gbrainFactStore) UpsertRedirect(ctx context.Context, r Redirect) error {
	from, to := strings.TrimSpace(r.From), strings.TrimSpace(r.To)
	if from == "" || to == "" {
		return fmt.Errorf("gbrain UpsertRedirect: from and to are required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := s.ensureEntityPage(ctx, from); err != nil {
		return err
	}
	if err := s.ensureEntityPage(ctx, to); err != nil {
		return err
	}
	return s.client.AddLink(ctx, entitySlug(from), entitySlug(to),
		gbrainLinkRedirect, gbrainLinkSource, string(payload))
}

// edgeContext serialises the non-topological edge fields for link context.
func edgeContext(e IndexEdge) string {
	b, err := json.Marshal(struct {
		Timestamp string `json:"timestamp"`
		SourceSHA string `json:"source_sha"`
	}{e.Timestamp.UTC().Format(time.RFC3339), e.SourceSHA})
	if err != nil {
		return ""
	}
	return string(b)
}

// --- fact reads -----------------------------------------------------------

// GetFact reads one fact by ID.
func (s *gbrainFactStore) GetFact(ctx context.Context, id string) (TypedFact, bool, error) {
	page, ok, err := s.getPage(ctx, factSlug(id))
	if err != nil || !ok {
		return TypedFact{}, false, err
	}
	f, err := factFromPage(page)
	if err != nil {
		return TypedFact{}, false, err
	}
	return f, true, nil
}

// factFromPage decodes the authoritative blob back into a TypedFact.
func factFromPage(page gbrain.Page) (TypedFact, error) {
	raw, ok := blobFromFrontmatter(page.Frontmatter, gbrainFactBlobKey)
	if !ok {
		return TypedFact{}, fmt.Errorf("gbrain page %s: missing %s frontmatter", page.Slug, gbrainFactBlobKey)
	}
	var f TypedFact
	if err := decodeBlob(raw, &f); err != nil {
		return TypedFact{}, fmt.Errorf("gbrain page %s: %w", page.Slug, err)
	}
	return f, nil
}

// entityFromPage decodes the authoritative blob back into an IndexEntity.
func entityFromPage(page gbrain.Page) (IndexEntity, error) {
	raw, ok := blobFromFrontmatter(page.Frontmatter, gbrainEntityBlobKey)
	if !ok {
		return IndexEntity{}, fmt.Errorf("gbrain page %s: missing %s frontmatter", page.Slug, gbrainEntityBlobKey)
	}
	var e IndexEntity
	if err := decodeBlob(raw, &e); err != nil {
		return IndexEntity{}, fmt.Errorf("gbrain page %s: %w", page.Slug, err)
	}
	return e, nil
}

// hydrateFacts fetches and decodes facts for the given fact slugs, skipping any
// that have vanished. Results are sorted by fact ID so callers see a
// deterministic order regardless of traversal order.
func (s *gbrainFactStore) hydrateFacts(ctx context.Context, slugs []string) ([]TypedFact, error) {
	seen := make(map[string]bool, len(slugs))
	out := make([]TypedFact, 0, len(slugs))
	for _, slug := range slugs {
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		page, ok, err := s.getPage(ctx, slug)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		f, err := factFromPage(page)
		if err != nil {
			// A page under atoms/ without our blob is not ours to interpret.
			// Skip rather than fail the query: a brain shared with a human may
			// legitimately contain foreign atoms.
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListFactsForEntity returns every fact hanging off an entity.
func (s *gbrainFactStore) ListFactsForEntity(ctx context.Context, slug string) ([]TypedFact, error) {
	edges, err := s.client.Traverse(ctx, entitySlug(slug), gbrainLinkFact, "out", 1)
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(edges))
	for _, e := range edges {
		slugs = append(slugs, e.ToSlug)
	}
	return s.hydrateFacts(ctx, slugs)
}

// ListEdgesForEntity reconstructs typed edges incident to an entity.
//
// gbrain's traversal is per-link-type and there is no "all types" edge query,
// so this derives the type set from the entity's own facts and walks each one.
// An edge written by UpsertEdge for a predicate that no fact uses is therefore
// invisible here. The extractor always writes the fact and the edge together,
// so that gap is not reachable through the normal path — but it IS a real
// narrowing versus the SQLite store, which listed edges independently of facts.
func (s *gbrainFactStore) ListEdgesForEntity(ctx context.Context, slug string) ([]IndexEdge, error) {
	facts, err := s.ListFactsForEntity(ctx, slug)
	if err != nil {
		return nil, err
	}
	preds := map[string]bool{}
	for _, f := range facts {
		if f.Triplet != nil && strings.TrimSpace(f.Triplet.Predicate) != "" {
			preds[f.Triplet.Predicate] = true
		}
	}
	var out []IndexEdge
	for _, pred := range sortedStrings(preds) {
		edges, err := s.client.Traverse(ctx, entitySlug(slug), pred, "both", 1)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			// Skip fact->object edges; those are not entity-graph edges.
			if strings.HasPrefix(e.FromSlug, gbrainFactPrefix) {
				continue
			}
			from := entitySlugFromGBrain(e.FromSlug)
			to := entitySlugFromGBrain(e.ToSlug)
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

// applyEdgeContext restores timestamp and source SHA from a link context.
func applyEdgeContext(edge *IndexEdge, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	var payload struct {
		Timestamp string `json:"timestamp"`
		SourceSHA string `json:"source_sha"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return
	}
	edge.SourceSHA = payload.SourceSHA
	if ts, err := time.Parse(time.RFC3339, payload.Timestamp); err == nil {
		edge.Timestamp = ts
	}
}

// ListFactsByPredicateObject walks fact->object edges under one predicate.
func (s *gbrainFactStore) ListFactsByPredicateObject(ctx context.Context, predicate, object string) ([]TypedFact, error) {
	obj := tripletRefToEntitySlug(object)
	if strings.TrimSpace(predicate) == "" || obj == "" {
		return nil, nil
	}
	edges, err := s.client.Traverse(ctx, entitySlug(obj), predicate, "in", 1)
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(edges))
	for _, e := range edges {
		// Only fact pages; the subject->object entity edge shares this type.
		if strings.HasPrefix(e.FromSlug, gbrainFactPrefix) {
			slugs = append(slugs, e.FromSlug)
		}
	}
	facts, err := s.hydrateFacts(ctx, slugs)
	if err != nil {
		return nil, err
	}
	// The traversal matches on the RESOLVED entity slug, so a fact whose object
	// is a differently-qualified reference to the same entity would also land
	// here. Filter to an exact triplet match to preserve the SQLite contract.
	out := make([]TypedFact, 0, len(facts))
	for _, f := range facts {
		if f.Triplet != nil && f.Triplet.Predicate == predicate && f.Triplet.Object == object {
			out = append(out, f)
		}
	}
	return out, nil
}

// ListFactsByTriplet returns facts matching subject+predicate with an object
// prefix filter.
func (s *gbrainFactStore) ListFactsByTriplet(ctx context.Context, subject, predicate, objectPrefix string) ([]TypedFact, error) {
	subj := tripletRefToEntitySlug(subject)
	if subj == "" {
		return nil, nil
	}
	facts, err := s.ListFactsForEntity(ctx, subj)
	if err != nil {
		return nil, err
	}
	var out []TypedFact
	for _, f := range facts {
		if f.Triplet == nil {
			continue
		}
		if f.Triplet.Subject != subject || f.Triplet.Predicate != predicate {
			continue
		}
		if objectPrefix != "" && !strings.HasPrefix(f.Triplet.Object, objectPrefix) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// ListReinforcedFactsByPredicate scans for reinforced facts. An empty predicate
// matches all of them.
func (s *gbrainFactStore) ListReinforcedFactsByPredicate(ctx context.Context, predicate string) ([]TypedFact, error) {
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

// ListAllFacts returns every fact, ordered by ID.
func (s *gbrainFactStore) ListAllFacts(ctx context.Context) ([]TypedFact, error) {
	metas, err := s.allPageMetas(ctx, gbrainFactPrefix, gbrain.PageTypeAtom)
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(metas))
	for _, m := range metas {
		slugs = append(slugs, m.Slug)
	}
	return s.hydrateFacts(ctx, slugs)
}

// ListAllFactsPaged returns up to limit facts with ID strictly after afterID.
//
// gbrain paginates by offset, not by key, so this scans the listing and slices
// by ID. The caller's memory stays bounded, but the read cost does not: unlike
// the SQLite store's keyset query, this is proportional to the corpus on every
// call.
func (s *gbrainFactStore) ListAllFactsPaged(ctx context.Context, afterID string, limit int) ([]TypedFact, error) {
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
func (s *gbrainFactStore) CountFacts(ctx context.Context) (int, error) {
	metas, err := s.allPageMetas(ctx, gbrainFactPrefix, gbrain.PageTypeAtom)
	if err != nil {
		return 0, err
	}
	return len(metas), nil
}

// allPageMetas lists every page under a slug prefix, cursor-paginated.
//
// gbrain's list_pages caps at ~100 rows and silently drops `offset`, so this
// delegates to Client.ListAllPages, which walks `updated_after` ascending. The
// earlier version of this function returned an error above the cap because no
// cursor existed yet.
func (s *gbrainFactStore) allPageMetas(ctx context.Context, prefix, pageType string) ([]gbrain.PageMeta, error) {
	return s.client.ListAllPages(ctx, gbrain.ListPageOptions{
		Type:       pageType,
		SlugPrefix: prefix,
		Limit:      gbrainListPageSize,
	})
}

// ResolveRedirect follows a redirect link, if one exists.
func (s *gbrainFactStore) ResolveRedirect(ctx context.Context, slug string) (string, bool, error) {
	edges, err := s.client.Traverse(ctx, entitySlug(slug), gbrainLinkRedirect, "out", 1)
	if err != nil {
		return "", false, err
	}
	for _, e := range edges {
		if to := entitySlugFromGBrain(e.ToSlug); to != "" {
			return to, true, nil
		}
	}
	return "", false, nil
}

// IterateEntities invokes fn for every entity page, in slug order.
func (s *gbrainFactStore) IterateEntities(ctx context.Context, fn func(IndexEntity) error) error {
	metas, err := s.allPageMetas(ctx, gbrainEntityPrefix, "")
	if err != nil {
		return err
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Slug < metas[j].Slug })
	for _, m := range metas {
		page, ok, err := s.getPage(ctx, m.Slug)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		e, err := entityFromPage(page)
		if err != nil {
			continue // foreign page in the entities/ namespace
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// --- canonical hashes -----------------------------------------------------

// CanonicalHashFacts hashes every fact with ReinforcedAt cleared, matching the
// SQLite and in-memory stores byte for byte, so the cross-backend contract
// tests hold against the same corpus.
func (s *gbrainFactStore) CanonicalHashFacts(ctx context.Context) (string, error) {
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

// CanonicalHashAll extends the facts hash over entities and edges. Facts keep
// their ReinforcedAt here, matching the SQLite implementation.
func (s *gbrainFactStore) CanonicalHashAll(ctx context.Context) (string, error) {
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
	sort.Slice(entities, func(i, j int) bool { return entities[i].Slug < entities[j].Slug })
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
func (s *gbrainFactStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// sortedStrings returns the set's members in ascending order.
func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
