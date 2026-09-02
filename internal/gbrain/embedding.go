package gbrain

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nex-crm/wuphf/internal/config"
	"github.com/nex-crm/wuphf/internal/provider"
)

// ollamaListTimeout bounds the `ollama list` probe used for local embedding
// model discovery. It is intentionally short: discovery runs on setup paths
// where a slow or wedged ollama must not stall the office launch.
const ollamaListTimeout = 3 * time.Second

// openAIEmbeddingModel is the gbrain `--embedding-model` value used when an
// OpenAI key is configured. OpenAI's key serves both chat and embeddings, so
// it is the strongest available embedder.
const openAIEmbeddingModel = "openai:text-embedding-3-large"

// voyageEmbeddingModel is the gbrain `--embedding-model` value used when a
// Voyage key is configured.
//
// This matters specifically for Claude users. Anthropic ships NO embeddings
// endpoint (their docs name Voyage as the recommended companion), so an
// Anthropic key alone cannot produce vectors. Voyage is therefore the path to
// real semantic retrieval WITHOUT demanding an OpenAI key, and both gbrain and
// WUPHF's own internal/embedding package already support it.
const voyageEmbeddingModel = "voyage:voyage-3-large"

// Test seams. These default to the real implementations and are overridden in
// unit tests so embedding selection can be exercised without a live ollama
// binary, OpenAI credentials, or a real gbrain subprocess.
var (
	selectOpenAIKey      = config.ResolveOpenAIAPIKey
	selectVoyageKey      = resolveVoyageAPIKey
	ollamaEmbeddingModel = detectOllamaEmbeddingModel
	localRuntimeEmbedder = detectLocalRuntimeEmbedder
	runGBrain            = Run
)

// resolveVoyageAPIKey reads the Voyage key. Deliberately env-only and never
// falls back to ANTHROPIC_API_KEY: Voyage is a separate company, and sending
// the user's Anthropic key to api.voyageai.com would be a cross-provider
// credential leak. This mirrors the rule already enforced in
// internal/embedding/anthropic.go.
func resolveVoyageAPIKey() string {
	return strings.TrimSpace(os.Getenv("VOYAGE_API_KEY"))
}

// OllamaEmbeddingModel returns the name of a locally-pulled Ollama embedding
// model suitable for gbrain, or "" when ollama is not on PATH or no embedding
// model is pulled. It prefers "nomic-embed-text"; otherwise it returns any
// pulled model whose name contains "embed". It never pulls a model (no network
// side effects) — it only inspects what is already present.
func OllamaEmbeddingModel() string {
	return ollamaEmbeddingModel()
}

func detectOllamaEmbeddingModel() string {
	if _, err := exec.LookPath("ollama"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), ollamaListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ollama", "list")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return parseOllamaEmbeddingModel(stdout.String())
}

// parseOllamaEmbeddingModel extracts the best embedding model name from the
// output of `ollama list`. The first column is the model NAME; the header row
// (NAME ...) is skipped because it contains no "embed" token. A trailing
// ":latest" tag is dropped because gbrain accepts the bare name.
func parseOllamaEmbeddingModel(listing string) string {
	var fallback string
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		lower := strings.ToLower(name)
		model := strings.TrimSuffix(name, ":latest")
		if strings.Contains(lower, "nomic-embed-text") {
			return model
		}
		if fallback == "" && strings.Contains(lower, "embed") {
			fallback = model
		}
	}
	return fallback
}

// SelectEmbeddingModel returns the best available gbrain `--embedding-model`
// value, in precedence order:
//
//  1. OpenAI key           -> "openai:text-embedding-3-large" (1536d)
//  2. Voyage key           -> "voyage:voyage-3-large"         (1024d)
//  3. Local Ollama model   -> "ollama:<model>"                (typically 768d)
//  4. Other local runtime  -> "openai-compatible:<model>"     (probed)
//  5. Otherwise ""         -> keyword-only, no vector arm
//
// A hosted key outranks the local model deliberately: a user who has already
// supplied one has paid for the stronger embedder, and silently substituting a
// smaller local model would quietly degrade their retrieval. The local model is
// the FALLBACK that removes the need for a dedicated key, not the default.
//
// An Anthropic key alone still yields "" at this layer. Anthropic ships no
// embeddings endpoint, so it cannot produce vectors — but it is NOT useless
// here: gbrain's query expansion defaults to anthropic:claude-haiku and runs
// off the same key, which recovers much of what the vector arm provides. See
// ExpansionAvailable.
func SelectEmbeddingModel() string {
	if strings.TrimSpace(selectOpenAIKey()) != "" {
		return openAIEmbeddingModel
	}
	if strings.TrimSpace(selectVoyageKey()) != "" {
		return voyageEmbeddingModel
	}
	if model := strings.TrimSpace(OllamaEmbeddingModel()); model != "" {
		return "ollama:" + model
	}
	// Any other configured local runtime (mlx-lm, exo, or a bare
	// openai-compatible endpoint). Unlike the Ollama branch this must PROBE:
	// serving /v1/chat/completions says nothing about serving /v1/embeddings,
	// and picking wrong means a brain that has to be wiped to fix, because
	// embedding_model sizes gbrain's schema.
	if model := strings.TrimSpace(localRuntimeEmbedder()); model != "" {
		return model
	}
	return ""
}

