package gbrain

// pages.go — graph + page-lifecycle calls the wiki context layer needs on top
// of the Client surface in mcp.go.
//
// mcp.go already covers query/search/get_page/list_pages/put_page/add_link/
// get_links. This file adds only the gaps the FactStore implementation hit:
//
//	Traverse    — traverse_graph, the typed directed walk that replaces the
//	              SQLite triplet indexes. get_links is undirected and untyped,
//	              so it cannot answer "who champions X".
//	RemoveLink  — required by the set-REPLACE semantics of the category layer.
//	DeletePage  — the only path that retires a fact; FactStore has no
//	              DeleteFact, so TextIndex.Delete carries it.
//
// It also adds ListAllPages: the cursor-paginated full scan. gbrain's
// list_pages caps at ~100 rows and silently drops `offset`, so a correct full
// enumeration needs an `updated_after` cursor. Deliberately there is NO
// exported non-paginating list helper — one would silently truncate, which is
// the defect this package exists to hide from callers.
//
// Contract notes verified against gbrain 0.42.58.0 by direct probe:
//   - traverse_graph returns EDGES ([]GraphEdge) only when link_type AND
//     direction are supplied; without them it returns nodes. Both are therefore
//     required here.
//   - add_link takes `from`/`to`, not `from_slug`/`to_slug`, and round-trips an
//     arbitrary `context` string verbatim — which is what lets an IndexEdge
//     carry its timestamp and source SHA without a column of its own.
//   - put_page REWRITES the title (it title-cases) and COERCES an unknown type
//     to "concept". Nothing may depend on either round-tripping. Frontmatter is
//     preserved verbatim as JSONB and is the only safe carrier for structured
//     data.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	toolTraverseGraph = "traverse_graph"
	toolRemoveLink    = "remove_link"
	toolDeletePage    = "delete_page"
	toolRestorePage   = "restore_page"
)

// BulkTimeout is the deadline for calls that touch many rows (list_pages over a
// large brain, traversals at depth). The Client's default call timeout is tuned
// for single-page reads and is too tight for these.
const BulkTimeout = 60 * time.Second

// PageTypeAtom is gbrain's "smallest extractable claim unit" type. It is the
// carrier for a WUPHF TypedFact: one atom page per fact. Verified to survive
// put_page/get_page without coercion on 0.42.58.0.
const PageTypeAtom = "atom"

// GraphEdge is one edge returned by traverse_graph when link_type and direction
// are supplied.
type GraphEdge struct {
	FromSlug string `json:"from_slug"`
	ToSlug   string `json:"to_slug"`
	LinkType string `json:"link_type"`
	Context  string `json:"context"`
	Depth    int    `json:"depth"`
}

// Traverse walks the link graph from slug under exactly one link type.
// direction is "in", "out", or "both". Supplying linkType is what makes gbrain
// return edges rather than nodes, so an empty linkType yields no results rather
// than silently switching result shapes.
func (c *Client) Traverse(ctx context.Context, slug, linkType, direction string, depth int) ([]GraphEdge, error) {
	slug = strings.TrimSpace(slug)
	linkType = strings.TrimSpace(linkType)
	if slug == "" || linkType == "" {
		return nil, nil
	}
	if direction == "" {
		direction = "both"
	}
	if depth <= 0 {
		depth = 1
	}
	raw, err := c.CallTool(ctx, toolTraverseGraph, map[string]any{
		"slug":      slug,
		"link_type": linkType,
		"direction": direction,
		"depth":     depth,
	})
	if err != nil {
		return nil, fmt.Errorf("gbrain traverse_graph %s/%s: %w", slug, linkType, err)
	}
	if isEmptyResult(raw) {
		return nil, nil
	}
	var edges []GraphEdge
	if err := decodeJSON(raw, &edges); err != nil {
		return nil, fmt.Errorf("decode gbrain traverse_graph %s: %w", slug, err)
	}
	return edges, nil
}

// RemoveLink deletes a typed edge. An empty linkType removes every edge between
// the pair, matching the CLI's unlink semantics.
func (c *Client) RemoveLink(ctx context.Context, from, to, linkType string) error {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" {
		return fmt.Errorf("gbrain remove_link: from and to are required")
	}
	args := map[string]any{"from": from, "to": to}
	if s := strings.TrimSpace(linkType); s != "" {
		args["link_type"] = s
	}
	if _, err := c.CallTool(ctx, toolRemoveLink, args); err != nil {
		return fmt.Errorf("gbrain remove_link %s->%s: %w", from, to, err)
	}
	return nil
}

// DeletePage soft-deletes a page. gbrain keeps it recoverable inside its
// retention window, which is what makes this safe to call from a reconcile
// loop that may be acting on stale input.
func (c *Client) DeletePage(ctx context.Context, slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil
	}
	if _, err := c.CallTool(ctx, toolDeletePage, map[string]any{"slug": slug}); err != nil {
		return fmt.Errorf("gbrain delete_page %s: %w", slug, err)
	}
	return nil
}

// ListPageOptions extends ListOptions with the filters a full corpus scan
// needs. ListOptions itself is left untouched so existing callers are
// unaffected.
type ListPageOptions struct {
	Type           string
	Tag            string
	SlugPrefix     string
	Limit          int
	Offset         int
	IncludeDeleted bool
}

// isEmptyResult reports whether a tool result carries no rows. gbrain signals
// absence with an empty body or a JSON null rather than an error.
func isEmptyResult(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw == "" || raw == "null" || raw == "[]"
}

