package team

// wiki_gbrain_mapping_test.go — unit coverage for the WUPHF ⇄ gbrain mapping.
//
// These run everywhere, with no gbrain binary and no brain: the mapping is pure
// and it is where a silent data-loss bug would live. The live contract tests
// that exercise a real brain are opt-in (see gbrainTestStore).

import (
	"strings"
	"testing"
	"time"
)

func TestTripletRefToEntitySlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare slug", "carol-mei", "carol-mei"},
		{"kind qualified", "project:apac-launch", "apac-launch"},
		{"company qualified", "company:acme", "acme"},
		{"whitespace trimmed", "  person:tom-reed  ", "tom-reed"},
		{"empty", "", ""},
		// A literal object (a free-text job title) has no entity to resolve to.
		// It must not panic and must not silently become a different slug.
		{"literal with colon", "title:VP of Partnerships", "VP of Partnerships"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tripletRefToEntitySlug(tc.in); got != tc.want {
				t.Errorf("tripletRefToEntitySlug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSlugRoundTrips(t *testing.T) {
	t.Run("fact", func(t *testing.T) {
		if got := factIDFromSlug(factSlug("f-123")); got != "f-123" {
			t.Errorf("fact slug round-trip = %q, want %q", got, "f-123")
		}
		if got := factIDFromSlug("entities/carol-mei"); got != "" {
			t.Errorf("factIDFromSlug on a non-fact = %q, want empty", got)
		}
	})
	t.Run("entity", func(t *testing.T) {
		if got := entitySlugFromGBrain(entitySlug("carol-mei")); got != "carol-mei" {
			t.Errorf("entity slug round-trip = %q, want %q", got, "carol-mei")
		}
		if got := entitySlugFromGBrain("atoms/f-1"); got != "" {
			t.Errorf("entitySlugFromGBrain on a non-entity = %q, want empty", got)
		}
	})
	t.Run("category", func(t *testing.T) {
		if got := categoryFromGBrainSlug(gbrainCategorySlug("ai-agents")); got != "ai-agents" {
			t.Errorf("category slug round-trip = %q, want %q", got, "ai-agents")
		}
	})
}

// TestArticleSlugRoundTrip covers the base64url path encoding. Article paths
// carry "/" and "." which are not slug-safe, so a lossy encoding here would
// silently detach articles from their categories.
func TestArticleSlugRoundTrip(t *testing.T) {
	paths := []string{
		"team/companies/acme.md",
		"wiki/people/carol-mei.md",
		"team/.categories/ai-agents.md",
		"team/concepts/mql.md",
		// Unicode and spaces must survive: article titles are user-authored.
		"team/people/josé-garcía.md",
		"team/notes/Q3 planning.md",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			slug := articleSlug(p)
			if !strings.HasPrefix(slug, gbrainArticlePrefix) {
				t.Fatalf("articleSlug(%q) = %q, missing prefix", p, slug)
			}
			// The encoded segment must be slug-safe: no slashes or dots.
			seg := strings.TrimPrefix(slug, gbrainArticlePrefix)
			if strings.ContainsAny(seg, "/.") {
				t.Errorf("articleSlug(%q) segment %q is not slug-safe", p, seg)
			}
			if got := articlePathFromSlug(slug); got != p {
				t.Errorf("articlePathFromSlug round-trip = %q, want %q", got, p)
			}
		})
	}
	if got := articlePathFromSlug("articles/!!!not-base64!!!"); got != "" {
		t.Errorf("articlePathFromSlug on bad payload = %q, want empty", got)
	}
	if got := articlePathFromSlug("entities/carol-mei"); got != "" {
		t.Errorf("articlePathFromSlug on a non-article = %q, want empty", got)
	}
}

