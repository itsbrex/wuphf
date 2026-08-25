package team

import (
	"strings"
	"testing"
)

// A task with no channel has no conversation, so a card ABOUT it has nowhere to
// go and is skipped rather than redirected.
//
// THE ASSERTION THAT MATTERS IS NOT "the skip happened". It is that NOTHING
// LANDS IN #general. The skip is the mechanism; general staying untouched is
// the invariant, and it is the one that has to survive someone later deciding a
// fallback would be friendlier. Every case below checks the invariant, not the
// mechanism.
//
// These paths currently cannot produce a homeless task in production — the
// resolver only returns "" once #general is switched off — so this is the
// untested side of the branch, which is exactly why it is tested here.

func newNoHomeNotifTestBroker(t *testing.T) (*Broker, *teamTask) {
	t.Helper()
	b := newTestBroker(t)
	b.members = []officeMember{
		{Slug: "ceo", Name: "CEO", BuiltIn: true},
		{Slug: "eng", Name: "Engineer"},
	}
	b.channels = []teamChannel{
		{Slug: GeneralChannelSlug, Name: GeneralChannelSlug, Members: []string{"human", "ceo", "eng"}},
	}
	b.tasks = []teamTask{{
		ID: "OFFICE-77", Title: "Homeless task", Owner: "eng",
		Channel: "", // no conversation home
		status:  "in_progress", LifecycleState: LifecycleStateRunning,
	}}
	return b, &b.tasks[0]
}

// messagesIn returns every message the broker holds for a channel.
func messagesIn(b *Broker, slug string) []channelMessage {
	var out []channelMessage
	for i := range b.messages {
		if normalizeChannelSlug(b.messages[i].Channel) == slug {
			out = append(out, b.messages[i])
		}
	}
	return out
}

func TestHomelessTaskCardsNeverLandInGeneral(t *testing.T) {
	cases := []struct {
		name string
		fire func(b *Broker, task *teamTask)
	}{
		{"reassign", func(b *Broker, task *teamTask) {
			b.postTaskReassignNotificationsLocked("ceo", task, "designer")
		}},
		{"cancel", func(b *Broker, task *teamTask) {
			b.postTaskCancelNotificationsLocked("ceo", task, "no longer needed")
		}},
		{"request_changes", func(b *Broker, task *teamTask) {
			b.postTaskRequestChangesNotificationsLocked("ceo", task, "needs another pass")
		}},
		{"rejected", func(b *Broker, task *teamTask) {
			b.postTaskRejectedNotificationsLocked("ceo", task, "not this quarter")
		}},
		{"issue_created", func(b *Broker, task *teamTask) {
			_ = b.postIssueCreatedCardLocked("ceo", task)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, task := newNoHomeNotifTestBroker(t)
			b.mu.Lock()
			before := len(b.messages)
			tc.fire(b, task)
			after := len(b.messages)
			leaked := messagesIn(b, GeneralChannelSlug)
			b.mu.Unlock()

			if len(leaked) != 0 {
				t.Errorf("a %s card for a homeless task landed in #general: %d message(s), first=%q — "+
					"the card must be skipped, never redirected into a shared room",
					tc.name, len(leaked), strings.TrimSpace(leaked[0].Content))
			}
			if after != before {
				t.Errorf("a %s card for a homeless task posted %d message(s) somewhere; expected none",
					tc.name, after-before)
			}
		})
	}
}

// The skip must be specific to having no home, not a blanket "these cards never
// post". A task WITH a channel still gets its card — otherwise the guard above
// would be silently disabling the whole notification path and every assertion
// in this file would pass for the wrong reason.
func TestTaskCardsStillPostWhenTheTaskHasAHome(t *testing.T) {
	b, task := newNoHomeNotifTestBroker(t)
	b.mu.Lock()
	task.Channel = GeneralChannelSlug
	before := len(b.messages)
	b.postTaskReassignNotificationsLocked("ceo", task, "designer")
	posted := len(b.messages) - before
	b.mu.Unlock()

	if posted == 0 {
		t.Error("a task WITH a home posted no reassign card — the no-home guard is too broad " +
			"and has disabled the notification path entirely")
	}
}

// taskCardHasNoHome itself: nil and whitespace-only channels are homeless too.
// A whitespace channel would otherwise slip past a bare != "" check and get
// normalised into "general" one line later.
func TestTaskCardHasNoHomeTreatsWhitespaceAsHomeless(t *testing.T) {
	if !taskCardHasNoHome("probe", nil) {
		t.Error("a nil task must count as homeless")
	}
	for _, ch := range []string{"", "   ", "\t"} {
		task := &teamTask{ID: "OFFICE-1", Channel: ch}
		if !taskCardHasNoHome("probe", task) {
			t.Errorf("channel %q must count as homeless; normalizeChannelSlug would turn it into %q",
				ch, GeneralChannelSlug)
		}
	}
	if taskCardHasNoHome("probe", &teamTask{ID: "OFFICE-1", Channel: "product"}) {
		t.Error("a task with a real channel must not count as homeless")
	}
}
