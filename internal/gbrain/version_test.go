package gbrain

import (
	"context"
	"strings"
	"testing"
)

// withStubVersion installs a fake `gbrain --version` and clears the cache.
func withStubVersion(t *testing.T, out string, err error) {
	t.Helper()
	prev := runGBrain
	runGBrain = func(context.Context, ...string) (string, error) { return out, err }
	resetVersionCache()
	t.Cleanup(func() {
		runGBrain = prev
		resetVersionCache()
	})
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.48.1.0", "0.48.0.0", 1},
		{"0.42.58.0", "0.48.0.0", -1},
		{"0.48.0.0", "0.48.0.0", 0},
		// A missing component counts as zero, so these are equal. gbrain's
		// four-part scheme does not fit semver, hence the hand-rolled compare.
		{"0.48", "0.48.0.0", 0},
		{"1.0.0.0", "0.99.99.99", 1},
		// Numeric, not lexicographic: "0.9" must not sort above "0.48".
		{"0.9.0.0", "0.48.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseVersionLineSkipsBanner(t *testing.T) {
	// `gbrain --version` emits an upgrade banner alongside the version, so the
	// parser must scan for a version-shaped token rather than take a position.
	out := "UPGRADE_AVAILABLE 0.42.58.0 0.48.1.0\ngbrain 0.48.1.0\n"
	if got := parseVersionLine(out); got == "" {
		t.Fatal("parseVersionLine found no version in banner output")
	}
	if got := parseVersionLine("no version here"); got != "" {
		t.Errorf("parseVersionLine on junk = %q, want empty", got)
	}
}

// TestNeedsPutPageRestore is the load-bearing case: it decides whether an extra
// MCP call is made on EVERY page write.
func TestNeedsPutPageRestore(t *testing.T) {
	t.Run("old gbrain keeps the workaround", func(t *testing.T) {
		withStubVersion(t, "gbrain 0.42.58.0", nil)
		if !NeedsPutPageRestore(context.Background()) {
			t.Error("0.42 must keep the restore: without it a re-created page is invisible")
		}
	})
	t.Run("current gbrain drops it", func(t *testing.T) {
		withStubVersion(t, "gbrain 0.48.1.0", nil)
		if NeedsPutPageRestore(context.Background()) {
			t.Error("0.48 fixed this upstream; the extra call per write is waste")
		}
	})
	t.Run("unknown version keeps the workaround", func(t *testing.T) {
		// When in doubt, be slow rather than lossy: the workaround is correct on
		// every version, so guessing "new enough" would trade a round-trip for
		// silent data loss.
		withStubVersion(t, "", errNotInstalledForTest{})
		if !NeedsPutPageRestore(context.Background()) {
			t.Error("an unknown version must keep the workaround")
		}
	})
}

func TestVersionAdvisory(t *testing.T) {
	t.Run("silent when current", func(t *testing.T) {
		withStubVersion(t, "gbrain 0.48.1.0", nil)
		if got := VersionAdvisory(context.Background()); got != "" {
			t.Errorf("advisory on a current gbrain = %q, want empty", got)
		}
	})
	t.Run("names the risk and the fix when stale", func(t *testing.T) {
		withStubVersion(t, "gbrain 0.42.58.0", nil)
		got := VersionAdvisory(context.Background())
		if !strings.Contains(got, "0.42.58.0") {
			t.Errorf("advisory does not name the installed version: %q", got)
		}
		if !strings.Contains(got, "self-upgrade") {
			t.Errorf("advisory does not name the fix: %q", got)
		}
	})
}

type errNotInstalledForTest struct{}

func (errNotInstalledForTest) Error() string { return "not installed" }
