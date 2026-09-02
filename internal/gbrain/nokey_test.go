package gbrain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withScratchBrainConfig writes a gbrain config.json into an isolated
// GBRAIN_HOME and returns its path.
func withScratchBrainConfig(t *testing.T, cfg map[string]any) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GBRAIN_HOME", home)
	dir := filepath.Join(home, ".gbrain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// readBrainConfig reads a gbrain config back.
func readBrainConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

// noHostedKeys removes every hosted credential so the keyless path is exercised.
func noHostedKeys(t *testing.T) {
	t.Helper()
	prev := selectOpenAIKey
	selectOpenAIKey = func() string { return "" }
	t.Cleanup(func() { selectOpenAIKey = prev })
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("WUPHF_OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("WUPHF_ANTHROPIC_API_KEY", "")
}

func TestConfigureNoKeyFallback(t *testing.T) {
	t.Run("fills chat and expansion when nothing is set", func(t *testing.T) {
		noHostedKeys(t)
		t.Setenv("GBRAIN_CLAUDE_CLI_BIN", "/usr/local/bin/claude") // stand in for PATH
		path := withScratchBrainConfig(t, map[string]any{"engine": "pglite"})

		got, err := ConfigureNoKeyFallback(context.Background(), "http://127.0.0.1:7890/v1")
		if err != nil {
			t.Fatalf("ConfigureNoKeyFallback: %v", err)
		}
		if !got.Configured() {
			t.Fatalf("nothing was configured: %s", got)
		}

		cfg := readBrainConfig(t, path)
		if cfg["chat_model"] != "claude-cli:"+claudeCLIDefaultModel {
			t.Errorf("chat_model = %v, want the claude-cli recipe", cfg["chat_model"])
		}
		// litellm, NOT "openai-compatible": the latter is an implementation tag,
		// not a provider id, and gbrain throws "Unknown provider" on it while
		// isAvailable silently swallows the error.
		if cfg["expansion_model"] != shimRecipeID+":"+shimExpansionModel {
			t.Errorf("expansion_model = %v, want the litellm recipe", cfg["expansion_model"])
		}
		urls, _ := cfg["provider_base_urls"].(map[string]any)
		if urls[shimRecipeID] != "http://127.0.0.1:7890/v1" {
			t.Errorf("provider_base_urls[%s] = %v, want the shim URL", shimRecipeID, urls[shimRecipeID])
		}
	})

	t.Run("a hosted key means this does not apply at all", func(t *testing.T) {
		prev := selectOpenAIKey
		selectOpenAIKey = func() string { return "sk-real" }
		t.Cleanup(func() { selectOpenAIKey = prev })
		path := withScratchBrainConfig(t, map[string]any{"engine": "pglite"})

		got, _ := ConfigureNoKeyFallback(context.Background(), "http://127.0.0.1:7890/v1")
		if got.Configured() {
			t.Error("applied the keyless fallback despite a hosted key being present")
		}
		if cfg := readBrainConfig(t, path); cfg["chat_model"] != nil {
			t.Errorf("config was modified: %v", cfg)
		}
	})

	t.Run("never overwrites the operator's own choices", func(t *testing.T) {
		// The point is to fill gaps. Overwriting a deliberate configuration
		// would be worse than leaving one.
		noHostedKeys(t)
		t.Setenv("GBRAIN_CLAUDE_CLI_BIN", "/usr/local/bin/claude")
		path := withScratchBrainConfig(t, map[string]any{
			"engine":          "pglite",
			"chat_model":      "ollama:llama3",
			"expansion_model": "ollama:llama3",
		})

		ConfigureNoKeyFallback(context.Background(), "http://127.0.0.1:7890/v1") //nolint:errcheck
		cfg := readBrainConfig(t, path)
		if cfg["chat_model"] != "ollama:llama3" {
			t.Errorf("clobbered chat_model: %v", cfg["chat_model"])
		}
		if cfg["expansion_model"] != "ollama:llama3" {
			t.Errorf("clobbered expansion_model: %v", cfg["expansion_model"])
		}
	})

	t.Run("preserves other providers' base URLs", func(t *testing.T) {
		// provider_base_urls is shared. Replacing the map rather than merging
		// would silently break an unrelated provider the user configured.
		noHostedKeys(t)
		t.Setenv("GBRAIN_CLAUDE_CLI_BIN", "/usr/local/bin/claude")
		path := withScratchBrainConfig(t, map[string]any{
			"engine":             "pglite",
			"provider_base_urls": map[string]any{"ollama": "http://localhost:11434/v1"},
		})

		ConfigureNoKeyFallback(context.Background(), "http://127.0.0.1:7890/v1") //nolint:errcheck
		urls, _ := readBrainConfig(t, path)["provider_base_urls"].(map[string]any)
		if urls["ollama"] != "http://localhost:11434/v1" {
			t.Errorf("dropped an unrelated provider base URL: %v", urls)
		}
		if urls[shimRecipeID] == nil {
			t.Error("did not add the shim base URL")
		}
	})

	t.Run("no claude CLI means no chat model", func(t *testing.T) {
		noHostedKeys(t)
		t.Setenv("GBRAIN_CLAUDE_CLI_BIN", "")
		t.Setenv("PATH", t.TempDir()) // nothing on PATH
		if got := SelectChatModel(); got != "" {
			t.Errorf("SelectChatModel() = %q with no CLI available, want empty", got)
		}
	})

	t.Run("missing config is skipped, not an error", func(t *testing.T) {
		noHostedKeys(t)
		t.Setenv("GBRAIN_HOME", t.TempDir()) // no .gbrain/config.json
		got, err := ConfigureNoKeyFallback(context.Background(), "http://127.0.0.1:7890/v1")
		if err != nil {
			t.Errorf("absence of a brain should not be an error: %v", err)
		}
		if got.Configured() {
			t.Error("configured something with no brain present")
		}
	})
}