// TestFactBlobRoundTrip is the load-bearing test for this backend: the fact
// record survives ONLY through the base64 blob, so any loss here is invisible
// data corruption rather than a failure.
func TestFactBlobRoundTrip(t *testing.T) {
	until := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	reinforced := time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)
	want := TypedFact{
		ID:         "f-1",
		EntitySlug: "carol-mei",
		Kind:       "person",
		Type:       "relationship",
		Triplet:    &Triplet{Subject: "carol-mei", Predicate: "role_at", Object: "company:acme"},
		// Deliberately hostile text: apostrophes, a colon, a newline, quotes,
		// and unicode all break naive YAML quoting.
		Text:            "Carol's role: \"VP, Partnerships\" at Acme\nStarted in Q2 — 日本語",
		Confidence:      0.87,
		ValidFrom:       time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
		ValidUntil:      &until,
		Supersedes:      []string{"f-0"},
		ContradictsWith: []string{"f-9"},
		SourceType:      "chat",
		SourcePath:      "team/chat/2026-04-22.md",
		SentenceOffset:  3,
		ArtifactExcerpt: "…she's the VP: Partnerships…",
		CreatedAt:       time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
		CreatedBy:       "extractor",
		ReinforcedAt:    &reinforced,
	}

	blob, err := encodeBlob(want)
	if err != nil {
		t.Fatalf("encodeBlob: %v", err)
	}
	var got TypedFact
	if err := decodeBlob(blob, &got); err != nil {
		t.Fatalf("decodeBlob: %v", err)
	}

	if got.Text != want.Text {
		t.Errorf("Text round-trip:\n got %q\nwant %q", got.Text, want.Text)
	}
	if got.Triplet == nil || *got.Triplet != *want.Triplet {
		t.Errorf("Triplet round-trip = %+v, want %+v", got.Triplet, want.Triplet)
	}
	if !got.ValidFrom.Equal(want.ValidFrom) {
		t.Errorf("ValidFrom = %v, want %v", got.ValidFrom, want.ValidFrom)
	}
	if got.ValidUntil == nil || !got.ValidUntil.Equal(*want.ValidUntil) {
		t.Errorf("ValidUntil = %v, want %v", got.ValidUntil, want.ValidUntil)
	}
	if got.ReinforcedAt == nil || !got.ReinforcedAt.Equal(*want.ReinforcedAt) {
		t.Errorf("ReinforcedAt = %v, want %v", got.ReinforcedAt, want.ReinforcedAt)
	}
	if got.Confidence != want.Confidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, want.Confidence)
	}
	if strings.Join(got.Supersedes, ",") != strings.Join(want.Supersedes, ",") {
		t.Errorf("Supersedes = %v, want %v", got.Supersedes, want.Supersedes)
	}
	if got.ArtifactExcerpt != want.ArtifactExcerpt {
		t.Errorf("ArtifactExcerpt = %q, want %q", got.ArtifactExcerpt, want.ArtifactExcerpt)
	}
}

// TestBuildPageContentIsDeterministic guards the content_hash: an unstable key
// order would make every reconcile look like a change and churn the brain's
// generation clock.
func TestBuildPageContentIsDeterministic(t *testing.T) {
	fm := map[string]string{
		"type":          "atom",
		"wuphf_fact":    "YmxvYg==",
		"wuphf_fact_id": "f-1",
		"subject":       "carol-mei",
		"predicate":     "role_at",
		"object":        "company:acme",
	}
	first := buildPageContent(fm, "body")
	for i := 0; i < 20; i++ {
		if got := buildPageContent(fm, "body"); got != first {
			t.Fatalf("buildPageContent is not deterministic on iteration %d:\n%q\nvs\n%q", i, got, first)
		}
	}
	// Frontmatter keys must be emitted in sorted order, which is what makes the
	// output stable across runs regardless of Go's map iteration order.
	objectAt := strings.Index(first, "\nobject:")
	predicateAt := strings.Index(first, "\npredicate:")
	typeAt := strings.Index(first, "\ntype:")
	if objectAt < 0 || predicateAt < 0 || typeAt < 0 {
		t.Fatalf("expected keys missing from frontmatter:\n%s", first)
	}
	if !(objectAt < predicateAt && predicateAt < typeAt) {
		t.Fatalf("frontmatter is not key-sorted (object=%d predicate=%d type=%d):\n%s",
			objectAt, predicateAt, typeAt, first)
	}
}

// TestBuildPageContentEscapesHostileValues proves the advisory frontmatter keys
// cannot break the YAML block, which would corrupt the authoritative blob
// sitting beside them.
func TestBuildPageContentEscapesHostileValues(t *testing.T) {
	fm := map[string]string{
		"type":      "atom",
		"predicate": "role_at",
		"object":    "title: \"VP\"\nnot_a_key: injected",
	}
	out := buildPageContent(fm, "body")
	// The hostile newline must be escaped inside the quoted scalar, so the
	// document still has exactly two frontmatter delimiters.
	if strings.Count(out, "\n---\n") != 0 && strings.Count(out, "---\n") != 2 {
		t.Fatalf("hostile value broke the frontmatter block:\n%s", out)
	}
	if strings.Contains(out, "\nnot_a_key: injected") {
		t.Fatalf("hostile value injected a frontmatter key:\n%s", out)
	}
}

func TestEntityKindToPageType(t *testing.T) {
	cases := map[string]string{
		"person":    "person",
		"company":   "company",
		"project":   "project",
		"team":      "concept",
		"workspace": "concept",
		"":          "concept",
		"PERSON":    "person",
	}
	for in, want := range cases {
		if got := entityKindToPageType(in); got != want {
			t.Errorf("entityKindToPageType(%q) = %q, want %q", in, got, want)
		}
	}
}
