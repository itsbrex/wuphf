package team

// wiki_gbrain_mapping.go — the WUPHF ⇄ gbrain data mapping.
//
// gbrain models a brain as pages + typed links + tags. WUPHF models the wiki as
// typed facts (subject/predicate/object), entities, edges, redirects, and a
// Wikipedia-style category layer. This file is the single place that translates
// between the two, so the store implementations stay readable.
//
// Namespace layout inside the brain:
//
//	atoms/<factID>            one page per TypedFact      (type: atom)
//	entities/<slug>           one page per IndexEntity    (type: person|company|project|concept)
//	categories/<slug>         one page per category       (type: concept)
//	articles/<b64(path)>      one page per wiki article   (type: note)
//
// Edges are gbrain links, never pages:
//
//	subject --<predicate>--> object     the entity graph + fact triplets
//	from    --redirect-->    to         slug merges
//	article --category-->    category   category membership
//	child   --parent_category--> parent category tree
//
// Why frontmatter carries a base64 JSON blob rather than YAML fields
// ===================================================================
// gbrain preserves frontmatter verbatim as JSONB, but the value has to survive
// a YAML round-trip on the way in. Fact text is arbitrary user content: it can
// contain apostrophes, colons, newlines, and unicode, every one of which needs
// different YAML quoting. Rather than build a correct YAML escaper, the full
// record is marshalled to JSON and base64-encoded into a single scalar. That is
// lossless for any input and cannot be broken by a YAML edge case.
//
// The human-readable fields (predicate, subject, object) are ALSO emitted as
// plain frontmatter keys. They are advisory only — useful when a human opens
// the brain, never read back by the store — so their escaping does not matter.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// gbrain namespace prefixes. Trailing slash included so callers can use them
// directly as slug_prefix filters on list_pages.
const (
	gbrainFactPrefix     = "atoms/"
	gbrainEntityPrefix   = "entities/"
	gbrainCategoryPrefix = "categories/"
	gbrainArticlePrefix  = "articles/"
)

// gbrainLinkSource marks every edge WUPHF writes, so a brain shared with a
// human's own notes keeps machine-written edges distinguishable from manual
// ones. gbrain exposes this via `gbrain link-sources`.
const gbrainLinkSource = "wuphf"

// Reserved link types for the non-triplet edges. Predicates from fact triplets
// use their own name as the link type, so these must not collide with any
// predicate the extractor emits.
const (
	gbrainLinkRedirect       = "wuphf_redirect"
	gbrainLinkCategory       = "wuphf_category"
	gbrainLinkParentCategory = "wuphf_parent_category"
)

// gbrainFactBlobKey is the frontmatter key holding the base64 JSON TypedFact.
const gbrainFactBlobKey = "wuphf_fact"

// gbrainEntityBlobKey is the frontmatter key holding the base64 JSON IndexEntity.
const gbrainEntityBlobKey = "wuphf_entity"

// entityPageTypes is the closed set entityKindToPageType can return, and the
// `types` filter used to restrict entity retrieval to entity pages.
//
// It must stay in sync with the switch below; the test asserts that.
var entityPageTypes = []string{"person", "company", "project", "concept"}

// entityKindToPageType maps a WUPHF entity kind onto gbrain's page-type
// vocabulary. Kinds without a native counterpart (team, workspace) are mapped
// explicitly to "concept" so the stored type is greppable and the set of types
// an entity page can carry stays closed — which is what makes entityPageTypes
// a safe retrieval filter.
//
// gbrain does NOT coerce unknown types: since v0.38 the page type is an open
// string and a custom type round-trips unchanged (verified on 0.48.1.0 by
// writing type "category" and reading it back). An earlier comment here
// claimed coercion; that was wrong, and correcting it is what allows the
// category layer to take a type of its own.
func entityKindToPageType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "person":
		return "person"
	case "company":
		return "company"
	case "project":
		return "project"
	default:
		// team, workspace, and anything the extractor invents later.
		return "concept"
	}
}

// factSlug returns the gbrain slug for a fact ID.
func factSlug(factID string) string {
	return gbrainFactPrefix + strings.TrimSpace(factID)
}

// factIDFromSlug reverses factSlug. Returns "" when the slug is not a fact.
func factIDFromSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if !strings.HasPrefix(slug, gbrainFactPrefix) {
		return ""
	}
	return strings.TrimPrefix(slug, gbrainFactPrefix)
}

