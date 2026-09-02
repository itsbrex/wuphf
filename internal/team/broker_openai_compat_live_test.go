package team

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// liveShimEnabled gates the tests that spend real provider tokens.
func liveShimEnabled() bool {
	return strings.TrimSpace(os.Getenv("WUPHF_SHIM_LIVE_TEST")) == "1"
}

// TestOpenAIChatCompletionsLive_SuccessPath proves the shim returns a REAL
// completion from the configured agent CLI in a shape an openai-compatible
// client will accept.
//
// The offline tests only cover refusals. They would all still pass if the
// success path returned a malformed body, which is exactly the failure that
// would make this route useless to gbrain.
func TestOpenAIChatCompletionsLive_SuccessPath(t *testing.T) {
	if !liveShimEnabled() {
		t.Skip("spends provider tokens: set WUPHF_SHIM_LIVE_TEST=1")
	}
	t.Setenv("WUPHF_RUNTIME_HOME", t.TempDir())
	b := newTestBroker(t)
	if err := b.StartOnPort(0); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer b.Stop()

	body := `{"model":"m","messages":[
		{"role":"system","content":"Reply with exactly one word."},
		{"role":"user","content":"Say OK"}]}`
	req, _ := http.NewRequest(http.MethodPost, "http://"+b.Addr()+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.Token())

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}

	// Decode with the strictness an openai-compatible adapter applies: the
	// nesting is load-bearing, and a "close enough" shape fails at the client.
	var decoded struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("response is not parseable as a chat completion: %v\n%s", err, raw)
	}
	if decoded.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", decoded.Object)
	}
	if len(decoded.Choices) != 1 {
		t.Fatalf("got %d choices, want exactly 1", len(decoded.Choices))
	}
	c := decoded.Choices[0]
	if c.Message.Role != "assistant" {
		t.Errorf("role = %q, want assistant", c.Message.Role)
	}
	if c.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", c.FinishReason)
	}
	if strings.TrimSpace(c.Message.Content) == "" {
		t.Error("assistant content is empty — the CLI produced nothing")
	}
	t.Logf("upstream CLI replied: %.80q", c.Message.Content)
}

// TestGBrainConsumesTheShim proves the thing that justifies the shim: that
// gbrain — its only intended consumer — can actually be pointed at it and can
// parse what it returns.
//
// Routing, and why it is `litellm` specifically
// ============================================
// gbrain has no provider recipe literally named "openai-compatible"; that is an
// IMPLEMENTATION tag, not a provider id, and using it as one throws "Unknown
// provider" which isAvailable swallows — expansion then degrades to the bare
// query with no error surfaced anywhere. Recipes are named by SERVICE.
//
// The recipe must satisfy three things at once, and on gbrain 0.42 nothing did:
//
//  1. declare an `expansion` touchpoint (openai/anthropic/google did; the
//     openai-compat ones declared only embedding),
//  2. accept an arbitrary model name (`user_provided_models`), since the shim
//     dispatches to whatever CLI is configured and has no model list, and
//  3. require no API key, since a subscription-only user has none.
//
// gbrain 0.48's `litellm` recipe is the first to satisfy all three:
// expansion with `models: []` + `user_provided_models`, and `auth_env.required:
// []`. That is the supported route.
//
// Two traps this test was rewritten to avoid, both of which made an earlier
// version pass while proving NOTHING:
//
//  1. `gbrain config set` exits 0 without persisting to the plane the gateway
//     reads, so the config must be written to config.json and VERIFIED.
//  2. asserting "the query did not error" is vacuous: gbrain exits 0 printing
//     "No results." on an empty brain, having never called expansion. The test
//     must assert the shim was ACTUALLY HIT, which it does by counting
//     requests through a proxy in front of the real broker route.
func TestGBrainConsumesTheShim(t *testing.T) {
	if !liveShimEnabled() {
		t.Skip("spends provider tokens: set WUPHF_SHIM_LIVE_TEST=1")
	}
	home := strings.TrimSpace(os.Getenv("GBRAIN_HOME"))
	if home == "" {
		t.Skip("set GBRAIN_HOME to a scratch brain; this rewrites its config")
	}
	bin, err := exec.LookPath("gbrain")
	if err != nil {
		t.Skip("gbrain not on PATH")
	}

	t.Setenv("WUPHF_RUNTIME_HOME", t.TempDir())
	b := newTestBroker(t)
	if err := b.StartOnPort(0); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer b.Stop()

	// Counting proxy in front of the REAL broker route, so a hit is provable
	// rather than inferred. It injects the broker token so gbrain needs no key.
	var mu sync.Mutex
	var hits, lastStatus int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits++
		mu.Unlock()

		fwd, _ := http.NewRequest(r.Method, "http://"+b.Addr()+r.URL.Path, strings.NewReader(string(body)))
		fwd.Header.Set("Content-Type", "application/json")
		fwd.Header.Set("Authorization", "Bearer "+b.Token())
		resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(fwd)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		out, _ := io.ReadAll(resp.Body)
		mu.Lock()
		lastStatus = resp.StatusCode
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(out)
	}))
	defer proxy.Close()

	cfgPath := filepath.Join(home, ".gbrain", "config.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Skipf("no gbrain config at %s: %v", cfgPath, err)
	}
	t.Cleanup(func() { _ = os.WriteFile(cfgPath, raw, 0o600) })

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse gbrain config: %v", err)
	}
	cfg["provider_base_urls"] = map[string]any{"litellm": proxy.URL + "/v1"}
	cfg["expansion_model"] = "litellm:wuphf-configured-provider"
	patched, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, patched, 0o600); err != nil {
		t.Fatalf("write gbrain config: %v", err)
	}

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "GBRAIN_HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// `config set` writes a plane the gateway may not read, so verify the file
	// plane took rather than trusting the write.
	if out, _ := run("config", "get", "expansion_model"); !strings.Contains(out, "litellm:") {
		t.Fatalf("expansion_model did not take; got %q", strings.TrimSpace(out))
	}

	// Seed content. gbrain prints "No results." on an empty brain and never
	// calls expansion at all, so a test that depends on leftover pages from
	// other tests passes or fails on ordering rather than on behaviour. The
	// contract tests purge every namespace, so this must stand alone.
	seed := `{"slug":"notes/shim-expansion-probe","content":"---\ntype: note\n---\n\n` +
		`The Orbit Launch project ships the new satellite telemetry pipeline in Q3.\n"}`
	if out, err := run("call", "put_page", seed); err != nil {
		t.Fatalf("seed page failed: %v\n%s", err, truncateForLog(out))
	}

	out, err := run("query", "what is the orbit launch project about")
	t.Logf("gbrain query output:\n%s", truncateForLog(out))
	if err != nil {
		t.Fatalf("gbrain query failed with the shim as its expansion model: %v", err)
	}

	mu.Lock()
	got, status := hits, lastStatus
	mu.Unlock()

	// THE assertion. Without it this passes on an empty brain having exercised
	// nothing at all.
	if got == 0 {
		t.Fatal("gbrain never called the shim — expansion did not fire, so this proves nothing")
	}
	t.Logf("shim received %d request(s), last upstream status %d", got, status)
	if status != http.StatusOK {
		t.Errorf("shim returned %d to gbrain, want 200", status)
	}
	for _, marker := range []string{"expansion failed", "gateway unavailable", "could not parse"} {
		if strings.Contains(strings.ToLower(out), marker) {
			t.Errorf("gbrain rejected the shim response (%q in output)", marker)
		}
	}
}

func truncateForLog(s string) string {
	if len(s) > 1200 {
		return s[:1200] + "…"
	}
	return s
}
