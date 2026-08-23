package team

import (
	"strings"
	"testing"
)

// TestAnswerIsSilentDismissal pins the FYI contract: clicking Acknowledge on a
// non-blocking notice clears the card WITHOUT posting a channel message, so no
// teammate wakes to acknowledge the acknowledgement. Anything carrying intent —
// a typed note, or any other choice — still posts.
func TestAnswerIsSilentDismissal(t *testing.T) {
	cases := []struct {
		name   string
		req    humanInterview
		answer interviewAnswer
		want   bool
	}{
		{"bare acknowledge on a notice is silent", humanInterview{Kind: "notice", From: "app-builder"}, interviewAnswer{ChoiceID: "acknowledge"}, true},
		{"acknowledge with a note still posts", humanInterview{Kind: "notice", From: "app-builder"}, interviewAnswer{ChoiceID: "acknowledge", CustomText: "ship it tomorrow"}, false},
		{"approval acknowledge still posts", humanInterview{Kind: "approval", From: "ceo"}, interviewAnswer{ChoiceID: "acknowledge"}, false},
		{"approve on a notice still posts", humanInterview{Kind: "notice", From: "ceo"}, interviewAnswer{ChoiceID: "approve"}, false},
		{"case and padding are ignored", humanInterview{Kind: " Notice "}, interviewAnswer{ChoiceID: " Acknowledge "}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := answerIsSilentDismissal(tc.req, tc.answer); got != tc.want {
				t.Fatalf("answerIsSilentDismissal(%+v, %+v) = %v, want %v", tc.req, tc.answer, got, tc.want)
			}
		})
	}
}

// TestDescribeTaskChanges pins what a human's manual edit reports to the
// channel: only fields a person would care about, with long bodies summarised
// rather than dumped, and silence when nothing moved.
func TestDescribeTaskChanges(t *testing.T) {
	base := teamTask{ID: "dunde-2", Title: "Ship it", Owner: "pm", status: "running", Details: "old body"}

	if got := describeTaskChanges(&base, &base); len(got) != 0 {
		t.Fatalf("identical tasks must report no changes, got %v", got)
	}
	if got := describeTaskChanges(nil, &base); got != nil {
		t.Fatalf("nil previous must be silent, got %v", got)
	}

	next := base
	next.status = "done"
	next.Owner = ""
	next.Title = "Ship it now"
	next.Details = "a much longer rewritten body that should not be dumped into the channel verbatim"
	got := describeTaskChanges(&base, &next)
	if len(got) != 4 {
		t.Fatalf("want 4 changes (title/status/owner/description), got %d: %v", len(got), got)
	}
	joined := ""
	for _, g := range got {
		joined += g + "|"
	}
	for _, want := range []string{"status running -> done", "owner @pm -> (unassigned)", "description rewritten"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "much longer rewritten body") {
		t.Errorf("description body must be summarised, not dumped: %q", joined)
	}
}
