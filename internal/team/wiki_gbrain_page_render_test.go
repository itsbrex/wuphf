package team

// wiki_gbrain_page_render_test.go — unit coverage for the recommended-shape
// page renderer. Pure, runs everywhere, no brain required.
//
// The anchor round-trip is the load-bearing property: it is the ONLY thing
// connecting a gbrain chunk hit back to WUPHF fact IDs. If it breaks, search
// silently returns nothing rather than failing.

import (
	"strings"
	"testing"
	"time"
)

func testFact(id, text string, day int) TypedFact {
	return TypedFact{
		ID:         id,
		EntitySlug: "esme-walker",
		Kind:       "person",
		Type:       "status",
		Triplet:    &Triplet{Subject: "esme-walker", Predicate: "role_at", Object: "company:dunder-mifflin"},
		Text:       text,
		SourceType: "chat",
		ValidFrom:  time.Date(2026, 4, day, 12, 0, 0, 0, time.UTC),
		CreatedAt:  time.Date(2026, 4, day, 12, 0, 0, 0, time.UTC),
	}
}

func TestRenderEntityPage_TwoLayerShape(t *testing.T) {
	e := IndexEntity{
		Slug: "esme-walker", CanonicalSlug: "esme-walker", Kind: "person",
		Aliases: []string{"E. Walker"},
		Signals: Signals{PersonName: "Esme Walker", JobTitle: "Operations Lead"},
	}
	facts := map[string]TypedFact{
		"f-1": testFact("f-1", "Esme Walker was promoted to Operations Lead.", 22),
		"f-2": testFact("f-2", "Esme Walker shipped the Mobile Revamp specs.", 18),
	}
	page, err := renderEntityPage(e, facts)
	if err != nil {
		t.Fatalf("renderEntityPage: %v", err)
	}

	// gbrain splits compiled_truth from timeline on the FIRST `---` after the
	// frontmatter block. Without it the whole page lands in compiled_truth and
	// the timeline is never chunked as its own source.
	body := page[strings.Index(page, "---\n\n")+len("---\n\n"):] // past frontmatter close
	if !strings.Contains(body, "\n---\n") {
		t.Fatalf("page has no compiled-truth/timeline separator:\n%s", page)
	}
	if !strings.Contains(page, "## Timeline") {
		t.Error("page has no Timeline section")
	}
	if !strings.Contains(page, "## State") {
		t.Error("page has no State section")
	}
	// Executive summary is the schema doc's "read only this" line.
	if !strings.Contains(page, "> Esme Walker") {
		t.Errorf("page has no executive summary blockquote:\n%s", page)
	}

	// Reverse-chronological: the 22nd must precede the 18th.
	i22, i18 := strings.Index(page, "2026-04-22"), strings.Index(page, "2026-04-18")
	if i22 < 0 || i18 < 0 || i22 > i18 {
		t.Errorf("timeline is not reverse-chronological (22nd at %d, 18th at %d)", i22, i18)
	}
}

func TestRenderEntityPage_IsDeterministic(t *testing.T) {
	e := IndexEntity{Slug: "x", Kind: "person", Signals: Signals{PersonName: "X"}}
	facts := map[string]TypedFact{}
	for _, id := range []string{"f-1", "f-2", "f-3", "f-4", "f-5"} {
		facts[id] = testFact(id, "text for "+id, 20) // identical dates force the ID tiebreak
	}
	first, err := renderEntityPage(e, facts)
	if err != nil {
		t.Fatalf("renderEntityPage: %v", err)
	}
	for i := 0; i < 15; i++ {
		got, err := renderEntityPage(e, facts)
		if err != nil {
			t.Fatalf("renderEntityPage: %v", err)
		}
		if got != first {
			t.Fatalf("render is not deterministic on iteration %d; content_hash would churn every reconcile", i)
		}
	}
}

// TestFactAnchorRoundTrip is the critical path: render a timeline, then recover
// exactly the fact IDs from the rendered text the way Search does.
func TestFactAnchorRoundTrip(t *testing.T) {
	e := IndexEntity{Slug: "esme-walker", Kind: "person", Signals: Signals{PersonName: "Esme Walker"}}
	facts := map[string]TypedFact{}
	want := []string{"09522ea7b2884673", "f-2", "fact_3", "FACT-4"}
	for i, id := range want {
		facts[id] = testFact(id, "fact text "+id, 20-i)
	}
	page, err := renderEntityPage(e, facts)
	if err != nil {
		t.Fatalf("renderEntityPage: %v", err)
	}

	got := parseFactAnchors(page)
	gotSet := map[string]bool{}
	for _, id := range got {
		gotSet[id] = true
	}
	for _, id := range want {
		if !gotSet[id] {
			t.Errorf("anchor %q did not survive the render/parse round-trip; search would silently drop this fact", id)
		}
	}
}

