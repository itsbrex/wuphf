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
func resolveWikiBackend() (backend string, explicit bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(WikiBackendEnv))) {
	case WikiBackendGBrain:
		return WikiBackendGBrain, true
	case WikiBackendMemory:
		return WikiBackendMemory, true
	default:
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
		log.Printf("wiki: using in-memory index (%s=%s)", WikiBackendEnv, WikiBackendMemory)
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

	idx, err := NewGBrainEntityIndex(ctx, root)
	if err == nil {
		// State the retrieval capability that is ACTUALLY active. Silent
		// degradation to keyword-only is the failure mode this area invites:
		// retrieval keeps answering, just worse, with nothing in the logs.
		log.Printf("wiki: context layer backed by gbrain — retrieval: %s", gbrain.RetrievalMode())
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
