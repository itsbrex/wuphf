package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nex-crm/wuphf/internal/company"
	"github.com/nex-crm/wuphf/internal/config"
)

// ErrQuit is returned by quit commands so the caller can signal clean exit.
var ErrQuit = errors.New("quit")

func cmdHelp(ctx *SlashContext, args string) error {
	help := "Commands:\n\n" +
		"  /agent                 list | <slug>\n" +
		"  /agents                Manage your team\n" +
		"  /calendar              View calendar\n" +
		"  /doctor                Check readiness and runtime health\n\n" +
		"  /config <sub>          show | set | path\n" +
		"  /detect                Detect AI platforms\n" +
		"  /init                  Run setup\n" +
		"  /provider              Switch LLM provider\n\n" +
		"  /help                  This help\n" +
		"  /clear                 Clear messages\n" +
		"  /quit                  Exit WUPHF"
	ctx.AddMessage("system", help)
	return nil
}

func cmdClear(ctx *SlashContext, args string) error {
	ctx.AddMessage("system", "Messages cleared.")
	return nil
}

func cmdQuit(ctx *SlashContext, args string) error {
	return ErrQuit
}

func cmdInit(ctx *SlashContext, args string) error {
	cfg, _ := config.Load()
	if cfg.LLMProvider == "" {
		cfg.LLMProvider = "claude-code"
	}
	if cfg.ActiveBlueprint() == "" {
		if manifest, err := company.LoadManifest(); err == nil {
			if refs := manifest.BlueprintRefsByKind("operation"); len(refs) > 0 {
				cfg.SetActiveBlueprint(refs[0].ID)
			}
		}
	}
	if cfg.TeamLeadSlug == "" {
		if manifest, err := company.LoadRuntimeManifest("."); err == nil && strings.TrimSpace(manifest.Lead) != "" {
			cfg.TeamLeadSlug = manifest.Lead
		}
	}
	if err := config.Save(cfg); err != nil {
		return err
	}

	label := cfg.ActiveBlueprint()
	if strings.TrimSpace(label) == "" {
		label = "none"
	}
	ctx.AddMessage("system", fmt.Sprintf("Setup defaults saved. Provider: %s | Blueprint template: %s", cfg.LLMProvider, label))
	ctx.AddMessage("system", config.OneSetupBlurb())

	// Provider API key summary
	type pkEntry struct {
		name string
		set  bool
	}
	providerKeys := []pkEntry{
		{"Gemini", config.ResolveGeminiAPIKey() != ""},
		{"Anthropic", config.ResolveAnthropicAPIKey() != ""},
		{"OpenAI", config.ResolveOpenAIAPIKey() != ""},
		{"Minimax", config.ResolveMinimaxAPIKey() != ""},
	}
	var pkLines []string
	for _, pk := range providerKeys {
		status := "not set"
		if pk.set {
			status = "configured"
		}
		pkLines = append(pkLines, fmt.Sprintf("  %s: %s", pk.name, status))
	}
	ctx.AddMessage("system", "Provider API keys:\n"+strings.Join(pkLines, "\n"))
	return nil
}

func cmdProvider(ctx *SlashContext, args string) error {
	options := []PickerOption{
		{Label: "Codex CLI", Value: "codex", Description: "Codex via codex CLI"},
		{Label: "Claude Code", Value: "claude-code", Description: "Claude via claude-code CLI"},
		{Label: "Opencode CLI", Value: "opencode", Description: "Opencode via opencode CLI (BYO provider)"},
	}
	if ctx.ShowPicker != nil {
		ctx.ShowPicker("Switch LLM Provider", options)
		return nil
	}
	var sb strings.Builder
	sb.WriteString("LLM providers:\n")
	for _, opt := range options {
		sb.WriteString(fmt.Sprintf("  • %s — %s\n", opt.Label, opt.Description))
	}
	ctx.AddMessage("system", strings.TrimRight(sb.String(), "\n"))
	return nil
}

// --- shared helpers ---

func formatMapResult(m map[string]any) string {
	for _, key := range []string{"answer", "message", "result", "text"} {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", m)
	}
	return string(b)
}
