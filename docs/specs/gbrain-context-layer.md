# gbrain as the wiki context layer

Status: landed on `feat/gbrain-context-layer`, behind `WUPHF_WIKI_BACKEND`.

Replaces the SQLite + bleve pairing under the wiki context layer with gbrain
(PGLite or Postgres). Everything above the `FactStore` and `TextIndex`
interfaces is unchanged: retrieval routing, `QueryHandler`, the 19 `/wiki/*`
routes, the MCP `wuphf_wiki_lookup` tool, and the 86 web components.

## Why the interfaces were the seam

`internal/team/wiki_index.go` already isolated storage behind two interfaces:
`FactStore` (25 methods) and `TextIndex` (4). Replacing the layer therefore
meant implementing 29 methods, not rewriting the wiki. The git markdown repo
remains the substrate and the source of truth; the brain is a derived index.

## Data mapping

gbrain models a brain as pages, typed links, and tags. It has no
subject/predicate/object table, so the triplet is reconstructed from links.

| WUPHF | gbrain |
|---|---|
| `TypedFact` | page `atoms/<factID>`, type `atom` |
| `IndexEntity` | page `entities/<slug>`, type `person`/`company`/`project`/`concept` |
| `IndexEdge` | link `entities/<subject>` →`<predicate>`→ `entities/<object>` |
| `Redirect` | link `wuphf_redirect`, payload in link context |
| category membership | link `wuphf_category` from `articles/<b64(path)>` |
| category tree | link `wuphf_parent_category` |
| `TextIndex.Search` | `query` (gbrain's own hybrid RRF pipeline) |
| `ListFactsByPredicateObject` | `traverse_graph` direction=in, filtered to `atoms/` |

### Frontmatter carries a base64 JSON blob

The authoritative record travels as base64-encoded JSON in a single frontmatter
key (`wuphf_fact`, `wuphf_entity`), not as YAML fields. Fact text is arbitrary
user content containing apostrophes, colons, newlines, and unicode, each of
which needs different YAML quoting. Base64 is lossless for any input and cannot
be broken by a YAML edge case. Human-readable `subject`/`predicate`/`object`
keys are emitted alongside it, advisory only, never read back.

This matters because gbrain **rewrites** two fields on write: it title-cases
`title` and coerces an unknown `type` to `concept`. Neither is durable storage.

## gbrain behaviours discovered by probing 0.42.58.0

Recorded here because none are documented and all are load-bearing.

1. **`put_page` does not clear `deleted_at`.** Writing to a soft-deleted slug
   updates the row but leaves it invisible to `get_page` and to search. A fact
   that was retired and later re-extracted would vanish permanently with no
   error. Every write therefore goes through `gbrainFactStore.putPage`, which
   calls `restore_page` after the write. `restore_page` is idempotent on a live
   page (`already_active`) and not-found on a new one, which is ignored.
   Regression test: `TestGBrainFactStore_ResurrectsDeletedFact`.
2. **`traverse_graph` returns edges only when both `link_type` and `direction`
   are supplied.** Without them it returns nodes. `get_links` is undirected and
   untyped and cannot answer "who champions X".
3. **`add_link` takes `from`/`to`**, not `from_slug`/`to_slug`, and round-trips
   an arbitrary `context` string verbatim. That is what lets an `IndexEdge`
   carry its timestamp and source SHA without a column of its own.
4. **`list_pages` returns metadata without frontmatter**, so every full scan
   costs one list plus one `get_page` per row.
5. **`atom`** is gbrain's "smallest extractable claim unit" type and survives a
   round-trip uncoerced. It is the fact carrier.
6. **The upgrade banner goes to stderr**, so stdout parses cleanly.

## Costs versus the store it replaces

These are real regressions, accepted deliberately.

- **Write amplification: up to 4 writes per fact** (the atom page plus three
  links). PGLite is single-writer, so writes serialise behind `writeMu`. Bulk
  reconcile is materially slower than SQLite.
- **Read amplification on full scans.** `ListAllFacts`, `IterateEntities`, and
  both canonical hashes are O(corpus) round-trips, where SQLite was one query.
- **`ListAllFactsPaged` is no longer a keyset query.** gbrain paginates by
  offset, so the method scans and slices. Caller memory stays bounded; read cost
  does not.
- **`ListEdgesForEntity` narrowed.** gbrain has no "all link types" edge query,
  so the type set is derived from the entity's own facts. An edge written by
  `UpsertEdge` for a predicate no fact uses is invisible. The extractor always
  writes fact and edge together, so this is not reachable on the normal path.
- **Search costs one `get_page` per hit.** `SearchHit.Entity` renders as the
  citation title and gbrain's query result carries no frontmatter. Bounded by
  topK.

## Failure policy

`WUPHF_WIKI_BACKEND` selects the backend:

- **unset** (default): try gbrain; on failure log a WARNING and fall back to the
  in-memory index. Without this, any deployment without gbrain loses the wiki
  entirely and every broker test would need a live brain.
- **`gbrain`**: gbrain or nothing. An operator who pinned it must not silently
  get a different store.
- **`memory`**: previous in-memory behaviour.

The fallback is loud because a context layer that quietly degrades to an empty
store answers "no facts found" for every question, which reads as a product bug
rather than the missing dependency it is.

## Testing

- `wiki_gbrain_mapping_test.go` — pure unit coverage, runs everywhere. Covers
  slug round-trips, the hostile-input blob round-trip, frontmatter determinism
  (which guards `content_hash` churn), and YAML injection through the advisory
  keys.
- `wiki_index_gbrain_contract_test.go` — live contract coverage against a real
  brain. Opt-in, and destructive within the wuphf namespaces:

  ```bash
  GBRAIN_HOME=~/.wuphf-gbrain-ctx-home WUPHF_GBRAIN_TEST=1 \
    OPENAI_API_KEY=sk-... go test ./internal/team/ -run TestGBrain -count=1
  ```

  `OPENAI_API_KEY` must be exported explicitly. The Go test harness points
  `WUPHF_RUNTIME_HOME` at a temp dir, so `config.ResolveOpenAIAPIKey()` reads an
  empty config and `gbrainEnv()` forwards no key to the subprocess. Without it
  the tests pass but cover the keyword arm only, because gbrain writes chunks
  with NULL embeddings. Check with `gbrain stats`: Embedded should equal Chunks.

## Enabling embeddings

gbrain REFUSES `gbrain config set embedding_model` on PGLite: the model and
dimensions size the schema, so they are file-plane only and must be stable
across connects. There is no in-place upgrade and no `--force`. The procedure
is wipe and re-init:

```bash
export OPENAI_API_KEY=sk-...
export GBRAIN_HOME="$HOME/.wuphf-gbrain-ctx-home"
rm -rf "$GBRAIN_HOME/.gbrain/brain.pglite"
gbrain init --pglite --embedding-model openai:text-embedding-3-large
```

Two traps:

1. **A pre-existing `embedding_disabled: true` silently wins over
   `--embedding-model`.** init honours it as a deferred-setup sentinel and
   writes no model, with no warning. Remove the key from `config.json` first.
2. **Use OpenAI.** `content_chunks.embedding` is declared `vector(1536)` with
   `model` defaulting to `text-embedding-3-large`, so OpenAI at 1536d is a
   native fit. Voyage (1024d) and ZeroEntropy (2560d) fight the column.

`put_page` embeds synchronously when a key is present, so no background pass is
needed on the write path.

For the WUPHF context layer this wipe is cheap: the brain is a derived index and
the git markdown repo is the substrate, so the extractor repopulates it.

## Benchmark result

### The baseline correction that changes the verdict

The first rounds of this comparison benchmarked gbrain against SQLite + bleve
(`NewPersistentWikiIndex`). **That code path was never running.**
`broker_wiki_lifecycle.go` called `NewWikiIndex` directly, and
`NewPersistentWikiIndex` was reachable only from this bench. Production ran the
IN-MEMORY store, rebuilt from markdown on every boot.

Benchmarking against sqlite therefore measured a path no user ever exercised.
`--backend=memory` measures the real one.

| Metric | **memory (what production ran)** | sqlite+bleve (dead code) | **gbrain entity (new)** |
|---|---|---|---|
| Ship gate (recall@20) | **60% RED** | 100% | **94% GREEN** |
| status class pass rate | **0%** | 100% | 85% |
| Micro-recall | **53.3%** | 99.7% | **97.6%** |
| nDCG@10 | **0.466** | 0.809 | **0.720** |
| MRR | **0.441** | 0.802 | **0.858** |
| recall@1 | 0.069 | 0.106 | **0.121** |
| recall@10 | 0.508 | 0.845 | **0.739** |
| Retrieval p50 | 0.18 ms | 0.09 ms | 226 ms |

Against the real baseline gbrain is not a marginal trade, it is a large win:
micro-recall roughly doubles (53% -> 98%), MRR nearly doubles (0.44 -> 0.86),
and the ship gate goes RED to GREEN. Most starkly, **status queries scored 0%
in production** — "What does Marcus Lee do?" returned nothing relevant, for 20
of the 50 bench queries — and now pass at 85%.

The in-memory store also lost every fact on restart. gbrain persists.

The cost is latency: 0.18 ms to 226 ms, an MCP round-trip per query. That is
structural. It only matters where retrieval sits in a tight loop; behind an LLM
call it is noise.

### Why the atom shape failed

One page per fact (the atom shape) scored 84% RED — worse than the recommended
one-page-per-entity shape at 94% GREEN. Writing a page per fact turned gbrain
into flat RAG over 475 fragments, which is exactly what its schema doc argues
against. The implementation, not gbrain, was that gap.

gbrain still trails the (never-running) sqlite path on deep-list ranking —
recall@10 0.739 vs 0.845 — because "this entity's facts, newest first" orders
worse deep in a list than per-fact BM25. That is the one place the dead code was
genuinely better, and it is worth revisiting if deep recall starts mattering.

## Pagination: FIXED with an updated_after cursor

gbrain's `list_pages` caps at ~100 rows and ACCEPTS BUT SILENTLY DROPS `offset`
(`core/operations.ts` calls `engine.listPages({type, updated_after, limit,
...scope})` — no offset parameter exists; offset=0 and offset=2 return
byte-identical rows). Both naive loops are wrong: stopping on a short batch
truncates at the cap, looping until empty never terminates.

`Client.ListAllPages` walks a real cursor instead. Two gbrain arguments ARE
honoured and together they paginate:

- `sort=updated_asc` — the default is descending, against which no forward
  cursor can walk.
- `updated_after` — strictly greater-than.

Because the comparison is strict, advancing to the batch MAXIMUM would drop rows
sharing that timestamp that did not fit. The cursor advances to the
SECOND-LARGEST DISTINCT timestamp, so boundary rows are deliberately re-fetched
and deduplicated by slug. The cursor strictly increases each round, so it
terminates. The one unrepresentable case — a full batch whose rows all share one
timestamp, a tie cluster larger than the page size — returns an error rather
than silently skipping rows.

Verified live: `TestGBrainFactStore_FullScanPastListCap` writes 120 entity pages
(past the cap) and asserts CountFacts, ListAllFacts, and IterateEntities all see
the full set. Before the cursor, CountFacts returned 100 for a 120-fact corpus.

There is deliberately NO exported non-paginating list helper: one would silently
truncate, which is the defect this package exists to hide from callers.

### Prerequisite bug fixed alongside it

`PageMeta` declared `json:"updated"` but gbrain emits `updated_at`, so the field
silently decoded EMPTY. That blanked `LastEditedTs` on every wiki page in
`wiki_gbrain_adapter.go` and left the cursor with nothing to advance on.
`PageMeta.UnmarshalJSON` now accepts either spelling.

## Known latency defect in this adapter (not gbrain's fault)

`gbrainTextIndex.Search` issues ONE `get_page` per hit to populate
`SearchHit.Entity`, which renders as the citation title. At the bench's topK=20
that is 20 sequential MCP round-trips per query, and it dominates the measured
264 ms p50.

It is fixable without touching gbrain: encode the entity slug into the atom slug
(`atoms/<entity>__<factID>` rather than `atoms/<factID>`) so `Entity` is
derivable from the search result with zero extra calls. That is a slug-scheme
change, so it needs the mapping helpers, the contract tests, and a re-bench.

Deliberately NOT done yet: it would cut latency but cannot touch the ranking
numbers (nDCG@10 0.425 vs 0.809), and those are what decide whether this backend
is viable at all. Optimising the latency of a retrieval path that returns worse
results first would be gold-plating a possible dead end.

## Not done

- **No benchmark run.** `bench/slice-1/` and the CI gate
  (recall@3 >= 0.90, nDCG@10 >= 0.95 in `wiki_query_eval_test.go`) have not been
  run against the gbrain backend. Retrieval quality versus bleve is unmeasured.
- **The founder's own brain (`~/.gbrain`) still has `embedding_disabled: true`**
  and is therefore keyword-only. Enabling it means the same wipe-and-re-init
  against real data plus `gbrain sync`; not done, because it is destructive.
- **No migration path.** Nothing backfills an existing SQLite wiki index into a
  brain. A fresh brain starts empty and repopulates as the extractor runs.
- **`internal/gbrain/pages.go` duplicates nothing but extends `Client`**;
  `ListOptions` was left untouched and `ListPageOptions` added beside it, so the
  diff against upstream `mcp.go` stays reviewable.
