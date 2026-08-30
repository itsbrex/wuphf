package team

// wiki_gbrain_page_render.go — renders WUPHF entities as gbrain's RECOMMENDED
// page shape, and parses them back.
//
// Source: gbrain's own docs/GBRAIN_RECOMMENDED_SCHEMA.md ("Brain: The
// LLM-Maintained Knowledge Base"). The three founding principles it states are:
//
//	1. Every piece of knowledge has a primary home (MECE directories) —
//	   one page per entity, filed under people/ companies/ projects/ concepts/.
//	2. Compiled Truth + Timeline (two-layer pages) — above the `---` is the
//	   always-current synthesis; below it is an append-only, dated, sourced
//	   evidence log.
//	3. Enrichment fires on every signal.
//
// Why this replaced the first implementation
// ==========================================
// The first gbrain backend wrote ONE PAGE PER FACT (475 atom pages for the
// bench corpus). That is precisely the pattern this document exists to argue
// against: "the synthesis is pre-computed. Unlike RAG, where the LLM re-derives
// knowledge from scratch every query, your brain has already done the work."
// One page per fact turns gbrain back into flat RAG over 475 fragments, and it
// measured that way — status queries scored 60% because a person's facts were
// scattered across hundreds of separately-ranked pages.
//
// Under this shape a person's whole timeline lives on ONE page, so a single
// retrieval hit yields every fact about them.
//
// How fact-level citations survive
// ================================
// WUPHF cites individual facts (SearchHit.FactID), but gbrain retrieves pages
// and chunks. The bridge is a per-line anchor:
//
//	- **2026-04-22** | chat — Esme Walker is Ops Lead at Dunder Mifflin. ^f-abc123
//
// `^id` is Obsidian's block-reference syntax, which suits a repo that already
// ships Obsidian-vault compatibility. Verified against gbrain 0.42.58.0: the
// page splits at `---` into the `compiled_truth` and `timeline` columns, BOTH
// are chunked (chunk_source distinguishes them), and the anchors survive
// verbatim in the timeline chunk. So a chunk hit can be mapped back to the
// exact facts it contains.
//
// Full fidelity rides in frontmatter, not in the prose. The rendered timeline
// line is lossy by design (it is for humans and for the text index); the
// authoritative TypedFact records travel as a base64 JSON map under
// `wuphf_facts`. gbrain's page-size sanity check counts only
// compiled_truth + timeline, so the blob does not push a page toward the 50KB
// warn / 500KB block thresholds.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// gbrain's MECE directories, by WUPHF entity kind. Filing every entity under
// exactly one of these is principle 1; the directory IS the primary home.
const (
	gbrainDirPeople    = "people/"
	gbrainDirCompanies = "companies/"
	gbrainDirProjects  = "projects/"
	gbrainDirConcepts  = "concepts/"
)

// gbrainFactsBlobKey holds the base64 JSON map of factID -> TypedFact. This is
// the authoritative record; the rendered timeline is a derived view.
const gbrainFactsBlobKey = "wuphf_facts"

// factAnchorRe extracts the fact IDs a chunk of timeline text refers to.
// Anchored to end-of-line so a `^` inside prose cannot be mistaken for one.
var factAnchorRe = regexp.MustCompile(`\^([A-Za-z0-9_-]+)\s*$`)

// entityPageSlug returns the gbrain slug for an entity, filed into the MECE
// directory for its kind. The slug IS the entity identity, per the schema doc's
// "canonical slugs" rule, so this must stay stable for a given (kind, slug).
func entityPageSlug(kind, slug string) string {
	return entityDirForKind(kind) + strings.TrimSpace(slug)
}

// entityDirForKind maps a WUPHF entity kind onto its primary-home directory.
// Kinds without a natural home (team, workspace) file under concepts/, which
// the schema doc defines as "mental models and frameworks you'd teach" — the
// closest fit, and an explicit choice rather than a silent default.
func entityDirForKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "person":
		return gbrainDirPeople
	case "company":
		return gbrainDirCompanies
	case "project":
		return gbrainDirProjects
	default:
		return gbrainDirConcepts
	}
}

// allEntityDirs lists every directory entity pages can live in, for scans.
func allEntityDirs() []string {
	return []string{gbrainDirPeople, gbrainDirCompanies, gbrainDirProjects, gbrainDirConcepts}
}

