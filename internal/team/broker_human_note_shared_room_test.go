package team

import (
	"strings"
	"testing"
)

// Whether a casual human message is stamped on a task turns on ONE question:
// does the room identify which task the human meant?
//
// That used to be answered with `channel == "general"` — a proxy, and one that
// inverts the moment #general is retired. A permanently-false sharedRoom makes
// `namedThisTask := !sharedRoom || ...` permanently TRUE, so every message is
// stamped on every task in the room: the exact broadcast storm this function
// exists to prevent, restored silently, compiling cleanly.
//
// The real condition is the number of non-system tasks in the room. These tests
// pin it from both sides, in a DM rather than #general, because a DM is where
// the proxy and the real condition disagree.

func newSharedRoomTestBroker(t *testing.T, room string, tasks ...teamTask) *Broker {
	t.Helper()
	b := newTestBroker(t)
	b.members = []officeMember{
		{Slug: "ceo", Name: "CEO", BuiltIn: true},
		{Slug: "eng", Name: "Engineer"},
	}
	b.channels = []teamChannel{{Slug: room, Name: room, Members: []string{"human", "ceo", "eng"}}}
	b.tasks = tasks
	return b
}

func runningTaskIn(id, room string) teamTask {
	return teamTask{
		ID: id, Channel: room, Title: id, Owner: "eng",
		LifecycleState: LifecycleStateRunning, status: "in_progress",
	}
}

func noteBodies(b *Broker) map[string]string {
	out := map[string]string{}
	for i := range b.tasks {
		if n := b.tasks[i].HumanNotePending; n != nil {
			out[b.tasks[i].ID] = n.Body
		}
	}
	return out
}

// ONE task in the room: the room IS the address, so a casual message that names
// nothing still reaches it. Under the old proxy a DM was never "shared", so
// this passed for the wrong reason; it must keep passing for the right one.
func TestHumanNoteRoomWithOneTaskIsItsOwnAddress(t *testing.T) {
	room := DMSlugFor("eng")
	b := newSharedRoomTestBroker(t, room, runningTaskIn("task-solo", room))

	b.mu.Lock()
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "human", Channel: room, Content: "can you take another look at this",
	})
	b.mu.Unlock()

	got := noteBodies(b)
	if _, ok := got["task-solo"]; !ok {
		t.Error("a message in a room holding exactly one task did not reach it; " +
			"with one task the room itself is the address")
	}
}

// SEVERAL tasks in the room: the message must name its target. This is the case
// the proxy could never express — a DM was always "not shared", so every task
// in it got stamped.
func TestHumanNoteRoomWithSeveralTasksRequiresNaming(t *testing.T) {
	room := DMSlugFor("eng")
	b := newSharedRoomTestBroker(t, room,
		runningTaskIn("task-a", room),
		runningTaskIn("task-b", room),
		runningTaskIn("task-c", room),
	)

	b.mu.Lock()
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "human", Channel: room, Content: "looks good, ship it",
	})
	b.mu.Unlock()

	if got := noteBodies(b); len(got) != 0 {
		t.Errorf("an unaddressed message was stamped on %d of 3 tasks in a shared room: %v — "+
			"this is the broadcast storm the function exists to prevent", len(got), got)
	}
}

// Naming one of several still reaches exactly that one.
func TestHumanNoteNamingOneOfSeveralReachesOnlyIt(t *testing.T) {
	room := DMSlugFor("eng")
	b := newSharedRoomTestBroker(t, room,
		runningTaskIn("task-a", room),
		runningTaskIn("task-b", room),
	)

	b.mu.Lock()
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "human", Channel: room, Content: "task-b needs another pass",
	})
	b.mu.Unlock()

	got := noteBodies(b)
	if _, ok := got["task-b"]; !ok {
		t.Error("naming task-b did not reach it")
	}
	if _, ok := got["task-a"]; ok {
		t.Error("naming task-b also stamped task-a")
	}
}

// A halt still reaches every RUNNING task in a shared room, unnamed. That
// exception is deliberate and must survive the change: "stop" means stop.
func TestHumanNoteHaltStillReachesEveryRunningTaskInASharedRoom(t *testing.T) {
	room := DMSlugFor("eng")
	b := newSharedRoomTestBroker(t, room,
		runningTaskIn("task-a", room),
		runningTaskIn("task-b", room),
	)

	b.mu.Lock()
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "human", Channel: room, Content: "stop — do not build a placeholder",
	})
	b.mu.Unlock()

	got := noteBodies(b)
	if len(got) != 2 {
		t.Errorf("a halt reached %d of 2 running tasks: %v", len(got), got)
	}
	for id, body := range got {
		if !strings.HasPrefix(strings.ToLower(body), "stop") {
			t.Errorf("%s: unexpected note body %q", id, body)
		}
	}
}

// System tasks are excluded from the COUNT as well as from stamping. The
// Backup & Migration task owns #general, so counting it would make a room that
// really holds one real task look shared and silently stop addressing it.
func TestHumanNoteSystemTasksDoNotMakeARoomLookShared(t *testing.T) {
	room := DMSlugFor("eng")
	system := teamTask{ID: "task-system", Channel: room, Title: "Backup & Migration", System: true}
	b := newSharedRoomTestBroker(t, room, runningTaskIn("task-solo", room), system)

	b.mu.Lock()
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "human", Channel: room, Content: "any progress?",
	})
	b.mu.Unlock()

	got := noteBodies(b)
	if _, ok := got["task-solo"]; !ok {
		t.Error("a system task made a one-task room look shared, so the real task was never addressed")
	}
	if _, ok := got["task-system"]; ok {
		t.Error("a system task was stamped with a human note")
	}
}

// ── the SCOPING itself ──────────────────────────────────────────────────────

// The count applies to DMs ONLY. A named room keeps today's behaviour: an
// unaddressed human post reaches every waiting task in it, however many there
// are. This is the guard against "simplifying" the rule into the universal
// version — that change was tried and reverted, because
// TestHumanNoteWakesOwnerOnWaitingTaskStates puts six tasks in one named room
// and expects exactly this.
func TestHumanNoteNamedRoomIsUnaffectedByTaskCount(t *testing.T) {
	const room = "delivery"
	b := newSharedRoomTestBroker(t, room,
		runningTaskIn("task-a", room),
		runningTaskIn("task-b", room),
		runningTaskIn("task-c", room),
	)

	b.mu.Lock()
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "human", Channel: room, Content: "any progress on these?",
	})
	b.mu.Unlock()

	if got := noteBodies(b); len(got) != 3 {
		t.Errorf("a named room reached %d of 3 tasks: %v — named rooms keep today's "+
			"behaviour; the task count is scoped to DMs", len(got), got)
	}
}

// #general likewise keeps today's behaviour, and specifically keeps it when it
// holds exactly ONE task — the case a universal count would flip. The lobby is
// never the address.
func TestHumanNoteGeneralIsNeverTheAddressEvenWithOneTask(t *testing.T) {
	b := newSharedRoomTestBroker(t, GeneralChannelSlug,
		runningTaskIn("task-solo", GeneralChannelSlug),
	)

	b.mu.Lock()
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "human", Channel: GeneralChannelSlug, Content: "how is the week looking?",
	})
	b.mu.Unlock()

	if got := noteBodies(b); len(got) != 0 {
		t.Errorf("an unaddressed lobby post was stamped on %v — #general is never the "+
			"address, however few tasks it holds", got)
	}
}
