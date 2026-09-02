package gbrain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resetProbeCache clears the process-lifetime memo between cases.
func resetProbeCache(t *testing.T) {
	t.Helper()
	probeMu.Lock()
	probeCache = map[string]bool{}
	probeMu.Unlock()
}

// TestProbeEmbeddings covers the case this probe exists for: a local runtime
// that serves chat but cannot embed. Assuming "configured runtime => can embed"
// would select an embedder that fails, and because embedding_model sizes
// gbrain's schema, fixing that means wiping the brain.
func TestProbeEmbeddings(t *testing.T) {
	t.Run("a real embedder is accepted with its dimensions", func(t *testing.T) {
		resetProbeCache(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/embeddings") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"embedding": make([]float64, 768)}},
				})
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		got := ProbeEmbeddings(context.Background(), srv.URL, "nomic-embed-text")
		if !got.OK {
			t.Fatalf("probe rejected a working embedder: %s", got.Reason)
		}
		// Dimensions matter: they size gbrain's vector column.
		if got.Dimensions != 768 {
			t.Errorf("Dimensions = %d, want 768", got.Dimensions)
		}
	})

	t.Run("chat-only runtime is rejected", func(t *testing.T) {
		resetProbeCache(t)
		// Serves /v1/models but 404s embeddings — mlx_lm.server's shape.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/embeddings") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if got := ProbeEmbeddings(context.Background(), srv.URL, "llama3"); got.OK {
			t.Error("probe accepted a runtime that 404s /v1/embeddings")
		}
	})

	t.Run("200 with no vector is rejected", func(t *testing.T) {
		resetProbeCache(t)
		// The subtle failure: the route answers, but produces nothing usable.
		// A status-code-only check would wrongly accept this.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
		}))
		defer srv.Close()

		got := ProbeEmbeddings(context.Background(), srv.URL, "llama3")
		if got.OK {
			t.Error("probe accepted a 200 response carrying no embedding vector")
		}
		if !strings.Contains(got.Reason, "no embedding vector") {
			t.Errorf("Reason = %q, want it to name the empty vector", got.Reason)
		}
	})

	t.Run("unreachable endpoint is a negative, not an error", func(t *testing.T) {
		resetProbeCache(t)
		// Selection must survive a dead runtime: this is a choice among
		// embedders, so "does not work" is an answer, not a fault.
		got := ProbeEmbeddings(context.Background(), "http://127.0.0.1:1", "any")
		if got.OK {
			t.Error("probe accepted an unreachable endpoint")
		}
		if got.Reason == "" {
			t.Error("a negative result must carry a reason for the startup log")
		}
	})

	t.Run("empty inputs are rejected without a request", func(t *testing.T) {
		resetProbeCache(t)
		if got := ProbeEmbeddings(context.Background(), "", "m"); got.OK {
			t.Error("empty base URL accepted")
		}
		if got := ProbeEmbeddings(context.Background(), "http://x", ""); got.OK {
			t.Error("empty model accepted")
		}
	})

	t.Run("negative results are cached", func(t *testing.T) {
		resetProbeCache(t)
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/embeddings") {
				calls++
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ProbeEmbeddings(context.Background(), srv.URL, "llama3")
		before := calls
		ProbeEmbeddings(context.Background(), srv.URL, "llama3")
		if calls != before {
			t.Errorf("probe re-queried a known-negative endpoint (%d then %d calls)", before, calls)
		}
	})
}

// TestAPIURLTolerates_v1Suffix mirrors normalizeOpenAICompatEndpoint's rule:
// operators paste a runtime's listening address with or without /v1, and both
// must resolve to the same route rather than producing /v1/v1/embeddings.
func TestAPIURLToleratesV1Suffix(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8080":     "http://127.0.0.1:8080/v1/embeddings",
		"http://127.0.0.1:8080/":    "http://127.0.0.1:8080/v1/embeddings",
		"http://127.0.0.1:8080/v1":  "http://127.0.0.1:8080/v1/embeddings",
		"http://127.0.0.1:8080/v1/": "http://127.0.0.1:8080/v1/embeddings",
	}
	for in, want := range cases {
		if got := embeddingsURL(in); got != want {
			t.Errorf("embeddingsURL(%q) = %q, want %q", in, got, want)
		}
	}
}