// entitySlug returns the gbrain slug for a WUPHF entity slug. Entity slugs are
// globally unique in the wiki (they are the article identity), so a single flat
// namespace is safe and keeps triplet-object resolution trivial.
func entitySlug(slug string) string {
	return gbrainEntityPrefix + strings.TrimSpace(slug)
}

// entitySlugFromGBrain reverses entitySlug. Returns "" when not an entity page.
func entitySlugFromGBrain(slug string) string {
	slug = strings.TrimSpace(slug)
	if !strings.HasPrefix(slug, gbrainEntityPrefix) {
		return ""
	}
	return strings.TrimPrefix(slug, gbrainEntityPrefix)
}

// tripletRefToEntitySlug normalizes a triplet subject or object into the entity
// slug it references.
//
// WUPHF triplet objects come in two shapes (§4.2): a bare slug ("carol-mei") or
// a kind-qualified reference ("project:apac-launch"). Both denote the same
// entity page, so the kind prefix is stripped. A literal object that names no
// entity (for example a free-text job title) still round-trips through here
// unchanged; it simply will not resolve to a page, which is the correct
// behaviour — the graph edge is only created when both ends are entities.
func tripletRefToEntitySlug(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if idx := strings.Index(ref, ":"); idx >= 0 {
		return strings.TrimSpace(ref[idx+1:])
	}
	return ref
}

// categorySlug returns the gbrain slug for a category.
func gbrainCategorySlug(category string) string {
	return gbrainCategoryPrefix + strings.TrimSpace(category)
}

// categoryFromGBrainSlug reverses categorySlug.
func categoryFromGBrainSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if !strings.HasPrefix(slug, gbrainCategoryPrefix) {
		return ""
	}
	return strings.TrimPrefix(slug, gbrainCategoryPrefix)
}

// articleSlug encodes a wiki-root-relative article path into a gbrain slug.
//
// Article paths contain "/" and "." (e.g. "wiki/people/carol-mei.md"), neither
// of which is safe in a slug segment. base64url without padding is reversible,
// slug-safe, and stable — the same path always yields the same slug, which is
// what makes the category links idempotent across reconciles.
func articleSlug(articlePath string) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(articlePath)))
	return gbrainArticlePrefix + enc
}

// articlePathFromSlug reverses articleSlug. Returns "" when the slug is not an
// article page or its payload does not decode.
func articlePathFromSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if !strings.HasPrefix(slug, gbrainArticlePrefix) {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(slug, gbrainArticlePrefix))
	if err != nil {
		return ""
	}
	return string(raw)
}

// encodeBlob marshals v to JSON and base64-encodes it for safe frontmatter
// carriage. See the file header for why this is not plain YAML.
func encodeBlob(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// decodeBlob reverses encodeBlob into out.
func decodeBlob(raw string, out any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty blob")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	return json.Unmarshal(b, out)
}

// blobFromFrontmatter pulls a base64 blob out of a gbrain frontmatter map.
// gbrain returns JSONB as map[string]any, so the value arrives as a string.
func blobFromFrontmatter(fm map[string]any, key string) (string, bool) {
	if fm == nil {
		return "", false
	}
	v, ok := fm[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

// yamlScalar renders s as a YAML double-quoted scalar. Used only for the
// advisory human-readable frontmatter keys; the authoritative data travels in
// the base64 blob, so this needs to be safe, not clever.
func yamlScalar(s string) string {
	b, err := json.Marshal(s) // JSON string escaping is valid YAML double-quoted style
	if err != nil {
		return `""`
	}
	return string(b)
}

// buildPageContent assembles a markdown page: a YAML frontmatter block followed
// by the body. gbrain parses the frontmatter into JSONB and keeps the body as
// compiled_truth, which is what its chunker and search index consume.
func buildPageContent(frontmatter map[string]string, body string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	// Deterministic key order keeps content_hash stable across reconciles, so
	// an unchanged fact does not churn the brain's generation clock.
	for _, k := range sortedKeys(frontmatter) {
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(yamlScalar(frontmatter[k]))
		sb.WriteString("\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

// sortedKeys returns map keys in ascending order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Small maps; insertion sort keeps this allocation-free of a sort import
	// dependency in the hot reconcile path.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
