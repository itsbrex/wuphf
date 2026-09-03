package teammcp

import "testing"

func TestIsBotPairDM(t *testing.T) {
	cases := []struct {
		channel string
		slug    string
		want    bool
	}{
		{"designer__pm", "designer", true},
		{"designer__pm", "pm", true},
		{"designer__pm", "cos", false},            // not a member
		{"human__pm", "pm", false},                // human DM, not a pair DM
		{"cos__human", "cos", false},              // human DM, reversed order
		{"human:sam__pm", "pm", false},            // named human session
		{"general", "designer", false},            // plain channel
		{"designer__designer", "designer", false}, // self pair is invalid
		{"__pm", "pm", false},                     // empty side
	}
	for _, c := range cases {
		if got := isBotPairDM(c.channel, c.slug); got != c.want {
			t.Errorf("isBotPairDM(%q, %q) = %v, want %v", c.channel, c.slug, got, c.want)
		}
	}
}
