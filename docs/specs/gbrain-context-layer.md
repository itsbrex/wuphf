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

## Benchmark result: the swap is a REGRESSION

`bench/slice-1` (500 artifacts, 475 facts, 50 queries), same corpus and scoring
for both backends via the new `--backend` flag. gbrain ran with embeddings fully
populated (484/484 chunks), so its vector arm was active.

| Metric | SQLite + bleve | gbrain | Delta |
|---|---|---|---|
| Ship gate (recall@20 pass rate) | **100%** | **60%** | RED |
| Micro-recall | 99.73% | 84.88% | -14.9pp |
| nDCG@10 | 0.8091 | 0.4250 | **-47%** |
| MRR | 0.8023 | 0.5117 | -36% |
| recall@10 | 0.8450 | 0.4555 | -46% |
| Retrieval p50 | 0.09 ms | 264 ms | **~2900x slower** |
| Retrieval p95 | 0.52 ms | 557 ms | ~1070x slower |

Per class, gbrain: multi_hop 10% pass (was 100%), status 60%, relationship 80%.

NOTE: this run predates the `ensureEntityPage` fix, so the typed graph walks
were inert and BM25 carried the results alone. A re-run is required before the
numbers are final. The latency gap is structural and will not move: it is an
MCP round-trip plus one `get_page` per hit, against an in-process index.

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
