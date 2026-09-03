package team

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nex-crm/wuphf/internal/channel"
)

// Consult relay markers.
//
// When your agent goes and asks another agent something, you see two quiet
// lines in your DM with your agent: one when it sends, one when a reply comes
// back. Clicking either opens that agent-to-agent conversation read-only.
//
// WHY THESE ARE DERIVED, NOT EMITTED
//
// A marker an agent chooses to post is a marker an agent can fabricate: it
// could announce "asked Social" having messaged nobody, and the human would
// have no way to tell a real consult from an invented one. These markers are
// projections of the agent-to-agent messages themselves, computed on read. The
// marker exists if and only if the message exists. An agent cannot emit one; it
// can only cause one by actually sending something.
//
// WHY ON READ RATHER THAN ON WRITE
//
// Materialising markers at write time also preserves the honesty property —
// appendMessageLocked (broker_publish.go) is the single chokepoint every
// message passes through, and that is the hook to use if markers ever need to
// outlive the conversation they describe. It was not chosen here because:
//   - it writes two extra rows per consult turn, which compete for the message
//     cap in broker_gc.go; a chatty consult could evict real conversation
//   - it leaves a blind window: consults that happened before the feature
//     landed would have no markers, while derivation covers them retroactively
//   - a stored copy can drift from the thing it describes; a projection cannot
//
// The derivation costs nothing extra: the /messages handler already walks
// b.messages once to filter by channel, and this walk rides along.
//
// PLACEMENT
//
// For a message in DM(a, b) where neither side is the human:
//   - the SENDER's DM with the human gets "Messaged <b>"      (direction sent)
//   - the RECIPIENT's DM with the human gets "Message from <a>" (direction received)
//
// That rule is symmetric and falls out of the data. In the common case — your
// agent asks a peer, the peer answers — both markers land in your DM with your
// agent, because that agent is first the sender and then the recipient. There
// is no special case for it.

// consultRelayKind is the message kind the web renders as a centered marker
// rather than a chat bubble. It never carries an author: nobody said it.
const consultRelayKind = "consult_relay"

const (
	consultRelayDirectionSent     = "sent"
	consultRelayDirectionReceived = "received"
)

// consultRelayPayload is the wire shape the marker card reads. The agent is
// carried as a slug, not a display name — the web already resolves slugs to
// names and avatars from the roster, so the two cannot drift.
type consultRelayPayload struct {
	// Direction is relative to the agent whose DM this marker appears in:
	// "sent" = that agent messaged Agent; "received" = Agent messaged them.
	Direction string `json:"direction"`
	// Agent is the OTHER agent in the consult — the one named on the marker.
	Agent string `json:"agent"`
	// Channel is the agent-to-agent DM, so clicking opens the real thing.
	Channel string `json:"channel"`
}

