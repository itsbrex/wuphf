package gbrain

// nokey.go — make gbrain fully functional for a user who has NO API key.
//
// The problem
// ===========
// gbrain speaks HTTP to model providers. A user on a Claude Pro or ChatGPT
// subscription has no key: their credentials live inside the `claude` CLI. Such
// a user previously got a brain with no chat model and no query expansion,
// leaving retrieval on the bare keyword arm.
//
// Two different mechanisms are needed, because gbrain's touchpoints are
// declared per-recipe and no single recipe covers both:
//
//	chat       -> gbrain's own `claude-cli` recipe (0.48+). It drives the CLI
//	              as a subprocess using its OAuth session, so no key is needed
//	              and no shim is involved. Native and strictly better than
//	              proxying.
//	expansion  -> the broker's OpenAI-compatible shim, addressed through the
//	              `litellm` recipe. claude-cli CANNOT serve this: it declares
//	              ONLY a chat touchpoint, so isAvailable('expansion') rejects
//	              it. litellm is the one recipe declaring expansion with
//	              user_provided_models and no required auth, which is exactly
//	              what an arbitrary local endpoint needs.
//
// Embeddings are deliberately absent. Anthropic publishes no embeddings
// endpoint, and a chat model cannot produce a usable vector, so a no-key user
// gets keyword retrieval plus expansion — not semantic search. See
// SelectEmbeddingModel for the embedder chain, which is a separate decision.
//
// Nothing here clobbers a value the operator already chose. The whole point is
// to fill in what is MISSING; overwriting a deliberate configuration would be
// worse than leaving the gap.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nex-crm/wuphf/internal/config"
)

// claudeCLIDefaultModel is the chat model requested from the claude-cli recipe.
// It must be one the recipe declares, or the touchpoint check rejects it.
const claudeCLIDefaultModel = "claude-sonnet-4-6"

// shimExpansionModel is the model name sent to the shim. The shim dispatches to
// whatever CLI the office has configured and ignores this string, but gbrain
// requires SOME model, and the litellm recipe accepts arbitrary names
// (user_provided_models).
const shimExpansionModel = "wuphf-configured-provider"

// shimRecipeID is the gbrain recipe used to address the broker's shim.
//
// NOT "openai-compatible": that is an implementation tag, not a provider id,
// and using it as one throws "Unknown provider" which gbrain's isAvailable
// swallows — expansion then silently degrades to the bare query. `litellm` is
// the recipe that declares an expansion touchpoint with user_provided_models
// and auth_env.required: [].
const shimRecipeID = "litellm"

// claudeCLIAvailable reports whether the `claude` binary is on PATH, which is
// the entire auth surface for the claude-cli recipe.
func claudeCLIAvailable() bool {
	if bin := strings.TrimSpace(os.Getenv("GBRAIN_CLAUDE_CLI_BIN")); bin != "" {
		return true
	}
	_, err := exec.LookPath("claude")
	return err == nil
}

// hasHostedKey reports whether any hosted provider key is configured. When one
// is, gbrain can reach a model on its own and none of this applies.
func hasHostedKey() bool {
	return strings.TrimSpace(selectOpenAIKey()) != "" ||
		strings.TrimSpace(config.ResolveAnthropicAPIKey()) != ""
}

// SelectChatModel returns the gbrain chat_model for a keyless host, or "".
func SelectChatModel() string {
	if hasHostedKey() || !claudeCLIAvailable() {
		return ""
	}
	return "claude-cli:" + claudeCLIDefaultModel
}

// NoKeyFallback describes what ConfigureNoKeyFallback changed, for logging.
type NoKeyFallback struct {
	ChatModel      string
	ExpansionModel string
	ShimBaseURL    string
	Skipped        string
}

// Configured reports whether anything was actually applied.
func (f NoKeyFallback) Configured() bool {
	return f.ChatModel != "" || f.ExpansionModel != ""
}

// String renders a one-line summary for the startup log.
func (f NoKeyFallback) String() string {
	if f.Skipped != "" {
		return "no-key fallback not applied: " + f.Skipped
	}
	var parts []string
	if f.ChatModel != "" {
		parts = append(parts, "chat="+f.ChatModel)
	}
	if f.ExpansionModel != "" {
		parts = append(parts, "expansion="+f.ExpansionModel+" via "+f.ShimBaseURL)
	}
	if len(parts) == 0 {
		return "no-key fallback: nothing to fill in"
	}
	return "no-key fallback: " + strings.Join(parts, ", ")
}

// ConfigureNoKeyFallback fills in gbrain's chat and expansion models for a host
// with no API key, pointing expansion at the broker's shim at shimBaseURL
// (the broker's "http://host:port/v1").
//
// It is a no-op when a hosted key exists, when no brain is configured, or when
// the operator has already set these fields — this fills gaps, it does not
// impose choices.
//
// The config is written to the FILE plane. `gbrain config set` exits 0 without
// persisting where the gateway reads these, so writing the file is the only
// reliable path.
func ConfigureNoKeyFallback(ctx context.Context, shimBaseURL string) (NoKeyFallback, error) {
	var out NoKeyFallback

	if hasHostedKey() {
		out.Skipped = "a hosted API key is configured; gbrain can reach a model directly"
		return out, nil
	}
	cfgPath, err := configFilePath()
	if err != nil {
		out.Skipped = "no gbrain config found"
		return out, nil //nolint:nilerr // absence is not an error here
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		out.Skipped = "no gbrain config found"
		return out, nil //nolint:nilerr
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return out, fmt.Errorf("parse gbrain config: %w", err)
	}

	changed := false

	// chat: native via the CLI's own OAuth session, no shim in the path.
	if _, set := cfg["chat_model"]; !set {
		if model := SelectChatModel(); model != "" {
			cfg["chat_model"] = model
			out.ChatModel = model
			changed = true
		}
	}

	// expansion: only reachable through the shim.
	if _, set := cfg["expansion_model"]; !set && strings.TrimSpace(shimBaseURL) != "" {
		cfg["expansion_model"] = shimRecipeID + ":" + shimExpansionModel
		out.ExpansionModel = cfg["expansion_model"].(string)
		out.ShimBaseURL = shimBaseURL

		// Merge rather than replace: provider_base_urls may carry endpoints for
		// other providers that must survive.
		urls := map[string]any{}
		if existing, ok := cfg["provider_base_urls"].(map[string]any); ok {
			for k, v := range existing {
				urls[k] = v
			}
		}
		urls[shimRecipeID] = shimBaseURL
		cfg["provider_base_urls"] = urls
		changed = true
	}

	if !changed {
		out.Skipped = "chat_model and expansion_model are already configured"
		return out, nil
	}

	patched, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return out, fmt.Errorf("encode gbrain config: %w", err)
	}
	if err := os.WriteFile(cfgPath, patched, 0o600); err != nil {
		return out, fmt.Errorf("write gbrain config: %w", err)
	}
	return out, nil
}

// configFilePath resolves gbrain's config.json, honouring GBRAIN_HOME so a
// scratch brain is never confused with the user's real one.
func configFilePath() (string, error) {
	home := strings.TrimSpace(os.Getenv("GBRAIN_HOME"))
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	path := filepath.Join(home, ".gbrain", "config.json")
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}
