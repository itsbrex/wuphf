package team

// Legacy knowledge preservation: a workspace upgrading from the office-era
// product keeps its wiki articles (wiki/team/**.md) and per-agent notebook
// notes (wiki/agents/<agent>/notebook/*.md) as Knowledge pages, verbatim,
// appended to every knowledge response.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedLegacyWiki writes a small office-era wiki tree under home/.wuphf/wiki.
func seedLegacyWiki(t *testing.T, home string) {
	t.Helper()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(home, ".wuphf", "wiki", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("team/research/rag-brief.md", strings.Join([]string{
		"---",
		`title: "RAG retrieval quality brief"`,
		"owner: rag-engineer",
		"---",
		"",
		"Hybrid search beats pure semantic retrieval in our benchmarks.",
		"",
		"## Findings",
		"",
		"BM25 plus embeddings fused with RRF improved recall by 18%.",
		"",
		"Rank-sensitive metrics matter more than hit rate.",
		"",
		"## Next steps",
		"",
		"Wire the reranker into slice 2.",
	}, "\n"))
	write("team/decisions/OFFICE-59.md", "# Adopt RRF fusion\n\nDecision: fuse BM25 and semantic scores with RRF.\n")
	write("team/.obsidian/app.json", "{}")
	write("agents/rag-engineer/notebook/papers-survey.md", "# RAG papers survey\n\nNotes on 2024-2025 retrieval techniques.\n")
	write("agents/rag-engineer/notebook/.gitkeep", "")
	write("agents/outbound/notebook/empty-note.md", "\n\n")
}

func TestLoadLegacyKnowledgePages(t *testing.T) {
	home := t.TempDir()
	seedLegacyWiki(t, home)
	pages := loadLegacyKnowledgePages(filepath.Join(home, ".wuphf", "wiki"))

	if len(pages) != 3 {
		titles := make([]string, 0, len(pages))
		for _, p := range pages {
			titles = append(titles, p.Category+" / "+p.Title)
		}
		t.Fatalf("pages = %d (%v), want 3 (2 wiki + 1 notebook; .obsidian, .gitkeep, empty skipped)", len(pages), titles)
	}

	byID := map[string]appKnowledgePage{}
	for _, p := range pages {
		byID[p.ID] = p
	}

	brief, ok := byID["legacy-wiki-"+slugifyKnowledgeID("research/rag-brief.md")]
	if !ok {
		t.Fatalf("missing wiki brief page; got %v", byID)
	}
	// Frontmatter title wins; the folder is the category; content is verbatim.
	if brief.Title != "RAG retrieval quality brief" {
		t.Fatalf("brief title = %q", brief.Title)
	}
	if brief.Category != "Team wiki · research" {
		t.Fatalf("brief category = %q", brief.Category)
	}
	if !strings.Contains(brief.Lead, "Hybrid search beats pure semantic") {
		t.Fatalf("brief lead = %q", brief.Lead)
	}
	if len(brief.Sections) != 2 || brief.Sections[0].Heading != "Findings" || len(brief.Sections[0].Paras) != 2 {
		t.Fatalf("brief sections = %+v", brief.Sections)
	}
	if brief.Summary == "" || brief.References == nil || brief.Infobox == nil {
		t.Fatalf("brief must have a summary and non-nil slices: %+v", brief)
	}
	if len(brief.Categories) == 0 || brief.Categories[0] != legacyKnowledgeCategoryTag {
		t.Fatalf("brief categories = %v", brief.Categories)
	}

	decision, ok := byID["legacy-wiki-"+slugifyKnowledgeID("decisions/OFFICE-59.md")]
	if !ok || decision.Title != "Adopt RRF fusion" {
		t.Fatalf("decision page = %+v ok=%v (H1 must become the title)", decision, ok)
	}

	note, ok := byID["legacy-notebook-"+slugifyKnowledgeID("rag-engineer-papers-survey.md")]
	if !ok || note.Category != "Notebook · rag-engineer" || note.Title != "RAG papers survey" {
		t.Fatalf("notebook page = %+v ok=%v", note, ok)
	}
}

func TestLoadLegacyKnowledgePagesMissingTree(t *testing.T) {
	if pages := loadLegacyKnowledgePages(filepath.Join(t.TempDir(), "nope")); len(pages) != 0 {
		t.Fatalf("missing tree must yield no pages, got %d", len(pages))
	}
}

// TestAppKnowledgeIncludesLegacyPages locks the endpoint contract: the
// preserved pages append to the response even when synthesis is unavailable —
// an upgrading user sees their old wiki/notebooks the moment they open the
// Knowledge tab, provider or no provider.
func TestAppKnowledgeIncludesLegacyPages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUPHF_RUNTIME_HOME", home)
	seedLegacyWiki(t, home)

	b := newTestBroker(t)
	b.knowledgeBrainOverride = newFakeBrain()
	// No provider: synthesis fails, but the legacy pages must still serve.
	withFakeAppsLLM(t, func(context.Context, string, string) (string, error) {
		return "", fmt.Errorf("no provider on this host")
	})
	if err := b.StartOnPort(0); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer b.Stop()
	base := fmt.Sprintf("http://%s", b.Addr())

	regBody, _ := json.Marshal(map[string]any{
		"name": "Upgraded App", "description": "Lives in a workspace with a legacy wiki.",
		"html": validAppHTML,
	})
	created := postAppsAsAgent(t, base+"/apps", b.Token(), appBuilderSlug, regBody)
	app, _ := created["app"].(map[string]any)
	id, _ := app["id"].(string)
	if id == "" {
		t.Fatalf("no app id: %v", created)
	}

	status, out := getAppsJSON(t, base+"/apps/"+id+"/knowledge", b.Token())
	if status != http.StatusOK {
		t.Fatalf("GET knowledge: %d", status)
	}
	pages, _ := out["pages"].([]any)
	if len(pages) != 3 {
		t.Fatalf("pages = %d, want the 3 preserved legacy pages", len(pages))
	}
	titles := make([]string, 0, len(pages))
	for _, raw := range pages {
		p, _ := raw.(map[string]any)
		titles = append(titles, fmt.Sprint(p["title"]))
	}
	joined := strings.Join(titles, " | ")
	for _, want := range []string{"RAG retrieval quality brief", "Adopt RRF fusion", "RAG papers survey"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("legacy page %q missing from response: %s", want, joined)
		}
	}
}
