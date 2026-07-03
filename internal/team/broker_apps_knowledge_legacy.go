package team

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nex-crm/wuphf/internal/config"
)

// broker_apps_knowledge_legacy.go — preserve the previous product's wiki
// articles and per-agent notebook notes as Knowledge pages.
//
// The office-era wuphf kept a files-on-disk knowledge base under
// <runtime home>/.wuphf/wiki: team articles in team/<category>/*.md and each
// agent's draft notes in agents/<agent>/notebook/*.md. The operator product
// synthesizes cited pages instead — but an upgrading workspace must not lose
// what it already wrote. Every knowledge response therefore appends the legacy
// pages, preserved VERBATIM (no synthesis, no fabricated citations), under
// "Team wiki · <category>" / "Notebook · <agent>" categories. A workspace
// without a legacy tree contributes nothing.

// legacyKnowledgeMaxPages bounds the payload for enormous legacy trees. The cap
// is logged when hit — never a silent truncation.
const legacyKnowledgeMaxPages = 200

const legacyKnowledgeCategoryTag = "Imported from your previous workspace"

// legacyKnowledgePages loads the legacy tree once per broker; the previous
// product no longer writes to it, so it is immutable for this process's life.
func (b *Broker) legacyKnowledgePages() []appKnowledgePage {
	b.legacyKnowledgeOnce.Do(func() {
		root := filepath.Join(config.RuntimeHomeDir(), ".wuphf", "wiki")
		b.legacyKnowledge = loadLegacyKnowledgePages(root)
		if n := len(b.legacyKnowledge); n > 0 {
			fmt.Fprintf(os.Stderr, "broker: preserved %d legacy wiki/notebook page(s) into Knowledge\n", n)
		}
	})
	return b.legacyKnowledge
}

// loadLegacyKnowledgePages reads team articles and notebook notes from a legacy
// wiki root. A missing root, or one with no markdown, yields nil.
func loadLegacyKnowledgePages(root string) []appKnowledgePage {
	var pages []appKnowledgePage

	// Team wiki articles: team/<category>/**.md (the category is the first
	// folder under team/; root-level files read as plain "Team wiki").
	// A walk error (missing tree, unreadable entry) ends the walk; whatever was
	// collected up to that point is still preserved — this is best-effort
	// archaeology, not a transaction.
	teamRoot := filepath.Join(root, "team")
	_ = filepath.Walk(teamRoot, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			// Editor/system dirs (.obsidian, .git) are not articles.
			if strings.HasPrefix(info.Name(), ".") && path != teamRoot {
				return filepath.SkipDir
			}
			return nil
		}
		// Walk only yields paths under teamRoot, so a prefix trim IS the
		// relative path — no error case to swallow.
		rel := strings.TrimPrefix(path, teamRoot+string(filepath.Separator))
		category := "Team wiki"
		if dir := filepath.Dir(rel); dir != "." {
			category = "Team wiki · " + strings.SplitN(filepath.ToSlash(dir), "/", 2)[0]
		}
		if page, ok := legacyPageFromFile(path, "legacy-wiki-"+slugifyKnowledgeID(filepath.ToSlash(rel)), category); ok {
			pages = append(pages, page)
		}
		return nil
	})

	// Notebook notes: agents/<agent>/notebook/*.md — each old office agent's
	// draft notes, kept under that agent's name.
	agentDirs, _ := os.ReadDir(filepath.Join(root, "agents"))
	for _, agentDir := range agentDirs {
		if !agentDir.IsDir() || strings.HasPrefix(agentDir.Name(), ".") {
			continue
		}
		agent := agentDir.Name()
		notes, _ := os.ReadDir(filepath.Join(root, "agents", agent, "notebook"))
		for _, note := range notes {
			if note.IsDir() {
				continue
			}
			path := filepath.Join(root, "agents", agent, "notebook", note.Name())
			id := "legacy-notebook-" + slugifyKnowledgeID(agent+"-"+note.Name())
			if page, ok := legacyPageFromFile(path, id, "Notebook · "+agent); ok {
				pages = append(pages, page)
			}
		}
	}

	// Stable order: wiki articles first (the reviewed, promoted knowledge), then
	// notebooks (draft scratch); alphabetical within.
	rank := func(p appKnowledgePage) int {
		if strings.HasPrefix(p.Category, "Team wiki") {
			return 0
		}
		return 1
	}
	sort.Slice(pages, func(i, j int) bool {
		if r1, r2 := rank(pages[i]), rank(pages[j]); r1 != r2 {
			return r1 < r2
		}
		if pages[i].Category != pages[j].Category {
			return pages[i].Category < pages[j].Category
		}
		return pages[i].Title < pages[j].Title
	})
	if len(pages) > legacyKnowledgeMaxPages {
		fmt.Fprintf(os.Stderr, "broker: legacy knowledge capped at %d of %d pages\n", legacyKnowledgeMaxPages, len(pages))
		pages = pages[:legacyKnowledgeMaxPages]
	}
	return pages
}

