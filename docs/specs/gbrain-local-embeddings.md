# Plan: use the selected local LLM for embeddings, not an extra OpenAI key

Status: chain IMPLEMENTED 2026-08-31. Two follow-ups remain (local-runtime
probing beyond Ollama, and the subscription-only chat shim).

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

**1. ~~Precedence~~ RESOLVED.** The founder settled this: a supplied hosted key
wins, local is the fallback. See Decision below.

**2. Only Ollama is detected.** `internal/provider/binding.go` defines
`KindOllama`, and `internal/config/config.go:116` documents "compatible local
runtimes (mlx-lm, ollama, exo)". vLLM, exo, mlx-lm, and generic
`openai-compatible` bindings are all invisible to the embedding chain.

**3. ~~The wiki backend never calls `EnsureBrain`~~ FIXED.**
`newWikiIndexForBackend` now calls it (idempotent, never re-inits over a working
brain) and logs `RetrievalMode()`, so a local user gets a brain created rather
than silently landing on the in-memory fallback.

**4. Chat endpoint ≠ embedding endpoint.** This is the substantive risk. A
local runtime serving a chat model usually does NOT serve `/v1/embeddings`, and
Anthropic has no embeddings API at all (which is why `SelectEmbeddingModel`
already returns "" for an Anthropic-only setup). Selection must PROBE, not
assume.

## Decision (founder, 2026-08-31)

**A supplied OpenAI key wins. Local is the fallback.** A user who has already
paid for the stronger embedder must not be silently downgraded to a smaller
local model. The local path exists to remove the *requirement* for a dedicated
key, not to override one.

Implemented chain (`SelectEmbeddingModel`):

| # | Condition | gbrain `--embedding-model` | Dim |
|---|---|---|---|
| 1 | `OPENAI_API_KEY` | `openai:text-embedding-3-large` | 1536 |
| 2 | `VOYAGE_API_KEY` | `voyage:voyage-3-large` | 1024 |
| 3 | Local Ollama embed model pulled | `ollama:<model>` | ~768 |
| 4 | none | keyword-only | — |

Voyage was added at step 2 because it is the practical answer for a Claude user:
Anthropic ships **no embeddings endpoint at all** (their own docs name Voyage as
the recommended companion), so an Anthropic key cannot make vectors, and Voyage
gives real semantic retrieval without demanding an OpenAI key. gbrain supports
it natively and `internal/embedding/anthropic.go` already speaks it. The key is
read env-only and never falls back to `ANTHROPIC_API_KEY` — that would ship the
user's Anthropic credential to a third party.

## Can Claude CLI or Codex CLI generate embeddings?

**No, and it should not be attempted.** Three separate reasons:

1. **Anthropic has no embeddings API.** Not a gap in our wiring — the endpoint
   does not exist. Confirmed against Anthropic's own docs, which point to Voyage
   instead.
2. **Prompting a chat model for a vector produces meaningless numbers.** An LLM
   can emit 1536 floats, but they carry no metric structure: cosine similarity
   over them is noise. It would rank *worse* than keyword search while looking
   like semantic search — the most expensive kind of wrong.
3. **Codex CLI's ChatGPT OAuth is not a platform API key.** Those credentials
   are scoped to the ChatGPT backend, not the platform embeddings endpoint.
   Pointing one at the other is credential misuse, and it would break the moment
   the scope is enforced.

### What the CLIs CAN do instead

The vector arm earns its keep by bridging vocabulary mismatch between a query
and the documents. A chat model does that job directly, and **gbrain already
implements it** as multi-query expansion:

- `core/search/expansion.ts` gates only on a chat model being reachable —
  `if (!gatewayIsAvailable('expansion')) return [query]`. It is INDEPENDENT of
  embeddings, so it works in a keyword-only brain.
- Its default is `anthropic:claude-haiku-4-5`, and WUPHF's `gbrainEnv()` already
  forwards `ANTHROPIC_API_KEY`.

