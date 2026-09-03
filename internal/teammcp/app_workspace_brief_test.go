package teammcp

import "testing"

// The broker appends the app workspace brief to a build task's details; the
// creating agent never re-reads the task, so team_task create must echo the
// brief back (a human eval watched an agent rebuild in /tmp and run out of
// turns because it never learned the project was already scaffolded).
func TestAppWorkspaceBriefFrom(t *testing.T) {
	brief := "App workspace ready: a project for this app is already scaffolded and showing a LIVE preview as `app_1`. " +
		"The project source lives at `/home/x/.wuphf/apps/app_1/src` — work there directly."
	cases := map[string]string{
		"":                                  "",
		"Score inbound leads.":              "",
		"Score inbound leads.\n\n" + brief:  brief,
		brief + "\n\nAcceptance: it opens.": brief,
		"Prose.\n\n" + brief + "\n\nMore.":  brief,
	}
	for details, want := range cases {
		if got := appWorkspaceBriefFrom(details); got != want {
			t.Fatalf("appWorkspaceBriefFrom(%q) = %q, want %q", details, got, want)
		}
	}
}