// TestParseFactAnchorsIgnoresProseCarets guards against a caret inside fact
// text being mistaken for an anchor, which would fabricate a citation to a
// fact ID that does not exist.
func TestParseFactAnchorsIgnoresProseCarets(t *testing.T) {
	chunk := strings.Join([]string{
		"- **2026-04-22** | chat — She wrote x^2 + y^2 in the doc. ^f-real",
		"- **2026-04-21** | chat — A line with a ^caret midway through it. ^f-second",
		"Some prose that ends with a stray ^token",
	}, "\n")
	got := parseFactAnchors(chunk)

	// The stray trailing token IS anchor-shaped and is expected to parse; the
	// point of this test is that mid-line carets are NOT picked up.
	for _, bad := range []string{"2", "caret"} {
		for _, id := range got {
			if id == bad {
				t.Errorf("mid-line caret %q was parsed as a fact anchor: %v", bad, got)
			}
		}
	}
	if len(got) < 2 || got[0] != "f-real" || got[1] != "f-second" {
		t.Errorf("expected [f-real f-second ...] in order, got %v", got)
	}
}

func TestSnippetForFact(t *testing.T) {
	chunk := strings.Join([]string{
		"## Timeline",
		"- **2026-04-22** | chat — Esme Walker was promoted to Operations Lead. ^f-1",
		"- **2026-04-18** | meeting — Esme Walker shipped the specs. ^f-2",
	}, "\n")

	if got := snippetForFact(chunk, "f-1"); got != "Esme Walker was promoted to Operations Lead." {
		t.Errorf("snippetForFact(f-1) = %q", got)
	}
	if got := snippetForFact(chunk, "f-2"); got != "Esme Walker shipped the specs." {
		t.Errorf("snippetForFact(f-2) = %q", got)
	}
	if got := snippetForFact(chunk, "f-missing"); got != "" {
		t.Errorf("snippetForFact for an absent anchor = %q, want empty", got)
	}
}

// TestRenderTimelineLineFlattensNewlines guards the one-entry-per-line contract
// the anchor parser depends on: a fact whose text contains a newline would
// otherwise split into two lines and orphan its anchor.
func TestRenderTimelineLineFlattensNewlines(t *testing.T) {
	f := testFact("f-multi", "First line.\nSecond line.\n\nThird.", 22)
	line := renderTimelineLine(f)
	if strings.Contains(line, "\n") {
		t.Fatalf("rendered timeline line contains a newline: %q", line)
	}
	if !strings.HasSuffix(line, "^f-multi") {
		t.Errorf("line does not end with its anchor: %q", line)
	}
	if got := parseFactAnchors(line); len(got) != 1 || got[0] != "f-multi" {
		t.Errorf("anchor lost on multi-line text: %v", got)
	}
}

func TestEntityPageSlugMECEDirectories(t *testing.T) {
	cases := map[string]string{
		"person":  "people/carol-mei",
		"company": "companies/carol-mei",
		"project": "projects/carol-mei",
		"team":    "concepts/carol-mei",
		"":        "concepts/carol-mei",
	}
	for kind, want := range cases {
		if got := entityPageSlug(kind, "carol-mei"); got != want {
			t.Errorf("entityPageSlug(%q) = %q, want %q", kind, got, want)
		}
		if got := entitySlugFromPageSlug(want); got != "carol-mei" {
			t.Errorf("entitySlugFromPageSlug(%q) = %q, want carol-mei", want, got)
		}
	}
	if got := entitySlugFromPageSlug("atoms/f-1"); got != "" {
		t.Errorf("entitySlugFromPageSlug on a non-entity page = %q, want empty", got)
	}
}

// TestDecodeEntityPageRoundTrip proves the authoritative records survive the
// frontmatter blobs, including hostile fact text.
func TestDecodeEntityPageRoundTrip(t *testing.T) {
	e := IndexEntity{
		Slug: "carol-mei", CanonicalSlug: "carol-mei", Kind: "person",
		Aliases: []string{"C. Mei"},
		Signals: Signals{PersonName: "Carol Mei", JobTitle: "VP: Partnerships"},
	}
	hostile := testFact("f-1", "Carol's role: \"VP, Partnerships\" — 日本語\nnewline", 22)
	facts := map[string]TypedFact{"f-1": hostile}

	page, err := renderEntityPage(e, facts)
	if err != nil {
		t.Fatalf("renderEntityPage: %v", err)
	}

	// Simulate what gbrain hands back: the frontmatter as a decoded map.
	fm := map[string]any{}
	for _, line := range strings.Split(page, "\n") {
		if !strings.Contains(line, ": ") || strings.HasPrefix(line, "- ") {
			continue
		}
		k := line[:strings.Index(line, ": ")]
		v := strings.TrimSpace(line[strings.Index(line, ": ")+2:])
		v = strings.TrimPrefix(strings.TrimSuffix(v, `"`), `"`)
		fm[k] = v
		if k == gbrainFactsBlobKey {
			break
		}
	}

	gotEntity, gotFacts, err := decodeEntityPage(fm, "people/carol-mei")
	if err != nil {
		t.Fatalf("decodeEntityPage: %v", err)
	}
	if gotEntity.Slug != e.Slug || gotEntity.Signals.JobTitle != e.Signals.JobTitle {
		t.Errorf("entity round-trip lost data: %+v", gotEntity)
	}
	got, ok := gotFacts["f-1"]
	if !ok {
		t.Fatal("fact f-1 missing after round-trip")
	}
	if got.Text != hostile.Text {
		t.Errorf("fact text round-trip:\n got %q\nwant %q", got.Text, hostile.Text)
	}
}
