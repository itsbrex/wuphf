package team

import "testing"

// officeChangeEvent.Slug is polymorphic: a MEMBER slug for the member_* kinds,
// a CHANNEL slug for the channel_* kinds. shouldBackfillTaskOwner is where that
// ambiguity is resolved, so it is where both halves get pinned.
//
// The member branch had the same defect as the rest of the slug tranche — one
// side normalised, the other not — and it failed CLOSED and silently: the
// backfill simply did not happen, which looks like nothing at all rather than
// like a bug.
func TestShouldBackfillTaskOwnerNormalisesBothSides(t *testing.T) {
	// A task the backfill should be willing to touch: open, unblocked, owned.
	task := func(owner, channel string) teamTask {
		return teamTask{Owner: owner, Channel: channel, status: "open"}
	}

	t.Run("member: a capitalised owner still matches", func(t *testing.T) {
		// task.Owner is stored with a plain TrimSpace and no slug
		// normalisation, so "Designer" is genuinely storable. Before the fix
		// this was compared against a lowercased, channel-normalised event
		// slug, never matched, and the owner was never backfilled.
		if !shouldBackfillTaskOwner("member_created", "designer", task("Designer", "product")) {
			t.Error(`owner "Designer" did not match member_created "designer" — ` +
				`the comparison is normalising only one side`)
		}
	})

	t.Run("member: spacing and underscores match too", func(t *testing.T) {
		cases := [][2]string{
			{"  designer  ", "designer"},
			{"gtm_lead", "gtm-lead"},
			{"GTM Lead", "gtm-lead"},
		}
		for _, c := range cases {
			if !shouldBackfillTaskOwner("member_created", c[1], task(c[0], "product")) {
				t.Errorf("owner %q did not match member_created %q", c[0], c[1])
			}
		}
	})

	t.Run("member: a genuinely different owner still does not match", func(t *testing.T) {
		// The widening must not turn into "matches anything".
		if shouldBackfillTaskOwner("member_created", "designer", task("engineer", "product")) {
			t.Error("owner \"engineer\" matched member_created \"designer\"")
		}
	})

	t.Run("channel: the half that already worked still works", func(t *testing.T) {
		if !shouldBackfillTaskOwner("channel_updated", "product", task("designer", "product")) {
			t.Error("channel_updated \"product\" did not match a task in #product")
		}
		// NOTE: this case fails against the pre-fix code, but that is an
		// artifact of the contract change, NOT a bug that existed in
		// production. Before the fix the CALLER normalised the event slug, so
		// the real path matched fine; only a direct unit call like this one saw
		// a raw slug. It is here to pin the new contract — the function
		// normalises its own input — not to claim a channel-side regression.
		if !shouldBackfillTaskOwner("channel_created", "Product", task("designer", "#product")) {
			t.Error("the channel branch must normalise its own input now that the caller does not")
		}
		if shouldBackfillTaskOwner("channel_updated", "gtm", task("designer", "product")) {
			t.Error("channel_updated \"gtm\" matched a task in #product")
		}
	})

	t.Run("a DM channel slug survives the channel branch", func(t *testing.T) {
		// Guards the same hazard as broker_dm.go: the channel branch must keep
		// using normalizeChannelSlug, which preserves "__". Swap in the actor
		// normaliser here and a task homed in a DM stops matching its own
		// channel event.
		dm := DMSlugFor("cos")
		if !shouldBackfillTaskOwner("channel_updated", dm, task("cos", dm)) {
			t.Errorf("a task homed in DM %q did not match its own channel_updated event", dm)
		}
	})

	t.Run("kinds outside the three are ignored", func(t *testing.T) {
		for _, kind := range []string{"member_removed", "channel_removed", "office_reseeded", ""} {
			if shouldBackfillTaskOwner(kind, "designer", task("designer", "product")) {
				t.Errorf("kind %q should not trigger a backfill", kind)
			}
		}
	})

	t.Run("finished or blocked tasks are still skipped", func(t *testing.T) {
		for _, status := range []string{"done", "canceled", "cancelled", "review"} {
			tk := task("Designer", "product")
			tk.status = status
			if shouldBackfillTaskOwner("member_created", "designer", tk) {
				t.Errorf("status %q should not be backfilled", status)
			}
		}
		blocked := task("Designer", "product")
		blocked.blocked = true
		if shouldBackfillTaskOwner("member_created", "designer", blocked) {
			t.Error("a blocked task should not be backfilled")
		}
	})
}
