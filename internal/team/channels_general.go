package team

import (
	"errors"
	"fmt"

	"github.com/nex-crm/wuphf/internal/channel"
)

// The broker-facing face of the #general kill switch.
//
// Shape borrowed from shouldMintPerTaskChannel (broker_tasks_worktrees.go):
// keep the function, keep every call site, return a constant, and put the
// rationale in the doc comment. That is this tree's proven way to switch a
// behaviour off reversibly — the callers keep their fallback branch, so
// flipping the constant back restores the old behaviour without re-threading
// anything.
//
// The actual boolean lives in internal/channel (see general.go there), because
// internal/company and internal/operations also mint #general and cannot
// import internal/team without a cycle. This file is where broker code should
// look; internal/channel is where the switch physically is.
//
// CREATE / ROUTE / LIST only. Nothing here deletes. #general's persisted
// channel row, its messages, and the task-general system task stay on disk,
// and the archive guard at broker_migration_channels.go:73 that keeps
// general's history out of the fold stays exactly as it is.

// GeneralChannelSlug is the slug of the shared #general channel.
const GeneralChannelSlug = channel.GeneralSlug

// ErrNoHomeChannel is returned by homeChannelForLocked when #general is switched off
// and the actor cannot be resolved to a roster member, so there is no DM to
// route to either. It is deliberately an error and not a fallback slug: a
// fallback would be a channel nobody reads, which is the "leak" the founder
// asked us to prevent. Callers must surface it, not swallow it.
var ErrNoHomeChannel = errors.New("team: no home channel for actor")

// generalChannelEnabled reports whether #general may be created, routed to, or
// listed. THE flip for the kill switch; see internal/channel/general.go for
// why #general is going away, why it is a switch rather than a delete, and how
// to revive it (change the constant there, or gate it on an env var following
// internal/team/governor.go:109).
//
// Returns true today. This stage threads the seam through every resurrection
// point without changing behaviour.
func generalChannelEnabled() bool {
	return channel.GeneralEnabled()
}

// strictChannelWrites reports whether a write addressed to a channel that does
// not exist should fail loudly instead of being quietly redirected.
//
// It is the inverse of generalChannelEnabled, and that is not a coincidence:
// while #general exists it is the universal fallback, so a misaddressed write
// always has somewhere to land and silently landing there is tolerable. Once
// #general is gone that same silence becomes a dropped message, so writes have
// to start failing where they used to fall through.
//
// Unused at this stage by design — a later stage turns the write paths strict.
// Kept here so the switch and its consequence live together and cannot drift.
func strictChannelWrites() bool {
	return !generalChannelEnabled()
}

// groupDMsEnabled reports whether a group DM may be created, routed to, or
// listed. A SEPARATE switch from generalChannelEnabled, on purpose: reviving
// the shared room and reviving multi-agent DMs are two different product
// decisions, so they get two constants.
//
// A group DM is a channel by another name — internal/channel/types.go
// documents ChannelTypeGroup as "Group DMs (human + N agents)", which is the
// same several-agents-in-one-room shape #general is being retired to stop.
// Left alive, it becomes the new #general within a week.
//
// The point of DM-first is that every conversation has exactly two
// participants, so the human can follow it. Tagging another agent inside a DM
// sends the agent you are talking to off to consult them and report back; it
// does not pull them into the room.
//
// Create / route / list only, same as general: existing group rows still load
// and read, so nothing on disk becomes unreachable. See
// internal/channel/general.go for the switch and the revival routes.
//
// Returns true today.
func groupDMsEnabled() bool {
	return channel.GroupDMsEnabled()
}

// isGroupDM reports whether a channel is a multi-participant DM rather than a
// 1:1 one.
//
// Two independent signals, because a group row can be identified by either:
// its slug is a GroupSlug hash, so it names no single partner agent (unlike
// "human__ceo" or the legacy "dm-ceo"); and it carries more than two members.
// Checking both catches a row whose members drifted as well as one whose slug
// did, and the member count is the direct expression of the rule that matters
// — three or more participants in one conversation is the thing being retired.
func (ch *teamChannel) isGroupDM() bool {
	if !ch.isDM() {
		return false
	}
	return DMTargetAgent(ch.Slug) == "" || len(ch.Members) > 2
}

// homeChannelForLocked resolves the channel an actor's work belongs in when no
// explicit channel was given. It is the no-leak replacement for the
// `if channel == "" { channel = "general" }` fallback that is currently spread
// across the write paths.
//
// Exactly three outcomes, and deliberately no fourth:
//
//  1. #general is enabled            -> GeneralChannelSlug, as today.
//  2. Disabled, actor is on the roster -> that agent's DM, created if missing.
//  3. Disabled, actor does not resolve -> ErrNoHomeChannel.
//
// The third outcome is the point. Returning some default slug for an
// unresolvable actor would put messages in a channel nobody reads, which is
// exactly the leak this switch exists to prevent. A caller that cannot name a
// real recipient must fail rather than invent one.
//
// Caller MUST hold b.mu: this reads the roster and may append to b.channels
// via ensureDMConversationLocked.
func (b *Broker) homeChannelForLocked(actorSlug string) (string, error) {
	if generalChannelEnabled() {
		return GeneralChannelSlug, nil
	}

	slug := normalizeActorSlug(actorSlug)
	if slug == "" {
		return "", fmt.Errorf("%w: empty actor", ErrNoHomeChannel)
	}
	if b.findMemberLocked(slug) == nil {
		return "", fmt.Errorf("%w: %q is not a roster member", ErrNoHomeChannel, slug)
	}

	dmSlug := DMSlugFor(slug)
	if dmSlug == "" {
		return "", fmt.Errorf("%w: no DM slug for %q", ErrNoHomeChannel, slug)
	}
	// ensureDMConversationLocked may rewrite the slug to the canonical
	// pair-sorted form when the channel store is live, so trust what it
	// returns over what we computed.
	ch := b.ensureDMConversationLocked(dmSlug)
	if ch == nil {
		return "", fmt.Errorf("%w: could not open a DM with %q", ErrNoHomeChannel, slug)
	}
	return ch.Slug, nil
}