// RestorePage clears a page's soft-delete tombstone.
//
// This exists because put_page does NOT clear deleted_at: writing to a
// soft-deleted slug updates the row but leaves it invisible to get_page and to
// search. Verified against gbrain 0.42.58.0. Any upsert path that can target a
// previously deleted slug must call this, or the write silently disappears.
//
// It is idempotent on a live page ("already_active") and returns a not-found
// error for a slug that never existed, which callers may ignore.
func (c *Client) RestorePage(ctx context.Context, slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil
	}
	if _, err := c.CallTool(ctx, toolRestorePage, map[string]any{"slug": slug}); err != nil {
		return fmt.Errorf("gbrain restore_page %s: %w", slug, err)
	}
	return nil
}

// ListAllPages enumerates EVERY page matching opts, working around the fact
// that gbrain's list_pages cannot paginate by offset.
//
// The problem
// ===========
// list_pages caps at ~100 rows server-side and ACCEPTS BUT SILENTLY DROPS
// `offset` — core/operations.ts calls engine.listPages({type, updated_after,
// limit, ...scope}) and there is no offset parameter at all (verified: offset=0
// and offset=2 return byte-identical rows). So the two obvious loops are both
// wrong: stopping on a short batch truncates at the cap, and looping until an
// empty batch never terminates.
//
// The cursor
// ==========
// `updated_after` IS honoured, and so is `sort=updated_asc`. Together they give
// a forward cursor: sort ascending, remember the batch's newest timestamp, ask
// for everything after it.
//
// `updated_after` is STRICTLY greater-than, so advancing the cursor to the
// batch maximum would drop any rows sharing that exact timestamp that did not
// fit in the batch. The cursor therefore advances only to the SECOND-largest
// distinct timestamp in the batch, so the boundary rows are deliberately
// re-fetched next round and deduplicated by slug. That trades a little repeated
// work for not losing rows.
//
// Termination: the cursor strictly increases every iteration, because the next
// batch's second-largest timestamp is at least the previous batch's maximum.
// The one unrepresentable case is a batch whose rows ALL share one timestamp
// and which fills the cap — a tie cluster larger than the page size, where no
// cursor value can advance without loss. That returns an error rather than
// silently skipping rows.
func (c *Client) ListAllPages(ctx context.Context, opts ListPageOptions) ([]PageMeta, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	seen := map[string]bool{}
	var out []PageMeta
	cursor := ""

	for iter := 0; ; iter++ {
		if iter > maxListPageIterations {
			return nil, fmt.Errorf("gbrain list_pages: cursor did not terminate after %d pages", maxListPageIterations)
		}
		batch, raw, err := c.listPageBatchCursor(ctx, opts, cursor)
		if err != nil {
			return nil, err
		}
		if raw == 0 {
			return out, nil
		}
		for _, p := range batch {
			if !seen[p.Slug] {
				seen[p.Slug] = true
				out = append(out, p)
			}
		}
		// A short batch means the cursor reached the end.
		if raw < opts.Limit {
			return out, nil
		}
		next, ok := penultimateTimestamp(batch)
		if !ok {
			return nil, fmt.Errorf(
				"gbrain list_pages: %d rows share one updated_at timestamp, which exceeds the page size; cannot advance the cursor without dropping rows",
				raw)
		}
		cursor = next
	}
}

// maxListPageIterations bounds ListAllPages. At 100 rows a page this allows a
// 100k-page brain while still failing fast on a cursor that cannot advance.
const maxListPageIterations = 1000

// listPageBatchCursor fetches one ascending page starting after `cursor`.
func (c *Client) listPageBatchCursor(ctx context.Context, opts ListPageOptions, cursor string) (kept []PageMeta, raw int, err error) {
	args := map[string]any{
		"limit": opts.Limit,
		// Ascending order is what makes updated_after a forward cursor; the
		// default is descending, against which the cursor cannot walk.
		"sort": "updated_asc",
	}
	if cursor != "" {
		args["updated_after"] = cursor
	}
	if t := strings.TrimSpace(opts.Type); t != "" {
		args["type"] = t
	}
	if tag := strings.TrimSpace(opts.Tag); tag != "" {
		args["tag"] = tag
	}
	// slug_prefix is deliberately NOT sent. It is not a list_pages parameter —
	// it never was, which is why the filter below exists — and since 0.48
	// gbrain warns "unknown parameter ... ignored. A future release rejects
	// unknown parameters." Sending it bought nothing and would eventually be a
	// hard error.
	if opts.IncludeDeleted {
		args["include_deleted"] = true
	}
	out, err := c.CallTool(ctx, toolListPages, args)
	if err != nil {
		return nil, 0, err
	}
	if isEmptyResult(out) {
		return nil, 0, nil
	}
	var pages []PageMeta
	if err := decodeJSON(out, &pages); err != nil {
		return nil, 0, fmt.Errorf("decode gbrain list_pages: %w", err)
	}
	raw = len(pages)
	if prefix := strings.TrimSpace(opts.SlugPrefix); prefix != "" {
		filtered := pages[:0]
		for _, p := range pages {
			if strings.HasPrefix(p.Slug, prefix) {
				filtered = append(filtered, p)
			}
		}
		pages = filtered
	}
	return pages, raw, nil
}

// penultimateTimestamp returns the second-largest DISTINCT updated_at in a
// batch — the safe cursor value, since rows at the maximum may be incomplete.
// Reports false when the batch has fewer than two distinct timestamps.
func penultimateTimestamp(batch []PageMeta) (string, bool) {
	distinct := map[string]bool{}
	for _, p := range batch {
		if ts := strings.TrimSpace(p.Updated); ts != "" {
			distinct[ts] = true
		}
	}
	if len(distinct) < 2 {
		return "", false
	}
	all := make([]string, 0, len(distinct))
	for ts := range distinct {
		all = append(all, ts)
	}
	// RFC3339 with a fixed offset sorts correctly as a string, which is the
	// format gbrain emits (…Z).
	sort.Strings(all)
	return all[len(all)-2], true
}
