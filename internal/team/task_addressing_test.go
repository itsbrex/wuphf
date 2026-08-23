package team

import "testing"

// TestTextMentionsTaskIDTokenBoundaries pins the matching rule that decides
// which task a message in the shared room wakes. Getting this wrong is not a
// cosmetic bug: a loose match wakes the wrong owner, a tight one reintroduces
// the dead air the wake exists to prevent.
func TestTextMentionsTaskIDTokenBoundaries(t *testing.T) {
	cases := []struct {
		name string
		body string
		id   string
		want bool
	}{
		{"plain mention", "please look at DUNDE-12 today", "DUNDE-12", true},
		{"trailing period", "fixed DUNDE-12.", "DUNDE-12", true},
		{"parenthesised", "the regression (DUNDE-12) is back", "DUNDE-12", true},
		// Case-SENSITIVE on purpose: the chat surface only linkifies the
		// uppercase form and the lowercase literal task-N, so a lowercase
		// "dunde-12" is not something the human saw themselves address.
		{"lowercase does not address", "what happened to dunde-12?", "DUNDE-12", false},
		{"wrong case rejected", "see Dunde-12", "DUNDE-12", false},
		{"lowercase task form", "task-3 needs a second look", "task-3", true},
		{"start of message", "DUNDE-12 is wrong", "DUNDE-12", true},

		// The one that matters most: a shorter id must not match inside a
		// longer one, or posting about DUNDE-12 would also wake DUNDE-1.
		{"prefix of a longer id", "see DUNDE-12", "DUNDE-1", false},
		{"suffix collision", "see XDUNDE-12", "DUNDE-12", false},
		{"embedded in a word", "predunde-12ish", "DUNDE-12", false},
		{"absent", "nothing to do with tasks", "DUNDE-12", false},
		{"empty body", "", "DUNDE-12", false},
		{"empty id", "DUNDE-12", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := textMentionsTaskID(tc.body, tc.id); got != tc.want {
				t.Errorf("textMentionsTaskID(%q, %q) = %v, want %v", tc.body, tc.id, got, tc.want)
			}
		})
	}
}

// TestMessageAddressesTaskSignals pins the three ways a message names a task,
// and the one lookalike that must NOT count.
func TestMessageAddressesTaskSignals(t *testing.T) {
	task := &teamTask{ID: "DUNDE-12", ThreadID: "msg-7", Owner: "designer"}

	if !messageAddressesTask(channelMessage{ReplyTo: "msg-7", Content: "any thoughts?"}, task) {
		t.Error("a reply in the task's thread must address it")
	}
	if !messageAddressesTask(channelMessage{Content: "bumping DUNDE-12"}, task) {
		t.Error("naming the task id must address it")
	}
	if !messageAddressesTask(channelMessage{SourceTaskID: "DUNDE-12", Content: "delivered"}, task) {
		t.Error("a broker post carrying the task id must address it")
	}

	// An @mention of the owner is NOT an address: owners hold many tasks, so
	// treating it as one would wake all of them — the broadcast storm the old
	// #general guard was written to avoid.
	if messageAddressesTask(channelMessage{Content: "@designer can you take a look"}, task) {
		t.Error("mentioning the owner must not address every task they own")
	}
	if messageAddressesTask(channelMessage{ReplyTo: "msg-99", Content: "unrelated"}, task) {
		t.Error("a reply in someone else's thread must not address this task")
	}
}

// TestHumanNoteWakesOnlyTheAddressedTaskInTheSharedRoom is the regression that
// matters at the product level.
//
// The one-room change put every task in #general. The follow-up wake was gated
// on `channel != "general"`, so it silently switched off for the entire
// product: a human could post a redline on a task sitting in review and no
// owner would ever pick it up. Deleting the guard outright would have been the
// opposite bug — every waiting task waking on every lobby message.
func TestHumanNoteWakesOnlyTheAddressedTaskInTheSharedRoom(t *testing.T) {
	b := newTestBroker(t)

	b.mu.Lock()
	b.tasks = []teamTask{
		{ID: "DUNDE-1", Title: "In review", Channel: "general", Owner: "designer",
			status: "review", LifecycleState: LifecycleStateReview},
		{ID: "DUNDE-2", Title: "Also in review", Channel: "general", Owner: "eng",
			status: "review", LifecycleState: LifecycleStateReview},
	}
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "you", Channel: "general",
		Content: "the spacing on DUNDE-1 is still off, please redo it",
	})
	addressed := b.tasks[0].HumanNotePending
	bystander := b.tasks[1].HumanNotePending
	b.mu.Unlock()

	if addressed == nil {
		t.Error("the named task must receive the human note and wake — this is the dead-air bug")
	}
	if bystander != nil {
		t.Error("an unnamed task in the same room must NOT wake; that is the broadcast storm")
	}
}

// TestHaltReachesEveryRunningTaskInTheRoom pins the deliberate exception: in a
// one-room office, a human typing "stop" is addressing the whole team.
func TestHaltReachesEveryRunningTaskInTheRoom(t *testing.T) {
	b := newTestBroker(t)

	b.mu.Lock()
	b.tasks = []teamTask{
		{ID: "DUNDE-1", Title: "Running one", Channel: "general", Owner: "designer",
			status: "in_progress", LifecycleState: LifecycleStateRunning},
		{ID: "DUNDE-2", Title: "Running two", Channel: "general", Owner: "eng",
			status: "in_progress", LifecycleState: LifecycleStateRunning},
	}
	b.markHumanNoteOnChannelTasksLocked(channelMessage{
		From: "you", Channel: "general", Content: "stop, we are shipping something else first",
	})
	first, second := b.tasks[0].HumanNotePending, b.tasks[1].HumanNotePending
	b.mu.Unlock()

	if first == nil || second == nil {
		t.Fatal("a halt must reach every running task in the shared room")
	}
	if !first.Halt || !second.Halt {
		t.Error("the note must carry the halt flag so the owners actually stop")
	}
}