// legacyPageFromFile turns one legacy markdown file into a verbatim Knowledge
// page. Non-markdown, placeholder, and empty files report ok=false.
func legacyPageFromFile(path, id, category string) (appKnowledgePage, bool) {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") || !strings.EqualFold(filepath.Ext(base), ".md") {
		return appKnowledgePage{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return appKnowledgePage{}, false
	}
	title, lead, sections := parseLegacyMarkdown(string(raw))
	if title == "" {
		title = humanizeLegacyName(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if lead == "" && len(sections) == 0 {
		return appKnowledgePage{}, false // .gitkeep-grade emptiness
	}
	updated := ""
	if info, statErr := os.Stat(path); statErr == nil {
		updated = "Preserved from your previous workspace · " + info.ModTime().Format("Jan 2, 2006")
	}
	summary := lead
	if summary == "" && len(sections) > 0 && len(sections[0].Paras) > 0 {
		summary = sections[0].Paras[0]
	}
	if len(summary) > 240 {
		summary = summary[:237] + "…"
	}
	return appKnowledgePage{
		ID:         id,
		Title:      title,
		Category:   category,
		UpdatedAt:  updated,
		Summary:    summary,
		Infobox:    []appKnowledgeInfoRow{},
		Lead:       lead,
		Sections:   sections,
		References: []appKnowledgeRef{},
		Categories: []string{legacyKnowledgeCategoryTag},
		SeeAlso:    []string{},
	}, true
}

// parseLegacyMarkdown splits a legacy article into the page shape the reader
// renders: an optional YAML frontmatter title, the text before the first "## "
// as the lead, and one section per "## " heading. Content is preserved verbatim
// as paragraphs — no rewriting, no citations.
func parseLegacyMarkdown(raw string) (title, lead string, sections []appKnowledgeSection) {
	body := strings.ReplaceAll(raw, "\r\n", "\n")

	// YAML frontmatter: keep only a title, drop the rest of the block.
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---"); end >= 0 {
			front := body[4 : 4+end]
			for _, line := range strings.Split(front, "\n") {
				if t, ok := strings.CutPrefix(strings.TrimSpace(line), "title:"); ok {
					title = strings.Trim(strings.TrimSpace(t), `"'`)
				}
			}
			rest := body[4+end+4:]
			body = strings.TrimPrefix(rest, "\n")
		}
	}

	current := appKnowledgeSection{}
	flush := func() {
		if current.Heading == "" && len(current.Paras) == 0 {
			return
		}
		if current.Heading == "" {
			lead = strings.Join(current.Paras, "\n\n")
		} else {
			sections = append(sections, current)
		}
		current = appKnowledgeSection{}
	}

	var para []string
	endPara := func() {
		if len(para) == 0 {
			return
		}
		current.Paras = append(current.Paras, strings.Join(para, "\n"))
		para = nil
	}

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "# ") && title == "" && current.Heading == "" && len(current.Paras) == 0 && len(para) == 0:
			title = strings.TrimSpace(trimmed[2:])
		case strings.HasPrefix(trimmed, "## "):
			endPara()
			flush()
			current.Heading = strings.TrimSpace(trimmed[3:])
		case trimmed == "":
			endPara()
		default:
			para = append(para, line)
		}
	}
	endPara()
	flush()
	return title, lead, sections
}

// humanizeLegacyName turns a kebab/snake filename into a readable title.
func humanizeLegacyName(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	if len(words) == 0 {
		return name
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}
