package team

// wiki_index_gbrain_entity_categories.go — the Wikipedia-style category layer
// for the entity-page store.
//
// Markdown stays authoritative: an article's `categories:` frontmatter is the
// source of truth and these rows are a rebuildable derived cache, which is why
// UpsertArticleCategories has set-REPLACE semantics.
//
// Categories are their own pages under gbrain's MECE `concepts/` directory
// (the schema doc's home for "mental models and frameworks you'd teach"), and
// membership is a typed link. Article stubs live under `sources/`, which the
// doc reserves for raw imports and archived snapshots — an article page here is
// a pointer to a file in the wiki git repo, not brain-owned prose.

import (
	"context"
	"sort"
	"strings"

	"github.com/nex-crm/wuphf/internal/gbrain"
)

// gbrainDirSources is where article stubs are filed. See the file header.
const gbrainDirSources = "sources/"

// entityCategoryDir is the category namespace.
//
// Categories deliberately do NOT share `concepts/` with concept-kind entities.
// They did, and it was a real collision: entitySlugFromPageSlug treats anything
// under an entity directory as an entity, so a category page returned by search
// was mistaken for an entity and sent through the fact-hydration path. A
// category and a concept entity with the same slug would also have overwritten
// each other outright.
const entityCategoryDir = "categories/"

// entityCategoryPageType is the page type for category pages.
//
// Categories used to be typed "concept", which is also the fallback type for
// concept-KIND entities (team, workspace, anything the extractor invents). That
// is the same collision the directory split above fixed, still present in the
// type dimension: with both sharing one type, a `types` filter cannot separate
// them, and a category page outranks real entities in entity retrieval
// (verified — a category page took the #1 slot).
//
// "category" is not in gbrain's seed vocabulary, but page types are open
// strings since v0.38 and a custom type round-trips unchanged (verified on
// 0.48.1.0). See entityKindToPageType.
//
// Migration: pages written before this change keep type "concept" until they
// are next rewritten, so entity retrieval also keeps a slug-prefix check as a
// backstop rather than trusting the server-side filter alone.
const entityCategoryPageType = "category"

// entityCategorySlug returns the page slug for a category.
func entityCategorySlug(category string) string {
	return entityCategoryDir + strings.TrimSpace(category)
}

// categoryFromEntityPageSlug reverses entityCategorySlug for category pages.
// Returns "" for a concepts/ page that is not a category.
func categoryFromEntityPageSlug(pageSlug, category string) string { //nolint:unused // symmetry with the atom backend
	want := entityCategorySlug(category)
	if pageSlug == want {
		return category
	}
	return ""
}

// entityArticleSlug encodes a wiki-relative article path into a page slug.
// Reuses the base64url encoding so the mapping is identical to the atom
// backend's and an article keeps one identity across both.
func entityArticleSlug(articlePath string) string {
	return gbrainDirSources + strings.TrimPrefix(articleSlug(articlePath), gbrainArticlePrefix)
}

// entityArticlePathFromSlug reverses entityArticleSlug.
func entityArticlePathFromSlug(pageSlug string) string {
	if !strings.HasPrefix(pageSlug, gbrainDirSources) {
		return ""
	}
	return articlePathFromSlug(gbrainArticlePrefix + strings.TrimPrefix(pageSlug, gbrainDirSources))
}

// ensurePlainPage creates a minimal page at slug if none exists, so links have
// endpoints. gbrain's add_link rejects an edge with a missing endpoint.
func (s *gbrainEntityStore) ensurePlainPage(ctx context.Context, slug, pageType, title string, fm map[string]string) error {
	if _, err := s.client.GetPage(ctx, slug); err == nil {
		return nil
	} else if !isNotFound(err) {
		return err
	}
	full := map[string]string{"type": pageType}
	for k, v := range fm {
		full[k] = v
	}
	if _, err := s.client.PutPage(ctx, buildPageContent(full, title), gbrain.PutOptions{
		Slug:        slug,
		IngestedVia: gbrainLinkSource,
	}); err != nil {
		return err
	}
	if gbrain.NeedsPutPageRestore(ctx) {
		if err := s.client.RestorePage(ctx, slug); err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

// UpsertArticleCategories replaces an article's full category membership set.
func (s *gbrainEntityStore) UpsertArticleCategories(ctx context.Context, articlePath string, categories []string) error {
	articlePath = strings.TrimSpace(articlePath)
	if articlePath == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	from := entityArticleSlug(articlePath)
	existing, err := s.client.Traverse(ctx, from, gbrainLinkCategory, "out", 1)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, e := range existing {
		for _, cat := range []string{strings.TrimPrefix(e.ToSlug, entityCategoryDir)} {
			if cat != "" && cat != e.ToSlug {
				current[cat] = true
			}
		}
	}
	desired := map[string]bool{}
	for _, c := range categories {
		if c = strings.TrimSpace(c); c != "" {
			desired[c] = true
		}
	}
	if len(desired) == 0 && len(current) == 0 {
		return nil
	}
	if len(desired) > 0 {
		if err := s.ensurePlainPage(ctx, from, "note", articlePath,
			map[string]string{"wuphf_article_path": articlePath}); err != nil {
			return err
		}
	}
	for _, cat := range sortedStrings(desired) {
		if current[cat] {
			continue
		}
		if err := s.ensurePlainPage(ctx, entityCategorySlug(cat), entityCategoryPageType, cat,
			map[string]string{"wuphf_category": cat}); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx, from, entityCategorySlug(cat), gbrainLinkCategory, gbrainLinkSource, ""); err != nil {
			return err
		}
	}
	for _, cat := range sortedStrings(current) {
		if desired[cat] {
			continue
		}
		if err := s.client.RemoveLink(ctx, from, entityCategorySlug(cat), gbrainLinkCategory); err != nil {
			return err
		}
	}
	return nil
}

// ListArticlesInCategory returns the article paths filed under a category.
func (s *gbrainEntityStore) ListArticlesInCategory(ctx context.Context, category string) ([]string, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, nil
	}
	edges, err := s.client.Traverse(ctx, entityCategorySlug(category), gbrainLinkCategory, "in", 1)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		if path := entityArticlePathFromSlug(e.FromSlug); path != "" && !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListCategoriesForArticle returns an article's category slugs.
func (s *gbrainEntityStore) ListCategoriesForArticle(ctx context.Context, articlePath string) ([]string, error) {
	articlePath = strings.TrimSpace(articlePath)
	if articlePath == "" {
		return nil, nil
	}
	edges, err := s.client.Traverse(ctx, entityArticleSlug(articlePath), gbrainLinkCategory, "out", 1)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		cat := strings.TrimPrefix(e.ToSlug, entityCategoryDir)
		if cat != "" && cat != e.ToSlug && !seen[cat] {
			seen[cat] = true
			out = append(out, cat)
		}
	}
	sort.Strings(out)
	return out, nil
}

