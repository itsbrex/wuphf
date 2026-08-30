package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestInitFlowViewShowsReadinessSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prevLookPath := initFlowLookPathFn
	initFlowLookPathFn = func(name string) (string, error) {
		switch name {
		case "tmux", "claude":
			return "/usr/bin/" + name, nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	t.Cleanup(func() {
		initFlowLookPathFn = prevLookPath
	})

	flow := NewInitFlow()
	flow.phase = InitProviderChoice
	flow.provider = "claude-code"

	view := flow.View()
	if !containsAll(view, "Setup Readiness", "tmux office runtime", "LLM runtime", "Operation template") {
		t.Fatalf("expected readiness summary in init view, got %q", view)
	}
	if strings.Contains(view, "Nex") {
		t.Fatalf("init view must not mention Nex, got %q", view)
	}
}

func TestBlueprintOptionsIncludeTemplates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	options := BlueprintOptions()
	if len(options) == 0 {
		t.Fatal("expected blueprint options")
	}
	if options[0].Value == "" {
		t.Fatalf("expected blueprint option value, got %+v", options[0])
	}
}

func TestProviderOptionsIncludeCodex(t *testing.T) {
	options := ProviderOptions()
	for _, opt := range options {
		if opt.Value == "codex" {
			return
		}
	}
	t.Fatal("expected codex provider option")
}

func TestProviderOptionsIncludeOpencode(t *testing.T) {
	options := ProviderOptions()
	for _, opt := range options {
		if opt.Value == "opencode" {
			return
		}
	}
	t.Fatal("expected opencode provider option")
}

func TestProviderOptionsExcludeUnsupportedProviders(t *testing.T) {
	options := ProviderOptions()
	values := make([]string, 0, len(options))
	for _, opt := range options {
		values = append(values, opt.Value)
	}
	joined := strings.Join(values, ",")
	// Unsupported providers must not appear. Framed as a negative invariant
	// (rather than an exact allowlist) so adding new supported providers —
	// opencode, openclaw, etc. — doesn't require editing this test.
	for _, banned := range []string{"gemini", "nex-ask"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("expected provider options to hide %q, got %q", banned, joined)
		}
	}
}

func containsAll(s string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(s, needle) {
			return false
		}
	}
	return true
}
