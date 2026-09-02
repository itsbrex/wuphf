package team

import (
	"strings"
	"testing"
)

// Hermetic coverage for the gbrain category namespace mapping.
//
// The category layer had no tests at all on this backend: the shared harness in
// wiki_categories_test.go covers only the in-memory and SQLite stores. That gap
// let a real namespace collision ship — categories and concept-kind entities
// both lived under `concepts/`, so a category page was indistinguishable from
// an entity and a same-slug pair would overwrite each other.
//
// These are pure functions, so they run everywhere on every suite invocation.

// TestCategoryNamespaceIsDisjointFromEntities is the regression guard for that
// collision. It must stay true for every entity directory, not just the one
// that happened to clash.
func TestCategoryNamespaceIsDisjointFromEntities(t *testing.T) {
	const name = "ai-agents"
	catSlug := entityCategorySlug(name)

	for _, dir := range allEntityDirs() {
		if strings.HasPrefix(catSlug, dir) {
			t.Errorf("category slug %q sits under entity directory %q — search would "+
				"mistake a category page for an entity, and a same-slug pair would collide",
				catSlug, dir)
		}
	}

	// The consequence that actually bit: a category slug must not resolve as an
	// entity slug.
	if got := entitySlugFromPageSlug(catSlug); got != "" {
		t.Errorf("entitySlugFromPageSlug(%q) = %q, want empty — a category is not an entity",
			catSlug, got)
	}
}

func TestCategorySlugRoundTrip(t *testing.T) {
	for _, name := range []string{"ai-agents", "revenue-operations", "q3-planning"} {
		slug := entityCategorySlug(name)
		if !strings.HasPrefix(slug, entityCategoryDir) {
			t.Errorf("entityCategorySlug(%q) = %q, missing the category prefix", name, slug)
		}
		if got := strings.TrimPrefix(slug, entityCategoryDir); got != name {
			t.Errorf("category round-trip = %q, want %q", got, name)
		}
	}
}

// TestArticleSlugRoundTripOnEntityBackend covers the sources/ encoding. Article
// paths carry "/" and "." which are not slug-safe, so a lossy encoding would
// silently detach articles from their categories.
func TestArticleSlugRoundTripOnEntityBackend(t *testing.T) {
	paths := []string{
		"team/companies/acme.md",
		"team/.categories/ai-agents.md",
		"team/people/josé-garcía.md",
		"team/notes/Q3 planning.md",
	}
	for _, p := range paths {
		slug := entityArticleSlug(p)
		if !strings.HasPrefix(slug, gbrainDirSources) {
			t.Errorf("entityArticleSlug(%q) = %q, missing the sources prefix", p, slug)
		}
		seg := strings.TrimPrefix(slug, gbrainDirSources)
		if strings.ContainsAny(seg, "/.") {
			t.Errorf("article slug segment %q is not slug-safe", seg)
		}
		if got := entityArticlePathFromSlug(slug); got != p {
			t.Errorf("article round-trip = %q, want %q", got, p)
		}
		// An article must never be mistaken for an entity either.
		if got := entitySlugFromPageSlug(slug); got != "" {
			t.Errorf("entitySlugFromPageSlug(%q) = %q, want empty", slug, got)
		}
	}
}

func TestArticlePathFromSlugRejectsForeignSlugs(t *testing.T) {
	for _, s := range []string{"people/carol-mei", "categories/ai-agents", "sources/!!not-base64!!"} {
		if got := entityArticlePathFromSlug(s); got != "" {
			t.Errorf("entityArticlePathFromSlug(%q) = %q, want empty", s, got)
		}
	}
}