// entitySlugFromPageSlug reverses entityPageSlug. Returns "" when the slug is
// not in one of the entity directories.
func entitySlugFromPageSlug(pageSlug string) string {
	pageSlug = strings.TrimSpace(pageSlug)
	for _, dir := range allEntityDirs() {
		if strings.HasPrefix(pageSlug, dir) {
			return strings.TrimPrefix(pageSlug, dir)
		}
	}
	return ""
}

// pageTypeForKind maps a WUPHF kind onto gbrain's page-type vocabulary.
func pageTypeForKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "person":
		return "person"
	case "company":
		return "company"
	case "project":
		return "project"
	default:
		return "concept"
	}
}

// factsForRender orders facts newest-first, matching the schema doc's
// "reverse-chronological evidence log". Ties break on ID so the rendered page
// is byte-stable across reconciles — an unstable order would change
// content_hash on every write and churn gbrain's generation clock.
func factsForRender(facts map[string]TypedFact) []TypedFact {
	out := make([]TypedFact, 0, len(facts))
	for _, f := range facts {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := factRenderTime(out[i]), factRenderTime(out[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// factRenderTime picks the date a fact is filed under in the timeline.
func factRenderTime(f TypedFact) time.Time {
	if !f.ValidFrom.IsZero() {
		return f.ValidFrom
	}
	return f.CreatedAt
}

// renderTimelineLine renders one evidence-log entry in the schema doc's format:
//
//	- **YYYY-MM-DD** | Source — What happened. ^factID
//
// The anchor is last so factAnchorRe can pin it to end-of-line.
func renderTimelineLine(f TypedFact) string {
	source := strings.TrimSpace(f.SourceType)
	if source == "" {
		source = "unknown"
	}
	// Newlines would break the one-entry-per-line contract the anchor parser
	// depends on, so fact text is flattened.
	text := strings.Join(strings.Fields(f.Text), " ")
	if text == "" {
		text = "(no text)"
	}
	return fmt.Sprintf("- **%s** | %s — %s ^%s",
		factRenderTime(f).UTC().Format("2006-01-02"), source, text, f.ID)
}

// parseFactAnchors returns the fact IDs referenced by a chunk of timeline text,
// in the order they appear, deduplicated.
//
// This is what converts a gbrain chunk hit back into WUPHF fact citations. A
// chunk covering a whole person's timeline yields every fact about them, which
// is the retrieval property the one-page-per-entity shape buys.
func parseFactAnchors(chunk string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(chunk, "\n") {
		m := factAnchorRe.FindStringSubmatch(strings.TrimRight(line, " \t"))
		if len(m) < 2 {
			continue
		}
		id := m[1]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// snippetForFact returns the timeline line for a fact ID out of a chunk, so a
// citation shows the specific evidence line rather than the whole chunk.
// Falls back to "" when the chunk does not contain that anchor.
func snippetForFact(chunk, factID string) string {
	suffix := "^" + factID
	for _, line := range strings.Split(chunk, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if !strings.HasSuffix(trimmed, suffix) {
			continue
		}
		body := strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
		// Strip the "- **date** | source — " prefix so the snippet is prose.
		if idx := strings.Index(body, "— "); idx >= 0 {
			body = body[idx+len("— "):]
		}
		return strings.TrimSpace(body)
	}
	return ""
}

// renderEntityPage builds the full two-layer markdown page for an entity.
//
// Layout follows the schema doc exactly: frontmatter, an H1, a one-paragraph
// executive summary, State, Open Threads, then `---`, then the Timeline. gbrain
// splits on that `---` into its compiled_truth and timeline columns.
func renderEntityPage(e IndexEntity, facts map[string]TypedFact) (string, error) {
	entityBlob, err := encodeBlob(e)
	if err != nil {
		return "", fmt.Errorf("encode entity %s: %w", e.Slug, err)
	}
	factsBlob, err := encodeBlob(facts)
	if err != nil {
		return "", fmt.Errorf("encode facts for %s: %w", e.Slug, err)
	}

	fm := map[string]string{
		"type":              pageTypeForKind(e.Kind),
		gbrainEntityBlobKey: entityBlob,
		gbrainFactsBlobKey:  factsBlob,
		"wuphf_slug":        e.Slug,
		"wuphf_kind":        e.Kind,
	}
	if len(e.Aliases) > 0 {
		// Advisory: the schema doc's alias field is what stops split-brain
		// pages. Authoritative copy is in the entity blob.
		fm["aliases"] = strings.Join(e.Aliases, ", ")
	}

	ordered := factsForRender(facts)

	var b strings.Builder
	title := strings.TrimSpace(e.Signals.PersonName)
	if title == "" {
		title = e.Slug
	}
	fmt.Fprintf(&b, "# %s\n\n", title)

	// Executive summary — the schema doc's "if you read only this, you know the
	// state of play". Derived from the current facts, never hand-written.
	fmt.Fprintf(&b, "> %s\n\n", entitySummary(e, ordered))

	b.WriteString("## State\n")
	wroteState := false
	for _, line := range entityStateLines(e) {
		fmt.Fprintf(&b, "- %s\n", line)
		wroteState = true
	}
	if !wroteState {
		// The schema doc is explicit: leave empty sections as [No data yet]
		// rather than omitting them, because "the structure itself is a prompt
		// for future enrichment".
		b.WriteString("- [No data yet]\n")
	}
	b.WriteString("\n")

	// Timeline below the horizontal rule. gbrain splits here.
	b.WriteString("---\n\n## Timeline\n")
	if len(ordered) == 0 {
		b.WriteString("- [No data yet]\n")
	}
	for _, f := range ordered {
		b.WriteString(renderTimelineLine(f))
		b.WriteString("\n")
	}

	return buildPageContent(fm, b.String()), nil
}

// entitySummary composes the one-paragraph executive summary from the entity's
// signals and its most recent facts.
func entitySummary(e IndexEntity, ordered []TypedFact) string {
	name := strings.TrimSpace(e.Signals.PersonName)
	if name == "" {
		name = e.Slug
	}
	if title := strings.TrimSpace(e.Signals.JobTitle); title != "" {
		return fmt.Sprintf("%s — %s. %d recorded fact(s).", name, title, len(ordered))
	}
	if len(ordered) > 0 {
		return fmt.Sprintf("%s. Most recent: %s", name,
			strings.Join(strings.Fields(ordered[0].Text), " "))
	}
	return fmt.Sprintf("%s. [No data yet]", name)
}

// entityStateLines renders the structured State block from entity signals.
func entityStateLines(e IndexEntity) []string {
	var out []string
	if v := strings.TrimSpace(e.Signals.JobTitle); v != "" {
		out = append(out, "**Role:** "+v)
	}
	if v := strings.TrimSpace(e.Signals.Email); v != "" {
		out = append(out, "**Email:** "+v)
	}
	if v := strings.TrimSpace(e.Signals.Domain); v != "" {
		out = append(out, "**Domain:** "+v)
	}
	if v := strings.TrimSpace(e.Kind); v != "" {
		out = append(out, "**Kind:** "+v)
	}
	if len(e.Aliases) > 0 {
		out = append(out, "**Aliases:** "+strings.Join(e.Aliases, ", "))
	}
	return out
}

// decodeEntityPage recovers the authoritative entity and fact records from a
// page's frontmatter. A page missing the blobs is not ours to interpret.
func decodeEntityPage(fm map[string]any, pageSlug string) (IndexEntity, map[string]TypedFact, error) {
	var e IndexEntity
	raw, ok := blobFromFrontmatter(fm, gbrainEntityBlobKey)
	if !ok {
		return e, nil, fmt.Errorf("gbrain page %s: missing %s frontmatter", pageSlug, gbrainEntityBlobKey)
	}
	if err := decodeBlob(raw, &e); err != nil {
		return e, nil, fmt.Errorf("gbrain page %s entity: %w", pageSlug, err)
	}

	facts := map[string]TypedFact{}
	if rawFacts, ok := blobFromFrontmatter(fm, gbrainFactsBlobKey); ok {
		if err := decodeBlob(rawFacts, &facts); err != nil {
			return e, nil, fmt.Errorf("gbrain page %s facts: %w", pageSlug, err)
		}
	}
	return e, facts, nil
}
