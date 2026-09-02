package team

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func newAgentDMTestBroker(t *testing.T) *Broker {
	t.Helper()
	b := newTestBroker(t)
	b.mu.Lock()
	b.members = append(b.members,
		officeMember{Slug: "writer", Name: "Writer", Role: "specialist"},
		officeMember{Slug: "editor", Name: "Editor", Role: "specialist"},
		officeMember{Slug: "scout", Name: "Scout", Role: "specialist"},
	)
	b.rebuildMemberIndexLocked()
	b.mu.Unlock()
	return b
}

func TestAgentDMWriteGuard(t *testing.T) {
	b := newAgentDMTestBroker(t)

	// Members post freely; the pair channel auto-creates via the consult
	// relay's ensure path.
	msg, err := b.PostMessage("writer", "writer__editor", "subject line B or A?", nil, "")
	if err != nil {
		t.Fatalf("member post into agent DM: %v", err)
	}
	if _, _, ok := isAgentToAgentDM(msg.Channel); !ok {
		t.Fatalf("posted channel %q is not recognized as an agent pair DM", msg.Channel)
	}

	// Humans can read the thread (the markers' read-only view) but never
	// write into it.
	if _, err := b.PostMessage("you", msg.Channel, "let me chime in", nil, ""); err == nil {
		t.Fatal("human post into an agent DM must be rejected")
	}

	// Agents outside the pair are rejected too.
	if _, err := b.PostMessage("scout", msg.Channel, "me too", nil, ""); err == nil {
		t.Fatal("non-member agent post into an agent DM must be rejected")
	}
}

func TestAgentDMWakeAllowedCapAndRollover(t *testing.T) {
	b := newAgentDMTestBroker(t)

	// Non-pair channels are never capped.
	if !b.AgentDMWakeAllowed("general") {
		t.Fatal("general must not be wake-capped")
	}
	if !b.AgentDMWakeAllowed(DMSlugFor("writer")) {
		t.Fatal("human DM must not be wake-capped")
	}

	// The first agentDMWakeCap wakes pass, then the DM goes quiet.
	for i := 0; i < agentDMWakeCap; i++ {
		if !b.AgentDMWakeAllowed("editor__writer") {
			t.Fatalf("wake %d unexpectedly capped", i+1)
		}
	}
	if b.AgentDMWakeAllowed("editor__writer") {
		t.Fatal("wake beyond the cap must be suppressed")
	}

	// An expired window frees the cap again.
	b.mu.Lock()
	aged := make([]time.Time, 0, agentDMWakeCap)
	for range b.agentDMWakes["editor__writer"] {
		aged = append(aged, time.Now().Add(-agentDMWakeWindow-time.Minute))
	}
	b.agentDMWakes["editor__writer"] = aged
	b.mu.Unlock()
	if !b.AgentDMWakeAllowed("editor__writer") {
		t.Fatal("wake after window rollover must be allowed")
	}
}

func TestChannelListingsExcludeAgentDMs(t *testing.T) {
	b := newAgentDMTestBroker(t)
	if _, err := b.PostMessage("writer", "writer__editor", "seed", nil, ""); err != nil {
		t.Fatalf("seed post: %v", err)
	}

	for _, query := range []string{"", "?type=dm"} {
		req := httptest.NewRequest("GET", "/api/channels"+query, nil)
		rec := httptest.NewRecorder()
		b.handleChannels(rec, req)
		var body struct {
			Channels []teamChannel `json:"channels"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode channels (%q): %v", query, err)
		}
		for _, ch := range body.Channels {
			if ch.Slug == "editor__writer" {
				t.Fatalf("agent DM leaked into channel listing (query %q)", query)
			}
		}
	}
}
