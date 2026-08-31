package team

import (
	"strings"
	"testing"
)

// TestWikiBackendDefaultsToMemoryUnderTest is a regression guard for a real
// incident: defaulting the wiki backend to gbrain made the broker suite connect
// to the DEVELOPER'S REAL BRAIN.
//
// gbrain is user-global. With no GBRAIN_HOME override it resolves to ~/.gbrain,
// so every broker test that started the wiki worker spawned `gbrain serve`
// against the developer's actual knowledge base — reading it, and able to write
// to it. It also paid a 30s connect timeout per broker construction, which kept
// goroutines alive past t.TempDir() cleanup and surfaced as "directory not
// empty" failures that named no cause.
//
// Tests must default to the in-memory index. Anything wanting gbrain opts in
// explicitly, alongside an isolated GBRAIN_HOME.
func TestWikiBackendDefaultsToMemoryUnderTest(t *testing.T) {
	t.Setenv(WikiBackendEnv, "")

	backend, explicit := resolveWikiBackend()
	if backend != WikiBackendMemory {
		t.Fatalf("resolveWikiBackend() = %q under test, want %q — tests would reach the developer's real brain",
			backend, WikiBackendMemory)
	}
	if explicit {
		t.Error("the test-mode default must not report itself as an explicit operator choice")
	}
}

// TestWikiBackendHonoursExplicitOptIn proves the guard above does not make the
// gbrain backend unreachable from tests that genuinely want it.
func TestWikiBackendHonoursExplicitOptIn(t *testing.T) {
	t.Setenv(WikiBackendEnv, WikiBackendGBrain)
	backend, explicit := resolveWikiBackend()
	if backend != WikiBackendGBrain || !explicit {
		t.Fatalf("resolveWikiBackend() = (%q,%v), want (%q,true)", backend, explicit, WikiBackendGBrain)
	}
}

// TestWikiBackendIgnoresUnknownValue pins that a typo degrades to the default
// rather than disabling the wiki outright.
func TestWikiBackendIgnoresUnknownValue(t *testing.T) {
	t.Setenv(WikiBackendEnv, "gbrian") // deliberate typo
	backend, explicit := resolveWikiBackend()
	if explicit {
		t.Error("an unrecognised value must not count as an explicit choice")
	}
	if strings.TrimSpace(backend) == "" {
		t.Error("an unrecognised value must fall back to a usable backend")
	}
}