**So for a Claude user with an API key, the no-embedder path already works with
zero new wiring**: keyword retrieval plus LLM query expansion. `RetrievalMode()`
now reports exactly which of the three modes is live, so this is stated at
startup rather than silently degrading.

### The remaining gap: subscription-only CLI users

A user on a Claude Pro or ChatGPT subscription with **no API key of any kind**
still cannot reach a chat model from gbrain, because gbrain speaks HTTP to model
providers and the CLI is a subprocess.

**SHIPPED, BUT NOT YET REACHABLE BY GBRAIN.** Read this before relying on it.

`internal/team/broker_openai_compat.go` serves `POST /v1/chat/completions` on the
broker, proxying to the configured agent CLI via
`provider.RunConfiguredOneShotCtx`. It is behind the broker's Bearer auth, is
non-streaming only, and is proven end-to-end by
`TestOpenAIChatCompletionsLive_SuccessPath`: a real Claude CLI call returning a
valid chat-completion body.

**What does not work is pointing gbrain at it.** On gbrain 0.42.58.0 there is no
recipe whose `expansion` touchpoint can be redirected to a custom endpoint:

| Attempt | Why it fails |
|---|---|
| `expansion_model: openai-compatible:<model>` | No such provider recipe exists. gbrain names recipes by SERVICE (ollama, litellm, llama-server); `implementation: "openai-compatible"` is an internal detail, not a provider id. `resolveRecipe` throws "Unknown provider", `isAvailable` catches it, expansion silently degrades to the bare query with NO error surfaced. |
| `litellm:` / `llama-server:` / `ollama:` | These declare only `embedding` (and reranker) touchpoints. `isAvailable` returns false on the missing touchpoint before auth is considered. |
| `openai:` with `provider_base_urls` override | The openai recipe DOES declare expansion and its base URL IS overridable, but the touchpoint allowlists `['gpt-5.2','gpt-4o-mini']` with no `user_provided_models`. An arbitrary model name is rejected; an allowlisted name with the base URL redirected still produced no call. |

An earlier version of this document gave the first row as working wiring. That
was wrong and is corrected here.

The route is kept because it is correct, tested, and costs nothing idle. Re-test
when gbrain either adds `expansion` to an openai-compatible-implementation
recipe or adds `user_provided_models` to one that already has it. Worth
retrying on 0.48+, which is several minors ahead of what this was verified
against. `TestGBrainConsumesTheShim` is skipped with the full reasoning and is
the place to re-enable.

This buys EXPANSION, not embeddings. Nothing here changes the fact that a chat
model cannot produce a usable vector.

A second, heavier option for true offline embeddings with no key and no Ollama
is bundling a small ONNX embedder (bge-small-en-v1.5, 384d, ~130MB) in-process,
which is the route supermemory takes. It is the only path to real vectors with
zero external dependencies, and it costs binary size plus an ONNX runtime.

## Remaining work

### Probe local runtimes beyond Ollama (gap 2 + gap 4)

`internal/provider/binding.go` defines `KindOllama`, and config documents
"compatible local runtimes (mlx-lm, ollama, exo)", but only Ollama is detected.
Extending to vLLM / exo / mlx-lm / generic `openai-compatible` bindings requires
a PROBE, because a chat endpoint does not imply an embeddings endpoint:

1. `GET {base}/v1/models` — cheap, lists whether an embedding model is loaded.
2. `POST {base}/v1/embeddings` with a one-token input — the only conclusive
   test, since some runtimes list models they will not embed with.

Cache per (base URL, model) for the process lifetime, bound it with the same 3s
budget as `detectOllamaEmbeddingModel`, and treat any error as "no embeddings
here". A wedged local runtime must never block the office from starting.

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

## Resolved

The precedence question is settled above: hosted key wins, local is the
fallback. The active mode is named at startup via `RetrievalMode()`.
