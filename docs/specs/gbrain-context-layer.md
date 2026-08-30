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
    go test ./internal/team/ -run TestGBrain -count=1
  ```

## Not done

- **No benchmark run.** `bench/slice-1/` and the CI gate
  (recall@3 >= 0.90, nDCG@10 >= 0.95 in `wiki_query_eval_test.go`) have not been
  run against the gbrain backend. Retrieval quality versus bleve is unmeasured.
- **Embeddings are off.** Both the reference brain and the founder's own
  (`~/.gbrain/config.json`) set `embedding_disabled: true`, so gbrain's hybrid
  search is running its keyword arm only. The vector arm needs an embedding
  provider key.
- **No migration path.** Nothing backfills an existing SQLite wiki index into a
  brain. A fresh brain starts empty and repopulates as the extractor runs.
- **`internal/gbrain/pages.go` duplicates nothing but extends `Client`**;
  `ListOptions` was left untouched and `ListPageOptions` added beside it, so the
  diff against upstream `mcp.go` stays reviewable.
