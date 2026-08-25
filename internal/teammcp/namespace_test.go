package teammcp

import "testing"

// Tool names are derived from the server key, and those fully-qualified strings
// live in users' permission allowlists, saved skills, and transcripts we cannot
// reach. A rename silently revokes every grant written against the old name,
// with no error naming the cause. So the old key is an ALIAS, forever.

func TestServerKeysIncludeEveryLegacyKey(t *testing.T) {
	keys := ServerKeys()
	if len(keys) == 0 || keys[0] != ServerKey {
		t.Fatalf("the canonical key must come first, got %v", keys)
	}

	have := map[string]bool{}
	for _, k := range keys {
		have[k] = true
	}
	for _, legacy := range LegacyServerKeys {
		if !have[legacy] {
			t.Errorf("legacy key %q must stay registered: a permission granted against it would silently stop matching", legacy)
		}
	}
}

func TestServerKeysAreUniqueAndNonEmpty(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range ServerKeys() {
		if k == "" {
			t.Error("an empty server key would namespace tools as mcp____<tool>")
		}
		if seen[k] {
			t.Errorf("duplicate server key %q: the entry would be registered twice", k)
		}
		seen[k] = true
	}
}

// A name written down before a rename must still be recognised afterwards.
// This is the assertion that fails the moment someone "cleans up" the alias.
func TestAcceptsQualifiedToolNameUnderEveryKey(t *testing.T) {
	for _, key := range ServerKeys() {
		qualified := "mcp__" + key + "__team_task"
		if !AcceptsQualifiedToolName(qualified) {
			t.Errorf("%q must still be recognised; a stale allowlist entry cannot be rewritten remotely", qualified)
		}
	}
}

func TestAcceptsQualifiedToolNameRejectsOtherServers(t *testing.T) {
	for _, other := range []string{
		"mcp__some-other-server__team_task",
		"team_task",
		"",
		"mcp__" + ServerKey + "__", // prefix with no tool is not a tool
	} {
		if AcceptsQualifiedToolName(other) {
			t.Errorf("%q must not be treated as one of ours", other)
		}
	}
}

func TestQualifiedToolNameUsesTheCanonicalKey(t *testing.T) {
	got := QualifiedToolName("team_task")
	want := "mcp__" + ServerKey + "__team_task"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if !AcceptsQualifiedToolName(got) {
		t.Fatal("a name we generate must be a name we accept")
	}
}

// The canonical key must never also appear in the legacy list: ServerKeys()
// de-duplicates, but the overlap would mean someone had "renamed" to the name
// they already had and believed the alias was doing something.
func TestCanonicalKeyIsNotAlsoListedAsLegacy(t *testing.T) {
	for _, legacy := range LegacyServerKeys {
		if legacy == ServerKey {
			t.Fatalf("%q is both the canonical and a legacy key", legacy)
		}
	}
}
