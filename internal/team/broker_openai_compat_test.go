package team

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestSplitOpenAIMessages covers the flattening from a chat transcript to the
// (system, user) pair the one-shot provider hook accepts.
func TestSplitOpenAIMessages(t *testing.T) {
	t.Run("system and user are separated", func(t *testing.T) {
		sys, user := splitOpenAIMessages([]openAIChatMessage{
			{Role: "system", Content: "You expand search queries."},
			{Role: "user", Content: "Where does Esme Walker work?"},
		})
		if sys != "You expand search queries." {
			t.Errorf("system = %q", sys)
		}
		if user != "Where does Esme Walker work?" {
			t.Errorf("user = %q", user)
		}
	})

	t.Run("multiple system messages are joined, not dropped", func(t *testing.T) {
		sys, _ := splitOpenAIMessages([]openAIChatMessage{
			{Role: "system", Content: "A"},
			{Role: "developer", Content: "B"},
			{Role: "user", Content: "q"},
		})
		if !strings.Contains(sys, "A") || !strings.Contains(sys, "B") {
			t.Errorf("system lost content: %q", sys)
		}
	})

	t.Run("a multi-turn transcript keeps every turn", func(t *testing.T) {
		// Taking only the last message would silently discard context on any
		// multi-turn request.
		_, user := splitOpenAIMessages([]openAIChatMessage{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "reply"},
			{Role: "user", Content: "second"},
		})
		for _, want := range []string{"first", "reply", "second"} {
			if !strings.Contains(user, want) {
				t.Errorf("user prompt dropped %q: %q", want, user)
			}
		}
		// Non-user roles keep their label so the CLI can tell who said what.
		if !strings.Contains(user, "assistant: reply") {
			t.Errorf("assistant turn lost its role prefix: %q", user)
		}
	})

	t.Run("a role-less message is treated as user", func(t *testing.T) {
		_, user := splitOpenAIMessages([]openAIChatMessage{{Content: "bare"}})
		if user != "bare" {
			t.Errorf("user = %q, want the bare content", user)
		}
	})

	t.Run("empty content is skipped", func(t *testing.T) {
		sys, user := splitOpenAIMessages([]openAIChatMessage{
			{Role: "system", Content: "   "},
			{Role: "user", Content: "q"},
		})
		if sys != "" {
			t.Errorf("whitespace-only system message became %q", sys)
		}
		if user != "q" {
			t.Errorf("user = %q", user)
		}
	})
}

// TestOpenAIChatCompletionsRejectsStreaming is a contract test for the one
// refusal that matters.
//
// gbrain's openai-compatible adapter may request SSE. Answering a stream:true
// request with a single JSON body would leave the client waiting for frames
// that never arrive — a hang, not an error, and far harder to diagnose than a
// 400. So streaming is refused explicitly rather than silently downgraded.
func TestOpenAIChatCompletionsRejectsStreaming(t *testing.T) {
	t.Setenv("WUPHF_RUNTIME_HOME", t.TempDir())
	b := newTestBroker(t)
	if err := b.StartOnPort(0); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer b.Stop()

	body := `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	code, payload := postOpenAIChat(t, b, body)
	if code != http.StatusBadRequest {
		t.Fatalf("stream:true returned %d, want 400 — a silent non-stream reply would hang the client", code)
	}
	if !strings.Contains(payload, "streaming is not supported") {
		t.Errorf("error body does not explain the refusal: %s", payload)
	}
}

// TestOpenAIChatCompletionsRejectsEmptyPrompt guards against forwarding a
// contentless request to a subprocess that bills tokens.
func TestOpenAIChatCompletionsRejectsEmptyPrompt(t *testing.T) {
	t.Setenv("WUPHF_RUNTIME_HOME", t.TempDir())
	b := newTestBroker(t)
	if err := b.StartOnPort(0); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer b.Stop()

	code, _ := postOpenAIChat(t, b, `{"model":"m","messages":[{"role":"system","content":"only a system prompt"}]}`)
	if code != http.StatusBadRequest {
		t.Errorf("a request with no user content returned %d, want 400", code)
	}
}

// TestOpenAIChatCompletionsRequiresAuth pins that the route sits behind the
// broker's Bearer auth like every other mutating surface. It reaches a provider
// subprocess, so an unauthenticated caller must not be able to spend tokens.
func TestOpenAIChatCompletionsRequiresAuth(t *testing.T) {
	t.Setenv("WUPHF_RUNTIME_HOME", t.TempDir())
	b := newTestBroker(t)
	if err := b.StartOnPort(0); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer b.Stop()

	req, err := http.NewRequest(http.MethodPost,
		"http://"+b.Addr()+"/v1/chat/completions",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Deliberately no Authorization header.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		t.Error("unauthenticated request reached the provider — this route spends tokens")
	}
}

// postOpenAIChat posts an authenticated chat-completions request.
func postOpenAIChat(t *testing.T, b *Broker, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		"http://"+b.Addr()+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}