// ensureAgentPairDMLocked creates the DM channel for an agent-to-agent pair.
//
// Both sides must be real roster members: a pair DM is only meaningful between
// agents that exist, and creating one for an invented slug would mint a channel
// nobody can ever reach. Returns nil when the slug is not an agent pair or
// either side is unknown, which the caller surfaces as "channel not found".
//
// Membership is the ONLY thing that grants access here — the two participants
// are written into Members, and canAccessChannelLocked gates on exactly that
// (broker_channel_access.go). An agent reaches its consult because it is a
// participant, never because it is exempt.
//
// Caller must hold b.mu.
func (b *Broker) ensureAgentPairDMLocked(slug string) *teamChannel {
	a, bb, ok := isAgentToAgentDM(slug)
	if !ok {
		return nil
	}
	a = normalizeActorSlug(a)
	bb = normalizeActorSlug(bb)
	if b.findMemberLocked(a) == nil || b.findMemberLocked(bb) == nil {
		return nil
	}
	canonical := normalizeChannelSlug(channel.DirectSlug(a, bb))
	if ch := b.findChannelLocked(canonical); ch != nil {
		return ch
	}
	if b.channelStore != nil {
		// Registered for type-based DM detection, exactly as the human path
		// does. A failure here is non-fatal: the broker-local channel below is
		// what access and rendering actually read.
		_, _ = b.channelStore.GetOrCreateDirect(a, bb)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b.channels = append(b.channels, teamChannel{
		Slug:        canonical,
		Name:        canonical,
		Type:        "dm",
		Description: "Direct messages between " + a + " and " + bb,
		Members:     []string{a, bb},
		CreatedBy:   "wuphf",
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	return &b.channels[len(b.channels)-1]
}

// mergeByTimestamp folds derived rows into an already-chronological message
// list, keeping the result chronological. Rows with equal timestamps keep base
// before extra, so a marker never jumps ahead of the message that produced it.
//
// A plain append + sort would do the same, but both inputs are already ordered,
// so merging is the honest shape and one walk instead of a sort.
//
// String comparison is a valid time ordering here because every timestamp in
// the broker is written as time.Now().UTC().Format(time.RFC3339) — fixed width,
// always UTC, always "Z". A marker copies its source message's timestamp
// verbatim, so the two sides cannot disagree on format. If a non-UTC offset
// ever enters the message log this comparison silently mis-orders, so keep the
// UTC invariant.
func mergeByTimestamp(base, extra []channelMessage) []channelMessage {
	out := make([]channelMessage, 0, len(base)+len(extra))
	i, j := 0, 0
	for i < len(base) && j < len(extra) {
		if extra[j].Timestamp < base[i].Timestamp {
			out = append(out, extra[j])
			j++
			continue
		}
		out = append(out, base[i])
		i++
	}
	out = append(out, base[i:]...)
	out = append(out, extra[j:]...)
	return out
}

// isAgentToAgentDM reports whether slug is a 1:1 DM between two agents, and
// returns the pair. A DM with a human on either side is not a consult.
func isAgentToAgentDM(slug string) (string, string, bool) {
	a, b, ok := DMParticipants(slug)
	if !ok {
		return "", "", false
	}
	if isHumanMessageSender(a) || isHumanMessageSender(b) {
		return "", "", false
	}
	return a, b, true
}

// reHomeTaskOutOfHumanDMLocked keeps a human's 1:1 DM private. A task
// created inside the human's DM with agent A but owned by agent B would
// otherwise drag B's every working note into that private thread (and the
// task-owner promotion would hand B membership to make it stick). Such a
// task lives in the A⇄B pair DM instead: B works there, A is the partner,
// and the consult markers give the human a read-only window from the DM
// they were already in. A task the DM's own agent owns stays put. Caller
// must hold b.mu.
func (b *Broker) reHomeTaskOutOfHumanDMLocked(slug, owner string) string {
	agentSide := humanDMAgent(slug)
	if agentSide == "" {
		return slug
	}
	o := normalizeActorSlug(owner)
	if o == "" || o == agentSide || b.findMemberLocked(o) == nil {
		return slug
	}
	pair := b.ensureAgentPairDMLocked(normalizeChannelSlug(channel.DirectSlug(agentSide, o)))
	if pair == nil {
		return slug
	}
	return pair.Slug
}

// humanDMAgent returns the agent whose 1:1 DM with the human `slug` is, or ""
// when slug is not a human-to-agent DM.
func humanDMAgent(slug string) string {
	a, b, ok := DMParticipants(slug)
	if !ok {
		return ""
	}
	switch {
	case isHumanMessageSender(a):
		return normalizeActorSlug(b)
	case isHumanMessageSender(b):
		return normalizeActorSlug(a)
	}
	return ""
}

// deriveConsultMarkersLocked returns the relay markers that belong in `channel`,
// synthesized from the agent-to-agent DM traffic of the agent that channel
// belongs to. Returns nil for any channel that is not a human-to-agent DM.
//
// The returned rows are RESPONSE-ONLY. They are never appended to b.messages,
// so they never reach persistence, the notifier, or the agent-context builder —
// an agent's own prompt is unaffected by markers rendered for the human.
//
// Caller must hold b.mu.
func (b *Broker) deriveConsultMarkersLocked(channel string) []channelMessage {
	agent := humanDMAgent(channel)
	if agent == "" {
		return nil
	}
	var out []channelMessage
	for _, msg := range b.messages {
		peer, direction, ok := consultRelayFor(msg, agent)
		if !ok {
			continue
		}
		marker, err := newConsultRelayMarker(msg, channel, peer, direction)
		if err != nil {
			// A marker is observational; a marshal failure must never break
			// the conversation it annotates.
			continue
		}
		out = append(out, marker)
	}
	return out
}

// consultRelayFor decides whether msg produces a marker in `agent`'s DM with
// the human, and if so which peer it names and in which direction.
func consultRelayFor(msg channelMessage, agent string) (peer, direction string, ok bool) {
	if _, _, isConsult := isAgentToAgentDM(msg.Channel); !isConsult {
		return "", "", false
	}
	other := DMOtherParticipant(msg.Channel, agent)
	if other == "" {
		// agent is not a participant in this consult — not their business, and
		// the marker would leak a conversation they are not part of.
		return "", "", false
	}
	sender := normalizeActorSlug(msg.From)
	switch sender {
	case normalizeActorSlug(agent):
		return other, consultRelayDirectionSent, true
	case normalizeActorSlug(other):
		return other, consultRelayDirectionReceived, true
	}
	// Somebody who is not a participant posted into the pair DM. Not a consult
	// turn; say nothing rather than guess a direction.
	return "", "", false
}

// newConsultRelayMarker builds one response-only marker row.
//
// The ID is DERIVED from the source message id, so it is stable across reads:
// the web can key on it, and `since_id` polling stays coherent instead of
// seeing a "new" marker on every poll.
func newConsultRelayMarker(src channelMessage, channel, peer, direction string) (channelMessage, error) {
	raw, err := json.Marshal(consultRelayPayload{
		Direction: direction,
		Agent:     peer,
		Channel:   normalizeChannelSlug(src.Channel),
	})
	if err != nil {
		return channelMessage{}, err
	}
	return channelMessage{
		ID:      fmt.Sprintf("relay-%s-%s", direction, strings.TrimSpace(src.ID)),
		Channel: channel,
		Kind:    consultRelayKind,
		// No From. A relay marker is an event, not somebody talking — there is
		// no sender to render, and inventing one (a "system" author with a face
		// and a profile link) is the exact thing the office does not do.
		From:      "",
		Content:   "",
		Timestamp: src.Timestamp,
		Payload:   raw,
	}, nil
}
