package team

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
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

// TestGBrainConsumesTheShim is DISABLED, and the reason is the finding.
//
// It was written to prove the thing that justifies the shim: that gbrain — its
// only intended consumer — can be pointed at it. It cannot, on gbrain
// 0.42.58.0, and the blocker is in gbrain's recipe definitions rather than in
// anything here.
//
// What was tried, and why each failed
// ===================================
//  1. expansion_model "openai-compatible:<model>". There IS NO
//     openai-compatible provider recipe. gbrain names recipes by SERVICE
//     (ollama, litellm, llama-server); several carry
//     implementation:"openai-compatible", but that is an implementation detail,
//     not a provider id. resolveRecipe throws "Unknown provider",
//     gateway.isAvailable catches and returns false, and expansion silently
//     degrades to the bare query. No error surfaces.
//  2. The openai-compatible-implementation recipes. litellm, llama-server and
//     ollama declare only `embedding` (and reranker) touchpoints — never
//     `expansion`. isAvailable returns false on the missing touchpoint before
//     auth is even considered.
//  3. Masquerading as `openai` with its base_url overridden. The openai recipe
//     DOES declare expansion, and provider_base_urls overrides its endpoint.
//     But its expansion touchpoint allowlists models ['gpt-5.2','gpt-4o-mini']
//     with no user_provided_models, so an arbitrary name is rejected. Using an
//     allowlisted name and pointing the base URL at the shim STILL did not
//     produce a call — verified with the config confirmed via `config get`.
//
// So: no gbrain recipe declares an expansion touchpoint whose endpoint can be
// redirected. The shim is correct and proven end-to-end by
// TestOpenAIChatCompletionsLive_SuccessPath, but its intended consumer cannot
// currently reach it.
//
// Re-enable this when gbrain either adds `expansion` to an
// openai-compatible-implementation recipe, or adds user_provided_models to one
// that has it. Worth retrying on 0.48+, which is several minors ahead of what
// this was tested against.
func TestGBrainConsumesTheShim(t *testing.T) {
	t.Skip("blocked upstream: no gbrain recipe exposes a redirectable expansion touchpoint — see the comment above")
}
