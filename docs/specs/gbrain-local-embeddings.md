# Plan: use the selected local LLM for embeddings, not an extra OpenAI key

Status: PLAN. Not implemented. Written 2026-08-31.

## The ask

Today, giving the wiki context layer semantic retrieval means handing WUPHF an
OpenAI API key that exists only to serve gbrain embeddings. For a user already
running a local model for their agents, that is a second credential for a
capability they are already paying for in hardware. The goal is: **if a local
runtime is already selected for agents, embeddings should use it.**

## What already exists (most of this is built)

`internal/gbrain/embedding.go` already implements a selection chain:

```go
func SelectEmbeddingModel() string {
    if openAIKey != "" { return "openai:text-embedding-3-large" }
    if m := OllamaEmbeddingModel(); m != "" { return "ollama:" + m }
    return ""  // keyword-only
}
```

- `OllamaEmbeddingModel()` shells `ollama list`, prefers `nomic-embed-text`,
  otherwise any pulled model whose name contains `embed`. It never pulls
  (no network side effects) and is bounded by a 3s timeout.
- `EnsureBrain()` is idempotent and inits with `--embedding-model <selected>`
  or `--no-embedding`.
- `internal/tui/init_flow.go:385` calls `EnsureBrain` during `/init`.
- `internal/team/memory_backend.go:258` gates the gbrain MEMORY backend on
  `EmbeddingAvailable()`.

And gbrain supports the target: `gateway.ts:833` notes "for openai-compatible
without auth requirements (Ollama local), treat as always-available", and
`provider_base_urls` is a first-class config key, so any OpenAI-compatible
endpoint can serve embeddings.

## The four gaps

**1. Precedence is backwards for this goal.** OpenAI wins whenever a key is
present, even when the user selected a local provider. The user's ask is the
opposite: prefer what is already configured.

**2. Only Ollama is detected.** `internal/provider/binding.go` defines
`KindOllama`, and `internal/config/config.go:116` documents "compatible local
runtimes (mlx-lm, ollama, exo)". vLLM, exo, mlx-lm, and generic
`openai-compatible` bindings are all invisible to the embedding chain.

**3. The wiki backend never calls `EnsureBrain`.** `newWikiIndexForBackend`
connects and, on failure, falls back to the in-memory index with a warning. A
user with a local model and no brain yet gets the fallback, never an offer to
create one. The memory backend and `/init` do this properly; the wiki path
does not.

**4. Chat endpoint ≠ embedding endpoint.** This is the substantive risk. A
local runtime serving a chat model usually does NOT serve `/v1/embeddings`, and
Anthropic has no embeddings API at all (which is why `SelectEmbeddingModel`
already returns "" for an Anthropic-only setup). Selection must PROBE, not
assume.

## Proposed design

### Selection chain

Replace the current chain with one that consults the configured provider first:

```
1. Selected provider is a local runtime AND its endpoint serves embeddings
     -> openai-compatible:<model> + provider_base_urls entry
2. Ollama is on PATH with a pulled embedding model
     -> ollama:<model>                       (works today)
3. An OpenAI key is configured
     -> openai:text-embedding-3-large        (best quality, costs a key)
4. Nothing
     -> keyword-only, stated once at startup, not silently
```

Rationale for putting the local runtime above OpenAI: the user explicitly asked
not to need a second credential. A user who WANTS OpenAI embeddings can still
force them, so this is a default change, not a capability removal.

### Probing (gap 4)

Before selecting a local endpoint, confirm it actually embeds:

1. `GET {base}/v1/models` — cheap, and lists whether an embedding model is
   loaded.
2. `POST {base}/v1/embeddings` with a one-token input — the only conclusive
   test, since some runtimes list models they will not embed with.

Cache the result per (base URL, model) for the process lifetime. Bound it with
the same 3s budget as `detectOllamaEmbeddingModel`, and treat any error as "no
embeddings here" rather than failing the boot. A wedged local runtime must
never block the office from starting.

### Dimensions and the migration trap

`embedding_model` and `embedding_dimensions` SIZE THE SCHEMA. gbrain refuses
`config set` on them and requires wipe-and-re-init (documented in
`gbrain-context-layer.md`). Consequences:

- `nomic-embed-text` is 768d; `text-embedding-3-large` is 1536d. Switching
  embedder means rebuilding the brain.
- A pre-existing `embedding_disabled: true` SILENTLY beats `--embedding-model`,
  so the sentinel must be cleared first.
- For the wiki context layer this is cheap: the brain is a DERIVED index and the
  git markdown repo is the substrate, so a wipe costs a reconcile, not data.
  `EnsureBrain` must state that plainly before it wipes anything.

Selection must therefore be sticky. Once a brain exists, its configured model
wins over anything the chain would pick now; a changed chain result becomes a
prompt to the user, never an automatic re-init. The current `EnsureBrain`
already has this property and it must be preserved.

### Quality expectation, stated honestly

`nomic-embed-text` (768d) is materially weaker than
`text-embedding-3-large` (1536d). The bench harness can measure exactly how much
on our own corpus:

```bash
bash scripts/bench-embedders.sh   # to be written; wraps the existing --backend flag
```

Run `bench/slice-1` under each embedder and record ship gate, nDCG@10, MRR and
p50 the same way the backend comparison is recorded. Until that runs, the
quality delta is unmeasured, and no claim should be made about it.

## Work breakdown

| # | Change | Files | Size |
|---|---|---|---|
| 1 | Probe helper: does this base URL embed? | `internal/gbrain/embedding.go` | S |
| 2 | Read the selected provider binding into the chain | `internal/gbrain/embedding.go`, `internal/provider/binding.go` | M |
| 3 | Emit `provider_base_urls` at init for a local endpoint | `internal/gbrain/embedding.go` | S |
| 4 | Call `EnsureBrain` from the wiki backend path | `internal/team/wiki_index_backend.go` | S |
| 5 | Startup line stating the active embedder, once | `internal/team/wiki_index_backend.go` | S |
| 6 | Bench both embedders, record in the spec | `bench/slice-1`, docs | M |

Items 1-3 are the substance. Item 4 is the wiring gap that makes any of it
reachable from the wiki. Item 6 is what turns "local embeddings work" into
"local embeddings are good enough", and should gate the default flip.

## Open question for the founder

Should a local embedder be preferred over a configured OpenAI key by default, or
only when no OpenAI key exists?

The ask implies the former. The counter-argument is that a user who has already
supplied an OpenAI key has paid for the better embedder, and silently using a
768d local model instead would quietly degrade their retrieval. A middle option:
prefer local when the SELECTED AGENT PROVIDER is local (the user has clearly
opted into local inference), and prefer OpenAI otherwise. That reads the user's
existing choice rather than guessing.

Recommendation: the middle option, with the active embedder named at startup so
the choice is never invisible.
