package gbrain

// embedding_probe.go — discover whether a local OpenAI-compatible runtime can
// actually produce embeddings.
//
// Why a probe and not a lookup
// ===========================
// `internal/provider/binding.go` defines three local runtimes that all speak
// the same OpenAI-compatible HTTP surface: mlx-lm, ollama, and exo. It is
// tempting to assume that a configured local runtime can embed, because it
// serves /v1/chat/completions.
//
// It cannot be assumed. Serving chat says nothing about serving embeddings:
//
//   - mlx_lm.server historically ships chat completions with no /v1/embeddings
//     route at all.
//   - Ollama serves embeddings only for models that were BUILT as embedders
//     (nomic-embed-text, mxbai-embed-large). Asking llama3 to embed fails.
//   - exo's surface varies by version.
//
// Guessing wrong is not a soft failure. gbrain's embedding_model sizes the
// schema and cannot be changed without wiping the brain (see
// docs/specs/gbrain-context-layer.md), so selecting a runtime that turns out
// not to embed means a brain that has to be rebuilt to fix.
//
// So this asks the endpoint, in two escalating steps, and believes only the
// conclusive one.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// probeTimeout bounds every probe request. It matches ollamaListTimeout: a
// wedged or slow local runtime must never delay office startup, and "no answer
// within 3 seconds" is operationally identical to "cannot embed".
const probeTimeout = 3 * time.Second

// probeCache memoises probe results for the process lifetime, keyed by
// "baseURL|model". Selection runs on startup paths that may be hit repeatedly,
// and the answer cannot change without the operator restarting the runtime.
var (
	probeMu    sync.Mutex
	probeCache = map[string]bool{}
)

// probeHTTPClient is overridable in tests so the probe can be exercised without
// a live local runtime.
var probeHTTPClient = &http.Client{Timeout: probeTimeout}

// EmbeddingProbeResult reports what a probe learned about an endpoint.
type EmbeddingProbeResult struct {
	// OK is true only when the endpoint returned a usable embedding vector.
	OK bool
	// Dimensions is the length of the returned vector, 0 when OK is false.
	// Callers need this because gbrain's embedding_dimensions sizes the schema.
	Dimensions int
	// Reason explains a negative result, for the startup log. Never a raw
	// response body: a local runtime's error text is unbounded and may echo
	// the probe input back.
	Reason string
}

// ProbeEmbeddings reports whether an OpenAI-compatible endpoint can embed with
// the given model.
//
// Two steps, because the cheap one is not conclusive:
//
//  1. GET {base}/v1/models — cheap, and a hard negative when the endpoint is
//     unreachable. It is NOT a positive: several runtimes list models they will
//     refuse to embed with.
//  2. POST {base}/v1/embeddings with a one-token input — the only conclusive
//     test. A vector comes back or it does not.
//
// Any error is a negative, never a propagated failure: the caller is choosing
// among embedders and "this one does not work" is an answer, not a fault.
func ProbeEmbeddings(ctx context.Context, baseURL, model string) EmbeddingProbeResult {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	model = strings.TrimSpace(model)
	if baseURL == "" || model == "" {
		return EmbeddingProbeResult{Reason: "no base URL or model configured"}
	}

	key := baseURL + "|" + model
	probeMu.Lock()
	cached, seen := probeCache[key]
	probeMu.Unlock()
	if seen && !cached {
		return EmbeddingProbeResult{Reason: "probed earlier in this process: no embeddings"}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// Step 1: reachability. A dead endpoint fails here in milliseconds rather
	// than making the caller wait out the embeddings POST.
	if err := probeReachable(ctx, baseURL); err != nil {
		return cacheProbe(key, EmbeddingProbeResult{Reason: "endpoint unreachable: " + err.Error()})
	}

	// Step 2: the conclusive test.
	dims, err := probeEmbed(ctx, baseURL, model)
	if err != nil {
		return cacheProbe(key, EmbeddingProbeResult{Reason: "no usable /v1/embeddings: " + err.Error()})
	}
	return cacheProbe(key, EmbeddingProbeResult{OK: true, Dimensions: dims})
}

// cacheProbe records and returns a probe result.
func cacheProbe(key string, r EmbeddingProbeResult) EmbeddingProbeResult {
	probeMu.Lock()
	probeCache[key] = r.OK
	probeMu.Unlock()
	return r
}

// probeReachable does the cheap GET /v1/models liveness check.
func probeReachable(ctx context.Context, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL(baseURL), nil)
	if err != nil {
		return err
	}
	resp, err := probeHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Any HTTP answer proves something is listening. A 404 on /v1/models is
	// fine — some runtimes omit it while still serving embeddings — so only a
	// transport error counts as unreachable.
	return nil
}

// probeEmbed posts a one-token embedding request and returns the vector length.
func probeEmbed(ctx context.Context, baseURL, model string) (int, error) {
	body, err := json.Marshal(map[string]any{"model": model, "input": "ping"})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingsURL(baseURL), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := probeHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Deliberately does not include the body: a local runtime's error text
		// is unbounded and may echo the probe input back into our logs.
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}

	var decoded struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		// A 200 with no vector is the exact failure this probe exists to catch:
		// the route answers, but produces nothing usable.
		return 0, fmt.Errorf("responded 200 with no embedding vector")
	}
	return len(decoded.Data[0].Embedding), nil
}

// modelsURL builds {base}/v1/models, tolerating a base that already ends in
// /v1. Mirrors normalizeOpenAICompatEndpoint's handling in internal/provider:
// operators routinely paste a runtime's listening address without the /v1.
func modelsURL(baseURL string) string { return apiURL(baseURL, "models") }

// embeddingsURL builds {base}/v1/embeddings on the same rules.
func embeddingsURL(baseURL string) string { return apiURL(baseURL, "embeddings") }

func apiURL(baseURL, path string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/" + path
	}
	return baseURL + "/v1/" + path
}
