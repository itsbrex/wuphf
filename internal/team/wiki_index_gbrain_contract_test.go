package team

// wiki_index_gbrain_contract_test.go — live contract coverage for the gbrain
// FactStore.
//
// This backend cannot be exercised in-process: it needs a running `gbrain
// serve` and a real brain. The tests are therefore opt-in, gated on
// WUPHF_GBRAIN_TEST=1 plus a GBRAIN_HOME pointing at a brain that is safe to
// mutate. Everything they do is destructive inside the wuphf namespaces, so
// pointing this at a personal brain would delete real pages — hence the
// explicit opt-in rather than "run if gbrain is installed".
//
// Run with:
//
//	GBRAIN_HOME=~/.wuphf-gbrain-ctx-home WUPHF_GBRAIN_TEST=1 \
//	  OPENAI_API_KEY=$(...) go test ./internal/team/ -run TestGBrain -count=1
//
// OPENAI_API_KEY must be exported explicitly to exercise the vector arm. The Go
// test harness points WUPHF_RUNTIME_HOME at a temp dir for isolation, so
// config.ResolveOpenAIAPIKey() reads an EMPTY config and gbrainEnv() forwards no
// key to the `gbrain serve` subprocess. Without it the tests still pass, but
// gbrain writes chunks with NULL embeddings and every assertion here is covering
// the keyword arm only. Verify with `gbrain stats`: Embedded should equal Chunks.
//
// The value of these tests is that they feed the SAME assertions the SQLite and
// in-memory stores already pass (via the shared harnesses in
// wiki_index_typed_query_test.go and wiki_categories_test.go), so a divergence
// between backends fails rather than silently changing retrieval behaviour.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nex-crm/wuphf/internal/gbrain"
)

// gbrainTestEnabled reports whether the live gbrain contract tests should run.
func gbrainTestEnabled() bool {
	return strings.TrimSpace(os.Getenv("WUPHF_GBRAIN_TEST")) == "1"
}

