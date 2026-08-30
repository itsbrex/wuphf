package team

// wiki_index_gbrain_categories.go — the Wikipedia-style category layer on gbrain.
//
// Markdown stays authoritative: an article's `categories:` frontmatter is the
// source of truth and these rows are a rebuildable derived cache. That is why
// UpsertArticleCategories has set-REPLACE semantics — the derived index must
// track the article's frontmatter exactly on every reconcile, including
// removals.
//
// gbrain has no category primitive, so membership is modelled as typed links:
//
//	articles/<b64(path)> --wuphf_category--> categories/<slug>
//	categories/<child>   --wuphf_parent_category--> categories/<parent>
//
// Replace semantics therefore mean "traverse, unlink what is gone, link what is
// new" rather than a single DELETE + INSERT. Concurrent reconciles of the same
// article would interleave those steps, so both replace paths hold writeMu for
// the whole read-modify-write, not just the writes.

import (
	"context"
	"sort"
	"strings"
)

// ensureCategoryPage creates the category's page if it does not exist yet.
// Links require both endpoints to exist, and categories are otherwise implicit.
func (s *gbrainFactStore) ensureCategoryPage(ctx context.Context, category string) error {
	slug := categorySlug(category)
	if _, ok, err := s.getPage(ctx, slug); err != nil {
		return err
	} else if ok {
		return nil
	}
	fm := map[string]string{"type": "concept", "wuphf_category": category}
	return s.putPage(ctx, slug, buildPageContent(fm, category))
}

// ensureArticlePage creates the article's stub page if absent. The page exists
// only to anchor category links; the article's real content lives in the wiki
// git repo, which remains the substrate.
func (s *gbrainFactStore) ensureArticlePage(ctx context.Context, articlePath string) error {
	slug := articleSlug(articlePath)
	if _, ok, err := s.getPage(ctx, slug); err != nil {
		return err
	} else if ok {
		return nil
	}
	fm := map[string]string{"type": "note", "wuphf_article_path": articlePath}
	return s.putPage(ctx, slug, buildPageContent(fm, articlePath))
}

// UpsertArticleCategories replaces an article's full category membership set.
func (s *gbrainFactStore) UpsertArticleCategories(ctx context.Context, articlePath string, categories []string) error {
	articlePath = strings.TrimSpace(articlePath)
	if articlePath == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	from := articleSlug(articlePath)

	// Current membership.
	existing, err := s.client.Traverse(ctx, from, gbrainLinkCategory, "out", 1)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, e := range existing {
		if cat := categoryFromGBrainSlug(e.ToSlug); cat != "" {
			current[cat] = true
		}
	}

	// Desired membership.
	desired := map[string]bool{}
	for _, c := range categories {
		if c = strings.TrimSpace(c); c != "" {
			desired[c] = true
		}
	}

	// An article with no categories still needs no stub page; skip the write
	// entirely when there is nothing to link and nothing to unlink.
	if len(desired) == 0 && len(current) == 0 {
		return nil
	}
	if len(desired) > 0 {
		if err := s.ensureArticlePage(ctx, articlePath); err != nil {
			return err
		}
	}

	for _, cat := range sortedStrings(desired) {
		if current[cat] {
			continue
		}
		if err := s.ensureCategoryPage(ctx, cat); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx, from, categorySlug(cat), gbrainLinkCategory, gbrainLinkSource, ""); err != nil {
			return err
		}
	}
	for _, cat := range sortedStrings(current) {
		if desired[cat] {
			continue
		}
		if err := s.client.RemoveLink(ctx, from, categorySlug(cat), gbrainLinkCategory); err != nil {
			return err
		}
	}
	return nil
}

// ListArticlesInCategory returns the article paths filed under a category.
func (s *gbrainFactStore) ListArticlesInCategory(ctx context.Context, category string) ([]string, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, nil
	}
	edges, err := s.client.Traverse(ctx, categorySlug(category), gbrainLinkCategory, "in", 1)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		if path := articlePathFromSlug(e.FromSlug); path != "" && !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListCategoriesForArticle returns an article's category slugs.
func (s *gbrainFactStore) ListCategoriesForArticle(ctx context.Context, articlePath string) ([]string, error) {
	articlePath = strings.TrimSpace(articlePath)
	if articlePath == "" {
		return nil, nil
	}
	edges, err := s.client.Traverse(ctx, articleSlug(articlePath), gbrainLinkCategory, "out", 1)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		if cat := categoryFromGBrainSlug(e.ToSlug); cat != "" && !seen[cat] {
			seen[cat] = true
			out = append(out, cat)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListAllCategories returns every category with its article count.
func (s *gbrainFactStore) ListAllCategories(ctx context.Context) ([]CategoryCount, error) {
	pages, err := s.allPageMetas(ctx, gbrainCategoryPrefix, "")
	if err != nil {
		return nil, err
	}
	out := make([]CategoryCount, 0, len(pages))
	for _, p := range pages {
		cat := categoryFromGBrainSlug(p.Slug)
		if cat == "" {
			continue
		}
		articles, err := s.ListArticlesInCategory(ctx, cat)
		if err != nil {
			return nil, err
		}
		out = append(out, CategoryCount{Slug: cat, Count: len(articles)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// UpsertCategoryParents replaces a category's parent edges.
func (s *gbrainFactStore) UpsertCategoryParents(ctx context.Context, category string, parents []string) error {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	from := categorySlug(category)
	existing, err := s.client.Traverse(ctx, from, gbrainLinkParentCategory, "out", 1)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, e := range existing {
		if p := categoryFromGBrainSlug(e.ToSlug); p != "" {
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
		if err := s.ensureCategoryPage(ctx, category); err != nil {
			return err
		}
	}
	for _, p := range sortedStrings(desired) {
		if current[p] {
			continue
		}
		if err := s.ensureCategoryPage(ctx, p); err != nil {
			return err
		}
		if err := s.client.AddLink(ctx, from, categorySlug(p), gbrainLinkParentCategory, gbrainLinkSource, ""); err != nil {
			return err
		}
	}
	for _, p := range sortedStrings(current) {
		if desired[p] {
			continue
		}
		if err := s.client.RemoveLink(ctx, from, categorySlug(p), gbrainLinkParentCategory); err != nil {
			return err
		}
	}
	return nil
}

// ListCategoryParents returns a category's parent slugs.
func (s *gbrainFactStore) ListCategoryParents(ctx context.Context, category string) ([]string, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, nil
	}
	edges, err := s.client.Traverse(ctx, categorySlug(category), gbrainLinkParentCategory, "out", 1)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		if p := categoryFromGBrainSlug(e.ToSlug); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListAllCategoryParents returns every category to parent edge.
func (s *gbrainFactStore) ListAllCategoryParents(ctx context.Context) ([]CategoryParent, error) {
	pages, err := s.allPageMetas(ctx, gbrainCategoryPrefix, "")
	if err != nil {
		return nil, err
	}
	var out []CategoryParent
	for _, p := range pages {
		cat := categoryFromGBrainSlug(p.Slug)
		if cat == "" {
			continue
		}
		parents, err := s.ListCategoryParents(ctx, cat)
		if err != nil {
			return nil, err
		}
		for _, parent := range parents {
			out = append(out, CategoryParent{Category: cat, Parent: parent})
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
