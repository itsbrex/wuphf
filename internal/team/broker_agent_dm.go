package team

import "time"

// Hardening for agent↔agent pair DMs — the channels behind the consult
// relay (broker_consult_relay.go). The relay makes those conversations
// observable; this file makes them safe:
//
//   - guardAgentDMPostLocked keeps the pair thread writable only by its
//     two member agents. The human observes through the consult markers'
//     read-only view; a human post would put words into a conversation
//     the markers present as agent-to-agent.
//   - AgentDMWakeAllowed caps partner wakes per DM window so two agents
//     cannot ping-pong each other forever. Structural loop protection,
//     not prompt-level hope: past the cap the message still lands, only
//     the wake is suppressed until the window rolls over.

const (
	agentDMWakeWindow = 30 * time.Minute
	agentDMWakeCap    = 6
)

// guardAgentDMPostLocked enforces the pair DM's write rules. Returns a
// non-empty rejection reason when `from` may not post into `channel`;
// "" for non-pair channels and for the two member agents. Callers must
// hold b.mu and have resolved channel to its canonical slug.
func (b *Broker) guardAgentDMPostLocked(channel, from string) string {
	x, y, ok := isAgentToAgentDM(channel)
	if !ok {
		return ""
	}
	sender := normalizeActorSlug(from)
	if isHumanMessageSender(sender) {
		return "agent DMs are observer-only for humans; open the thread from its consult marker instead"
	}
	if sender != normalizeActorSlug(x) && sender != normalizeActorSlug(y) {
		return "agent DM is limited to its two members"
	}
	return ""
}

// AgentDMWakeAllowed reports whether a message in `channel` may wake the
// DM partner, and records the wake against the per-DM cap when it may.
// Always true for channels that are not agent↔agent pair DMs.
func (b *Broker) AgentDMWakeAllowed(channel string) bool {
	if _, _, ok := isAgentToAgentDM(channel); !ok {
		return true
	}
	slug := normalizeChannelSlug(channel)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.agentDMWakes == nil {
		b.agentDMWakes = make(map[string][]time.Time)
	}
	recent := b.agentDMWakes[slug][:0]
	for _, at := range b.agentDMWakes[slug] {
		if now.Sub(at) <= agentDMWakeWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= agentDMWakeCap {
		b.agentDMWakes[slug] = recent
		return false
	}
	b.agentDMWakes[slug] = append(recent, now)
	return true
}