// newGBrainTestStore connects to the configured brain and purges every page in
// the wuphf namespaces so each test starts clean.
//
// Purging rather than creating a fresh brain per test is deliberate: `gbrain
// init` takes minutes, which would make the suite unusable. The trade is that
// the tests are destructive within their own namespaces.
func newGBrainTestStore(t *testing.T) FactStore {
	t.Helper()
	if !gbrainTestEnabled() {
		t.Skip("live gbrain contract tests are opt-in: set WUPHF_GBRAIN_TEST=1 and GBRAIN_HOME")
	}
	// Generous budget: the first call spawns `gbrain serve`, which boots PGLite.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	store, err := NewGBrainEntityStore(ctx)
	if err != nil {
		t.Fatalf("NewGBrainEntityStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	es, ok := store.(*gbrainEntityStore)
	if !ok {
		t.Fatalf("NewGBrainEntityStore returned %T, want *gbrainEntityStore", store)
	}
	purgeGBrainNamespaces(t, ctx, es.client)
	return store
}

// purgeGBrainNamespaces deletes every page WUPHF owns in the brain.
//
// Uses the cursor-paginated listing so a brain left dirty by the bench (which
// can hold far more than gbrain's ~100-row list_pages cap) is fully drained.
// Repeats until a pass finds nothing, since deleting shrinks the live set.
func purgeGBrainNamespaces(t *testing.T, ctx context.Context, client *gbrain.Client) {
	t.Helper()
	prefixes := append([]string{
		gbrainFactPrefix, gbrainEntityPrefix, gbrainCategoryPrefix, gbrainArticlePrefix, gbrainDirSources,
	}, allEntityDirs()...)
	for _, prefix := range prefixes {
		for pass := 0; ; pass++ {
			if pass > 50 {
				t.Fatalf("purge %s: still draining after %d passes", prefix, pass)
			}
			pages, err := client.ListAllPages(ctx, gbrain.ListPageOptions{
				SlugPrefix: prefix,
				Limit:      gbrainListPageSize,
			})
			if err != nil {
				t.Fatalf("purge list %s: %v", prefix, err)
			}
			if len(pages) == 0 {
				break
			}
			for _, p := range pages {
				if err := client.DeletePage(ctx, p.Slug); err != nil {
					t.Fatalf("purge delete %s: %v", p.Slug, err)
				}
			}
		}
	}
}

// TestGBrainFactStore_TripletRoundTrip is the core assertion: a fact written
// through the gbrain store comes back byte-identical, and the typed graph walks
// that retrieval depends on resolve to it.
func TestGBrainFactStore_TripletRoundTrip(t *testing.T) {
	store := newGBrainTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// The entity endpoints must exist before links can attach to them.
	for _, e := range []IndexEntity{
		{Slug: "carol-mei", CanonicalSlug: "carol-mei", Kind: "person",
			Signals: Signals{PersonName: "Carol Mei", JobTitle: "VP Partnerships"}},
		{Slug: "apac-launch", CanonicalSlug: "apac-launch", Kind: "project"},
		{Slug: "acme", CanonicalSlug: "acme", Kind: "company"},
	} {
		if err := store.UpsertEntity(ctx, e); err != nil {
			t.Fatalf("UpsertEntity %s: %v", e.Slug, err)
		}
	}

	champions := seedTriplet("f-champ", "carol-mei", "champions", "project:apac-launch")
	roleAt := seedTriplet("f-role", "carol-mei", "role_at", "company:acme")
	for _, f := range []TypedFact{champions, roleAt} {
		if err := store.UpsertFact(ctx, f); err != nil {
			t.Fatalf("UpsertFact %s: %v", f.ID, err)
		}
	}

	t.Run("GetFact round-trips the record", func(t *testing.T) {
		got, ok, err := store.GetFact(ctx, "f-champ")
		if err != nil {
			t.Fatalf("GetFact: %v", err)
		}
		if !ok {
			t.Fatal("GetFact: fact not found after write")
		}
		if got.Text != champions.Text {
			t.Errorf("Text = %q, want %q", got.Text, champions.Text)
		}
		if got.Triplet == nil || *got.Triplet != *champions.Triplet {
			t.Errorf("Triplet = %+v, want %+v", got.Triplet, champions.Triplet)
		}
		if got.EntitySlug != champions.EntitySlug {
			t.Errorf("EntitySlug = %q, want %q", got.EntitySlug, champions.EntitySlug)
		}
	})

	t.Run("missing fact reports not-found rather than erroring", func(t *testing.T) {
		_, ok, err := store.GetFact(ctx, "f-does-not-exist")
		if err != nil {
			t.Fatalf("GetFact on a missing ID returned an error: %v", err)
		}
		if ok {
			t.Error("GetFact reported found for a fact that was never written")
		}
	})

	// This is the walk retrieveRelationshipSingle depends on. If it regresses,
	// relationship queries silently fall back to lexical-only recall.
	t.Run("ListFactsByPredicateObject resolves the typed walk", func(t *testing.T) {
		facts, err := store.ListFactsByPredicateObject(ctx, "champions", "project:apac-launch")
		if err != nil {
			t.Fatalf("ListFactsByPredicateObject: %v", err)
		}
		if len(facts) != 1 || facts[0].ID != "f-champ" {
			t.Fatalf("got %d facts %v, want exactly [f-champ]", len(facts), factIDs(facts))
		}
	})

	t.Run("ListFactsByTriplet filters by object prefix", func(t *testing.T) {
		facts, err := store.ListFactsByTriplet(ctx, "carol-mei", "role_at", "company:")
		if err != nil {
			t.Fatalf("ListFactsByTriplet: %v", err)
		}
		if len(facts) != 1 || facts[0].ID != "f-role" {
			t.Fatalf("got %v, want exactly [f-role]", factIDs(facts))
		}
		none, err := store.ListFactsByTriplet(ctx, "carol-mei", "role_at", "project:")
		if err != nil {
			t.Fatalf("ListFactsByTriplet (non-matching prefix): %v", err)
		}
		if len(none) != 0 {
			t.Errorf("non-matching object prefix returned %v, want empty", factIDs(none))
		}
	})

	t.Run("ListFactsForEntity returns both facts", func(t *testing.T) {
		facts, err := store.ListFactsForEntity(ctx, "carol-mei")
		if err != nil {
			t.Fatalf("ListFactsForEntity: %v", err)
		}
		if len(facts) != 2 {
			t.Fatalf("got %v, want 2 facts", factIDs(facts))
		}
	})

	t.Run("CountFacts and ListAllFacts agree", func(t *testing.T) {
		n, err := store.CountFacts(ctx)
		if err != nil {
			t.Fatalf("CountFacts: %v", err)
		}
		all, err := store.ListAllFacts(ctx)
		if err != nil {
			t.Fatalf("ListAllFacts: %v", err)
		}
		if n != len(all) {
			t.Errorf("CountFacts = %d but ListAllFacts returned %d", n, len(all))
		}
		if n != 2 {
			t.Errorf("CountFacts = %d, want 2", n)
		}
	})

	// The hash is the drift detector the reconcile loop relies on. It must be
	// stable across reads and must move when a fact changes.
	t.Run("CanonicalHashFacts is stable and change-sensitive", func(t *testing.T) {
		first, err := store.CanonicalHashFacts(ctx)
		if err != nil {
			t.Fatalf("CanonicalHashFacts: %v", err)
		}
		second, err := store.CanonicalHashFacts(ctx)
		if err != nil {
			t.Fatalf("CanonicalHashFacts (repeat): %v", err)
		}
		if first != second {
			t.Errorf("hash is unstable across reads: %s vs %s", first, second)
		}

		changed := roleAt
		changed.Text = roleAt.Text + " (updated)"
		if err := store.UpsertFact(ctx, changed); err != nil {
			t.Fatalf("UpsertFact (update): %v", err)
		}
		third, err := store.CanonicalHashFacts(ctx)
		if err != nil {
			t.Fatalf("CanonicalHashFacts (after update): %v", err)
		}
		if third == first {
			t.Error("hash did not change after a fact's text changed")
		}
	})
}

// TestGBrainFactStore_Redirects covers slug merges.
func TestGBrainFactStore_Redirects(t *testing.T) {
	store := newGBrainTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, slug := range []string{"c-mei", "carol-mei"} {
		if err := store.UpsertEntity(ctx, IndexEntity{Slug: slug, CanonicalSlug: slug, Kind: "person"}); err != nil {
			t.Fatalf("UpsertEntity %s: %v", slug, err)
		}
	}
	if err := store.UpsertRedirect(ctx, Redirect{
		From: "c-mei", To: "carol-mei", MergedAt: time.Now().UTC(), MergedBy: "test", CommitSHA: "abc123",
	}); err != nil {
		t.Fatalf("UpsertRedirect: %v", err)
	}

	to, ok, err := store.ResolveRedirect(ctx, "c-mei")
	if err != nil {
		t.Fatalf("ResolveRedirect: %v", err)
	}
	if !ok || to != "carol-mei" {
		t.Errorf("ResolveRedirect(c-mei) = (%q, %v), want (carol-mei, true)", to, ok)
	}

	if _, ok, err := store.ResolveRedirect(ctx, "carol-mei"); err != nil {
		t.Fatalf("ResolveRedirect on a non-redirect: %v", err)
	} else if ok {
		t.Error("ResolveRedirect reported a redirect for a canonical slug")
	}
}

// TestGBrainTextIndex_Search proves search returns fact IDs (not raw gbrain
// page slugs) and populates the Entity field that citations render.
func TestGBrainTextIndex_Search(t *testing.T) {
	store := newGBrainTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := store.UpsertEntity(ctx, IndexEntity{
		Slug: "carol-mei", CanonicalSlug: "carol-mei", Kind: "person",
		Signals: Signals{PersonName: "Carol Mei"},
	}); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	fact := seedTriplet("f-role", "carol-mei", "role_at", "company:acme")
	fact.Text = "Carol Mei is VP of Partnerships at Acme."
	if err := store.UpsertFact(ctx, fact); err != nil {
		t.Fatalf("UpsertFact: %v", err)
	}

	es, ok := store.(*gbrainEntityStore)
	if !ok {
		t.Fatalf("store is %T, want *gbrainEntityStore", store)
	}
	text := &gbrainEntityTextIndex{store: es}

	hits, err := text.Search(ctx, "Carol Mei partnerships", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search returned no hits for a query that matches a written fact")
	}
	var found bool
	for _, h := range hits {
		if h.FactID == "f-role" {
			found = true
			if h.Entity != "carol-mei" {
				t.Errorf("hit.Entity = %q, want carol-mei (citations render this)", h.Entity)
			}
			if strings.TrimSpace(h.Snippet) == "" {
				t.Error("hit.Snippet is empty; citations would render blank")
			}
		}
		// Every hit must name a fact, never a raw gbrain slug.
		if strings.Contains(h.FactID, "/") {
			t.Errorf("hit.FactID = %q looks like a gbrain slug, not a fact ID", h.FactID)
		}
	}
	if !found {
		t.Errorf("expected f-role among hits, got %v", hitIDs(hits))
	}
}

// hitIDs renders hit fact IDs for assertion messages.
func hitIDs(hits []SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.FactID)
	}
	return out
}

