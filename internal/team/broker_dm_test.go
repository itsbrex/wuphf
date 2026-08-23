package team

import "testing"

// The canonical DM slug is the pair-sorted "<a>__<b>" from channel.DirectSlug.
// Recognition used to run through canonicalDMTargetAgent, which answers ""
// unless one side is the human — so an agent-to-agent pair was not a DM at
// all and fell through to channel routing. These pin the shape-based
// recognition and the viewer-relative lookup that replaced it.

func TestIsDMSlugRecognisesAgentToAgentPair(t *testing.T) {
	t.Parallel()
	// The bug: "ceo__designer" has no human side, so the old
	// canonicalDMTargetAgent-based test returned false and the consult relay
	// had no DM to route through.
	if !IsDMSlug("ceo__designer") {
		t.Fatal("agent-to-agent pair slug must be recognised as a DM")
	}
}

func TestIsDMSlugRecognisesHumanPairAndLegacyForms(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{
		"human__eng",
		"eng__human",
		"dm-eng",
		"dm-human-eng",
	} {
		if !IsDMSlug(slug) {
			t.Errorf("IsDMSlug(%q) = false, want true", slug)
		}
	}
}

func TestIsDMSlugRejectsNonPairSlugs(t *testing.T) {
	t.Parallel()
	// normalizeChannelSlug collapses every single "_" to "-" and preserves
	// only "__", so a regular channel can never wear the pair shape. These
	// guard the edges of that guarantee.
	for _, slug := range []string{
		"general",
		"eng-standup",
		"eng_standup", // single underscore normalizes to "eng-standup"
		"a__b__c",     // three parts is not a 1:1 pair
		"__b",         // empty first side
		"a__",         // empty second side
	} {
		if IsDMSlug(slug) {
			t.Errorf("IsDMSlug(%q) = true, want false", slug)
		}
	}
}

func TestDMTargetAgentStaysHumanRelative(t *testing.T) {
	t.Parallel()
	// A dozen call sites read DMTargetAgent as "the agent the human is talking
	// to". Widening IsDMSlug must not quietly change that contract.
	if got := DMTargetAgent("human__eng"); got != "eng" {
		t.Errorf("DMTargetAgent(human__eng) = %q, want eng", got)
	}
	if got := DMTargetAgent("dm-human-eng"); got != "eng" {
		t.Errorf("DMTargetAgent(dm-human-eng) = %q, want eng", got)
	}
	if got := DMTargetAgent("ceo__designer"); got != "" {
		t.Errorf("DMTargetAgent on an agent-to-agent DM must stay empty; got %q", got)
	}
}

func TestDMOtherParticipantResolvesRelativeToViewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		slug   string
		viewer string
		want   string
	}{
		{"agent pair, ceo viewing", "ceo__designer", "ceo", "designer"},
		{"agent pair, designer viewing", "ceo__designer", "designer", "ceo"},
		{"human pair, human viewing", "human__eng", "human", "eng"},
		{"human pair, agent viewing", "human__eng", "eng", "human"},
		{"human alias 'you' viewing", "human__eng", "you", "eng"},
		{"legacy slug, human viewing", "dm-human-eng", "human", "eng"},
		{"legacy slug, agent viewing", "dm-human-eng", "eng", "human"},
		{"viewer is not a participant", "ceo__designer", "pm", ""},
		{"empty viewer cannot be resolved", "ceo__designer", "", ""},
		{"not a DM", "general", "ceo", ""},
		{"group DM has no readable pair", "a__b__c", "a", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DMOtherParticipant(tc.slug, tc.viewer); got != tc.want {
				t.Errorf("DMOtherParticipant(%q, %q) = %q, want %q",
					tc.slug, tc.viewer, got, tc.want)
			}
		})
	}
}

func TestDMOtherParticipantNeverReturnsTheViewer(t *testing.T) {
	t.Parallel()
	// This is what makes the notifier's "don't echo an agent's own message
	// back to it" guard structural rather than a separate check.
	for _, viewer := range []string{"ceo", "designer", "human"} {
		for _, slug := range []string{"ceo__designer", "human__eng"} {
			if got := DMOtherParticipant(slug, viewer); got != "" && got == viewer {
				t.Errorf("DMOtherParticipant(%q, %q) returned the viewer", slug, viewer)
			}
		}
	}
}

func TestDMPartnerRefusesToGuessInAnAgentToAgentDM(t *testing.T) {
	t.Parallel()
	b := &Broker{}
	b.channels = []teamChannel{
		{Slug: "human__eng", Type: "dm", Members: []string{"human", "eng"}},
		{Slug: "ceo__designer", Type: "dm", Members: []string{"ceo", "designer"}},
	}

	if got := b.DMPartner("human__eng"); got != "eng" {
		t.Errorf("DMPartner(human__eng) = %q, want eng", got)
	}
	// Both members are real agents. The old "first non-human member wins" loop
	// handed the surface bridge a coin flip between them; with no viewer to
	// resolve against, "cannot route" is the only honest answer.
	if got := b.DMPartner("ceo__designer"); got != "" {
		t.Errorf("DMPartner on an agent-to-agent DM must not guess a side; got %q", got)
	}
}

// Not parallel: t.Setenv cannot be combined with t.Parallel.
func TestAgentMCPServersTreatsCanonicalDMAsDMMode(t *testing.T) {
	// The stale check here was strings.HasPrefix(channel, "dm-"), so every
	// canonical DM silently got the full server set including nex.
	t.Setenv("WUPHF_CHANNEL", "human__pm")
	got := agentMCPServers("pm")
	if len(got) != 1 || got[0] != "wuphf-office" {
		t.Fatalf("canonical DM must get the minimal server set; got %v", got)
	}
}

func TestAgentMCPServersKeepsFullSetOutsideDMs(t *testing.T) {
	t.Setenv("WUPHF_CHANNEL", "general")
	got := agentMCPServers("pm")
	if len(got) != 2 {
		t.Fatalf("non-DM channel must keep the full server set; got %v", got)
	}
}
