package team

import (
	"testing"

	"github.com/nex-crm/wuphf/internal/channel"
)

// preferredTaskChannelLocked decides where a task's conversation lives. Every
// task in the product currently lives in #general — shouldMintPerTaskChannel
// returns false unconditionally, so nothing mints a per-task room — which is
// why this resolver has to be correct BEFORE the shared room is switched off.
// If it still answered "general" at flip time, every task conversation would
// point at a channel that no longer exists.
//
// The contract: an explicit request wins; otherwise the owner's home, then the
// creator's home, then EMPTY.
func TestPreferredTaskChannelResolvesOwnerThenCreatorThenEmpty(t *testing.T) {
	newBroker := func(t *testing.T) *Broker {
		t.Helper()
		b := newTestBroker(t)
		b.mu.Lock()
		b.members = []officeMember{
			{Slug: "ceo", Name: "CEO", BuiltIn: true},
			{Slug: "designer", Name: "Designer"},
		}
		b.rebuildMemberIndexLocked()
		b.mu.Unlock()
		return b
	}

	t.Run("an explicit channel always wins, switch either way", func(t *testing.T) {
		for _, enabled := range []bool{true, false} {
			restore := channel.SetGeneralEnabledForTest(enabled)
			b := newBroker(t)
			b.mu.Lock()
			got := b.preferredTaskChannelLocked("  Product  ", "ceo", "designer", "t", "d")
			b.mu.Unlock()
			restore()
			if got != "product" {
				t.Errorf("general enabled=%v: got %q, want the normalised explicit channel %q", enabled, got, "product")
			}
		}
	})

	t.Run("switch on: still general, so nothing changes today", func(t *testing.T) {
		defer channel.SetGeneralEnabledForTest(true)()
		b := newBroker(t)
		b.mu.Lock()
		defer b.mu.Unlock()
		// Every shape, including the one with no actors at all, must answer
		// general while the room exists. This is what makes landing the
		// resolver before the flip a no-op.
		cases := [][2]string{
			{"ceo", "designer"},
			{"ceo", ""},
			{"", "designer"},
			{"", ""},
			{"nobody", "also-nobody"},
		}
		for _, c := range cases {
			if got := b.preferredTaskChannelLocked("", c[0], c[1], "t", "d"); got != GeneralChannelSlug {
				t.Errorf("createdBy=%q owner=%q: got %q, want %q", c[0], c[1], got, GeneralChannelSlug)
			}
		}
	})

	t.Run("switch off: the owner's DM wins over the creator's", func(t *testing.T) {
		defer channel.SetGeneralEnabledForTest(false)()
		b := newBroker(t)
		b.mu.Lock()
		defer b.mu.Unlock()
		got := b.preferredTaskChannelLocked("", "ceo", "designer", "t", "d")
		if DMTargetAgent(got) != "designer" {
			t.Errorf("got %q, want the owner (designer) DM, not the creator's", got)
		}
	})

	t.Run("switch off: falls back to the creator when there is no owner", func(t *testing.T) {
		defer channel.SetGeneralEnabledForTest(false)()
		b := newBroker(t)
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, owner := range []string{"", "   ", "someone-not-on-the-roster"} {
			got := b.preferredTaskChannelLocked("", "ceo", owner, "t", "d")
			if DMTargetAgent(got) != "ceo" {
				t.Errorf("owner=%q: got %q, want the creator (ceo) DM", owner, got)
			}
		}
	})

	t.Run("switch off: empty when neither resolves, and never general", func(t *testing.T) {
		defer channel.SetGeneralEnabledForTest(false)()
		b := newBroker(t)
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, c := range [][2]string{
			{"", ""},
			{"human", ""},
			{"ghost", "phantom"},
		} {
			got := b.preferredTaskChannelLocked("", c[0], c[1], "t", "d")
			if got != "" {
				t.Errorf("createdBy=%q owner=%q: got %q, want an empty home", c[0], c[1], got)
			}
		}
	})
}

// The emptiness test inside the resolver must run on the RAW request, not on
// the normalised one. normalizeChannelSlug("") returns "general" (its lobby
// fallback), so normalising first would make "no channel requested"
// indistinguishable from "explicitly requested #general" and quietly route
// every homeless task straight back into the room being retired.
//
// normalizeChannelSlug is deliberately not being changed here (that is its own
// stage), so this pins the behaviour the resolver has to work around.
func TestPreferredTaskChannelDoesNotLaunderEmptyThroughNormalizeChannelSlug(t *testing.T) {
	if got := normalizeChannelSlug(""); got != GeneralChannelSlug {
		t.Fatalf("precondition changed: normalizeChannelSlug(\"\") = %q, want %q — re-check the resolver's raw-emptiness test", got, GeneralChannelSlug)
	}

	defer channel.SetGeneralEnabledForTest(false)()
	b := newTestBroker(t)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.members = nil
	b.rebuildMemberIndexLocked()

	for _, requested := range []string{"", "   ", "\t"} {
		if got := b.preferredTaskChannelLocked(requested, "", "", "t", "d"); got != "" {
			t.Errorf("requested %q: got %q, want an empty home (the slug normaliser must not launder it into general)", requested, got)
		}
	}
}

// Every call site guards on "" before calling findChannelLocked, because that
// helper normalises its argument and would turn an empty home back into
// general — re-creating the leak one layer down. This pins the helper's
// behaviour so the guards cannot be "simplified" away later on the assumption
// that findChannelLocked("") is harmless.
func TestFindChannelLockedTurnsEmptyIntoGeneral(t *testing.T) {
	b := newTestBroker(t)
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := b.findChannelLocked("")
	if ch == nil {
		t.Skip("no general channel in this fixture; nothing to pin")
	}
	if ch.Slug != GeneralChannelSlug {
		t.Fatalf("findChannelLocked(\"\") resolved to %q", ch.Slug)
	}
}
