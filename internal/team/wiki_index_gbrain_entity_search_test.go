package team

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nex-crm/wuphf/internal/gbrain"
)

// Hermetic coverage for the chunk-to-fact mapping in
// gbrainEntityTextIndex.Search — the logic that carries this backend's
// retrieval quality.
//
// Until now that mapping was reachable only from the opt-in live contract
// tests, which CI never runs, so a regression in it would have been invisible
// until someone re-ran bench/slice-1 by hand. These tests need no brain, no
// network and no keys, so they run on every `bash scripts/test-go.sh`.

// seedEntityStore builds a store whose page cache is pre-populated, so
// loadPage never touches a client. A nil client is deliberate: any code path
// that reaches the network in these tests will panic rather than silently
// making the test a live one.
func seedEntityStore(entity string, facts ...TypedFact) *gbrainEntityStore {
	byID := make(map[string]TypedFact, len(facts))
	owner := make(map[string]string, len(facts))
	for _, f := range facts {
		byID[f.ID] = f
		owner[f.ID] = entity
	}
	return &gbrainEntityStore{
		client: nil,
		pages: map[string]*entityPageState{
			entity: {
				entity: IndexEntity{Slug: entity, CanonicalSlug: entity, Kind: "person"},
				facts:  byID,
			},
		},
		factOwner: owner,
	}
}

func searchFact(id, text string, day int) TypedFact {
	return TypedFact{
		ID:         id,
		EntitySlug: "esme-walker",
		Kind:       "person",
		Text:       text,
		SourceType: "chat",
		ValidFrom:  time.Date(2026, 4, day, 12, 0, 0, 0, time.UTC),
		CreatedAt:  time.Date(2026, 4, day, 12, 0, 0, 0, time.UTC),
	}
}

func TestEntitySearchMapsAnchoredChunksToFacts(t *testing.T) {
	store := seedEntityStore("esme-walker",
		searchFact("f-1", "Esme Walker was promoted to Operations Lead.", 22),
		searchFact("f-2", "Esme Walker shipped the Mobile Revamp specs.", 18),
	)
	idx := &gbrainEntityTextIndex{store: store}
	idx.queryFn = func(context.Context, string, int) ([]gbrain.Hit, error) {
		return []gbrain.Hit{{
			Slug:  "people/esme-walker",
			Score: 0.9,
			ChunkText: strings.Join([]string{
				"## Timeline",
				"- **2026-04-22** | chat — Esme Walker was promoted to Operations Lead. ^f-1",
				"- **2026-04-18** | chat — Esme Walker shipped the Mobile Revamp specs. ^f-2",
			}, "\n"),
		}}, nil
	}

	hits, err := idx.Search(context.Background(), "what does esme do", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %+v", len(hits), hits)
	}
	// One chunk carrying a whole timeline must yield EVERY fact in it. That is
	// the property the one-page-per-entity shape exists to buy.
	if hits[0].FactID != "f-1" || hits[1].FactID != "f-2" {
		t.Errorf("fact IDs = %s,%s want f-1,f-2", hits[0].FactID, hits[1].FactID)
	}
	if hits[0].Entity != "esme-walker" {
		t.Errorf("Entity = %q, want esme-walker (it renders as the citation title)", hits[0].Entity)
	}
	// The snippet must be the specific evidence line, not the whole chunk.
	if hits[0].Snippet != "Esme Walker was promoted to Operations Lead." {
		t.Errorf("Snippet = %q, want the single timeline line", hits[0].Snippet)
	}
}

