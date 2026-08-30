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

`bench/slice-1` (500 artifacts, 475 facts, 50 queries), same corpus and scoring
for both backends via the `--backend` flag. gbrain ran on a fresh brain with
embeddings live, so its vector arm was active.

| Metric | SQLite + bleve | gbrain (pre-fix) | gbrain (final) |
|---|---|---|---|
| Ship gate (recall@20 pass) | **100%** | 60% | **84%** (RED, gate 85%) |
| Micro-recall | 99.73% | 84.88% | 92.57% |
| nDCG@10 | 0.8091 | 0.4250 | 0.5642 |
| MRR | 0.8023 | 0.5117 | 0.5877 |
| Retrieval p50 | 0.09 ms | 264 ms | 390 ms |

The `ensureEntityPage` fix moved the gate from 60% to 84%. Per class, gbrain now
matches the baseline everywhere EXCEPT one:

| Class | SQLite | gbrain |
|---|---|---|
| multi_hop | 100% | **100%** |
| relationship | 100% | **100%** |
| counterfactual | 100% | **100%** |
| general | 100% | **100%** |
| status | 100% | **60%** |

**The typed graph walks port cleanly. The whole remaining gap is the untyped
text-search path.**

### Why status fails, and why the adapter cannot fix it

Status queries ("Where does Esme Walker work?") have large expected sets — 10 to
13 facts. gbrain returns 7 of 10 in its top 20 while bleve returns all 10.

Ruled out by direct experiment on the bench corpus:

- **Not the vector arm.** gbrain's keyword-only `search` returns the identical
  7/10 as its hybrid `query`.
- **Not missing text.** All three missed facts contain "Esme Walker" verbatim.
- **Not entity-page dilution.** Entity pages do rank (the `esme-walker` page
  comes back at position 2), but they occupy only one slot of twenty.
- **Not the fetch width.** Tripling the fetch to 60 does not change the top-20
  ordering.
- **`type` filtering does not work.** `query` accepts a `type` argument and
  ignores it — 19 atoms returned either way.

What remains is the ranking itself: gbrain places 13 non-expected facts above 3
expected ones. Its hybrid RRF is simply weaker than bleve's BM25 with an English
analyser for entity-scoped queries whose answer is "most of what we know about
this person". Nothing in the adapter changes that.

## Hard blocker: full scans cannot paginate

gbrain's `list_pages` caps at 100 rows and ACCEPTS BUT SILENTLY DROPS `offset`
(`core/operations.ts` calls `engine.listPages({type, updated_after, limit,
...scope})` — no offset parameter exists). Verified: offset=0 and offset=2
return byte-identical rows.

So `ListAllFacts`, `CountFacts`, `IterateEntities`, and both canonical hashes
cannot enumerate a corpus above 100 rows. They now return
`errCorpusExceedsListCap` rather than a truncated result, because a short fact
list or a wrong hash would corrupt reconcile decisions silently. Fixing this
needs an `updated_after` cursor (lossy on tied timestamps) or upstream support.

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
