package operations

import (
	"testing"

	"github.com/nex-crm/wuphf/internal/channel"
)

// Gate 7 of the #general kill switch lives in this package, so its proof does
// too: the broker-side test in internal/team cannot reach genericDefaultChannels.
//
// The synthesis path reaches the broker through
// blankSlateOfficeChannelsFromBlueprint (gate 4), which already skips a
// declared general, so this gate is defence in depth rather than the only
// thing standing between the switch and a resurrected channel. It still has to
// hold: a synthesized blueprint is a value other code can consume directly,
// and one that lists a channel the product no longer has is a lie in the data.
func TestGenericDefaultChannelsRespectsGeneralKillSwitch(t *testing.T) {
	hasGeneral := func(channels []StarterChannel) bool {
		for _, ch := range channels {
			if ch.Slug == channel.GeneralSlug {
				return true
			}
		}
		return false
	}

	t.Run("switch on: general leads the list", func(t *testing.T) {
		defer channel.SetGeneralEnabledForTest(true)()
		channels := genericDefaultChannels(nil)
		if !hasGeneral(channels) {
			t.Fatal("switch on: synthesis dropped #general, so this test cannot prove the gate")
		}
		if channels[0].Slug != channel.GeneralSlug {
			t.Errorf("general should stay first; got %q", channels[0].Slug)
		}
	})

	t.Run("switch off: general is absent, the rest survive", func(t *testing.T) {
		defer channel.SetGeneralEnabledForTest(false)()
		channels := genericDefaultChannels(nil)
		if hasGeneral(channels) {
			t.Error("switch off: synthesis resurrected #general (gate 7 genericDefaultChannels)")
		}
		// Gating general must not take the other starter channels with it.
		for _, want := range []string{"planning", "execution", "review"} {
			found := false
			for _, ch := range channels {
				if ch.Slug == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("gating general also dropped #%s", want)
			}
		}
	})
}
