package team

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression for the screenshot bug: a task the Chief of Staff opened inside
// the human's DM for @prospect-scout dragged the scout's working narration
// into that private thread. Tasks owned by someone other than the DM's bot
// re-home to the bot pair DM, and owner promotion never touches a 1:1 DM.

func newRehomeTestBroker(t *testing.T) *Broker {
	t.Helper()
	b := newTestBroker(t)
	b.mu.Lock()
	b.members = append(b.members,
		officeMember{Slug: "cos", Name: "Chief of Staff", Role: "lead", BuiltIn: true},
		officeMember{Slug: "prospect-scout", Name: "Rita Scout", Role: "Outbound Prospecting Analyst"},
	)
	b.rebuildMemberIndexLocked()
	b.ensureDMConversationLocked("cos__human")
	b.mu.Unlock()
	return b
}

func TestTaskInHumanDMReHomesToBotPairDM(t *testing.T) {
	b := newRehomeTestBroker(t)
	b.mu.Lock()
	defer b.mu.Unlock()

	got := b.preferredTaskChannelLocked("cos__human", "cos", "prospect-scout", "Find prospects", "")
	if got != "cos__prospect-scout" {
		t.Fatalf("task for another bot inside the human DM: channel = %q, want cos__prospect-scout", got)
	}
	pair := b.findChannelLocked("cos__prospect-scout")
	if pair == nil {
		t.Fatal("pair DM was not created by the re-home")
	}
	if len(pair.Members) != 2 || !containsString(pair.Members, "cos") || !containsString(pair.Members, "prospect-scout") {
		t.Fatalf("pair DM members = %v", pair.Members)
	}

	// The DM's own bot keeps its task where the conversation is.
	if got := b.preferredTaskChannelLocked("cos__human", "cos", "cos", "Plan the week", ""); got != "cos__human" {
		t.Fatalf("task owned by the DM's bot moved to %q", got)
	}
	// Non-DM channels are untouched.
	if got := b.preferredTaskChannelLocked(testTeamRoom, "cos", "prospect-scout", "x", ""); got != testTeamRoom {
		t.Fatalf("named channel re-homed to %q", got)
	}
}

func TestTaskOwnerPromotionNeverEntersHumanDM(t *testing.T) {
	b := newRehomeTestBroker(t)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.ensureTaskOwnerChannelMembershipLocked("cos__human", "prospect-scout")
	dm := b.findChannelLocked("cos__human")
	if dm == nil {
		t.Fatal("human DM missing")
	}
	if containsString(dm.Members, "prospect-scout") {
		t.Fatalf("third bot promoted into the human's DM: %v", dm.Members)
	}
	// The DM's own bot is still a legitimate owner there.
	b.ensureTaskOwnerChannelMembershipLocked("cos__human", "cos")
	if !containsString(b.findChannelLocked("cos__human").Members, "cos") {
		t.Fatal("the DM's own bot lost membership")
	}
}

// A DM admits exactly the two participants its slug names — even when the
// stored member list has drifted to include a third bot.
func TestDMAdmitsOnlyItsSlugParticipants(t *testing.T) {
	b := newRehomeTestBroker(t)
	b.mu.Lock()
	defer b.mu.Unlock()

	// Simulate membership drift: a third bot in the stored roster row.
	dm := b.findChannelLocked("cos__human")
	dm.Members = append(dm.Members, "prospect-scout")

	cases := []struct {
		slug, channel string
		want          bool
	}{
		{"cos", "cos__human", true},
		{"you", "cos__human", true},
		{"system", "cos__human", true},
		{"prospect-scout", "cos__human", false}, // drifted member, still denied
		{"cos", "human__prospect-scout", false}, // the lead has no DM bypass
		{"prospect-scout", "human__prospect-scout", true},
	}
	for _, c := range cases {
		if got := b.canAccessChannelLocked(c.slug, c.channel); got != c.want {
			t.Errorf("canAccess(%s, %s) = %v, want %v", c.slug, c.channel, got, c.want)
		}
	}

	// Bot pair DMs admit their two bots and nobody else.
	b.ensureBotPairDMLocked("cos__prospect-scout")
	b.members = append(b.members, officeMember{Slug: "designer", Name: "Designer"})
	b.rebuildMemberIndexLocked()
	if !b.canAccessChannelLocked("cos", "cos__prospect-scout") || !b.canAccessChannelLocked("prospect-scout", "cos__prospect-scout") {
		t.Error("pair DM participants must be admitted")
	}
	if b.canAccessChannelLocked("designer", "cos__prospect-scout") {
		t.Error("third bot admitted into a pair DM")
	}
}