// TestEntitySearchFallsBackWhenChunkHasNoAnchors is a regression test for a real
// bug: gbrain boosts compiled_truth chunks 2x, and those carry NO anchors, so
// anchor-only parsing returned ZERO facts for exactly the queries the summary
// answers best.
func TestEntitySearchFallsBackWhenChunkHasNoAnchors(t *testing.T) {
	store := seedEntityStore("esme-walker",
		searchFact("f-1", "Esme Walker was promoted to Operations Lead.", 22),
		searchFact("f-2", "Esme Walker shipped the Mobile Revamp specs.", 18),
	)
	idx := &gbrainEntityTextIndex{store: store}
	idx.queryFn = func(context.Context, string, int) ([]gbrain.Hit, error) {
		return []gbrain.Hit{{
			Slug:      "people/esme-walker",
			Score:     0.95,
			ChunkText: "# esme-walker\n\n> Esme Walker — Operations Lead. 2 recorded fact(s).",
		}}, nil
	}

	hits, err := idx.Search(context.Background(), "who is esme walker", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 — an anchorless compiled_truth hit must still "+
			"return the entity's facts, or summary-shaped queries return nothing", len(hits))
	}
	// Newest first, matching the page's own ordering.
	if hits[0].FactID != "f-1" {
		t.Errorf("first fact = %s, want f-1 (newest)", hits[0].FactID)
	}
}

func TestEntitySearchIgnoresNonEntityPages(t *testing.T) {
	store := seedEntityStore("esme-walker", searchFact("f-1", "text", 22))
	idx := &gbrainEntityTextIndex{store: store}
	idx.queryFn = func(context.Context, string, int) ([]gbrain.Hit, error) {
		return []gbrain.Hit{
			// gbrain's `type` filter is accepted and IGNORED, so foreign pages
			// reach us and must be dropped client-side.
			{Slug: "sources/abc123", Score: 0.99, ChunkText: "an article stub ^f-999"},
			// Categories live in their own namespace precisely so they are not
			// mistaken for concept-kind entities here.
			{Slug: "categories/ai-agents", Score: 0.98, ChunkText: "a category page"},
			{Slug: "people/esme-walker", Score: 0.5, ChunkText: "- **2026-04-22** | chat — text ^f-1"},
		}, nil
	}

	hits, err := idx.Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, h := range hits {
		if h.FactID == "f-999" {
			t.Error("returned a fact ID parsed from a non-entity page")
		}
	}
	if len(hits) != 1 || hits[0].FactID != "f-1" {
		t.Errorf("got %+v, want only f-1", hits)
	}
}

func TestEntitySearchRespectsTopKAndDedupes(t *testing.T) {
	facts := make([]TypedFact, 0, 6)
	for i := 1; i <= 6; i++ {
		facts = append(facts, searchFact(string(rune('a'+i-1))+"-fact", "text", 20))
	}
	store := seedEntityStore("esme-walker", facts...)
	idx := &gbrainEntityTextIndex{store: store}
	idx.queryFn = func(context.Context, string, int) ([]gbrain.Hit, error) {
		// The same entity page returned twice: a real possibility, since one
		// page can produce both a compiled_truth and a timeline chunk.
		return []gbrain.Hit{
			{Slug: "people/esme-walker", Score: 0.9, ChunkText: "- x ^a-fact\n- y ^b-fact"},
			{Slug: "people/esme-walker", Score: 0.8, ChunkText: "- x ^a-fact\n- z ^c-fact"},
		}, nil
	}

	hits, err := idx.Search(context.Background(), "q", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want topK=3", len(hits))
	}
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.FactID] {
			t.Errorf("duplicate fact %s in results", h.FactID)
		}
		seen[h.FactID] = true
	}
}

func TestEntitySearchEmptyQueryReturnsNothing(t *testing.T) {
	idx := &gbrainEntityTextIndex{store: seedEntityStore("x")}
	idx.queryFn = func(context.Context, string, int) ([]gbrain.Hit, error) {
		t.Fatal("Search must not call the backend for an empty query")
		return nil, nil
	}
	hits, err := idx.Search(context.Background(), "   ", 10)
	if err != nil || len(hits) != 0 {
		t.Errorf("Search(empty) = (%v, %v), want (nil, nil)", hits, err)
	}
}