// TestGBrainFactStore_ResurrectsDeletedFact is a regression test for silent
// data loss.
//
// gbrain's put_page does not clear deleted_at, so before the fix an upsert onto
// a previously deleted slug wrote a row that get_page and search both refused
// to return. A fact that was retired and later re-extracted would vanish
// permanently, with no error anywhere. This test fails against a store whose
// write path does not restore the tombstone.
func TestGBrainFactStore_ResurrectsDeletedFact(t *testing.T) {
	store := newGBrainTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := store.UpsertEntity(ctx, IndexEntity{
		Slug: "carol-mei", CanonicalSlug: "carol-mei", Kind: "person",
	}); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	fact := seedTriplet("f-resurrect", "carol-mei", "role_at", "company:acme")
	if err := store.UpsertFact(ctx, fact); err != nil {
		t.Fatalf("UpsertFact (initial): %v", err)
	}

	es, ok := store.(*gbrainEntityStore)
	if !ok {
		t.Fatalf("store is %T, want *gbrainEntityStore", store)
	}
	text := &gbrainEntityTextIndex{store: es}

	// Retire the fact, the way the reconcile loop does.
	if err := text.Delete(ctx, "f-resurrect"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := store.GetFact(ctx, "f-resurrect"); err != nil {
		t.Fatalf("GetFact after delete: %v", err)
	} else if found {
		t.Fatal("fact is still visible after Delete")
	}

	// Re-extract the same fact. It MUST come back.
	if err := store.UpsertFact(ctx, fact); err != nil {
		t.Fatalf("UpsertFact (re-extract): %v", err)
	}
	got, found, err := store.GetFact(ctx, "f-resurrect")
	if err != nil {
		t.Fatalf("GetFact after re-extract: %v", err)
	}
	if !found {
		t.Fatal("re-extracted fact is invisible: put_page wrote into a tombstone")
	}
	if got.Text != fact.Text {
		t.Errorf("Text = %q, want %q", got.Text, fact.Text)
	}
}

// TestGBrainFactStore_LinksWithoutPreexistingEntities is a regression test for
// the failure that made the slice-1 multi_hop class collapse to 10%.
//
// gbrain's add_link REJECTS an edge whose endpoint page does not exist. The
// reconcile loop routinely writes a fact before the entity it references has
// its own page, so without ensureEntityPage every such link failed and the
// typed graph walks silently returned nothing — BM25 carried the results and
// the typed half of the design was inert.
//
// This test writes a fact with NO prior UpsertEntity calls and asserts the
// typed walk still resolves.
func TestGBrainFactStore_LinksWithoutPreexistingEntities(t *testing.T) {
	store := newGBrainTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Deliberately no UpsertEntity: the endpoints do not exist yet.
	fact := seedTriplet("f-noent", "wendy-vale", "champions", "project:orbit-launch")
	if err := store.UpsertFact(ctx, fact); err != nil {
		t.Fatalf("UpsertFact without pre-existing entities: %v", err)
	}

	facts, err := store.ListFactsByPredicateObject(ctx, "champions", "project:orbit-launch")
	if err != nil {
		t.Fatalf("ListFactsByPredicateObject: %v", err)
	}
	if len(facts) != 1 || facts[0].ID != "f-noent" {
		t.Fatalf("typed walk returned %v, want [f-noent]; link endpoints were not materialised", factIDs(facts))
	}

	owned, err := store.ListFactsForEntity(ctx, "wendy-vale")
	if err != nil {
		t.Fatalf("ListFactsForEntity: %v", err)
	}
	if len(owned) != 1 || owned[0].ID != "f-noent" {
		t.Fatalf("ownership walk returned %v, want [f-noent]", factIDs(owned))
	}
}

// TestGBrainFactStore_FullScanPastListCap is the cursor regression test.
//
// gbrain caps list_pages at ~100 rows and silently drops `offset`, so a naive
// scan truncated at the cap and reported, for instance, CountFacts=100 for a
// 120-fact corpus — a wrong answer that looks like a right one. Client
// .ListAllPages now walks an ascending `updated_after` cursor instead.
//
// This writes more ENTITIES than the cap, because under the one-page-per-entity
// shape it is entities, not facts, that occupy list_pages rows.
//
// Slow by design (120 page writes); skipped in short mode.
func TestGBrainFactStore_FullScanPastListCap(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 120 entity pages; skipped in short mode")
	}
	store := newGBrainTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	const total = 120 // > gbrain's ~100-row list_pages cap
	for i := 0; i < total; i++ {
		slug := fmt.Sprintf("cap-person-%03d", i)
		f := seedTriplet(fmt.Sprintf("f-cap-%03d", i), slug, "role_at", "company:cap-co")
		if err := store.UpsertFact(ctx, f); err != nil {
			t.Fatalf("UpsertFact %d: %v", i, err)
		}
	}

	n, err := store.CountFacts(ctx)
	if err != nil {
		t.Fatalf("CountFacts: %v", err)
	}
	if n < total {
		t.Errorf("CountFacts = %d, want >= %d — the cursor truncated the scan at the list_pages cap", n, total)
	}

	all, err := store.ListAllFacts(ctx)
	if err != nil {
		t.Fatalf("ListAllFacts: %v", err)
	}
	if len(all) < total {
		t.Errorf("ListAllFacts returned %d, want >= %d", len(all), total)
	}

	// Entity enumeration must clear the cap too.
	seen := 0
	if err := store.IterateEntities(ctx, func(IndexEntity) error { seen++; return nil }); err != nil {
		t.Fatalf("IterateEntities: %v", err)
	}
	if seen < total {
		t.Errorf("IterateEntities saw %d entities, want >= %d", seen, total)
	}
}