// categoryPages lists the concepts/ pages that are categories (they carry the
// wuphf_category frontmatter key).
func (s *gbrainEntityStore) categoryPages(ctx context.Context) ([]string, error) {
	kept, err := s.client.ListAllPages(ctx, gbrain.ListPageOptions{
		SlugPrefix: entityCategoryDir,
		Limit:      gbrainListPageSize,
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, meta := range kept {
		page, err := s.client.GetPage(ctx, meta.Slug)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, ok := blobFromFrontmatter(page.Frontmatter, "wuphf_category"); ok {
			out = append(out, strings.TrimPrefix(meta.Slug, entityCategoryDir))
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListAllCategories returns every category with its article count.
func (s *gbrainEntityStore) ListAllCategories(ctx context.Context) ([]CategoryCount, error) {
	cats, err := s.categoryPages(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CategoryCount, 0, len(cats))
	for _, cat := range cats {
		articles, err := s.ListArticlesInCategory(ctx, cat)
		if err != nil {
			return nil, err
		}
		out = append(out, CategoryCount{Slug: cat, Count: len(articles)})
	}
	return out, nil
}

// UpsertCategoryParents replaces a category's parent edges.
func (s *gbrainEntityStore) UpsertCategoryParents(ctx context.Context, category string, parents []string) error {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	from := entityCategorySlug(category)
	existing, err := s.client.Traverse(ctx, from, gbrainLinkParentCategory, "out", 1)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, e := range existing {
		p := strings.TrimPrefix(e.ToSlug, entityCategoryDir)
		if p != "" && p != e.ToSlug {
			current[p] = true
		}
	}
	desired := map[string]bool{}
	for _, p := range parents {
		if p = strings.TrimSpace(p); p != "" {
			desired[p] = true
		}
	}
	if len(desired) == 0 && len(current) == 0 {
		return nil
	}
	if len(desired) > 0 {
		if err := s.ensurePlainPage(ctx, from, entityCategoryPageType, category,
			map[string]string{"wuphf_category": category}); err != nil {
			return err
		}
	}
	for _, p := range sortedStrings(desired) {
		if current[p] {
			continue
		}
		if err := s.ensurePlainPage(ctx, entityCategorySlug(p), entityCategoryPageType, p,
			map[string]string{"wuphf_category": p}); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx, from, entityCategorySlug(p), gbrainLinkParentCategory, gbrainLinkSource, ""); err != nil {
			return err
		}
	}
	for _, p := range sortedStrings(current) {
		if desired[p] {
			continue
		}
		if err := s.client.RemoveLink(ctx, from, entityCategorySlug(p), gbrainLinkParentCategory); err != nil {
			return err
		}
	}
	return nil
}

// ListCategoryParents returns a category's parent slugs.
func (s *gbrainEntityStore) ListCategoryParents(ctx context.Context, category string) ([]string, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, nil
	}
	edges, err := s.client.Traverse(ctx, entityCategorySlug(category), gbrainLinkParentCategory, "out", 1)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		p := strings.TrimPrefix(e.ToSlug, entityCategoryDir)
		if p != "" && p != e.ToSlug && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListAllCategoryParents returns every category to parent edge.
func (s *gbrainEntityStore) ListAllCategoryParents(ctx context.Context) ([]CategoryParent, error) {
	cats, err := s.categoryPages(ctx)
	if err != nil {
		return nil, err
	}
	var out []CategoryParent
	for _, cat := range cats {
		parents, err := s.ListCategoryParents(ctx, cat)
		if err != nil {
			return nil, err
		}
		for _, p := range parents {
			out = append(out, CategoryParent{Category: cat, Parent: p})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Parent < out[j].Parent
	})
	return out, nil
}