// LocalRuntimeEmbedder returns an "openai-compatible:<model>" selector for a
// configured local runtime that PROVES it can embed, or "".
func LocalRuntimeEmbedder() string { return localRuntimeEmbedder() }

// detectLocalRuntimeEmbedder walks the local OpenAI-compatible runtimes and
// returns the first whose endpoint actually produces a vector.
//
// Ollama is deliberately excluded here: it is handled one step earlier by
// OllamaEmbeddingModel, which inspects `ollama list` and can name a real
// embedding model. This branch covers the runtimes where the only way to know
// is to ask.
func detectLocalRuntimeEmbedder() string {
	for _, kind := range []string{provider.KindMLXLM, provider.KindExo} {
		baseURL, model := provider.OpenAICompatDefaults(kind)
		if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(model) == "" {
			continue
		}
		if res := ProbeEmbeddings(context.Background(), baseURL, model); res.OK {
			return "openai-compatible:" + model
		}
	}
	return ""
}

// ExpansionAvailable reports whether gbrain can run LLM query expansion, which
// is the meaningful fallback when no embedder is configured.
//
// Why this is the answer to "can Claude or Codex generate embeddings"
// ==================================================================
// They cannot. Anthropic publishes no embeddings endpoint at all, and prompting
// any chat model to emit a vector produces numbers with no metric structure —
// cosine similarity over them is meaningless, so it would be worse than
// keyword search while looking like it worked.
//
// What a chat model CAN do is the job the vector arm actually performs for
// retrieval: bridging vocabulary mismatch between the query and the documents.
// gbrain already implements this as multi-query expansion, and crucially it is
// gated ONLY on a chat model being reachable, independent of embeddings
// (core/search/expansion.ts returns the bare query when the gateway is
// unavailable). Its default expansion model is anthropic:claude-haiku, and
// WUPHF's gbrainEnv already forwards ANTHROPIC_API_KEY — so for a Claude user
// with an API key this path needs no new wiring.
//
// The gap this does NOT cover is a subscription-only CLI user with no API key
// of any kind; reaching those requires an OpenAI-compatible chat shim. See
// docs/specs/gbrain-local-embeddings.md.
func ExpansionAvailable() bool {
	return strings.TrimSpace(config.ResolveAnthropicAPIKey()) != "" ||
		strings.TrimSpace(selectOpenAIKey()) != ""
}

// RetrievalMode names the retrieval capability actually available, for a
// single honest startup line rather than silent degradation.
func RetrievalMode() string {
	if model := SelectEmbeddingModel(); model != "" {
		// Name the embedder, because a local one is not just cheaper: it turns
		// a ~215ms hosted round-trip per query into a localhost call. See
		// docs/specs/gbrain-context-layer.md on where retrieval latency goes.
		return "semantic (" + model + ")"
	}
	if ExpansionAvailable() {
		return "keyword + LLM query expansion (no embedder configured)"
	}
	return "keyword only (no embedder, no expansion model)"
}

// EmbeddingAvailable reports whether gbrain can perform semantic (vector)
// retrieval — i.e. an OpenAI key or a local Ollama embedder is available. An
// Anthropic key alone returns false.
func EmbeddingAvailable() bool {
	return SelectEmbeddingModel() != ""
}

// BrainConfigured reports whether a gbrain brain already exists. It runs a
// cheap read (`gbrain config get embedding_model`) which fails with "No brain
// configured" when none has been initialized. Any other outcome is treated as
// "a brain exists" so EnsureBrain never re-inits over a working brain.
func BrainConfigured(ctx context.Context) bool {
	out, err := runGBrain(ctx, "config", "get", "embedding_model")
	if err != nil {
		return !indicatesNoBrain(err.Error())
	}
	return !indicatesNoBrain(out)
}

func indicatesNoBrain(s string) bool {
	return strings.Contains(strings.ToLower(s), "no brain configured")
}

// EnsureBrain idempotently ensures a gbrain brain exists, selecting the best
// available embedder. It is strictly idempotent: when a brain already exists it
// returns the current embedding_model (best-effort) without re-initializing.
// Only when no brain exists does it run `gbrain init --pglite` with the
// selected embedder (or `--no-embedding` when none is available).
//
// EnsureBrain MUST NOT run on every boot. Call it only from explicit setup
// (the /init flow) or lazily-once when gbrain is the selected backend and no
// brain exists yet.
func EnsureBrain(ctx context.Context) (string, error) {
	if BrainConfigured(ctx) {
		model, _ := runGBrain(ctx, "config", "get", "embedding_model")
		return strings.TrimSpace(model), nil
	}
	selected := SelectEmbeddingModel()
	args := []string{"init", "--pglite"}
	if selected != "" {
		args = append(args, "--embedding-model", selected)
	} else {
		args = append(args, "--no-embedding")
	}
	if _, err := runGBrain(ctx, args...); err != nil {
		return "", fmt.Errorf("gbrain init: %w", err)
	}
	mode := selected
	if mode == "" {
		mode = "keyword-only"
	}
	slog.Info("gbrain: initialized brain", "embeddings", mode)
	return selected, nil
}
