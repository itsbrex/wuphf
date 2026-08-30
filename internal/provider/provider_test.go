package provider_test

import (
	"testing"

	"github.com/nex-crm/wuphf/internal/provider"
)

// TestClaudeStreamFnBuilds verifies the function compiles and returns a channel.
// We don't actually exec `claude` in CI — just confirm the factory doesn't panic.
func TestClaudeStreamFnBuilds(t *testing.T) {
	fn := provider.CreateClaudeCodeStreamFn("test-agent")
	if fn == nil {
		t.Fatal("expected non-nil StreamFn")
	}
}

func TestCodexStreamFnBuilds(t *testing.T) {
	fn := provider.CreateCodexCLIStreamFn("test-agent")
	if fn == nil {
		t.Fatal("expected non-nil StreamFn")
	}
}
