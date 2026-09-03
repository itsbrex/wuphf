package team

// wiki_index_backend.go — backend selection for the wiki context layer.
//
// gbrain is the default. The SQLite + bleve pairing and the in-memory store
// remain reachable, but only as an explicit opt-out.
//
// Failure policy
// ==============
// ensureWikiWorker is documented as never broker-fatal, and that discipline is
// preserved here, with one deliberate exception:
//
//   - WUPHF_WIKI_BACKEND unset  → try gbrain; on failure log a WARNING and fall
//     back to the in-memory index. Without this, every deployment that does not
//     have gbrain installed loses the wiki entirely, and every broker test would
//     require a live brain.
//   - WUPHF_WIKI_BACKEND=gbrain → gbrain or nothing. An operator who asked for
//     gbrain explicitly must not silently get a different store; a fallback here
//     would hide an outage behind an empty-looking wiki.
//   - WUPHF_WIKI_BACKEND=memory → the previous in-memory behaviour, no gbrain.
//
// The fallback is loud on purpose. A context layer that quietly degrades to an
// empty store answers "no facts found" for every question, which reads as a
// product bug rather than as the missing dependency it is.

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nex-crm/wuphf/internal/gbrain"
)

// WikiBackendEnv selects the wiki index backend.
const WikiBackendEnv = "WUPHF_WIKI_BACKEND"

// Wiki index backend identifiers.
const (
	WikiBackendGBrain = "gbrain"
	WikiBackendMemory = "memory"
)

// resolveWikiBackend returns the configured backend and whether the operator
// named it explicitly. An unrecognised value is treated as unset so a typo
// degrades to the default rather than disabling the wiki.
//
// Under `go test` the default flips to the in-memory index. gbrain is a
// USER-GLOBAL resource: with no GBRAIN_HOME override it resolves to the
// developer's real brain at ~/.gbrain, so defaulting tests to gbrain made the
// broker suite spawn `gbrain serve` and read and write the developer's actual
// knowledge base. It also paid a 30s connect timeout per broker construction,
// which kept background goroutines alive past t.TempDir() cleanup and produced
// "directory not empty" flakes with no visible cause.
//
// A test that WANTS the gbrain backend still gets it by setting
// WUPHF_WIKI_BACKEND=gbrain explicitly, which is what the live contract tests
// do alongside an isolated GBRAIN_HOME.
func resolveWikiBackend() (backend string, explicit bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(WikiBackendEnv))) {
	case WikiBackendGBrain:
		return WikiBackendGBrain, true
	case WikiBackendMemory:
		return WikiBackendMemory, true
	default:
		if testing.Testing() {
			return WikiBackendMemory, false
		}
		return WikiBackendGBrain, false
	}
}

// newWikiIndexForBackend builds the WikiIndex for the configured backend.
//
// Returns an error only when the operator explicitly asked for gbrain and it is
// unreachable. In every other case it returns a usable index, logging a warning
// when it had to fall back.
func newWikiIndexForBackend(ctx context.Context, root string) (*WikiIndex, error) {
	backend, explicit := resolveWikiBackend()

	if backend == WikiBackendMemory {
		if explicit {
			log.Printf("wiki: using in-memory index (%s=%s)", WikiBackendEnv, WikiBackendMemory)
		}
		return NewWikiIndex(root), nil
	}

	// Create the brain if none exists yet, selecting the best available
	// embedder (hosted key > local model > none). Without this a user with a
	// local model and no brain silently got the in-memory fallback and never
	// learned a brain could have been created. EnsureBrain is idempotent and
	// never re-inits over a working brain, so this is safe on every boot.
	if _, err := gbrain.EnsureBrain(ctx); err != nil {
		log.Printf("wiki: gbrain brain init skipped: %v", err)
	}

	// Fill in chat + expansion for a host with NO API key, so a
	// subscription-only user gets more than bare keyword retrieval. Chat goes
	// native through gbrain's claude-cli recipe; expansion goes through the
	// broker's OpenAI-compatible shim, which is the only route gbrain exposes
	// for a custom endpoint. Never overwrites an operator's own choice.
	if shim := shimBaseURL(); shim != "" {
		if applied, err := gbrain.ConfigureNoKeyFallback(ctx, shim); err != nil {
			log.Printf("wiki: gbrain no-key fallback not applied: %v", err)
		} else if applied.Configured() {
			log.Printf("wiki: %s", applied)
		}
	}

	idx, err := NewGBrainEntityIndex(ctx, root)
	if err == nil {
		// State the retrieval capability that is ACTUALLY active. Silent
		// degradation to keyword-only is the failure mode this area invites:
		// retrieval keeps answering, just worse, with nothing in the logs.
		log.Printf("wiki: context layer backed by gbrain — retrieval: %s", gbrain.RetrievalMode())
		// A stale gbrain is a silent-data-loss risk the operator can fix in one
		// command, so say it once rather than compensating quietly forever.
		if advisory := gbrain.VersionAdvisory(ctx); advisory != "" {
			log.Printf("WARNING wiki: %s", advisory)
		}
		return idx, nil
	}
	if explicit {
		return nil, fmt.Errorf("wiki: %s=%s but the brain is unreachable: %w", WikiBackendEnv, WikiBackendGBrain, err)
	}
	log.Printf("WARNING wiki: gbrain unavailable (%v); falling back to the in-memory index. "+
		"Facts will NOT persist across restarts. Install gbrain, or set %s=%s to silence this.",
		err, WikiBackendEnv, WikiBackendMemory)
	return NewWikiIndex(root), nil
}

// brokerShimBase records the broker's own OpenAI-compatible base URL, set once
// the listener has an address. gbrain is configured to reach the shim there.
//
// It is a package var rather than a parameter because the wiki index is built
// during broker startup, at which point the caller does not thread its own
// address down this path.
var brokerShimBase atomic.Value // string

// SetBrokerShimBase records the broker's "http://host:port/v1" for gbrain.
func SetBrokerShimBase(base string) { brokerShimBase.Store(strings.TrimSpace(base)) }

// shimBaseURL returns the recorded shim base URL, or "".
func shimBaseURL() string {
	v, _ := brokerShimBase.Load().(string)
	return v
}