// The channel API cannot edit a DM's participants.
func TestDMParticipantsCannotBeEditedViaChannelAPI(t *testing.T) {
	b := newRehomeTestBroker(t)
	req := httptest.NewRequest("POST", "/api/channels", strings.NewReader(`{"action":"update","slug":"cos__human","members":["cos","prospect-scout"]}`))
	rec := httptest.NewRecorder()
	b.handleChannels(rec, req)
	if rec.Code != 400 {
		t.Fatalf("editing DM members: code = %d, body %s", rec.Code, rec.Body.String())
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if containsString(b.findChannelLocked("cos__human").Members, "prospect-scout") {
		t.Fatal("third bot written into the DM member list")
	}
}

// A task opened with NO channel by one agent for another must not fall
// back to the creator's own DM (the path the human eval caught): it lands
// in the creator⇄owner pair DM.
func TestTaskWithoutChannelForAnotherAgentLandsInPairDM(t *testing.T) {
	b := newRehomeTestBroker(t)
	b.mu.Lock()
	defer b.mu.Unlock()
	got := b.preferredTaskChannelLocked("", "cos", "prospect-scout", "Find prospects", "")
	if got != "cos__prospect-scout" {
		t.Fatalf("channel-less task for another agent: channel = %q, want cos__prospect-scout", got)
	}
	// The creator's own task still homes on its own DM.
	if got := b.preferredTaskChannelLocked("", "cos", "cos", "Plan the week", ""); got != "cos__human" {
		t.Fatalf("self-owned channel-less task: channel = %q, want cos__human", got)
	}
}

// Request cards are filed where the requester and the human both belong:
// never in a pair DM (the human cannot answer there) and never in another
// agent's 1:1 DM.
func TestRequestChannelKeepsThirdPartiesOutOfDMs(t *testing.T) {
	b := newRehomeTestBroker(t)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureBotPairDMLocked("cos__prospect-scout")
	cases := []struct{ from, channel, want string }{
		{"prospect-scout", "cos__human", "human__prospect-scout"},          // another agent's DM → own DM
		{"cos", "human__prospect-scout", "cos__human"},                     // the lead is not exempt
		{"prospect-scout", "cos__prospect-scout", "human__prospect-scout"}, // pair DM → own DM
		{"cos", "cos__human", "cos__human"},                                // own DM passes through
		{"cos", testTeamRoom, testTeamRoom},                                // shared room passes through
		{"you", "human__prospect-scout", "human__prospect-scout"},          // humans are never redirected
	}
	for _, c := range cases {
		if got := b.requestChannelForLocked(c.from, c.channel); got != c.want {
			t.Errorf("requestChannelFor(%s, %s) = %q, want %q", c.from, c.channel, got, c.want)
		}
	}
}

// The lead is not woken for work an agent opened for itself: the owner is
// already executing under the human's eye, and the lead's plan card would
// otherwise duplicate the task and land in the owner's DM.
func TestLeadNotWokenForSelfOwnedTask(t *testing.T) {
	self := teamTask{ID: "T-1", Owner: "designer", CreatedBy: "designer", Channel: "designer__human"}
	if shouldWakeLeadForTaskAction(officeActionLog{Kind: "task_created", Actor: "designer"}, self) {
		t.Fatal("lead woken for a self-owned task")
	}
	delegated := teamTask{ID: "T-2", Owner: "designer", CreatedBy: "cos", Channel: "cos__designer"}
	if !shouldWakeLeadForTaskAction(officeActionLog{Kind: "task_created", Actor: "cos"}, delegated) {
		t.Fatal("lead must still follow work it delegated")
	}
}

// An agent's inbox never contains messages from a DM it is not part of.
func TestInboxNeverLeaksAnotherDM(t *testing.T) {
	inCeoDM := channelMessage{From: "you", Channel: "cos__human", Content: "can you get @prospect-scout to look?", Tagged: []string{"prospect-scout"}}
	if messageBelongsToViewerInbox(inCeoDM, "prospect-scout", nil) {
		t.Fatal("a human ask inside the Chief of Staff's DM leaked into another agent's inbox")
	}
	if !messageBelongsToViewerInbox(inCeoDM, "cos", nil) {
		t.Fatal("the DM's own agent must see the human's message")
	}
	inRoom := channelMessage{From: "you", Channel: testTeamRoom, Content: "hello team"}
	if !messageBelongsToViewerInbox(inRoom, "prospect-scout", nil) {
		t.Fatal("a shared-room human message still belongs in every agent's inbox")
	}
}

// When B raises a request on work A delegated, A's DM gets a system pointer
// (not B's words) so the human knows a decision is waiting elsewhere.
func TestRequestPointerLandsInDelegatorDM(t *testing.T) {
	b := newRehomeTestBroker(t)
	b.mu.Lock()
	b.tasks = append(b.tasks, teamTask{ID: "T-9", Title: "Find prospects", Owner: "prospect-scout", CreatedBy: "cos", Channel: "cos__prospect-scout"})
	b.ensureBotPairDMLocked("cos__prospect-scout")
	b.mu.Unlock()

	req, err := b.CreateRequest(humanInterview{Kind: "approval", From: "prospect-scout", Channel: "cos__prospect-scout", Title: "Start work on Find prospects?", Question: "ok?", IssueID: "T-9"})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if req.Channel != "human__prospect-scout" {
		t.Fatalf("request card channel = %q, want the requester's own DM", req.Channel)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	pointer := false
	for _, m := range b.messages {
		if m.Channel == "cos__human" && m.Kind == "human_request_pointer" && m.From == "system" {
			pointer = true
		}
		if m.Channel == "cos__human" && m.From == "prospect-scout" {
			t.Fatal("the requester itself spoke inside the delegator's DM")
		}
	}
	if !pointer {
		t.Fatal("no system pointer in the delegator's DM")
	}
}
